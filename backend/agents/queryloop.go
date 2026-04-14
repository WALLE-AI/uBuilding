package agents

import (
	"context"
	"log/slog"
)

// QueryLoop is the core query loop state machine. It drives the iterative
// cycle of: context compression → API call → tool execution → continue/stop.
//
// Corresponds to TypeScript's queryLoop() in query.ts (~1730 lines).
// The full implementation handles 7+ exit paths, max_output_tokens recovery,
// tombstone/withheld mechanisms, and reactive compaction.
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
	state := &LoopState{
		Messages:       params.Messages,
		ToolUseContext: params.ToolUseContext,
		TurnCount:      0,
	}

	config := BuildQueryConfig(
		"", // sessionID populated by engine
		QueryGates{
			HistorySnip:     true,
			ContextCollapse: true,
			TokenBudget:     true,
		},
	)

	_ = config // will be used in full implementation

	for {
		// Check cancellation
		select {
		case <-ctx.Done():
			return Terminal{
				Reason:    TerminalAbortedStreaming,
				TurnCount: state.TurnCount,
			}
		default:
		}

		// ---------- Phase 1: Context Compression Pipeline ----------
		state.Messages = applyContextPipeline(ctx, state.Messages, deps, params.SystemPrompt, params.ToolUseContext)

		// ---------- Phase 2: Token Limit Check ----------
		warningState := calculateTokenWarningState(state.Messages, params.SystemPrompt)
		if warningState == tokenWarningBlocking {
			logger.Warn("token limit blocking, attempting reactive compact")
			if !state.HasAttemptedReactiveCompact {
				state.HasAttemptedReactiveCompact = true
				compactResult := deps.Autocompact(ctx, state.Messages, params.ToolUseContext, params.SystemPrompt, params.QuerySource)
				if compactResult != nil && compactResult.Applied {
					state.Messages = compactResult.Messages
					ch <- StreamEvent{Type: EventCompactBoundary}
					continue
				}
			}
			return Terminal{
				Reason:    TerminalPromptTooLong,
				TurnCount: state.TurnCount,
			}
		}

		// ---------- Phase 3: Build tool definitions for API ----------
		toolDefs := buildToolDefinitions(params.ToolUseContext)

		// ---------- Phase 4: Call Model (streaming) ----------
		callParams := CallModelParams{
			Messages:       state.Messages,
			SystemPrompt:   params.SystemPrompt,
			Model:          getModel(params),
			FallbackModel:  params.FallbackModel,
			ThinkingConfig: getThinkingConfig(params),
			Tools:          toolDefs,
			QuerySource:    params.QuerySource,
			TaskBudget:     params.TaskBudget,
		}

		if state.MaxOutputTokensOverride != nil {
			callParams.MaxOutputTokens = state.MaxOutputTokensOverride
		}

		modelCh, err := deps.CallModel(ctx, callParams)
		if err != nil {
			logger.Error("callModel failed", "error", err)
			ch <- StreamEvent{Type: EventError, Error: err.Error()}
			return Terminal{
				Reason:    TerminalModelError,
				TurnCount: state.TurnCount,
				Error:     err,
			}
		}

		// Consume streaming events and collect the assistant message
		var assistantMsg *Message
		var toolUseBlocks []ToolUseBlock

		for event := range modelCh {
			switch event.Type {
			case EventTextDelta, EventThinkingDelta:
				// Forward streaming deltas to the caller
				ch <- event

			case EventAssistant:
				assistantMsg = event.Message
				// Extract tool use blocks from the final assistant message
				if assistantMsg != nil {
					for _, block := range assistantMsg.Content {
						if block.Type == ContentBlockToolUse {
							toolUseBlocks = append(toolUseBlocks, ToolUseBlock{
								ID:    block.ID,
								Name:  block.Name,
								Input: block.Input,
							})
						}
					}
				}

			case EventError:
				// Handle API errors
				if isPromptTooLongError(event.Error) {
					if !state.HasAttemptedReactiveCompact {
						state.HasAttemptedReactiveCompact = true
						compactResult := deps.Autocompact(ctx, state.Messages, params.ToolUseContext, params.SystemPrompt, params.QuerySource)
						if compactResult != nil && compactResult.Applied {
							state.Messages = compactResult.Messages
							ch <- StreamEvent{Type: EventCompactBoundary}
							continue
						}
					}
					return Terminal{Reason: TerminalPromptTooLong, TurnCount: state.TurnCount}
				}
				ch <- event
				return Terminal{
					Reason:    TerminalModelError,
					TurnCount: state.TurnCount,
				}

			case EventRequestStart:
				ch <- event

			default:
				ch <- event
			}
		}

		// No assistant message received (stream ended unexpectedly)
		if assistantMsg == nil {
			return Terminal{
				Reason:    TerminalModelError,
				TurnCount: state.TurnCount,
			}
		}

		// Set UUID if missing
		if assistantMsg.UUID == "" {
			assistantMsg.UUID = deps.UUID()
		}

		// Append assistant message to history
		state.Messages = append(state.Messages, *assistantMsg)

		// Accumulate usage
		if assistantMsg.Usage != nil {
			ch <- StreamEvent{
				Type:    EventAssistant,
				Message: assistantMsg,
			}
		}

		state.TurnCount++

		// ---------- Phase 5: Handle stop_reason ----------

		// 5a: max_tokens recovery
		if assistantMsg.StopReason == "max_tokens" {
			if state.MaxOutputTokensRecoveryCount < MaxOutputTokensRecoveryLimit {
				state.MaxOutputTokensRecoveryCount++
				escalated := EscalatedMaxTokens
				state.MaxOutputTokensOverride = &escalated

				// Inject recovery message
				recoveryMsg := Message{
					Type: MessageTypeUser,
					UUID: deps.UUID(),
					Content: []ContentBlock{
						{Type: ContentBlockText, Text: "[Request interrupted due to output length limit. Continue from where you left off.]"},
					},
					IsMeta: true,
				}
				state.Messages = append(state.Messages, recoveryMsg)
				continue
			}
			return Terminal{
				Reason:    TerminalCompleted,
				TurnCount: state.TurnCount,
			}
		}

		// 5b: No tool use → completed
		if len(toolUseBlocks) == 0 {
			return Terminal{
				Reason:    TerminalCompleted,
				TurnCount: state.TurnCount,
			}
		}

		// ---------- Phase 6: Execute Tools ----------
		toolResultMessages := executeTools(ctx, toolUseBlocks, assistantMsg, params.ToolUseContext, deps, ch, logger)

		// Append tool result messages to history
		state.Messages = append(state.Messages, toolResultMessages...)

		// ---------- Phase 7: MaxTurns Check ----------
		if params.MaxTurns > 0 && state.TurnCount >= params.MaxTurns {
			return Terminal{
				Reason:    TerminalMaxTurns,
				TurnCount: state.TurnCount,
			}
		}

		// ---------- Phase 8: Token Budget Check ----------
		budgetDecision := checkTokenBudget(state, params.TaskBudget)
		if budgetDecision == budgetStop {
			return Terminal{
				Reason:    TerminalCompleted,
				TurnCount: state.TurnCount,
			}
		}

		// Continue to next iteration
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
func calculateTokenWarningState(messages []Message, systemPrompt string) tokenWarningLevel {
	// Estimate total tokens (rough: 4 chars ≈ 1 token)
	totalChars := len(systemPrompt)
	for _, msg := range messages {
		for _, block := range msg.Content {
			totalChars += len(block.Text) + len(block.Thinking) + len(string(block.Input))
		}
	}
	estimatedTokens := totalChars / 4

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

// applyContextPipeline runs the compression pipeline on messages.
func applyContextPipeline(ctx context.Context, messages []Message, deps QueryDeps, systemPrompt string, toolCtx *ToolUseContext) []Message {
	// Phase 1: Microcompact (local, zero API cost)
	microResult := deps.Microcompact(messages, toolCtx, "")
	if microResult != nil && microResult.Applied {
		messages = microResult.Messages
	}

	// Phase 2: Autocompact is handled by the caller when token budget triggers

	return messages
}

// buildToolDefinitions converts tools in the context to API tool definitions.
func buildToolDefinitions(toolCtx *ToolUseContext) []ToolDefinition {
	if toolCtx == nil {
		return nil
	}
	// Tools are provided through the ToolUseContext.Options
	// In the full implementation, this iterates over registered tools and
	// calls Tool.Prompt() + Tool.InputSchema()
	return nil
}

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

// isPromptTooLongError checks if an error message indicates prompt too long.
func isPromptTooLongError(errMsg string) bool {
	return contains(errMsg, "prompt is too long") || contains(errMsg, "prompt_too_long")
}

func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

type budgetDecision int

const (
	budgetContinue budgetDecision = 0
	budgetStop     budgetDecision = 1
)

// checkTokenBudget evaluates whether the task should continue based on budget.
func checkTokenBudget(state *LoopState, budget *TaskBudget) budgetDecision {
	if budget == nil {
		return budgetContinue
	}
	// Simple check: if remaining budget is exhausted, stop
	if budget.Remaining <= 0 && budget.Total > 0 {
		return budgetStop
	}
	return budgetContinue
}

// executeTools runs tool use blocks and returns the result messages.
// This is a simplified version; the full implementation uses the orchestration system.
func executeTools(
	ctx context.Context,
	toolUseBlocks []ToolUseBlock,
	assistantMsg *Message,
	toolCtx *ToolUseContext,
	deps QueryDeps,
	ch chan<- StreamEvent,
	logger *slog.Logger,
) []Message {
	var results []Message

	for _, block := range toolUseBlocks {
		select {
		case <-ctx.Done():
			// Cancelled: emit error results for remaining tools
			results = append(results, createToolErrorMessage(block.ID, "Cancelled by user", assistantMsg.UUID, deps.UUID()))
			continue
		default:
		}

		// Emit tool use event
		ch <- StreamEvent{
			Type: EventToolUse,
			Text: block.Name,
		}

		// For now, create a placeholder result since actual tool execution
		// will be wired in Step 6 (tool orchestration)
		result := Message{
			Type: MessageTypeUser,
			UUID: deps.UUID(),
			Content: []ContentBlock{
				{
					Type:      ContentBlockToolResult,
					ToolUseID: block.ID,
					Content:   "Tool execution not yet wired. Tool: " + block.Name,
				},
			},
			ToolUseResult:           "Tool: " + block.Name,
			SourceToolAssistantUUID: assistantMsg.UUID,
		}
		results = append(results, result)

		// Emit tool result event
		ch <- StreamEvent{
			Type:    EventToolResult,
			Message: &result,
		}
	}

	return results
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
