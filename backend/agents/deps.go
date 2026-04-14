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

	// UUID generates a new unique identifier string.
	UUID() string
}

// CallModelParams holds all parameters for a model API call (used by QueryDeps).
type CallModelParams struct {
	Messages        []Message        `json:"messages"`
	SystemPrompt    string           `json:"system_prompt"`
	ThinkingConfig  *ThinkingConfig  `json:"thinking_config,omitempty"`
	Tools           []ToolDefinition `json:"tools,omitempty"`
	Model           string           `json:"model"`
	FallbackModel   string           `json:"fallback_model,omitempty"`
	MaxOutputTokens *int             `json:"max_output_tokens,omitempty"`
	IsNonInteractiveSession bool     `json:"is_non_interactive_session,omitempty"`
	QuerySource     string           `json:"query_source,omitempty"`
	SkipCacheWrite  bool             `json:"skip_cache_write,omitempty"`
	TaskBudget      *TaskBudget      `json:"task_budget,omitempty"`
	OnStreamingFallback func()       `json:"-"`
}

// ToolDefinition describes a tool for the LLM API.
type ToolDefinition struct {
	Name        string      `json:"name"`
	Description string      `json:"description,omitempty"`
	InputSchema interface{} `json:"input_schema"`
}

// MicrocompactResult holds the outcome of a micro-compaction pass.
type MicrocompactResult struct {
	Messages     []Message `json:"messages"`
	TokensSaved  int       `json:"tokens_saved"`
	Applied      bool      `json:"applied"`
}

// AutocompactResult holds the outcome of an auto-compaction pass.
type AutocompactResult struct {
	Messages     []Message `json:"messages"`
	TokensSaved  int       `json:"tokens_saved"`
	Applied      bool      `json:"applied"`
	Summary      string    `json:"summary,omitempty"`
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

	// UUIDFn generates UUIDs.
	UUIDFn func() string
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

func (d *ProductionDeps) UUID() string {
	if d.UUIDFn != nil {
		return d.UUIDFn()
	}
	return ""
}
