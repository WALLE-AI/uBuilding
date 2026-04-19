package tool

import (
	"context"
	"encoding/json"

	"github.com/wall-ai/ubuilding/backend/agents"
)

// ---------------------------------------------------------------------------
// Tool interface — maps to TypeScript Tool<Input, Output, P> from Tool.ts
// ---------------------------------------------------------------------------

// Tool defines the contract for all tools in the agent engine.
// Each tool must implement this interface to be registered and executed.
type Tool interface {
	// Name returns the primary tool name (e.g., "Bash", "Read", "Edit").
	Name() string

	// Aliases returns alternative names for backward compatibility.
	Aliases() []string

	// Call executes the tool with the given input and returns a result.
	Call(ctx context.Context, input json.RawMessage, toolCtx *agents.ToolUseContext) (*ToolResult, error)

	// Description returns a human-readable description for the given input.
	Description(input json.RawMessage) string

	// InputSchema returns the JSON Schema for this tool's input.
	InputSchema() *JSONSchema

	// CheckPermissions determines if the tool is allowed to run with this input.
	CheckPermissions(input json.RawMessage, toolCtx *agents.ToolUseContext) (*PermissionResult, error)

	// IsConcurrencySafe returns true if this tool can run in parallel with other
	// concurrent-safe tools for the given input.
	IsConcurrencySafe(input json.RawMessage) bool

	// IsReadOnly returns true if this tool only reads state (no side effects).
	IsReadOnly(input json.RawMessage) bool

	// IsEnabled returns true if this tool is currently available.
	IsEnabled() bool

	// IsDestructive returns true if this tool performs irreversible operations.
	IsDestructive(input json.RawMessage) bool

	// MaxResultSizeChars returns the maximum tool result size before disk persistence.
	MaxResultSizeChars() int

	// Prompt returns the tool's description for the system prompt.
	Prompt(opts PromptOptions) string

	// ValidateInput checks if the input is valid before execution.
	ValidateInput(input json.RawMessage, toolCtx *agents.ToolUseContext) *ValidationResult

	// MapToolResultToParam converts a tool result to API-compatible format.
	MapToolResultToParam(result interface{}, toolUseID string) *agents.ContentBlock
}

// ---------------------------------------------------------------------------
// Supporting types
// ---------------------------------------------------------------------------

// ToolResult is the output of a tool execution.
type ToolResult struct {
	Data            interface{}                                             `json:"data"`
	NewMessages     []agents.Message                                        `json:"new_messages,omitempty"`
	ContextModifier func(ctx *agents.ToolUseContext) *agents.ToolUseContext `json:"-"`
}

// JSONSchema represents a JSON Schema definition for tool inputs.
type JSONSchema struct {
	Type       string                     `json:"type"`
	Properties map[string]*SchemaProperty `json:"properties,omitempty"`
	Required   []string                   `json:"required,omitempty"`
}

// SchemaProperty defines a single property in a JSON Schema.
type SchemaProperty struct {
	Type        string      `json:"type"`
	Description string      `json:"description,omitempty"`
	Enum        []string    `json:"enum,omitempty"`
	Default     interface{} `json:"default,omitempty"`
}

// PermissionResult represents the outcome of a permission check.
type PermissionResult struct {
	Behavior     string          `json:"behavior"` // "allow" | "deny" | "ask"
	UpdatedInput json.RawMessage `json:"updated_input,omitempty"`
	Message      string          `json:"message,omitempty"`
	// RiskLevel indicates assessed risk: "low" | "medium" | "high"
	RiskLevel string `json:"risk_level,omitempty"`
	// DecisionReason is a short machine-readable tag explaining why this
	// decision was taken (e.g. "preapproved", "blocklist", "default").
	// Maps to TypeScript `decisionReason` on PermissionResult.
	DecisionReason string `json:"decision_reason,omitempty"`
	// Suggestions are permission-rule additions the UI can offer the user
	// (e.g. "always allow WebFetch for example.com"). Empty when not applicable.
	Suggestions []PermissionUpdate `json:"suggestions,omitempty"`
}

// PermissionUpdate is a suggested modification to the permission rule set,
// surfaced by a tool's CheckPermissions when the result is "ask" or "allow".
// Mirrors a subset of claude-code's PermissionUpdate union.
type PermissionUpdate struct {
	// Type describes what kind of change (e.g. "addRules", "removeRules").
	Type string `json:"type"`
	// Destination indicates where the rule should be persisted.
	Destination string `json:"destination,omitempty"`
	// Rules are the rule entries to add/remove.
	Rules []PermissionRuleUpdate `json:"rules,omitempty"`
	// Behavior for addRules: "allow" | "deny" | "ask".
	Behavior string `json:"behavior,omitempty"`
}

// PermissionRuleUpdate is a single rule entry carried inside PermissionUpdate.
type PermissionRuleUpdate struct {
	ToolName    string `json:"tool_name"`
	RuleContent string `json:"rule_content,omitempty"`
}

// Permission behaviors.
const (
	PermissionAllow = "allow"
	PermissionDeny  = "deny"
	PermissionAsk   = "ask"
)

// Permission update types.
const (
	PermissionUpdateAddRules    = "addRules"
	PermissionUpdateRemoveRules = "removeRules"
)

// Permission destinations for PermissionUpdate.Destination.
const (
	PermissionDestinationLocal   = "localSettings"
	PermissionDestinationProject = "projectSettings"
	PermissionDestinationUser    = "userSettings"
	PermissionDestinationSession = "session"
)

// ValidationResult represents the outcome of input validation.
type ValidationResult struct {
	Valid   bool   `json:"result"`
	Message string `json:"message,omitempty"`
	Code    int    `json:"error_code,omitempty"`
}

// PromptOptions are passed to Tool.Prompt() for context-aware descriptions.
//
// The first two fields existed from day one; the remaining fields were added
// in Sprint S1 of the "tools prompt/feature deep alignment" effort so that
// individual Tool.Prompt() implementations can mirror the dynamic segments
// claude-code-main emits (git/sandbox blocks, ant-vs-external wording,
// PowerShell edition branch, fork-enabled AgentTool examples, etc.).
//
// All of the new fields are zero-valued by default. A tool that reads them
// must treat the zero value as "feature disabled / use conservative
// default", which keeps callers that still pass `PromptOptions{}` working
// unchanged.
type PromptOptions struct {
	Tools                 []Tool
	ToolPermissionContext *agents.ToolPermissionContext

	// UserType is "" for the default external prompt or "ant" for the
	// Anthropic-internal variant. Maps to TS `process.env.USER_TYPE`.
	UserType string

	// PlatformOS is runtime.GOOS at assembly time ("windows", "linux",
	// "darwin"). Used by Bash/PowerShell prompts to decide which syntax
	// guidance to emit.
	PlatformOS string

	// EmbeddedSearchTools signals that the host is running inside an IDE /
	// embedded environment where find/grep/ls should NOT be mentioned as
	// disallowed (they never reach a real shell). Mirrors the `embedded`
	// branch inside BashTool/prompt.ts.
	EmbeddedSearchTools bool

	// ForkEnabled toggles the AgentTool fork section (fork spawn, fork
	// examples, "When to fork" block). Host sets this from
	// `agents.ForkSubagentEnabled()`.
	ForkEnabled bool

	// SandboxEnabled toggles the Bash sandbox block. Off by default — the
	// Go backend does not yet ship a sandbox implementation.
	SandboxEnabled bool

	// AgentSwarmsEnabled toggles TaskCreate/TaskList/TaskUpdate teammate
	// wording (owner assignment, "find next task" workflow). Mirrors TS's
	// `isAgentSwarmsEnabled()` feature flag.
	AgentSwarmsEnabled bool

	// PowerShellEdition is one of "desktop" | "core" | "" (unknown). The
	// PowerShell tool uses it to emit edition-specific syntax guidance.
	PowerShellEdition string

	// MonthYear is time.Now().Format("January 2006"). WebSearch renders it
	// into the "current month" instruction so the LLM uses the right year
	// in queries. Exposed as an option (not recomputed inside the tool)
	// so tests can freeze it.
	MonthYear string

	// PreviewFormat selects the preview-block variant for
	// AskUserQuestion: "markdown" (default) or "html".
	PreviewFormat string

	// PlanModeInterviewEnabled mirrors TS's
	// `isPlanModeInterviewPhaseEnabled()`. When true, EnterPlanMode omits
	// the inline "What Happens in Plan Mode" section because hosts inject
	// the workflow instructions via a plan_mode attachment message.
	PlanModeInterviewEnabled bool

	// AgentToolIsCoordinator toggles the slim coordinator branch of the
	// Task/AgentTool prompt (getPrompt(…, isCoordinator=true)).
	AgentToolIsCoordinator bool

	// AgentListViaAttachment matches upstream
	// shouldInjectAgentListInMessages(): when true, the prompt replaces
	// the inline catalog with a <system-reminder> pointer.
	AgentListViaAttachment bool

	// IsTeammate mirrors utils/teammate.isTeammate() — cross-teammate
	// spawning is disabled, so the prompt hides name/team_name/mode.
	IsTeammate bool

	// IsInProcessTeammate mirrors utils/teammateContext.isInProcessTeammate()
	// — background/name/team_name/mode are all unavailable.
	IsInProcessTeammate bool

	// DisableBackgroundTasks mirrors env CLAUDE_CODE_DISABLE_BACKGROUND_TASKS.
	// When true, AgentTool omits the run_in_background paragraph.
	DisableBackgroundTasks bool

	// SubscriptionType is "" / "pro" / etc. (utils/auth.getSubscriptionType()).
	// Non-pro tiers see the "Launch multiple agents concurrently" tip.
	SubscriptionType string
}

// ---------------------------------------------------------------------------
// ToolDef — simplified builder for creating tools with defaults
// ---------------------------------------------------------------------------

// ToolDefaults provides safe default implementations for optional Tool methods.
type ToolDefaults struct{}

func (d ToolDefaults) Aliases() []string                        { return nil }
func (d ToolDefaults) IsConcurrencySafe(_ json.RawMessage) bool { return false }
func (d ToolDefaults) IsReadOnly(_ json.RawMessage) bool        { return false }
func (d ToolDefaults) IsDestructive(_ json.RawMessage) bool     { return false }
func (d ToolDefaults) IsEnabled() bool                          { return true }
func (d ToolDefaults) MaxResultSizeChars() int                  { return 100000 }
func (d ToolDefaults) ValidateInput(_ json.RawMessage, _ *agents.ToolUseContext) *ValidationResult {
	return &ValidationResult{Valid: true}
}
func (d ToolDefaults) CheckPermissions(input json.RawMessage, _ *agents.ToolUseContext) (*PermissionResult, error) {
	return &PermissionResult{Behavior: PermissionAllow, UpdatedInput: input}, nil
}

// ---------------------------------------------------------------------------
// Tools — collection type (matches TypeScript `type Tools = readonly Tool[]`)
// ---------------------------------------------------------------------------

// Tools is a slice of Tool. Using a named type makes it easy to track where
// tool sets are assembled, passed, and filtered across the codebase.
type Tools []Tool

// FindByName finds a tool by primary name or alias.
func (ts Tools) FindByName(name string) Tool {
	for _, t := range ts {
		if t.Name() == name {
			return t
		}
		for _, alias := range t.Aliases() {
			if alias == name {
				return t
			}
		}
	}
	return nil
}

// Names returns the primary names of all tools.
func (ts Tools) Names() []string {
	names := make([]string, len(ts))
	for i, t := range ts {
		names[i] = t.Name()
	}
	return names
}

// Enabled returns only the tools that are currently enabled.
func (ts Tools) Enabled() Tools {
	var result Tools
	for _, t := range ts {
		if t.IsEnabled() {
			result = append(result, t)
		}
	}
	return result
}
