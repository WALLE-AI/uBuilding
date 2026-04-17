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

	// InProgressToolUseIDs tracks currently executing tool IDs.
	inProgressMu         sync.Mutex
	inProgressToolUseIDs map[string]struct{}
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

// AgentDefinitions holds active and allowed agent types.
type AgentDefinitions struct {
	ActiveAgents      []AgentDef `json:"active_agents,omitempty"`
	AllAgents         []AgentDef `json:"all_agents,omitempty"`
	AllowedAgentTypes []string   `json:"allowed_agent_types,omitempty"`
}

// AgentDef represents a single agent definition.
type AgentDef struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Description string `json:"description,omitempty"`
}
