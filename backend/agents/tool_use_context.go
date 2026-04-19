package agents

import (
	"context"
	"sync"
)

// ToolPermissionContext holds the current permission configuration.
type ToolPermissionContext struct {
	Mode                             string                      `json:"mode"`
	AdditionalWorkingDirectories     map[string]string           `json:"additional_working_directories,omitempty"`
	AlwaysAllowRules                 map[string][]PermissionRule `json:"always_allow_rules,omitempty"`
	AlwaysDenyRules                  map[string][]PermissionRule `json:"always_deny_rules,omitempty"`
	AlwaysAskRules                   map[string][]PermissionRule `json:"always_ask_rules,omitempty"`
	IsBypassPermissionsModeAvailable bool                        `json:"is_bypass_permissions_mode_available"`
	ShouldAvoidPermissionPrompts     bool                        `json:"should_avoid_permission_prompts,omitempty"`
}

// PermissionRule defines a single permission matching rule.
type PermissionRule struct {
	Tool    string `json:"tool"`
	Pattern string `json:"pattern,omitempty"`
}

// NewEmptyToolPermissionContext returns a fresh, zero-config permission
// context suitable for tests and engine bootstrap. Maps to TypeScript
// `getEmptyToolPermissionContext()` in src/Tool.ts.
func NewEmptyToolPermissionContext() *ToolPermissionContext {
	return &ToolPermissionContext{
		Mode:                         "default",
		AdditionalWorkingDirectories: map[string]string{},
		AlwaysAllowRules:             map[string][]PermissionRule{},
		AlwaysDenyRules:              map[string][]PermissionRule{},
		AlwaysAskRules:               map[string][]PermissionRule{},
	}
}

// ToolUseContext carries all context needed during tool execution.
// It maps to the TypeScript ToolUseContext type from Tool.ts.
type ToolUseContext struct {
	// Options holds engine-level configuration for the tool execution.
	Options ToolUseOptions

	// Ctx is the Go context for cancellation propagation (replaces AbortController).
	Ctx        context.Context
	CancelFunc context.CancelFunc

	// ReadFileState caches file states to avoid repeated reads.
	ReadFileState *FileStateCache

	// GetAppState returns the current application state snapshot.
	GetAppState func() *AppState

	// SetAppState atomically updates the application state.
	SetAppState func(f func(prev *AppState) *AppState)

	// Messages is the running message history within this context.
	Messages []Message

	// AgentID is set only for subagents; empty for main thread.
	AgentID string

	// AgentType is the subagent type name (e.g., "code-review").
	AgentType string

	// QueryTracking tracks the chain of queries for analytics.
	QueryTracking *QueryChainTracking

	// AddNotification sends a notification to the UI (maps to TS addNotification).
	AddNotification func(text string)

	// HandleElicitation processes model elicitation requests.
	HandleElicitation func(prompt string) (string, error)

	// SetResponseLength callback for tracking response length.
	SetResponseLength func(length int)

	// AskUser is invoked by the AskUserQuestion tool. Hosts provide this
	// callback (CLI/UI/tests) to collect the user's answer. Returning an
	// error propagates back to the model as a tool failure.
	AskUser func(ctx context.Context, payload AskUserPayload) (AskUserResponse, error)

	// PlanMode tracks the current plan/normal mode. Mutated by the
	// EnterPlanMode / ExitPlanMode tools. Empty string == "normal".
	PlanMode string

	// EmitEvent, when non-nil, lets tools surface ancillary StreamEvents
	// (EventAskUser, EventPlanModeChange, …) through the engine's event pipe.
	// Hosts wire this to their internal event channel; nil = best-effort drop.
	EmitEvent func(StreamEvent)

	// TodoStore exposes a session-scoped todo list for the TodoWrite tool.
	// The engine sets this during bootstrap; nil means the tool is disabled.
	TodoStore interface{} // *tool/todo.Store, kept as interface{} to avoid import cycle

	// TaskManager is the background-shell job manager used by TaskOutput /
	// TaskStop / Bash (run_in_background). Typically a `*bg.Manager`.
	// Kept as interface{} to avoid import cycles.
	TaskManager interface{}

	// TaskGraph is the TodoV2 task-graph store used by the TaskCreate /
	// TaskGet / TaskUpdate / TaskList / TaskStop tools. Typically a
	// `*taskgraph.Store`. Kept as interface{} to avoid import cycles.
	TaskGraph interface{}

	// SpawnSubAgent, when non-nil, launches a subagent query and returns its
	// final textual answer. Wired by the engine; used by AgentTool.
	SpawnSubAgent func(ctx context.Context, params SubAgentParams) (string, error)

	// McpResources, when non-nil, exposes MCP-resource discovery & read access
	// to the ListMcpResources / ReadMcpResource tools. Hosts wire this to
	// their MCP client pool; nil = tool calls error out with "no MCP registry".
	McpResources McpResourceRegistry

	// --- A18 · fields needed by runAgent / fork / resume (Phase B+D) --------

	// ToolUseID is the id of the parent tool_use that spawned this ToolUseContext,
	// when one exists. Used to attribute async-agent notifications back to the
	// assistant tool_use block (maps to `toolUseContext.toolUseId` in TS).
	ToolUseID string

	// RenderedSystemPrompt holds the parent's already-rendered system prompt
	// bytes when available. Fork subagents reuse these verbatim to preserve
	// prompt-cache hits (see forkSubagent.ts — `override.systemPrompt`).
	// Empty string means "recompute under the child's cwd".
	RenderedSystemPrompt string

	// ContentReplacementState is the per-session state used by the tool-result
	// budget pipeline to decide which tool results to collapse. Cloned by
	// createSubagentContext so fork children make identical decisions as the
	// parent (cache stability). Kept opaque via interface{} to avoid a cycle
	// on compact/tool_result_budget.
	ContentReplacementState interface{}

	// SetAppStateForTasks is the "always reach the root store" channel used
	// by async agents. When the parent is itself an async agent, its
	// SetAppState is a no-op, but task registration / kill must still reach
	// the root — hosts wire this to the root store's setter.
	SetAppStateForTasks func(f func(prev *AppState) *AppState)

	// LocalDenialTracking is a per-agent denial counter used when SetAppState
	// is isolated (async subagents). Kept as interface{} to dodge imports.
	LocalDenialTracking interface{}

	// UpdateAttributionState is safe-to-share even when SetAppState is
	// stubbed (pure functional update). Used by analytics / attribution.
	UpdateAttributionState func(f func(prev interface{}) interface{})

	// UpdateFileHistoryState mirrors the TS updateFileHistoryState callback
	// so file-mutating tools can record snapshots. Nil = disabled.
	UpdateFileHistoryState func(f func(prev FileHistoryState) FileHistoryState)

	// PreserveToolUseResults requests that the engine keep toolUseResult on
	// streamed messages (used by in-process teammates with viewable
	// transcripts). Mirrors `toolUseContext.preserveToolUseResults`.
	PreserveToolUseResults bool

	// CriticalSystemReminder is an optional short string re-injected at every
	// user turn (mirrors `agentDefinition.criticalSystemReminder_EXPERIMENTAL`
	// threaded through createSubagentContext).
	CriticalSystemReminder string

	// InProgressToolUseIDs tracks currently executing tool IDs.
	inProgressMu         sync.Mutex
	inProgressToolUseIDs map[string]struct{}
}

// SubAgentParams configures a subagent query spawned via SpawnSubAgent.
// Mirrors the subset of the TS AgentTool Input relevant to sync dispatch.
type SubAgentParams struct {
	Description  string
	Prompt       string
	SubagentType string
	MaxTurns     int

	// Model is an optional override (alias or concrete id) passed through to
	// GetAgentModel. Empty string means "use the agent definition / parent".
	Model string
}

// ToolUseOptions holds engine-level configuration accessible during tool execution.
type ToolUseOptions struct {
	Commands                []interface{}
	Debug                   bool
	MainLoopModel           string
	Tools                   []interface{} // will be refined to tool.Tool
	Verbose                 bool
	ThinkingConfig          *ThinkingConfig
	IsNonInteractiveSession bool
	MaxBudgetUSD            float64
	CustomSystemPrompt      string
	AppendSystemPrompt      string
	QuerySource             string
	RefreshTools            func() []interface{}
	AgentDefinitions        *AgentDefinitions
	McpClients              []MCPClient
	Theme                   string

	// --- B04 · permission mode overlay for sub-agents ----------------------

	// AgentPermissionMode, when non-empty, overrides the effective
	// permission mode for a sub-agent's tool calls without mutating the
	// parent's ToolPermissionContext. Consumed by permission.Check.
	AgentPermissionMode string

	// ShouldAvoidPermissionPromptsOverride, when true, forces
	// ShouldAvoidPermissionPrompts=true for this context regardless of
	// AppState. Async sub-agents set this so the permission checker
	// short-circuits instead of hanging on an interactive prompt.
	ShouldAvoidPermissionPromptsOverride bool
}

// SetInProgressToolUseIDs safely updates the in-progress tool use set.
func (ctx *ToolUseContext) SetInProgressToolUseIDs(f func(prev map[string]struct{}) map[string]struct{}) {
	ctx.inProgressMu.Lock()
	defer ctx.inProgressMu.Unlock()
	ctx.inProgressToolUseIDs = f(ctx.inProgressToolUseIDs)
}

// GetInProgressToolUseIDs returns a copy of the current in-progress set.
func (ctx *ToolUseContext) GetInProgressToolUseIDs() map[string]struct{} {
	ctx.inProgressMu.Lock()
	defer ctx.inProgressMu.Unlock()
	result := make(map[string]struct{}, len(ctx.inProgressToolUseIDs))
	for k, v := range ctx.inProgressToolUseIDs {
		result[k] = v
	}
	return result
}

// IsAborted checks if the context has been cancelled.
func (ctx *ToolUseContext) IsAborted() bool {
	select {
	case <-ctx.Ctx.Done():
		return true
	default:
		return false
	}
}

// FileStateCache caches file read states to avoid redundant I/O.
type FileStateCache struct {
	mu    sync.RWMutex
	cache map[string]*FileState
}

// FileState holds the cached state of a single file.
type FileState struct {
	Path         string `json:"path"`
	LastModified int64  `json:"last_modified"`
	Size         int64  `json:"size"`
	ContentHash  string `json:"content_hash,omitempty"`
}

// NewFileStateCache creates a new empty file state cache.
func NewFileStateCache() *FileStateCache {
	return &FileStateCache{
		cache: make(map[string]*FileState),
	}
}

// Get retrieves a cached file state.
func (c *FileStateCache) Get(path string) (*FileState, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	s, ok := c.cache[path]
	return s, ok
}

// Set stores a file state in the cache.
func (c *FileStateCache) Set(path string, state *FileState) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cache[path] = state
}

// Has checks if a path exists in the cache.
func (c *FileStateCache) Has(path string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	_, ok := c.cache[path]
	return ok
}

// AppState is the simplified application state (replaces React AppState).
type AppState struct {
	ToolPermissionContext ToolPermissionContext `json:"tool_permission_context"`
	FastMode              bool                  `json:"fast_mode"`
	EffortValue           string                `json:"effort_value,omitempty"`
	AdvisorModel          string                `json:"advisor_model,omitempty"`
	MCP                   MCPState              `json:"mcp"`
	FileHistory           FileHistoryState      `json:"file_history,omitempty"`
}

// MCPState holds MCP (Model Context Protocol) server state.
type MCPState struct {
	Clients []MCPClient `json:"clients,omitempty"`
	Tools   []MCPTool   `json:"tools,omitempty"`
}

// MCPClient represents a connected MCP server.
type MCPClient struct {
	Name   string `json:"name"`
	Type   string `json:"type"` // "connected" | "pending" | "error"
	Status string `json:"status,omitempty"`
}

// MCPTool represents a tool provided by an MCP server.
type MCPTool struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	ServerName  string `json:"server_name"`
}

// McpResource is a single MCP resource advertised by a connected server.
// Maps to the `resources/list` item in the MCP spec.
type McpResource struct {
	URI         string `json:"uri"`
	Name        string `json:"name"`
	MimeType    string `json:"mimeType,omitempty"`
	Description string `json:"description,omitempty"`
	Server      string `json:"server"`
}

// McpResourceContent is a single content chunk returned by `resources/read`.
// Either Text or BlobSavedTo (filesystem path) is populated; never both.
type McpResourceContent struct {
	URI         string `json:"uri"`
	MimeType    string `json:"mimeType,omitempty"`
	Text        string `json:"text,omitempty"`
	BlobSavedTo string `json:"blobSavedTo,omitempty"`
}

// McpResourceRegistry is the minimal surface the ListMcpResources /
// ReadMcpResource tools use. Hosts with a real MCP client pool implement this;
// tests pass a fake. Returning an empty slice for an unknown server is NOT
// allowed — the implementation must return an error so the tool can surface
// "server not found".
type McpResourceRegistry interface {
	// ListServers reports the names of connected MCP servers.
	ListServers() []string

	// ListResources returns resources advertised by the named server. When
	// server == "" the caller wants resources from ALL connected servers
	// flattened (one round-trip per server, results concatenated).
	ListResources(ctx context.Context, server string) ([]McpResource, error)

	// ReadResource fetches the contents of a resource by URI from a named
	// server. Binary blobs SHOULD be persisted to disk by the implementation
	// and surfaced via McpResourceContent.BlobSavedTo.
	ReadResource(ctx context.Context, server, uri string) ([]McpResourceContent, error)
}

// FileHistoryState tracks file modification history for undo/redo.
type FileHistoryState struct {
	Snapshots map[string][]FileSnapshot `json:"snapshots,omitempty"`
}

// FileSnapshot records a single file state at a point in time.
type FileSnapshot struct {
	Path        string `json:"path"`
	Content     string `json:"content"`
	Timestamp   int64  `json:"timestamp"`
	MessageUUID string `json:"message_uuid,omitempty"`
}

// AgentDef / AgentDefinitions now live in agent_definition.go. They were
// extracted so the full BaseAgentDefinition model from claude-code-main can
// live next to its builder/loader siblings without bloating this file.
