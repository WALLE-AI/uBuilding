package provider

import (
	"context"

	"github.com/wall-ai/ubuilding/backend/agents"
	"github.com/wall-ai/ubuilding/backend/agents/prompt"
)

// ---------------------------------------------------------------------------
// Provider interface — maps to TypeScript callModel from query/deps.ts
// ---------------------------------------------------------------------------

// Provider defines the contract for LLM API adapters.
// Each provider (Anthropic, OpenAI-compatible) implements this interface.
type Provider interface {
	// CallModel sends a request to the LLM and returns a channel of streaming events.
	// The channel is closed when the response completes or an error occurs.
	// Cancellation is propagated through ctx.
	CallModel(ctx context.Context, params CallModelParams) (<-chan agents.StreamEvent, error)
}

// CallModelParams holds all parameters for a model API call.
// Maps to the TypeScript callModel parameters in query.ts lines 659-708.
type CallModelParams struct {
	// Messages is the conversation history to send.
	Messages []agents.Message `json:"messages"`

	// SystemPrompt is the system instruction (plain string, used when blocks are nil).
	SystemPrompt string `json:"system_prompt"`

	// SystemPromptBlocks are cache-aware system prompt blocks.
	// When set, these take precedence over SystemPrompt.
	SystemPromptBlocks []prompt.SystemPromptBlock `json:"system_prompt_blocks,omitempty"`

	// ThinkingConfig controls extended thinking behavior.
	ThinkingConfig *agents.ThinkingConfig `json:"thinking_config,omitempty"`

	// Tools is the list of tool definitions for the model.
	Tools []ToolDefinition `json:"tools,omitempty"`

	// Model specifies which model to use (e.g., "claude-sonnet-4-20250514").
	Model string `json:"model"`

	// FallbackModel is used when the primary model is unavailable.
	FallbackModel string `json:"fallback_model,omitempty"`

	// MaxOutputTokens overrides the default max output token limit.
	MaxOutputTokens *int `json:"max_output_tokens,omitempty"`

	// IsNonInteractiveSession indicates SDK/batch mode (affects permission handling).
	IsNonInteractiveSession bool `json:"is_non_interactive_session,omitempty"`

	// QuerySource identifies the origin of this query (e.g., "repl_main_thread", "sdk").
	QuerySource string `json:"query_source,omitempty"`

	// SkipCacheWrite disables prompt cache writes for this request.
	SkipCacheWrite bool `json:"skip_cache_write,omitempty"`

	// TaskBudget constrains the total token budget for this task.
	TaskBudget *agents.TaskBudget `json:"task_budget,omitempty"`

	// OnStreamingFallback is called when the provider falls back to a different model mid-stream.
	OnStreamingFallback func() `json:"-"`
}

// ToolDefinition describes a tool for the LLM API.
type ToolDefinition struct {
	Name        string      `json:"name"`
	Description string      `json:"description,omitempty"`
	InputSchema interface{} `json:"input_schema"`
}

// ---------------------------------------------------------------------------
// FallbackTriggeredError — signals that fallback model switch is needed
// ---------------------------------------------------------------------------

// FallbackTriggeredError is returned when the primary model triggers a fallback.
type FallbackTriggeredError struct {
	OriginalModel string
	FallbackModel string
}

func (e *FallbackTriggeredError) Error() string {
	return "fallback triggered: switching from " + e.OriginalModel + " to " + e.FallbackModel
}
