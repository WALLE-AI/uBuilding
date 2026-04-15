package agents

import (
	"context"
)

// QueryDeps defines the injectable dependencies for the query loop.
// Corresponds to TypeScript's QueryDeps in query/deps.ts.
// Using an interface enables mock testing of the core loop.
type QueryDeps interface {
	// CallModel sends a request to the LLM and returns a channel of streaming events.
	CallModel(ctx context.Context, params CallModelParams) (<-chan StreamEvent, error)

	// Microcompact performs local micro-compaction on messages (fold repeated reads/searches).
	Microcompact(messages []Message, toolCtx *ToolUseContext, querySource string) *MicrocompactResult

	// Autocompact performs LLM-powered summarization when context exceeds threshold.
	Autocompact(ctx context.Context, messages []Message, toolCtx *ToolUseContext, systemPrompt string, querySource string) *AutocompactResult

	// SnipCompact applies history snip compaction before microcompact.
	// Maps to TypeScript snipCompactIfNeeded() in snipCompact.ts (HISTORY_SNIP gate).
	// Returns snip result with messages, tokensFreed, and optional boundary message.
	SnipCompact(messages []Message) *SnipCompactResult

	// ContextCollapse applies incremental context collapse after microcompact.
	// Maps to TypeScript applyCollapsesIfNeeded() in contextCollapse/index.ts.
	ContextCollapse(ctx context.Context, messages []Message, toolCtx *ToolUseContext, querySource string) *ContextCollapseResult

	// ContextCollapseDrain drains all staged context collapses on overflow recovery.
	// Maps to TypeScript recoverFromOverflow() in contextCollapse/index.ts.
	ContextCollapseDrain(messages []Message, querySource string) *ContextCollapseDrainResult

	// ReactiveCompact performs emergency compaction on prompt-too-long/media errors.
	// Maps to TypeScript tryReactiveCompact() in reactiveCompact.ts.
	ReactiveCompact(ctx context.Context, messages []Message, toolCtx *ToolUseContext, systemPrompt string, querySource string, hasAttempted bool) *AutocompactResult

	// ExecuteTools runs tool calls and returns result messages.
	// Delegates to tool/orchestration.go or tool/streaming_executor.go.
	// The streaming bool indicates whether streaming execution is preferred.
	// Maps to TypeScript runTools() in toolOrchestration.ts.
	ExecuteTools(ctx context.Context, calls []ToolUseBlock, assistantMsg *Message, toolCtx *ToolUseContext, streaming bool) *ToolExecutionResult

	// BuildToolDefinitions converts the tools in ToolUseContext to API tool definitions.
	// Maps to TypeScript buildToolDefinitions() and toolToAPISchema() in utils/api.ts.
	// Returns nil when no tools are configured.
	BuildToolDefinitions(toolCtx *ToolUseContext) []ToolDefinition

	// UUID generates a new unique identifier string.
	UUID() string

	// ApplyToolResultBudget enforces per-message aggregate tool result budget.
	// Maps to applyToolResultBudget() in toolResultStorage.ts.
	// Returns messages with oversized tool results truncated.
	ApplyToolResultBudget(messages []Message, toolCtx *ToolUseContext, querySource string) []Message

	// GetAttachmentMessages returns attachment messages to inject after tool execution.
	// Maps to getAttachmentMessages() in utils/attachments.ts.
	// Includes file change notifications, date changes, memory attachments, etc.
	GetAttachmentMessages(toolCtx *ToolUseContext) []Message

	// StartMemoryPrefetch initiates async memory prefetch for the current turn.
	// Maps to startRelevantMemoryPrefetch() in utils/attachments.ts.
	// Returns a channel that will receive the prefetched memories.
	StartMemoryPrefetch(messages []Message, toolCtx *ToolUseContext) <-chan []Message
}

// ToolExecutionResult holds the aggregated result of running tool calls.
type ToolExecutionResult struct {
	Messages         []Message                               `json:"messages"`
	ContextModifiers []func(*ToolUseContext) *ToolUseContext `json:"-"`
}

// SnipCompactResult holds the outcome of a snip compaction pass.
// Maps to TypeScript snipCompactIfNeeded() return value.
type SnipCompactResult struct {
	Messages        []Message `json:"messages"`
	TokensFreed     int       `json:"tokens_freed"`
	BoundaryMessage *Message  `json:"boundary_message,omitempty"`
}

// ContextCollapseResult holds the outcome of context collapse.
type ContextCollapseResult struct {
	Messages []Message `json:"messages"`
	Applied  bool      `json:"applied"`
}

// ContextCollapseDrainResult holds the outcome of draining staged collapses.
type ContextCollapseDrainResult struct {
	Messages  []Message `json:"messages"`
	Committed int       `json:"committed"`
}

// CallModelParams holds all parameters for a model API call (used by QueryDeps).
type CallModelParams struct {
	Messages                []Message        `json:"messages"`
	SystemPrompt            string           `json:"system_prompt"`
	ThinkingConfig          *ThinkingConfig  `json:"thinking_config,omitempty"`
	Tools                   []ToolDefinition `json:"tools,omitempty"`
	Model                   string           `json:"model"`
	FallbackModel           string           `json:"fallback_model,omitempty"`
	MaxOutputTokens         *int             `json:"max_output_tokens,omitempty"`
	IsNonInteractiveSession bool             `json:"is_non_interactive_session,omitempty"`
	QuerySource             string           `json:"query_source,omitempty"`
	SkipCacheWrite          bool             `json:"skip_cache_write,omitempty"`
	TaskBudget              *TaskBudget      `json:"task_budget,omitempty"`
	OnStreamingFallback     func()           `json:"-"`
}

// ToolDefinition describes a tool for the LLM API.
type ToolDefinition struct {
	Name        string      `json:"name"`
	Description string      `json:"description,omitempty"`
	InputSchema interface{} `json:"input_schema"`
}

// MicrocompactResult holds the outcome of a micro-compaction pass.
type MicrocompactResult struct {
	Messages       []Message `json:"messages"`
	TokensSaved    int       `json:"tokens_saved"`
	Applied        bool      `json:"applied"`
	DeletedToolIDs []string  `json:"deleted_tool_ids,omitempty"`
}

// AutocompactResult holds the outcome of an auto-compaction pass.
type AutocompactResult struct {
	Messages    []Message        `json:"messages"`
	TokensSaved int              `json:"tokens_saved"`
	Applied     bool             `json:"applied"`
	Summary     string           `json:"summary,omitempty"`
	Metadata    *CompactMetadata `json:"metadata,omitempty"`
}

// ---------------------------------------------------------------------------
// ProductionDeps — default production implementation of QueryDeps
// ---------------------------------------------------------------------------

// ProductionDeps is the default production implementation of QueryDeps.
// It wires real Provider calls and compaction logic.
type ProductionDeps struct {
	// CallModelFn is the actual model calling function (typically from a Provider).
	CallModelFn func(ctx context.Context, params CallModelParams) (<-chan StreamEvent, error)

	// MicrocompactFn performs local micro-compaction.
	MicrocompactFn func(messages []Message, toolCtx *ToolUseContext, querySource string) *MicrocompactResult

	// AutocompactFn performs LLM-powered auto-compaction.
	AutocompactFn func(ctx context.Context, messages []Message, toolCtx *ToolUseContext, systemPrompt string, querySource string) *AutocompactResult

	// SnipCompactFn applies history snip compaction.
	SnipCompactFn func(messages []Message) *SnipCompactResult

	// ContextCollapseFn applies incremental context collapse.
	ContextCollapseFn func(ctx context.Context, messages []Message, toolCtx *ToolUseContext, querySource string) *ContextCollapseResult

	// ContextCollapseDrainFn drains staged collapses on overflow.
	ContextCollapseDrainFn func(messages []Message, querySource string) *ContextCollapseDrainResult

	// ReactiveCompactFn performs emergency compaction.
	ReactiveCompactFn func(ctx context.Context, messages []Message, toolCtx *ToolUseContext, systemPrompt string, querySource string, hasAttempted bool) *AutocompactResult

	// ExecuteToolsFn runs tool calls.
	ExecuteToolsFn func(ctx context.Context, calls []ToolUseBlock, assistantMsg *Message, toolCtx *ToolUseContext, streaming bool) *ToolExecutionResult

	// UUIDFn generates UUIDs.
	UUIDFn func() string

	// ApplyToolResultBudgetFn enforces per-message aggregate tool result budget.
	ApplyToolResultBudgetFn func(messages []Message, toolCtx *ToolUseContext, querySource string) []Message

	// GetAttachmentMessagesFn returns attachment messages post-tool.
	GetAttachmentMessagesFn func(toolCtx *ToolUseContext) []Message

	// BuildToolDefinitionsFn converts tools in ToolUseContext to API tool definitions.
	BuildToolDefinitionsFn func(toolCtx *ToolUseContext) []ToolDefinition

	// StartMemoryPrefetchFn initiates async memory prefetch.
	StartMemoryPrefetchFn func(messages []Message, toolCtx *ToolUseContext) <-chan []Message
}

func (d *ProductionDeps) CallModel(ctx context.Context, params CallModelParams) (<-chan StreamEvent, error) {
	return d.CallModelFn(ctx, params)
}

func (d *ProductionDeps) Microcompact(messages []Message, toolCtx *ToolUseContext, querySource string) *MicrocompactResult {
	if d.MicrocompactFn == nil {
		return &MicrocompactResult{Messages: messages, Applied: false}
	}
	return d.MicrocompactFn(messages, toolCtx, querySource)
}

func (d *ProductionDeps) Autocompact(ctx context.Context, messages []Message, toolCtx *ToolUseContext, systemPrompt string, querySource string) *AutocompactResult {
	if d.AutocompactFn == nil {
		return &AutocompactResult{Messages: messages, Applied: false}
	}
	return d.AutocompactFn(ctx, messages, toolCtx, systemPrompt, querySource)
}

func (d *ProductionDeps) SnipCompact(messages []Message) *SnipCompactResult {
	if d.SnipCompactFn == nil {
		return &SnipCompactResult{Messages: messages, TokensFreed: 0}
	}
	return d.SnipCompactFn(messages)
}

func (d *ProductionDeps) ContextCollapse(ctx context.Context, messages []Message, toolCtx *ToolUseContext, querySource string) *ContextCollapseResult {
	if d.ContextCollapseFn == nil {
		return &ContextCollapseResult{Messages: messages, Applied: false}
	}
	return d.ContextCollapseFn(ctx, messages, toolCtx, querySource)
}

func (d *ProductionDeps) ContextCollapseDrain(messages []Message, querySource string) *ContextCollapseDrainResult {
	if d.ContextCollapseDrainFn == nil {
		return &ContextCollapseDrainResult{Messages: messages, Committed: 0}
	}
	return d.ContextCollapseDrainFn(messages, querySource)
}

func (d *ProductionDeps) ReactiveCompact(ctx context.Context, messages []Message, toolCtx *ToolUseContext, systemPrompt string, querySource string, hasAttempted bool) *AutocompactResult {
	if d.ReactiveCompactFn == nil {
		return nil
	}
	return d.ReactiveCompactFn(ctx, messages, toolCtx, systemPrompt, querySource, hasAttempted)
}

func (d *ProductionDeps) ExecuteTools(ctx context.Context, calls []ToolUseBlock, assistantMsg *Message, toolCtx *ToolUseContext, streaming bool) *ToolExecutionResult {
	if d.ExecuteToolsFn == nil {
		// Default: return placeholder results for each tool call
		var msgs []Message
		for _, call := range calls {
			msgs = append(msgs, Message{
				Type: MessageTypeUser,
				Content: []ContentBlock{{
					Type:      ContentBlockToolResult,
					ToolUseID: call.ID,
					Content:   "Tool execution not configured. Tool: " + call.Name,
				}},
				SourceToolAssistantUUID: assistantMsg.UUID,
			})
		}
		return &ToolExecutionResult{Messages: msgs}
	}
	return d.ExecuteToolsFn(ctx, calls, assistantMsg, toolCtx, streaming)
}

func (d *ProductionDeps) UUID() string {
	if d.UUIDFn != nil {
		return d.UUIDFn()
	}
	return ""
}

func (d *ProductionDeps) ApplyToolResultBudget(messages []Message, toolCtx *ToolUseContext, querySource string) []Message {
	if d.ApplyToolResultBudgetFn == nil {
		return messages
	}
	return d.ApplyToolResultBudgetFn(messages, toolCtx, querySource)
}

func (d *ProductionDeps) GetAttachmentMessages(toolCtx *ToolUseContext) []Message {
	if d.GetAttachmentMessagesFn == nil {
		return nil
	}
	return d.GetAttachmentMessagesFn(toolCtx)
}

func (d *ProductionDeps) BuildToolDefinitions(toolCtx *ToolUseContext) []ToolDefinition {
	if d.BuildToolDefinitionsFn == nil {
		return nil
	}
	return d.BuildToolDefinitionsFn(toolCtx)
}

func (d *ProductionDeps) StartMemoryPrefetch(messages []Message, toolCtx *ToolUseContext) <-chan []Message {
	if d.StartMemoryPrefetchFn == nil {
		ch := make(chan []Message, 1)
		ch <- nil
		close(ch)
		return ch
	}
	return d.StartMemoryPrefetchFn(messages, toolCtx)
}
