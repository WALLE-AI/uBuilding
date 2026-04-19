// Package agents — sub-agent ToolUseContext cloning.
//
// Task B02 · ports src/utils/forkedAgent.ts::createSubagentContext. Produces
// a new ToolUseContext that isolates mutable state from the parent while
// sharing a curated subset of callbacks (per flags). Phase A's SpawnSubAgent
// constructed a child engine and relied on its defaultToolUseContext — Phase
// B integrates this helper so the sub-agent's context reflects the agent
// definition's permission mode (B04) and the parent's MCP / catalog / stores.
//
// Field semantics (match TS):
//   - Mutable state (ReadFileState, in-progress id sets, TodoStore) is
//     cloned or re-created so child mutations don't leak.
//   - AbortController — child ctx derived from parent; closing the parent
//     still cancels the child.
//   - SetAppState defaults to isolated (stub) unless ShareSetAppState is
//     true.
//   - AgentDefinitions / McpResources / EmitEvent / SpawnSubAgent are
//     safe to share — read-only or thread-safe.
package agents

import (
	"context"
	"sync"
)

// SubagentContextOverrides controls which pieces of parent state the child
// context shares. Mirrors the TS SubagentContextOverrides type.
type SubagentContextOverrides struct {
	// AgentID / AgentType label the sub-agent for transcript + events.
	AgentID   string
	AgentType string

	// ToolUseID tags the assistant tool_use that spawned this sub-agent.
	ToolUseID string

	// AgentPermissionMode, when non-empty, overrides the parent's
	// ToolPermissionContext.Mode for the child. Used to enforce Plan / etc.
	// per-agent without mutating the parent's shared context.
	AgentPermissionMode string

	// ShouldAvoidPermissionPrompts, when true, forces the child's
	// ToolPermissionContext.ShouldAvoidPermissionPrompts=true so sync
	// paths know not to try interactive UI. Typically set for async
	// sub-agents.
	ShouldAvoidPermissionPrompts bool

	// ShareSetAppState: the child's SetAppState points at the parent's.
	// When false (default) the child gets a no-op setter so parent state
	// cannot be mutated by sub-tool calls.
	ShareSetAppState bool

	// ShareAbortController: child context is derived from the parent's ctx
	// (parent cancel ⇒ child cancel). Default true; set false only for
	// detached background agents (Phase D).
	ShareAbortController bool

	// RenderedSystemPrompt + ContentReplacementState are the bytes needed
	// to let the sub-agent reuse the parent's prompt cache (Phase D fork
	// path). Optional in Phase B; pass through when available.
	RenderedSystemPrompt    string
	ContentReplacementState interface{}

	// Messages is the initial conversation the child starts with. Nil
	// means "start from empty".
	Messages []Message

	// Options overrides tool-use-option flags. When zero-value, the child
	// inherits the parent's options wholesale.
	Options *ToolUseOptions
}

// CreateSubagentContext builds a child ToolUseContext that isolates the
// pieces TS forkedAgent.ts isolates. `parent` must be non-nil; the returned
// context is ready to hand to runAgent / query.
//
// Ownership: the caller is responsible for the returned ctx.CancelFunc.
func CreateSubagentContext(parent *ToolUseContext, over SubagentContextOverrides) *ToolUseContext {
	if parent == nil {
		// Defensive — nothing meaningful to clone.
		return &ToolUseContext{Ctx: context.Background()}
	}

	childCtx, cancel := buildChildContext(parent, over.ShareAbortController)

	child := &ToolUseContext{
		Ctx:        childCtx,
		CancelFunc: cancel,

		// Mutable state — cloned / fresh so sibling agents don't race.
		ReadFileState: cloneFileStateCache(parent.ReadFileState),

		// Shared registries — read-only / thread-safe.
		TodoStore:    parent.TodoStore,
		TaskManager:  parent.TaskManager,
		TaskGraph:    parent.TaskGraph,
		McpResources: parent.McpResources,

		// Wiring flows from parent unchanged.
		SpawnSubAgent: parent.SpawnSubAgent,
		EmitEvent:     parent.EmitEvent,
		AskUser:       parent.AskUser,

		// Messages seeded from overrides (nil → empty slice).
		Messages: append([]Message(nil), over.Messages...),

		// Metadata.
		AgentID:                 firstNonEmpty(over.AgentID, parent.AgentID),
		AgentType:               firstNonEmpty(over.AgentType, parent.AgentType),
		ToolUseID:               over.ToolUseID,
		RenderedSystemPrompt:    over.RenderedSystemPrompt,
		ContentReplacementState: over.ContentReplacementState,
		PlanMode:                parent.PlanMode,

		// Query tracking — depth+1 when parent has one.
		QueryTracking: incrementChainDepth(parent.QueryTracking),
	}

	// Options: start from parent; copy-on-write only when caller supplied
	// an override.
	opts := parent.Options
	if over.Options != nil {
		opts = *over.Options
	}
	// AgentDefinitions always inherit — resolved agent registry is a
	// session-level concern.
	if opts.AgentDefinitions == nil {
		opts.AgentDefinitions = parent.Options.AgentDefinitions
	}
	// Apply permission-mode overlay (B04). We copy the embedded
	// ToolPermissionContext so the parent's store stays untouched.
	overlayPermissionContext(&opts, over)
	child.Options = opts

	// SetAppState — isolated by default.
	if over.ShareSetAppState {
		child.SetAppState = parent.SetAppState
		child.GetAppState = parent.GetAppState
	} else {
		child.SetAppState = func(func(prev *AppState) *AppState) {} // no-op
		child.GetAppState = parent.GetAppState                      // read still mirrors parent
	}
	// Tasks always reach the root store so async bash tasks can be
	// tracked across nested contexts.
	child.SetAppStateForTasks = parent.SetAppStateForTasks
	if child.SetAppStateForTasks == nil {
		child.SetAppStateForTasks = parent.SetAppState
	}

	return child
}

// buildChildContext derives the child ctx + cancel, shared or standalone.
func buildChildContext(parent *ToolUseContext, shared bool) (context.Context, context.CancelFunc) {
	if parent.Ctx == nil {
		ctx, cancel := context.WithCancel(context.Background())
		return ctx, cancel
	}
	if shared {
		// Share the parent's ctx — caller does not own the cancel.
		return parent.Ctx, func() {}
	}
	return context.WithCancel(parent.Ctx)
}

// overlayPermissionContext applies the override's permission tweaks onto a
// copy of the parent's ToolPermissionContext. Preserves the parent's rules
// by reference (maps are shared but we never mutate them in the child).
func overlayPermissionContext(opts *ToolUseOptions, over SubagentContextOverrides) {
	if over.AgentPermissionMode == "" && !over.ShouldAvoidPermissionPrompts {
		return
	}
	// Unfortunately ToolUseOptions doesn't embed ToolPermissionContext —
	// the mode lives on the AppState. Because the child's GetAppState is
	// inherited from the parent, we can't mutate AppState safely without
	// breaking parent readers. The overlay therefore records the desired
	// mode in ToolUseOptions.AgentPermissionMode / ShouldAvoidPermissionPrompts
	// so downstream callers (permission.Check) can consult the override.
	opts.AgentPermissionMode = over.AgentPermissionMode
	opts.ShouldAvoidPermissionPromptsOverride = over.ShouldAvoidPermissionPrompts
}

// cloneFileStateCache returns a deep copy of cache (nil-safe). Used so the
// child's Read tool sees the same freshness but later writes don't leak.
func cloneFileStateCache(cache *FileStateCache) *FileStateCache {
	out := NewFileStateCache()
	if cache == nil {
		return out
	}
	cache.mu.RLock()
	defer cache.mu.RUnlock()
	for k, v := range cache.cache {
		if v == nil {
			continue
		}
		cp := *v
		out.cache[k] = &cp
	}
	return out
}

// incrementChainDepth clones the parent's QueryChainTracking with depth+1.
// Returns nil when parent doesn't have one (the engine assigns on demand).
func incrementChainDepth(parent *QueryChainTracking) *QueryChainTracking {
	if parent == nil {
		return nil
	}
	return &QueryChainTracking{
		ChainID: parent.ChainID,
		Depth:   parent.Depth + 1,
	}
}

// Keep sync import live even if refactors remove the internal lock usage.
var _ = sync.RWMutex{}
