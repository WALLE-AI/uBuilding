package agents

import (
	"context"
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
	config  EngineConfig
	deps    QueryDeps
	logger  *slog.Logger

	// Session state
	sessionID       string
	messages        []Message
	totalUsage      Usage
	totalCostUSD    float64
	turnCount       int
	startTime       time.Time
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
func (e *QueryEngine) runQuery(ctx context.Context, prompt string, ch chan<- StreamEvent) {
	startTime := time.Now()

	// 1. Build user message and append to history
	userMsg := Message{
		Type: MessageTypeUser,
		UUID: e.deps.UUID(),
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
	ch <- StreamEvent{
		Type: EventSystemInit,
	}

	// Record transcript
	e.recordTranscript("user_message", userMsg)

	// 3. Build system prompt
	systemPrompt := e.buildSystemPrompt()

	// 4. Build tool use context
	var toolCtx *ToolUseContext
	if e.buildToolUseContext != nil {
		toolCtx = e.buildToolUseContext(ctx, messages)
	} else {
		toolCtx = e.defaultToolUseContext(ctx)
	}

	// 5. Build query params
	params := QueryParams{
		Messages:     messages,
		SystemPrompt: systemPrompt,
		ToolUseContext: toolCtx,
		QuerySource:  "repl_main_thread",
		MaxTurns:     e.config.MaxTurns,
		TaskBudget:   e.config.TaskBudget,
	}

	// 6. Run the query loop
	terminal := QueryLoop(ctx, params, e.deps, ch, e.logger)

	// 7. Update engine state with results
	e.mu.Lock()
	e.turnCount += terminal.TurnCount

	// Collect new messages from the query loop
	// The queryLoop sends messages through the channel; we also need to update
	// our internal history from the events that were sent.
	e.mu.Unlock()

	// 8. Emit result event
	durationMs := time.Since(startTime).Milliseconds()
	resultEvent := ResultEvent{
		Type:         "result",
		Subtype:      "success",
		DurationMs:   durationMs,
		NumTurns:     terminal.TurnCount,
		StopReason:   terminal.Reason,
		SessionID:    e.sessionID,
		TotalCostUSD: e.totalCostUSD,
		UUID:         e.deps.UUID(),
		CreatedAt:    time.Now(),
	}

	if terminal.Error != nil {
		resultEvent.Subtype = "error_during_execution"
		resultEvent.IsError = true
		resultEvent.Errors = []string{terminal.Error.Error()}
	}

	e.recordTranscript("result", resultEvent)

	ch <- StreamEvent{
		Type: EventDone,
	}
}

// buildSystemPrompt constructs the system prompt from configuration.
// This is a simplified version; the full version uses the 6-layer builder.
func (e *QueryEngine) buildSystemPrompt() string {
	prompt := ""
	if e.config.CustomSystemPrompt != "" {
		prompt = e.config.CustomSystemPrompt
	}
	if e.config.AppendSystemPrompt != "" {
		if prompt != "" {
			prompt += "\n\n"
		}
		prompt += e.config.AppendSystemPrompt
	}
	return prompt
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
