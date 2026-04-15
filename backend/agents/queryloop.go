package agents

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

// QueryLoop is the core query loop state machine. It drives the iterative
// cycle of: context compression → API call → tool execution → continue/stop.
//
// Corresponds to TypeScript's queryLoop() in query.ts (~1730 lines).
// Handles 12+ exit paths: completed, aborted_streaming, aborted_tools,
// max_turns, model_error, prompt_too_long, image_error, hook_stopped,
// stop_hook_prevented, blocking_limit, max_output_tokens (escalate/recovery),
// token_budget_continuation, collapse_drain_retry, reactive_compact_retry.
//
// Parameters:
//   - ctx: cancellation context (replaces AbortController)
//   - params: query parameters including messages, system prompt, tools
//   - deps: injectable dependencies (callModel, microcompact, autocompact, uuid)
//   - ch: output channel for streaming events (replaces AsyncGenerator yield)
//   - logger: structured logger
//
// Returns Terminal indicating why the loop stopped.
func QueryLoop(ctx context.Context, params QueryParams, deps QueryDeps, ch chan<- StreamEvent, logger *slog.Logger) Terminal {
	if logger == nil {
		logger = slog.Default()
	}

	state := &LoopState{
		Messages:       params.Messages,
		ToolUseContext: params.ToolUseContext,
		TurnCount:      1, // TS starts at 1
	}

	if params.MaxOutputTokensOverride != nil {
		state.MaxOutputTokensOverride = params.MaxOutputTokensOverride
	}

	config := BuildQueryConfig(
		"", // sessionID populated by engine
		QueryGates{
			HistorySnip:            true,
			ContextCollapse:        false,
			TokenBudget:            params.TaskBudget != nil,
			StreamingToolExecution: true,
			ReactiveCompact:        true,
		},
	)

	// Budget tracker for token budget continuation (TS L280)
	budgetTracker := NewBudgetTracker(params.TaskBudget)

	// task_budget.remaining tracking across compaction boundaries (TS L283-291)
	var taskBudgetRemaining *int

	// Stop hook registry
	stopHookRegistry := NewStopHookRegistry()
	if params.MaxTurns > 0 {
		stopHookRegistry.Register(&MaxTurnsHook{MaxTurns: params.MaxTurns})
	}
	stopHookRegistry.Register(&ApiErrorSkipHook{})

	// Command lifecycle tracking (TS L229)
	consumedCommandUUIDs := make([]string, 0)
	_ = consumedCommandUUIDs // will be used once command queue is wired

	for {
		// Destructure state at top of each iteration (TS L308-321)
		toolUseContext := state.ToolUseContext
		messages := state.Messages
		turnCount := state.TurnCount
		maxOutputTokensRecoveryCount := state.MaxOutputTokensRecoveryCount
		hasAttemptedReactiveCompact := state.HasAttemptedReactiveCompact
		maxOutputTokensOverride := state.MaxOutputTokensOverride
		tracking := state.AutoCompactTracking

		// Check cancellation before each iteration
		select {
		case <-ctx.Done():
			return Terminal{
				Reason:    TerminalAbortedStreaming,
				TurnCount: state.TurnCount,
			}
		default:
		}

		// Initialize or increment query chain tracking (TS L347-363)
		var queryTracking *QueryChainTracking
		if toolUseContext != nil && toolUseContext.QueryTracking != nil {
			queryTracking = &QueryChainTracking{
				ChainID: toolUseContext.QueryTracking.ChainID,
				Depth:   toolUseContext.QueryTracking.Depth + 1,
			}
		} else {
			queryTracking = &QueryChainTracking{
				ChainID: deps.UUID(),
				Depth:   0,
			}
		}
		if toolUseContext != nil {
			toolUseContext.QueryTracking = queryTracking
		}

		// ---------- Phase 1: Get messages after compact boundary (TS L365) ----------
		messagesForQuery := getMessagesAfterCompactBoundary(messages)

		// ---------- Phase 2: Snip Compaction (TS L401-410, HISTORY_SNIP) ----------
		var snipTokensFreed int
		if config.Gates.HistorySnip {
			snipResult := deps.SnipCompact(messagesForQuery)
			if snipResult != nil {
				messagesForQuery = snipResult.Messages
				snipTokensFreed = snipResult.TokensFreed
				if snipResult.BoundaryMessage != nil {
					ch <- StreamEvent{Type: EventSystem, Message: snipResult.BoundaryMessage}
				}
			}
		}

		// ---------- Phase 2b: Tool Result Budget (TS L369-394, enforceToolResultBudget) ----------
		messagesForQuery = deps.ApplyToolResultBudget(messagesForQuery, toolUseContext, params.QuerySource)

		// ---------- Phase 3: Microcompact (TS L414-426) ----------
		microcompactResult := deps.Microcompact(messagesForQuery, toolUseContext, params.QuerySource)
		if microcompactResult != nil && microcompactResult.Applied {
			messagesForQuery = microcompactResult.Messages
		}

		// ---------- Phase 4: Context Collapse (TS L440-447, CONTEXT_COLLAPSE) ----------
		if config.Gates.ContextCollapse {
			collapseResult := deps.ContextCollapse(ctx, messagesForQuery, toolUseContext, params.QuerySource)
			if collapseResult != nil && collapseResult.Applied {
				messagesForQuery = collapseResult.Messages
			}
		}

		// ---------- Phase 5: Build full system prompt (TS L449-451) ----------
		fullSystemPrompt := params.SystemPrompt
		if len(params.SystemContext) > 0 {
			for k, v := range params.SystemContext {
				fullSystemPrompt += "\n\n<" + k + ">\n" + v + "\n</" + k + ">"
			}
		}

		// ---------- Phase 6: Autocompact (TS L454-543) ----------
		compactionResult := deps.Autocompact(ctx, messagesForQuery, toolUseContext, params.SystemPrompt, params.QuerySource)
		var compactionApplied bool
		if compactionResult != nil && compactionResult.Applied {
			compactionApplied = true

			// task_budget: subtract pre-compact context from remaining (TS L508-515)
			if params.TaskBudget != nil {
				preCompactContext := estimateContextTokens(messagesForQuery, params.SystemPrompt)
				base := params.TaskBudget.Total
				if taskBudgetRemaining != nil {
					base = *taskBudgetRemaining
				}
				remaining := base - preCompactContext
				if remaining < 0 {
					remaining = 0
				}
				taskBudgetRemaining = &remaining
			}

			// Reset tracking on every compact (TS L521-526)
			tracking = &AutoCompactTracking{
				Compacted:   true,
				TurnID:      deps.UUID(),
				TurnCounter: 0,
			}

			// Emit compact boundary messages
			for _, msg := range compactionResult.Messages {
				ch <- StreamEvent{Type: EventAssistant, Message: &msg}
			}

			messagesForQuery = compactionResult.Messages
			ch <- StreamEvent{Type: EventCompactBoundary}
		}

		// Update toolUseContext.Messages (TS L546-549)
		if toolUseContext != nil {
			toolUseContext.Messages = messagesForQuery
		}

		// ---------- Phase 7: Token limit check / blocking preempt (TS L592-648) ----------
		// Skip when: compaction just happened, querySource is compact/session_memory,
		// reactive compact is enabled with auto-compact, or context-collapse owns it.
		skipBlockingPreempt := compactionApplied ||
			params.QuerySource == "compact" ||
			params.QuerySource == "session_memory" ||
			(config.Gates.ReactiveCompact && true) // auto-compact is default-on

		if !skipBlockingPreempt {
			warningState := calculateTokenWarningState(messagesForQuery, params.SystemPrompt, snipTokensFreed)
			if warningState == tokenWarningBlocking {
				logger.Warn("token limit blocking — context too large")
				errorMsg := Message{
					Type:       MessageTypeAssistant,
					UUID:       deps.UUID(),
					IsApiError: true,
					Content:    []ContentBlock{{Type: ContentBlockText, Text: "Your conversation is too long for the context window. Use /compact to reduce context size."}},
					Timestamp:  time.Now(),
				}
				ch <- StreamEvent{Type: EventAssistant, Message: &errorMsg}
				return Terminal{Reason: TerminalBlockingLimit, TurnCount: state.TurnCount}
			}
		}

		// ---------- Phase 7b: Start memory prefetch (TS L337-344) ----------
		// Fire-and-forget prefetch; consumed after tool execution in Phase 12c.
		memoryPrefetchCh := deps.StartMemoryPrefetch(messagesForQuery, toolUseContext)

		// ---------- Phase 8: Prepare API call (TS L560-580) ----------
		toolDefs := deps.BuildToolDefinitions(toolUseContext)
		currentModel := getModel(params)

		// Apply user context to messages (TS L661)
		apiMessages := messagesForQuery
		if len(params.UserContext) > 0 {
			apiMessages = prependUserContext(apiMessages, params.UserContext)
		}

		// ---------- Phase 9: Call Model with streaming (TS L653-953) ----------
		var assistantMsgs []Message
		var toolUseBlocks []ToolUseBlock
		var toolResults []Message
		needsFollowUp := false
		var streamingFallbackOccurred bool
		var modelCallError error

		callParams := CallModelParams{
			Messages:       apiMessages,
			SystemPrompt:   fullSystemPrompt,
			Model:          currentModel,
			FallbackModel:  params.FallbackModel,
			ThinkingConfig: getThinkingConfig(params),
			Tools:          toolDefs,
			QuerySource:    params.QuerySource,
			TaskBudget:     params.TaskBudget,
			OnStreamingFallback: func() {
				streamingFallbackOccurred = true
			},
		}

		if maxOutputTokensOverride != nil {
			callParams.MaxOutputTokens = maxOutputTokensOverride
		}
		if taskBudgetRemaining != nil {
			if callParams.TaskBudget == nil {
				callParams.TaskBudget = &TaskBudget{Total: params.TaskBudget.Total}
			}
			callParams.TaskBudget.Remaining = *taskBudgetRemaining
		}

		// API call with fallback retry loop (TS L654-953)
		attemptWithFallback := true
		for attemptWithFallback {
			attemptWithFallback = false

			modelCh, err := deps.CallModel(ctx, callParams)
			if err != nil {
				// Check for FallbackTriggeredError (TS L893-953)
				var fallbackErr *FallbackTriggeredError
				if errors.As(err, &fallbackErr) && params.FallbackModel != "" {
					// Switch model and retry
					currentModel = params.FallbackModel
					callParams.Model = currentModel
					attemptWithFallback = true

					// Tombstone orphaned messages
					yieldMissingToolResults(assistantMsgs, "Model fallback triggered", deps, ch)
					assistantMsgs = nil
					toolResults = nil
					toolUseBlocks = nil
					needsFollowUp = false

					ch <- StreamEvent{Type: EventSystem, Message: &Message{
						Type:    MessageTypeSystem,
						Level:   "warning",
						Content: []ContentBlock{{Type: ContentBlockText, Text: "Switched to fallback model due to high demand"}},
					}}
					continue
				}

				// Check for ImageSizeError / ImageResizeError (TS L970-978)
				var imgErr *ImageSizeError
				var imgResizeErr *ImageResizeError
				if errors.As(err, &imgErr) || errors.As(err, &imgResizeErr) {
					errorMsg := Message{
						Type:       MessageTypeAssistant,
						UUID:       deps.UUID(),
						IsApiError: true,
						Content:    []ContentBlock{{Type: ContentBlockText, Text: err.Error()}},
					}
					ch <- StreamEvent{Type: EventAssistant, Message: &errorMsg}
					return Terminal{Reason: TerminalImageError, TurnCount: state.TurnCount}
				}

				// General model error
				logger.Error("callModel failed", "error", err)
				modelCallError = err

				// Yield missing tool results for any orphaned tool_use blocks (TS L984)
				yieldMissingToolResults(assistantMsgs, err.Error(), deps, ch)

				// Surface the error (TS L990-992)
				ch <- StreamEvent{Type: EventError, Error: err.Error()}
				return Terminal{
					Reason:    TerminalModelError,
					TurnCount: state.TurnCount,
					Error:     modelCallError,
				}
			}

			// Consume streaming events (TS L708-863)
			for event := range modelCh {
				// Streaming fallback: tombstone orphaned messages (TS L712-741)
				if streamingFallbackOccurred {
					for _, orphan := range assistantMsgs {
						ch <- StreamEvent{Type: EventTombstone, Message: &orphan}
					}
					assistantMsgs = nil
					toolResults = nil
					toolUseBlocks = nil
					needsFollowUp = false
					streamingFallbackOccurred = false
				}

				switch event.Type {
				case EventTextDelta, EventThinkingDelta:
					ch <- event

				case EventAssistant:
					if event.Message != nil {
						msg := *event.Message
						if msg.UUID == "" {
							msg.UUID = deps.UUID()
						}
						assistantMsgs = append(assistantMsgs, msg)

						// Extract tool use blocks (TS L829-835)
						for _, block := range msg.Content {
							if block.Type == ContentBlockToolUse {
								toolUseBlocks = append(toolUseBlocks, ToolUseBlock{
									ID:    block.ID,
									Name:  block.Name,
									Input: block.Input,
								})
								needsFollowUp = true
							}
						}

						// Withhold recoverable errors (TS L788-825)
						// prompt-too-long, max-output-tokens, media-size errors
						withheld := false
						if msg.IsApiError && isPromptTooLongMsg(msg) {
							withheld = true
						}
						if msg.IsApiError && isMediaSizeErrorMsg(msg) {
							withheld = true
						}
						if isWithheldMaxOutputTokensMsg(msg) {
							withheld = true
						}

						if !withheld {
							ch <- StreamEvent{Type: EventAssistant, Message: &msg, StopReason: msg.StopReason, Usage: msg.Usage}
						}
					}

				case EventError:
					ch <- event

				case EventRequestStart:
					ch <- event

				default:
					ch <- event
				}
			}
		}

		// ---------- Phase 10: Post-streaming abort check (TS L1011-1052) ----------
		select {
		case <-ctx.Done():
			yieldMissingToolResults(assistantMsgs, "Interrupted by user", deps, ch)
			ch <- StreamEvent{Type: EventSystem, Message: &Message{
				Type:    MessageTypeSystem,
				Content: []ContentBlock{{Type: ContentBlockText, Text: "[Request interrupted by user]"}},
			}}
			return Terminal{Reason: TerminalAbortedStreaming, TurnCount: state.TurnCount}
		default:
		}

		// No assistant message received
		if len(assistantMsgs) == 0 {
			return Terminal{Reason: TerminalModelError, TurnCount: state.TurnCount}
		}

		// Yield tool use summary from previous turn (TS L1054-1060)
		if state.PendingToolUseSummary != nil {
			if summary := <-state.PendingToolUseSummary; summary != nil {
				ch <- StreamEvent{
					Type: EventType("tool_use_summary"),
					Text: summary.Summary,
				}
			}
		}

		// ---------- Phase 11: Handle no-tool-use (end_turn) — TS L1062-1357 ----------
		if !needsFollowUp {
			lastMsg := assistantMsgs[len(assistantMsgs)-1]

			// 11a: Prompt-too-long recovery chain (TS L1070-1183)
			isWithheld413 := lastMsg.IsApiError && isPromptTooLongMsg(lastMsg)
			isWithheldMedia := lastMsg.IsApiError && isMediaSizeErrorMsg(lastMsg)

			if isWithheld413 {
				// First: context collapse drain (TS L1089-1117)
				if config.Gates.ContextCollapse &&
					(state.Transition == nil || state.Transition.Reason != TransitionCollapseDrainRetry) {
					drainResult := deps.ContextCollapseDrain(messagesForQuery, params.QuerySource)
					if drainResult != nil && drainResult.Committed > 0 {
						state = &LoopState{
							Messages:                     drainResult.Messages,
							ToolUseContext:               toolUseContext,
							AutoCompactTracking:          tracking,
							MaxOutputTokensRecoveryCount: maxOutputTokensRecoveryCount,
							HasAttemptedReactiveCompact:  hasAttemptedReactiveCompact,
							MaxOutputTokensOverride:      nil,
							TurnCount:                    turnCount,
							Transition:                   &StateTransition{Reason: TransitionCollapseDrainRetry, Committed: drainResult.Committed},
						}
						continue
					}
				}
			}

			// Reactive compact for prompt-too-long or media errors (TS L1119-1183)
			if isWithheld413 || isWithheldMedia {
				compacted := deps.ReactiveCompact(ctx, messagesForQuery, toolUseContext, params.SystemPrompt, params.QuerySource, hasAttemptedReactiveCompact)
				if compacted != nil && compacted.Applied {
					// task_budget: subtract pre-compact context (TS L1138-1146)
					if params.TaskBudget != nil {
						preCompactContext := estimateContextTokens(messagesForQuery, params.SystemPrompt)
						base := params.TaskBudget.Total
						if taskBudgetRemaining != nil {
							base = *taskBudgetRemaining
						}
						remaining := base - preCompactContext
						if remaining < 0 {
							remaining = 0
						}
						taskBudgetRemaining = &remaining
					}

					for _, msg := range compacted.Messages {
						ch <- StreamEvent{Type: EventAssistant, Message: &msg}
					}
					ch <- StreamEvent{Type: EventCompactBoundary}

					state = &LoopState{
						Messages:                     compacted.Messages,
						ToolUseContext:               toolUseContext,
						MaxOutputTokensRecoveryCount: maxOutputTokensRecoveryCount,
						HasAttemptedReactiveCompact:  true,
						TurnCount:                    turnCount,
						Transition:                   &StateTransition{Reason: TransitionReactiveCompactRetry},
					}
					continue
				}

				// No recovery — surface the withheld error and exit (TS L1168-1175)
				ch <- StreamEvent{Type: EventAssistant, Message: &lastMsg}
				reason := TerminalPromptTooLong
				if isWithheldMedia {
					reason = TerminalImageError
				}
				return Terminal{Reason: reason, TurnCount: state.TurnCount}
			}

			// 11b: Max output tokens recovery (TS L1185-1256)
			if isWithheldMaxOutputTokensMsg(lastMsg) {
				// Escalating retry: escalate to 64k if no override yet (TS L1199-1221)
				if maxOutputTokensOverride == nil {
					escalated := EscalatedMaxTokens
					logger.Info("max_output_tokens escalation", "escalated_to", escalated)
					state = &LoopState{
						Messages:                     messagesForQuery,
						ToolUseContext:               toolUseContext,
						AutoCompactTracking:          tracking,
						MaxOutputTokensRecoveryCount: maxOutputTokensRecoveryCount,
						HasAttemptedReactiveCompact:  hasAttemptedReactiveCompact,
						MaxOutputTokensOverride:      &escalated,
						TurnCount:                    turnCount,
						Transition:                   &StateTransition{Reason: TransitionMaxOutputTokensEscalate},
					}
					continue
				}

				// Multi-turn recovery: inject nudge message (TS L1223-1252)
				if maxOutputTokensRecoveryCount < MaxOutputTokensRecoveryLimit {
					recoveryMsg := Message{
						Type: MessageTypeUser,
						UUID: deps.UUID(),
						Content: []ContentBlock{
							{Type: ContentBlockText, Text: "Output token limit hit. Resume directly — no apology, no recap of what you were doing. Pick up mid-thought if that is where the cut happened. Break remaining work into smaller pieces."},
						},
						IsMeta: true,
					}
					state = &LoopState{
						Messages:                     append(append(messagesForQuery, assistantMsgs...), recoveryMsg),
						ToolUseContext:               toolUseContext,
						AutoCompactTracking:          tracking,
						MaxOutputTokensRecoveryCount: maxOutputTokensRecoveryCount + 1,
						HasAttemptedReactiveCompact:  hasAttemptedReactiveCompact,
						TurnCount:                    turnCount,
						Transition:                   &StateTransition{Reason: TransitionMaxOutputTokensRecovery, Attempt: maxOutputTokensRecoveryCount + 1},
					}
					continue
				}

				// Recovery exhausted — surface the withheld error (TS L1254-1255)
				ch <- StreamEvent{Type: EventAssistant, Message: &lastMsg}
			}

			// 11c: Skip stop hooks when last message is API error (TS L1258-1264)
			if lastMsg.IsApiError {
				return Terminal{Reason: TerminalCompleted, TurnCount: state.TurnCount}
			}

			// 11d: Run stop hooks (TS L1267-1306)
			stopHookActive := state.StopHookActive != nil && *state.StopHookActive
			stopResult := stopHookRegistry.HandleStopHooks(
				ctx, state, lastMsg.StopReason, toolUseContext, stopHookActive, ch,
			)

			if stopResult.PreventContinuation {
				return Terminal{Reason: TerminalStopHookPrevented, TurnCount: state.TurnCount}
			}

			if len(stopResult.BlockingErrors) > 0 {
				// Inject blocking errors and retry (TS L1282-1306)
				state = &LoopState{
					Messages:                     append(append(messagesForQuery, assistantMsgs...), stopResult.BlockingErrors...),
					ToolUseContext:               toolUseContext,
					AutoCompactTracking:          tracking,
					MaxOutputTokensRecoveryCount: 0,
					HasAttemptedReactiveCompact:  hasAttemptedReactiveCompact, // preserve guard (TS L1292-1296)
					StopHookActive:               boolPtr(true),
					TurnCount:                    turnCount,
					Transition:                   &StateTransition{Reason: TransitionStopHookBlocking},
				}
				continue
			}

			// 11e: Token budget continuation (TS L1308-1355)
			if config.Gates.TokenBudget && params.TaskBudget != nil {
				agentID := ""
				if toolUseContext != nil {
					agentID = toolUseContext.AgentID
				}
				decision := CheckTokenBudget(
					budgetTracker,
					agentID,
					params.TaskBudget.Total,
					state.TurnCount,
				)

				if decision.Action == BudgetActionContinue {
					logger.Info("token budget continuation",
						"count", decision.ContinuationCount,
						"pct", decision.Pct,
					)
					nudgeMsg := Message{
						Type: MessageTypeUser,
						UUID: deps.UUID(),
						Content: []ContentBlock{
							{Type: ContentBlockText, Text: decision.NudgeMessage},
						},
						IsMeta: true,
					}
					state = &LoopState{
						Messages:                     append(append(messagesForQuery, assistantMsgs...), nudgeMsg),
						ToolUseContext:               toolUseContext,
						AutoCompactTracking:          tracking,
						MaxOutputTokensRecoveryCount: 0,
						HasAttemptedReactiveCompact:  false,
						TurnCount:                    turnCount,
						Transition:                   &StateTransition{Reason: TransitionTokenBudgetContinuation},
					}
					continue
				}

				if decision.CompletionEvent != nil {
					if decision.CompletionEvent.DiminishingReturns {
						logger.Info("token budget early stop: diminishing returns",
							"pct", decision.CompletionEvent.Pct)
					}
				}
			}

			return Terminal{Reason: TerminalCompleted, TurnCount: state.TurnCount}
		}

		// ---------- Phase 12: Execute Tools (TS L1360-1408) ----------
		shouldPreventContinuation := false

		// Emit tool_use events for each tool block
		for _, block := range toolUseBlocks {
			ch <- StreamEvent{Type: EventToolUse, Text: block.Name}
		}

		// Delegate to deps.ExecuteTools (wired to tool/orchestration or streaming_executor)
		execResult := deps.ExecuteTools(ctx, toolUseBlocks, &assistantMsgs[len(assistantMsgs)-1], toolUseContext, config.Gates.StreamingToolExecution)
		var toolResultMessages []Message
		if execResult != nil {
			toolResultMessages = execResult.Messages

			// Apply context modifiers from tool results (TS L1444-1454)
			for _, modifier := range execResult.ContextModifiers {
				if toolUseContext != nil {
					toolUseContext = modifier(toolUseContext)
				}
			}
		}

		// Emit tool_result events
		for i := range toolResultMessages {
			ch <- StreamEvent{Type: EventToolResult, Message: &toolResultMessages[i]}
		}
		toolResults = append(toolResults, toolResultMessages...)

		// Check for hook_stopped_continuation in tool results
		for _, msg := range toolResultMessages {
			if msg.Attachment != nil && msg.Attachment.Type == "hook_stopped_continuation" {
				shouldPreventContinuation = true
			}
		}

		// Generate tool use summary (TS L1412-1474) — fire-and-forget goroutine
		var nextPendingToolUseSummary <-chan *ToolUseSummaryMessage
		if config.Gates.EmitToolUseSummaries && len(toolUseBlocks) > 0 {
			summaryCh := make(chan *ToolUseSummaryMessage, 1)
			nextPendingToolUseSummary = summaryCh
			toolIDs := make([]string, len(toolUseBlocks))
			for i, b := range toolUseBlocks {
				toolIDs[i] = b.ID
			}
			go func() {
				defer close(summaryCh)
				summary := generateToolUseSummary(toolUseBlocks, toolResultMessages)
				if summary != "" {
					summaryCh <- &ToolUseSummaryMessage{
						UUID:                deps.UUID(),
						Summary:             summary,
						PrecedingToolUseIDs: toolIDs,
					}
				}
			}()
		}

		// ---------- Phase 12b: Post-tool abort check (TS L1484-1516) ----------
		select {
		case <-ctx.Done():
			ch <- StreamEvent{Type: EventSystem, Message: &Message{
				Type:    MessageTypeSystem,
				Content: []ContentBlock{{Type: ContentBlockText, Text: "[Request interrupted by user during tool execution]"}},
			}}
			return Terminal{Reason: TerminalAbortedTools, TurnCount: state.TurnCount}
		default:
		}

		// ---------- Phase 12c: Attachment pipeline + memory consume (TS L1577-1650) ----------
		// Consume memory prefetch
		if memoryPrefetchCh != nil {
			if prefetchedMemories := <-memoryPrefetchCh; len(prefetchedMemories) > 0 {
				for i := range prefetchedMemories {
					ch <- StreamEvent{Type: EventAttachment, Message: &prefetchedMemories[i]}
				}
				toolResults = append(toolResults, prefetchedMemories...)
			}
		}

		// Get attachment messages (file changes, date changes, etc.)
		if attachments := deps.GetAttachmentMessages(toolUseContext); len(attachments) > 0 {
			for i := range attachments {
				ch <- StreamEvent{Type: EventAttachment, Message: &attachments[i]}
			}
			toolResults = append(toolResults, attachments...)
		}

		// Hook prevented continuation (TS L1519-1521)
		if shouldPreventContinuation {
			return Terminal{Reason: TerminalHookStopped, TurnCount: state.TurnCount}
		}

		// Track post-compact turns (TS L1523-1533)
		if tracking != nil && tracking.Compacted {
			tracking.TurnCounter++
		}

		// ---------- Phase 13: MaxTurns check (TS L1704-1712) ----------
		nextTurnCount := turnCount + 1
		if params.MaxTurns > 0 && nextTurnCount > params.MaxTurns {
			ch <- StreamEvent{Type: EventAttachment, Message: &Message{
				Type: MessageTypeAttachment,
				Attachment: &AttachmentData{
					Type:      "max_turns_reached",
					MaxTurns:  params.MaxTurns,
					TurnCount: nextTurnCount,
				},
			}}
			return Terminal{
				Reason:    TerminalMaxTurns,
				TurnCount: nextTurnCount,
			}
		}

		// ---------- Phase 14: Refresh tools between turns (TS L1660-1671) ----------
		if toolUseContext != nil && toolUseContext.Options.RefreshTools != nil {
			toolUseContext.Options.Tools = toolUseContext.Options.RefreshTools()
		}

		// ---------- Phase 15: Prepare next iteration (TS L1715-1728) ----------
		state = &LoopState{
			Messages:                     append(append(messagesForQuery, assistantMsgs...), toolResults...),
			ToolUseContext:               toolUseContext,
			AutoCompactTracking:          tracking,
			TurnCount:                    nextTurnCount,
			MaxOutputTokensRecoveryCount: 0,
			HasAttemptedReactiveCompact:  false,
			MaxOutputTokensOverride:      nil,
			PendingToolUseSummary:        nextPendingToolUseSummary,
			StopHookActive:               state.StopHookActive,
			Transition:                   &StateTransition{Reason: TransitionNextTurn},
		}
	}
}

// ---------------------------------------------------------------------------
// Helper functions used by QueryLoop
// ---------------------------------------------------------------------------

type tokenWarningLevel int

const (
	tokenWarningNone     tokenWarningLevel = 0
	tokenWarningWarning  tokenWarningLevel = 1
	tokenWarningBlocking tokenWarningLevel = 2
)

// calculateTokenWarningState checks the current token usage against limits.
// snipTokensFreed is subtracted from the estimate to account for recent snip.
func calculateTokenWarningState(messages []Message, systemPrompt string, snipTokensFreed int) tokenWarningLevel {
	// Estimate total tokens (rough: 4 chars ≈ 1 token)
	totalChars := len(systemPrompt)
	for _, msg := range messages {
		for _, block := range msg.Content {
			totalChars += len(block.Text) + len(block.Thinking) + len(string(block.Input))
		}
	}
	estimatedTokens := totalChars/4 - snipTokensFreed

	// Default context window: 200k tokens
	const contextWindow = 200000
	ratio := float64(estimatedTokens) / float64(contextWindow)

	switch {
	case ratio >= 0.95:
		return tokenWarningBlocking
	case ratio >= 0.85:
		return tokenWarningWarning
	default:
		return tokenWarningNone
	}
}

// getMessagesAfterCompactBoundary returns messages after the last compact_boundary.
// Maps to TypeScript getMessagesAfterCompactBoundary() in utils/messages.ts.
func getMessagesAfterCompactBoundary(messages []Message) []Message {
	lastBoundary := -1
	for i, msg := range messages {
		if msg.Subtype == "compact_boundary" || msg.Subtype == "snip_boundary" {
			lastBoundary = i
		}
	}
	if lastBoundary == -1 {
		result := make([]Message, len(messages))
		copy(result, messages)
		return result
	}
	// Include the boundary message itself
	result := make([]Message, len(messages)-lastBoundary)
	copy(result, messages[lastBoundary:])
	return result
}

// prependUserContext prepends user context to the first user message for API calls.
func prependUserContext(messages []Message, userContext map[string]string) []Message {
	if len(userContext) == 0 {
		return messages
	}
	result := make([]Message, len(messages))
	copy(result, messages)
	for i, msg := range result {
		if msg.Type == MessageTypeUser {
			var prefix string
			for k, v := range userContext {
				prefix += "<" + k + ">\n" + v + "\n</" + k + ">\n"
			}
			if len(msg.Content) > 0 && msg.Content[0].Type == ContentBlockText {
				newContent := make([]ContentBlock, len(msg.Content))
				copy(newContent, msg.Content)
				newContent[0] = ContentBlock{
					Type: ContentBlockText,
					Text: prefix + newContent[0].Text,
				}
				result[i].Content = newContent
			}
			break
		}
	}
	return result
}

// buildToolDefinitions is now delegated to deps.BuildToolDefinitions().
// See QueryDeps interface in deps.go.

// getModel returns the model to use for the query.
func getModel(params QueryParams) string {
	if params.ToolUseContext != nil && params.ToolUseContext.Options.MainLoopModel != "" {
		return params.ToolUseContext.Options.MainLoopModel
	}
	return ""
}

// getThinkingConfig returns the thinking configuration for the query.
func getThinkingConfig(params QueryParams) *ThinkingConfig {
	if params.ToolUseContext != nil {
		return params.ToolUseContext.Options.ThinkingConfig
	}
	return nil
}

// isPromptTooLongMsg checks if an assistant Message is a prompt-too-long error.
func isPromptTooLongMsg(msg Message) bool {
	if !msg.IsApiError {
		return false
	}
	for _, block := range msg.Content {
		if block.Type == ContentBlockText && (contains(block.Text, "prompt is too long") || contains(block.Text, "prompt_too_long")) {
			return true
		}
	}
	return false
}

// isMediaSizeErrorMsg checks if an assistant Message is a media size error.
func isMediaSizeErrorMsg(msg Message) bool {
	if !msg.IsApiError {
		return false
	}
	for _, block := range msg.Content {
		if block.Type == ContentBlockText {
			if contains(block.Text, "image exceeds") ||
				contains(block.Text, "image is too large") ||
				contains(block.Text, "too many images") ||
				contains(block.Text, "media_size") {
				return true
			}
		}
	}
	return false
}

// isWithheldMaxOutputTokensMsg checks if a message should be withheld for
// max_output_tokens recovery. Maps to TS isWithheldMaxOutputTokens().
func isWithheldMaxOutputTokensMsg(msg Message) bool {
	if msg.Type != MessageTypeAssistant {
		return false
	}
	if msg.StopReason == "max_tokens" {
		return true
	}
	if msg.IsApiError {
		for _, block := range msg.Content {
			if block.Type == ContentBlockText && contains(block.Text, "max_output_tokens") {
				return true
			}
		}
	}
	return false
}

// yieldMissingToolResults emits synthetic tool_result blocks for any tool_use
// blocks in the assistant messages that lack matching results.
func yieldMissingToolResults(assistantMsgs []Message, errorMsg string, deps QueryDeps, ch chan<- StreamEvent) {
	for _, msg := range assistantMsgs {
		for _, block := range msg.Content {
			if block.Type == ContentBlockToolUse {
				errResult := createToolErrorMessage(block.ID, errorMsg, msg.UUID, deps.UUID())
				ch <- StreamEvent{Type: EventToolResult, Message: &errResult}
			}
		}
	}
}

// estimateContextTokens estimates the total context tokens for a conversation.
// Maps to finalContextTokensFromLastResponse() in TS query.ts.
// Uses a rough 4 chars ≈ 1 token heuristic.
func estimateContextTokens(messages []Message, systemPrompt string) int {
	totalChars := len(systemPrompt)
	for _, msg := range messages {
		for _, block := range msg.Content {
			totalChars += len(block.Text) + len(block.Thinking)
			if s, ok := block.Content.(string); ok {
				totalChars += len(s)
			}
			totalChars += len(string(block.Input))
		}
	}
	return totalChars / 4
}

func contains(s, substr string) bool {
	if len(substr) == 0 {
		return true
	}
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// boolPtr returns a pointer to a bool value.
func boolPtr(b bool) *bool {
	return &b
}

// generateToolUseSummary builds a concise human-readable summary of tool calls
// and their results. Maps to TS generateToolUseSummary in toolUseSummaryGenerator.ts.
func generateToolUseSummary(blocks []ToolUseBlock, results []Message) string {
	if len(blocks) == 0 {
		return ""
	}
	var parts []string
	for _, block := range blocks {
		// Find corresponding result
		resultSnippet := ""
		for _, msg := range results {
			for _, cb := range msg.Content {
				if cb.Type == ContentBlockToolResult && cb.ToolUseID == block.ID {
					if s, ok := cb.Content.(string); ok {
						if len(s) > 120 {
							resultSnippet = s[:120] + "..."
						} else {
							resultSnippet = s
						}
					}
				}
			}
		}
		entry := block.Name
		if resultSnippet != "" {
			entry += ": " + resultSnippet
		}
		parts = append(parts, entry)
	}
	summary := ""
	for i, p := range parts {
		if i > 0 {
			summary += "; "
		}
		summary += p
	}
	return summary
}

// createToolErrorMessage creates a tool error result message.
func createToolErrorMessage(toolUseID, errorMsg, assistantUUID, uuid string) Message {
	return Message{
		Type: MessageTypeUser,
		UUID: uuid,
		Content: []ContentBlock{
			{
				Type:      ContentBlockToolResult,
				ToolUseID: toolUseID,
				Content:   "<tool_use_error>" + errorMsg + "</tool_use_error>",
				IsError:   true,
			},
		},
		ToolUseResult:           errorMsg,
		SourceToolAssistantUUID: assistantUUID,
	}
}
