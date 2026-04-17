package agents

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// ---------------------------------------------------------------------------
// QueryEngine — session-level orchestrator
// Maps to TypeScript QueryEngine class in QueryEngine.ts
// ---------------------------------------------------------------------------

// QueryEngine manages a single conversation session. It holds the message
// history, tracks token usage, and provides SubmitMessage as the primary
// entry point for user interaction. Each call to SubmitMessage returns a
// channel of StreamEvent (replacing TypeScript's AsyncGenerator pattern).
type QueryEngine struct {
	mu sync.RWMutex

	// Configuration
	config EngineConfig
	deps   QueryDeps
	logger *slog.Logger

	// Session state
	sessionID         string
	messages          []Message
	totalUsage        Usage
	totalCostUSD      float64
	turnCount         int
	startTime         time.Time
	permissionDenials []PermissionDenial

	// Cancellation
	cancelFunc context.CancelFunc

	// Transcript recording (optional)
	transcriptCh chan TranscriptEntry

	// Tool use context builder
	buildToolUseContext func(ctx context.Context, messages []Message) *ToolUseContext
}

// TranscriptEntry records a single event for session persistence.
type TranscriptEntry struct {
	Timestamp time.Time   `json:"timestamp"`
	Type      string      `json:"type"`
	Data      interface{} `json:"data"`
}

// NewQueryEngine creates a new QueryEngine with the given configuration.
func NewQueryEngine(config EngineConfig, deps QueryDeps, opts ...EngineOption) *QueryEngine {
	e := &QueryEngine{
		config:    config,
		deps:      deps,
		logger:    slog.Default(),
		sessionID: deps.UUID(),
		startTime: time.Now(),
		messages:  make([]Message, 0, 64),
	}
	for _, opt := range opts {
		opt(e)
	}
	return e
}

// EngineOption is a functional option for configuring QueryEngine.
type EngineOption func(*QueryEngine)

// WithLogger sets a custom logger.
func WithLogger(logger *slog.Logger) EngineOption {
	return func(e *QueryEngine) {
		e.logger = logger
	}
}

// WithSessionID sets a specific session ID (e.g., for resume).
func WithSessionID(id string) EngineOption {
	return func(e *QueryEngine) {
		e.sessionID = id
	}
}

// WithTranscript enables transcript recording on the given channel.
func WithTranscript(ch chan TranscriptEntry) EngineOption {
	return func(e *QueryEngine) {
		e.transcriptCh = ch
	}
}

// WithToolUseContextBuilder sets a custom ToolUseContext builder.
func WithToolUseContextBuilder(fn func(ctx context.Context, messages []Message) *ToolUseContext) EngineOption {
	return func(e *QueryEngine) {
		e.buildToolUseContext = fn
	}
}

// SubmitMessage is the primary entry point for user interaction.
// It processes the user prompt through the query loop and returns a channel
// of StreamEvent. The channel is closed when the query completes.
// This maps to TypeScript's QueryEngine.submitMessage() AsyncGenerator.
func (e *QueryEngine) SubmitMessage(ctx context.Context, prompt string) <-chan StreamEvent {
	ch := make(chan StreamEvent, 128)

	// Create a cancellable child context
	queryCtx, cancel := context.WithCancel(ctx)

	e.mu.Lock()
	// Cancel any previous in-flight query
	if e.cancelFunc != nil {
		e.cancelFunc()
	}
	e.cancelFunc = cancel
	e.mu.Unlock()

	go func() {
		defer close(ch)
		defer cancel()
		e.runQuery(queryCtx, prompt, ch)
	}()

	return ch
}

// runQuery executes the full query lifecycle for a single user message.
// Aligns with TS QueryEngine.submitMessage() lifecycle.
func (e *QueryEngine) runQuery(ctx context.Context, prompt string, ch chan<- StreamEvent) {
	startTime := time.Now()

	// 1. Build user message and append to history
	userMsg := Message{
		Type:      MessageTypeUser,
		UUID:      e.deps.UUID(),
		Timestamp: time.Now(),
		Content: []ContentBlock{
			{Type: ContentBlockText, Text: prompt},
		},
	}

	e.mu.Lock()
	e.messages = append(e.messages, userMsg)
	messages := make([]Message, len(e.messages))
	copy(messages, e.messages)
	e.mu.Unlock()

	// 2. Emit system init event
	ch <- StreamEvent{Type: EventSystemInit}

	// Record transcript
	e.recordTranscript("user_message", userMsg)

	// 3. Build system prompt (full prompt system returns userContext/systemContext)
	systemPrompt, userContext, systemContext := e.buildSystemPrompt()

	// 4. Build tool use context
	var toolCtx *ToolUseContext
	if e.buildToolUseContext != nil {
		toolCtx = e.buildToolUseContext(ctx, messages)
	} else {
		toolCtx = e.defaultToolUseContext(ctx)
	}

	// 4b. Pre-query hooks: memory prefetch & skill discovery (TS L534-538)
	if e.config.LoadMemories != nil {
		memMsgs := e.config.LoadMemories(e.config.Cwd)
		if len(memMsgs) > 0 {
			messages = append(memMsgs, messages...)
			e.mu.Lock()
			e.messages = make([]Message, len(messages))
			copy(e.messages, messages)
			e.mu.Unlock()
		}
	}
	if e.config.DiscoverSkills != nil {
		_ = e.config.DiscoverSkills(e.config.Cwd) // skills feed into tool registration
	}

	// 4c. processUserInput — local commands, attachments (TS L540-580)
	puiResult := e.processUserInput(ctx, userMsg, messages, toolCtx)
	messages = puiResult.Messages
	toolCtx = puiResult.ToolCtx
	if puiResult.SkipQuery {
		// Emit local command output messages as events
		for _, outMsg := range puiResult.OutputMessages {
			ch <- StreamEvent{
				Type:    EventSystem,
				Message: &outMsg,
			}
		}
		return
	}

	// 5. Build query params (TS L675-686)
	params := QueryParams{
		Messages:          messages,
		SystemPrompt:      systemPrompt,
		UserContext:       userContext,
		SystemContext:     systemContext,
		ToolUseContext:    toolCtx,
		QuerySource:       "sdk",
		MaxTurns:          e.config.MaxTurns,
		TaskBudget:        e.config.TaskBudget,
		FallbackModel:     e.config.FallbackModel,
		OnCompactBoundary: e.config.OnCompactBoundary,
	}

	// 6. Run the query loop — collect events on an internal channel,
	// post-process them (usage accumulation, transcript, message sync),
	// then forward to the caller's channel.
	loopCh := make(chan StreamEvent, 128)
	go func() {
		defer close(loopCh)
		QueryLoop(ctx, params, e.deps, loopCh, e.logger)
	}()

	turnCount := 1
	var lastStopReason string

	for event := range loopCh {
		// --- Usage accumulation (TS L789-816) ---
		if event.Usage != nil {
			e.AddUsage(*event.Usage)
		}
		if event.StopReason != "" {
			lastStopReason = event.StopReason
		}

		// --- Message sync: push assistant/user/system messages to history ---
		if event.Message != nil {
			msg := event.Message
			switch {
			case msg.Type == MessageTypeAssistant:
				e.AppendMessage(*msg)
				e.recordTranscript("assistant_message", *msg)
				if msg.StopReason != "" {
					lastStopReason = msg.StopReason
				}
			case msg.Type == MessageTypeUser:
				e.AppendMessage(*msg)
				turnCount++
			case msg.Type == MessageTypeSystem && msg.Subtype == "compact_boundary":
				e.AppendMessage(*msg)
				// GC pre-compaction messages (TS L926-933)
				e.pruneMessagesBeforeLastBoundary()
				e.recordTranscript("compact_boundary", *msg)
			case msg.Type == MessageTypeAttachment:
				e.AppendMessage(*msg)
				// Handle max_turns_reached early exit (TS L842-874)
				if msg.Attachment != nil && msg.Attachment.Type == "max_turns_reached" {
					e.emitResult(ch, startTime, "error_max_turns", lastStopReason, turnCount, true,
						[]string{fmt.Sprintf("Reached maximum number of turns (%d)", msg.Attachment.MaxTurns)})
					return
				}
			}
		}

		// --- Max budget USD check (TS L972-1002) ---
		if e.config.MaxBudgetUSD > 0 {
			e.mu.RLock()
			cost := e.totalCostUSD
			e.mu.RUnlock()
			if cost >= e.config.MaxBudgetUSD {
				e.emitResult(ch, startTime, "error_max_budget_usd", lastStopReason, turnCount, true,
					[]string{fmt.Sprintf("Reached maximum budget ($%.2f)", e.config.MaxBudgetUSD)})
				return
			}
		}

		// Forward event to caller
		ch <- event
	}

	// 7. Extract result text from last assistant message (TS L1058-1133)
	e.mu.RLock()
	msgs := e.messages
	e.mu.RUnlock()

	textResult := ""
	isApiError := false
	for i := len(msgs) - 1; i >= 0; i-- {
		m := msgs[i]
		if m.Type == MessageTypeAssistant || m.Type == MessageTypeUser {
			if m.Type == MessageTypeAssistant {
				isApiError = m.IsApiError
				for _, block := range m.Content {
					if block.Type == ContentBlockText && block.Text != "" {
						textResult = block.Text
					}
				}
			}
			break
		}
	}

	// 8. Determine result subtype (TS L1082-1155)
	subtype := "success"
	isErr := isApiError
	var errors []string

	if !isResultSuccessful(msgs, lastStopReason) {
		subtype = "error_during_execution"
		isErr = true
		errors = []string{fmt.Sprintf("[ede_diagnostic] stop_reason=%s", lastStopReason)}
	}

	e.emitResult(ch, startTime, subtype, lastStopReason, turnCount, isErr, errors)
	_ = textResult // available for structured output
}

// emitResult sends the final result event and closes with EventDone.
func (e *QueryEngine) emitResult(
	ch chan<- StreamEvent,
	startTime time.Time,
	subtype string,
	stopReason string,
	turnCount int,
	isError bool,
	errors []string,
) {
	e.mu.RLock()
	usage := e.totalUsage
	cost := e.totalCostUSD
	denials := make([]PermissionDenial, len(e.permissionDenials))
	copy(denials, e.permissionDenials)
	e.mu.RUnlock()

	resultEvent := ResultEvent{
		Type:              "result",
		Subtype:           subtype,
		IsError:           isError,
		DurationMs:        time.Since(startTime).Milliseconds(),
		NumTurns:          turnCount,
		StopReason:        stopReason,
		SessionID:         e.sessionID,
		TotalCostUSD:      cost,
		Usage:             usage,
		PermissionDenials: denials,
		UUID:              e.deps.UUID(),
		CreatedAt:         time.Now(),
		Errors:            errors,
	}

	e.recordTranscript("result", resultEvent)
	ch <- StreamEvent{Type: EventResult, Data: nil, Message: nil}
	ch <- StreamEvent{Type: EventDone}
}

// pruneMessagesBeforeLastBoundary removes messages before the last compact
// boundary to free memory, matching TS L926-933.
func (e *QueryEngine) pruneMessagesBeforeLastBoundary() {
	e.mu.Lock()
	defer e.mu.Unlock()

	lastBoundary := -1
	for i, msg := range e.messages {
		if msg.Subtype == "compact_boundary" || msg.Subtype == "snip_boundary" {
			lastBoundary = i
		}
	}
	if lastBoundary > 0 {
		pruned := make([]Message, len(e.messages)-lastBoundary)
		copy(pruned, e.messages[lastBoundary:])
		e.messages = pruned
	}
}

// isResultSuccessful checks if the query ended successfully (TS isResultSuccessful).
func isResultSuccessful(messages []Message, lastStopReason string) bool {
	if lastStopReason == "end_turn" {
		return true
	}
	// Find last assistant or user message
	for i := len(messages) - 1; i >= 0; i-- {
		m := messages[i]
		if m.Type == MessageTypeAssistant {
			// Check for text or thinking content
			for _, block := range m.Content {
				if block.Type == ContentBlockText || block.Type == ContentBlockThinking {
					return true
				}
			}
			return false
		}
		if m.Type == MessageTypeUser {
			// User message with tool_result is valid terminal state
			for _, block := range m.Content {
				if block.Type == ContentBlockToolResult {
					return true
				}
			}
			return false
		}
	}
	return false
}

// buildSystemPrompt constructs the system prompt from configuration.
// When BaseSystemPrompt is set (legacy mode), uses the simplified 6-layer approach.
// Otherwise, delegates to prompt.BuildFullSystemPrompt for the complete prompt
// system matching TypeScript QueryEngine.submitMessage().
//
// Returns (systemPrompt, userContext, systemContext).
func (e *QueryEngine) buildSystemPrompt() (string, map[string]string, map[string]string) {
	// --- Legacy path: use BaseSystemPrompt if explicitly set ---
	if e.config.BaseSystemPrompt != "" {
		var layers []string
		layers = append(layers, e.config.BaseSystemPrompt)
		if e.config.CustomSystemPrompt != "" {
			layers = append(layers, e.config.CustomSystemPrompt)
		}
		if e.config.AppendSystemPrompt != "" {
			layers = append(layers, e.config.AppendSystemPrompt)
		}
		p := ""
		for i, layer := range layers {
			if i > 0 {
				p += "\n\n"
			}
			p += layer
		}
		return p, nil, nil
	}

	// --- Full prompt system path (Phase 4) ---
	// Use the injected BuildSystemPromptFn which wraps prompt.BuildFullSystemPrompt.
	// This avoids an import cycle (agents → prompt → agents).
	if e.config.BuildSystemPromptFn != nil {
		return e.config.BuildSystemPromptFn()
	}

	// Fallback: empty prompt (caller should set either BaseSystemPrompt or BuildSystemPromptFn)
	return "", nil, nil
}

// processUserInputResult holds the output of processUserInput.
type processUserInputResult struct {
	Messages  []Message
	ToolCtx   *ToolUseContext
	SkipQuery bool

	// Model overrides from prompt commands
	ModelOverride string
	AllowedTools  []string

	// OutputMessages are local command results to emit as events.
	// Only set when SkipQuery is true.
	OutputMessages []Message
}

// processUserInput processes the user prompt before entering the query loop.
// Maps to TypeScript processUserInput() in QueryEngine.ts L540-580.
// Handles local command detection, attachment injection, and user input
// discovery (which runs during streaming to overlap with model execution).
func (e *QueryEngine) processUserInput(
	ctx context.Context,
	userMsg Message,
	messages []Message,
	toolCtx *ToolUseContext,
) processUserInputResult {
	result := processUserInputResult{
		Messages: messages,
		ToolCtx:  toolCtx,
	}

	// Extract user text for command/attachment parsing
	userText := ""
	for _, block := range userMsg.Content {
		if block.Type == ContentBlockText {
			userText = block.Text
			break
		}
	}

	// ---------------------------------------------------------------
	// 1. Local command handling (TS L558-604)
	// ---------------------------------------------------------------
	if e.config.Commands != nil {
		parsed := ParseSlashCommand(userText, e.config.Commands)
		if parsed != nil && parsed.Command != nil {
			cmd := parsed.Command

			switch cmd.Type {
			case CommandTypeLocal:
				if cmd.Call != nil {
					cmdCtx := CommandContext{
						Ctx:      ctx,
						Messages: messages,
						ToolCtx:  toolCtx,
						SetMessages: func(fn func([]Message) []Message) {
							e.mu.Lock()
							e.messages = fn(e.messages)
							result.Messages = make([]Message, len(e.messages))
							copy(result.Messages, e.messages)
							e.mu.Unlock()
						},
					}
					cmdResult, err := cmd.Call(parsed.Args, cmdCtx)
					if err != nil {
						e.logger.Error("local command error", "command", parsed.Name, "error", err)
					} else if cmdResult != nil {
						switch cmdResult.Type {
						case "text":
							// Emit as a system message
							outMsg := Message{
								Type:    MessageTypeSystem,
								Subtype: "local_command",
								UUID:    e.deps.UUID(),
								Content: []ContentBlock{{Type: ContentBlockText, Text: cmdResult.Value}},
							}
							result.OutputMessages = append(result.OutputMessages, outMsg)
							result.SkipQuery = true
							return result

						case "compact":
							// Compact is delegated to the autocompact dep
							if e.deps != nil {
								compactResult := e.deps.Autocompact(ctx, messages, toolCtx, "", "slash_command")
								if compactResult != nil && compactResult.Applied {
									outMsg := Message{
										Type:    MessageTypeSystem,
										Subtype: "local_command",
										UUID:    e.deps.UUID(),
										Content: []ContentBlock{{Type: ContentBlockText, Text: fmt.Sprintf("Compacted conversation (%d tokens saved, %d messages remaining)", compactResult.TokensSaved, len(compactResult.Messages))}},
									}
									result.OutputMessages = append(result.OutputMessages, outMsg)
									// Update messages to compacted version
									e.mu.Lock()
									e.messages = compactResult.Messages
									e.mu.Unlock()
								} else {
									outMsg := Message{
										Type:    MessageTypeSystem,
										Subtype: "local_command",
										UUID:    e.deps.UUID(),
										Content: []ContentBlock{{Type: ContentBlockText, Text: "No compaction needed."}},
									}
									result.OutputMessages = append(result.OutputMessages, outMsg)
								}
							}
							result.SkipQuery = true
							return result

						case "skip":
							result.SkipQuery = true
							return result
						}
					}
				}

			case CommandTypePrompt:
				if cmd.GetPrompt != nil {
					cmdCtx := CommandContext{
						Ctx:      ctx,
						Messages: messages,
						ToolCtx:  toolCtx,
					}
					promptBlocks, err := cmd.GetPrompt(parsed.Args, cmdCtx)
					if err != nil {
						e.logger.Error("prompt command error", "command", parsed.Name, "error", err)
					} else if len(promptBlocks) > 0 {
						// Replace the user message content with the prompt expansion
						expandedMsg := Message{
							Type:      MessageTypeUser,
							UUID:      userMsg.UUID,
							Timestamp: userMsg.Timestamp,
							Content:   promptBlocks,
						}
						// Replace last message (the user message) with expanded
						result.Messages = append(result.Messages[:len(result.Messages)-1], expandedMsg)

						// Apply command overrides
						if cmd.Model != "" {
							result.ModelOverride = cmd.Model
						}
						if len(cmd.AllowedTools) > 0 {
							result.AllowedTools = cmd.AllowedTools
						}
					}
				}
			}

			// If command was found but not handled above, fall through to normal processing
			// (e.g., unknown command type or nil handler)
		}
	}

	// ---------------------------------------------------------------
	// 2. Attachment injection (TS L606-635)
	// ---------------------------------------------------------------
	if userText != "" {
		attachments := AtMentionedFilesProvider(ctx, AttachmentOptions{
			Input: userText,
			Cwd:   e.config.Cwd,
		})
		if len(attachments) > 0 {
			result.Messages = append(result.Messages, attachments...)
		}
	}

	return result
}

// defaultToolUseContext creates a default ToolUseContext.
func (e *QueryEngine) defaultToolUseContext(ctx context.Context) *ToolUseContext {
	childCtx, cancel := context.WithCancel(ctx)
	return &ToolUseContext{
		Ctx:           childCtx,
		CancelFunc:    cancel,
		ReadFileState: NewFileStateCache(),
		Options: ToolUseOptions{
			MainLoopModel:      e.config.UserSpecifiedModel,
			Verbose:            e.config.Verbose,
			ThinkingConfig:     e.config.ThinkingConfig,
			CustomSystemPrompt: e.config.CustomSystemPrompt,
			AppendSystemPrompt: e.config.AppendSystemPrompt,
		},
	}
}

// Interrupt cancels the current in-flight query.
func (e *QueryEngine) Interrupt() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.cancelFunc != nil {
		e.cancelFunc()
		e.cancelFunc = nil
	}
}

// GetMessages returns a copy of the current message history.
func (e *QueryEngine) GetMessages() []Message {
	e.mu.RLock()
	defer e.mu.RUnlock()
	result := make([]Message, len(e.messages))
	copy(result, e.messages)
	return result
}

// GetSessionID returns the session identifier.
func (e *QueryEngine) GetSessionID() string {
	return e.sessionID
}

// GetUsage returns the total accumulated token usage.
func (e *QueryEngine) GetUsage() Usage {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.totalUsage
}

// AppendMessage adds a message to the history (used by queryLoop to sync state).
func (e *QueryEngine) AppendMessage(msg Message) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.messages = append(e.messages, msg)
}

// AddUsage accumulates token usage from a single API call.
func (e *QueryEngine) AddUsage(usage Usage) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.totalUsage.InputTokens += usage.InputTokens
	e.totalUsage.OutputTokens += usage.OutputTokens
	e.totalUsage.CacheCreationInputTokens += usage.CacheCreationInputTokens
	e.totalUsage.CacheReadInputTokens += usage.CacheReadInputTokens
}

// UpdateMessages replaces the internal message history.
// Used by external callers to inject messages (e.g., slash commands, resume).
func (e *QueryEngine) UpdateMessages(msgs []Message) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.messages = make([]Message, len(msgs))
	copy(e.messages, msgs)
}

// ClearHistory resets the message history and turn count (e.g., /clear command).
func (e *QueryEngine) ClearHistory() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.messages = make([]Message, 0, 64)
	e.turnCount = 0
	e.permissionDenials = nil
}

// AddPermissionDenial records a permission denial.
func (e *QueryEngine) AddPermissionDenial(denial PermissionDenial) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.permissionDenials = append(e.permissionDenials, denial)
}

// GetPermissionDenials returns a copy of the permission denials.
func (e *QueryEngine) GetPermissionDenials() []PermissionDenial {
	e.mu.RLock()
	defer e.mu.RUnlock()
	result := make([]PermissionDenial, len(e.permissionDenials))
	copy(result, e.permissionDenials)
	return result
}

// recordTranscript writes an entry to the transcript channel if enabled.
func (e *QueryEngine) recordTranscript(entryType string, data interface{}) {
	if e.transcriptCh != nil {
		select {
		case e.transcriptCh <- TranscriptEntry{
			Timestamp: time.Now(),
			Type:      entryType,
			Data:      data,
		}:
		default:
			// Don't block if transcript channel is full
		}
	}
}
