package tool

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/wall-ai/ubuilding/backend/agents"
)

// ---------------------------------------------------------------------------
// Tool Hooks — pre/post tool execution hooks
// Maps to TypeScript toolHooks.ts (runPreToolUseHooks, runPostToolUseHooks)
// ---------------------------------------------------------------------------

// PreToolHookResult holds the outcome of a pre-tool-use hook.
type PreToolHookResult struct {
	// BlockingError if non-empty, prevents the tool from executing.
	BlockingError string

	// AdditionalContext provides extra context to inject before the tool runs.
	AdditionalContext string

	// PreventExecution if true, stops the tool from executing (soft block).
	PreventExecution bool

	// ModifiedInput replaces the tool input if non-nil.
	ModifiedInput json.RawMessage
}

// PostToolHookResult holds the outcome of a post-tool-use hook.
type PostToolHookResult struct {
	// BlockingError if non-empty, signals a blocking error from the hook.
	BlockingError string

	// AdditionalContext provides extra context to inject after the tool runs.
	AdditionalContext string

	// PreventContinuation if true, stops the query loop after this tool.
	PreventContinuation bool

	// StopReason is a human-readable reason when PreventContinuation is true.
	StopReason string

	// AttachmentMessages are additional messages to inject.
	AttachmentMessages []agents.Message
}

// ToolHook defines the interface for tool execution hooks.
// Hooks can inspect, modify, or block tool execution.
type ToolHook interface {
	// Name returns the hook identifier.
	Name() string

	// PreToolUse is called before tool execution.
	// Return nil to allow execution, or a result with BlockingError to prevent it.
	PreToolUse(ctx context.Context, params HookParams) *PreToolHookResult

	// PostToolUse is called after successful tool execution.
	PostToolUse(ctx context.Context, params HookParams, result *ToolResult) *PostToolHookResult

	// PostToolUseFailure is called after a tool execution failure.
	PostToolUseFailure(ctx context.Context, params HookParams, err error) *PostToolHookResult
}

// HookParams contains the parameters passed to hook functions.
type HookParams struct {
	ToolName     string
	ToolUseID    string
	Input        json.RawMessage
	ToolCtx      *agents.ToolUseContext
	AssistantMsg *agents.Message
}

// ---------------------------------------------------------------------------
// HookRegistry — manages tool hooks with thread-safe access
// ---------------------------------------------------------------------------

// HookRegistry manages a collection of tool hooks.
type HookRegistry struct {
	mu    sync.RWMutex
	hooks []ToolHook
}

// NewHookRegistry creates a new empty HookRegistry.
func NewHookRegistry() *HookRegistry {
	return &HookRegistry{}
}

// Register adds a hook to the registry.
func (r *HookRegistry) Register(hook ToolHook) {
	r.mu.Lock()
	defer r.mu.Unlock()
	// Prevent duplicate registrations
	for _, h := range r.hooks {
		if h.Name() == hook.Name() {
			return
		}
	}
	r.hooks = append(r.hooks, hook)
}

// Unregister removes a hook by name.
func (r *HookRegistry) Unregister(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, h := range r.hooks {
		if h.Name() == name {
			r.hooks = append(r.hooks[:i], r.hooks[i+1:]...)
			return
		}
	}
}

// RunPreToolUseHooks executes all registered pre-tool-use hooks in order.
// Returns the aggregated result. If any hook returns a BlockingError,
// execution stops and the error is returned immediately.
//
// Maps to TypeScript runPreToolUseHooks() in toolHooks.ts.
func (r *HookRegistry) RunPreToolUseHooks(ctx context.Context, params HookParams) *PreToolHookResult {
	r.mu.RLock()
	hooks := make([]ToolHook, len(r.hooks))
	copy(hooks, r.hooks)
	r.mu.RUnlock()

	var aggregated PreToolHookResult
	for _, hook := range hooks {
		if ctx.Err() != nil {
			return &PreToolHookResult{BlockingError: "aborted during pre-tool hooks"}
		}

		result := hook.PreToolUse(ctx, params)
		if result == nil {
			continue
		}

		// Blocking error: stop immediately
		if result.BlockingError != "" {
			return result
		}

		// Aggregate non-blocking results
		if result.AdditionalContext != "" {
			if aggregated.AdditionalContext != "" {
				aggregated.AdditionalContext += "\n"
			}
			aggregated.AdditionalContext += result.AdditionalContext
		}

		if result.ModifiedInput != nil {
			aggregated.ModifiedInput = result.ModifiedInput
		}

		if result.PreventExecution {
			aggregated.PreventExecution = true
		}
	}

	if aggregated.BlockingError != "" || aggregated.AdditionalContext != "" ||
		aggregated.ModifiedInput != nil || aggregated.PreventExecution {
		return &aggregated
	}
	return nil
}

// RunPostToolUseHooks executes all registered post-tool-use hooks in order.
// Returns the aggregated result.
//
// Maps to TypeScript runPostToolUseHooks() in toolHooks.ts.
func (r *HookRegistry) RunPostToolUseHooks(ctx context.Context, params HookParams, toolResult *ToolResult) *PostToolHookResult {
	r.mu.RLock()
	hooks := make([]ToolHook, len(r.hooks))
	copy(hooks, r.hooks)
	r.mu.RUnlock()

	var aggregated PostToolHookResult
	hasResult := false

	for _, hook := range hooks {
		if ctx.Err() != nil {
			return nil
		}

		result := hook.PostToolUse(ctx, params, toolResult)
		if result == nil {
			continue
		}
		hasResult = true

		if result.BlockingError != "" {
			aggregated.BlockingError = result.BlockingError
		}
		if result.AdditionalContext != "" {
			if aggregated.AdditionalContext != "" {
				aggregated.AdditionalContext += "\n"
			}
			aggregated.AdditionalContext += result.AdditionalContext
		}
		if result.PreventContinuation {
			aggregated.PreventContinuation = true
			aggregated.StopReason = result.StopReason
		}
		aggregated.AttachmentMessages = append(aggregated.AttachmentMessages, result.AttachmentMessages...)
	}

	if !hasResult {
		return nil
	}
	return &aggregated
}

// RunPostToolUseFailureHooks executes all registered failure hooks.
//
// Maps to TypeScript runPostToolUseFailureHooks() in toolHooks.ts.
func (r *HookRegistry) RunPostToolUseFailureHooks(ctx context.Context, params HookParams, err error) *PostToolHookResult {
	r.mu.RLock()
	hooks := make([]ToolHook, len(r.hooks))
	copy(hooks, r.hooks)
	r.mu.RUnlock()

	var aggregated PostToolHookResult
	hasResult := false

	for _, hook := range hooks {
		if ctx.Err() != nil {
			return nil
		}

		result := hook.PostToolUseFailure(ctx, params, err)
		if result == nil {
			continue
		}
		hasResult = true

		aggregated.AttachmentMessages = append(aggregated.AttachmentMessages, result.AttachmentMessages...)
		if result.BlockingError != "" {
			aggregated.BlockingError = result.BlockingError
		}
	}

	if !hasResult {
		return nil
	}
	return &aggregated
}

// CanUseToolFn is defined in orchestration.go — see that file for the
// permission check callback type used across the tool package.
