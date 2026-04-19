// Package agents — AgentDefinition data model.
//
// Ports claude-code-main's `BaseAgentDefinition` (src/tools/AgentTool/loadAgentsDir.ts)
// plus the `getSystemPrompt` lazy-closure concept. Covers tasks A01, A02, A14.
//
// AgentDefinition is the single source of truth for:
//   - built-in agents declared in Go code (see agent_builtin.go)
//   - user/project/policy agents loaded from *.md frontmatter (see agent_loader.go)
//   - agents loaded from agents.json
//
// Legacy `AgentDef` (Name/Type/Description) is kept as a thin wrapper so the
// existing prompt-injection callers keep compiling while they migrate.
package agents

import (
	"fmt"
	"os"
	"runtime"
	"strings"
)

// AgentSource identifies where an agent definition came from. It drives
// trust/plugin-only gating and display ordering. Mirrors
// src/utils/settings/constants.ts SettingSource + "built-in"/"plugin".
type AgentSource string

const (
	AgentSourceBuiltIn AgentSource = "built-in"
	AgentSourceUser    AgentSource = "userSettings"
	AgentSourceProject AgentSource = "projectSettings"
	AgentSourcePolicy  AgentSource = "policySettings"
	AgentSourcePlugin  AgentSource = "plugin"
)

// IsAdminTrusted reports whether the source is admin-trusted. Used by the
// per-agent MCP / hooks plugin-only policy (Phase C).
func (s AgentSource) IsAdminTrusted() bool {
	switch s {
	case AgentSourceBuiltIn, AgentSourcePlugin, AgentSourcePolicy:
		return true
	default:
		return false
	}
}

// AgentMemoryScope selects where persistent agent memory lives. Mirrors
// src/tools/AgentTool/agentMemory.ts `AgentMemoryScope`.
type AgentMemoryScope string

const (
	AgentMemoryScopeNone    AgentMemoryScope = ""
	AgentMemoryScopeUser    AgentMemoryScope = "user"
	AgentMemoryScopeProject AgentMemoryScope = "project"
	AgentMemoryScopeLocal   AgentMemoryScope = "local"
)

// AgentIsolation chooses an execution sandbox for the agent. Currently only
// "worktree" is supported on external builds. Mirrors the ant-only enum in
// loadAgentsDir.ts AgentJsonSchema.isolation.
type AgentIsolation string

const (
	AgentIsolationNone     AgentIsolation = ""
	AgentIsolationWorktree AgentIsolation = "worktree"
	AgentIsolationRemote   AgentIsolation = "remote"
)

// AgentEffortValue mirrors src/utils/effort.ts EffortValue: either a named
// level ("minimal"|"low"|"medium"|"high") or a raw integer (>=0). Empty name
// with zero integer == unset.
type AgentEffortValue struct {
	Name  string `json:"name,omitempty"`
	Value int    `json:"value,omitempty"`
}

// IsSet reports whether the effort value has been explicitly configured.
func (e AgentEffortValue) IsSet() bool {
	return e.Name != "" || e.Value != 0
}

// AgentMcpServerSpec represents an MCP server declaration in an agent
// frontmatter. Two shapes mirror loadAgentsDir.ts `AgentMcpServerSpec`:
//
//   - ByName: a string reference to an MCP server already configured elsewhere.
//   - Inline: { name: config } single-key map for a run-scoped server.
//
// Exactly one of ByName / Inline should be set after parsing.
type AgentMcpServerSpec struct {
	ByName string                 `json:"by_name,omitempty"`
	Inline map[string]interface{} `json:"inline,omitempty"`
}

// AgentPendingSnapshotUpdate mirrors BaseAgentDefinition.pendingSnapshotUpdate.
// Populated by the snapshot checker; consumed by the memory subsystem
// (Phase C: C13).
type AgentPendingSnapshotUpdate struct {
	SnapshotTimestamp string `json:"snapshot_timestamp"`
}

// SystemPromptCtx carries the data an agent's `GetSystemPrompt` closure
// needs to render a prompt. Kept small so both built-in closures and loader
// closures can be called without the whole ToolUseContext.
type SystemPromptCtx struct {
	// Model is the fully-resolved model identifier for this agent invocation.
	Model string
	// Tools is the list of tool names available to the agent (post-resolve).
	Tools []string
	// AdditionalWorkingDirectories lists extra roots that the prompt should
	// surface to the model (beyond CWD).
	AdditionalWorkingDirectories []string
	// IsNonInteractive matches `ToolUseContext.options.isNonInteractiveSession`.
	IsNonInteractive bool
	// Cwd is the agent's working directory (worktree path or parent cwd).
	Cwd string
}

// GetSystemPromptFn is the lazy closure stored on every AgentDefinition. It
// must be idempotent and side-effect free: loadAgentsDir.ts and runAgent.ts
// both call it multiple times (render + attachment + resume).
type GetSystemPromptFn func(ctx SystemPromptCtx) string

// AgentDefinition ports the union of BaseAgentDefinition + BuiltInAgentDefinition
// + CustomAgentDefinition + PluginAgentDefinition from loadAgentsDir.ts.
//
// Fields documented inline stay close to the TS field names so that grepping
// between languages works. Unknown TS optional fields keep their `omitempty`
// JSON tags.
type AgentDefinition struct {
	// Identity / display.
	AgentType  string      `json:"agent_type"`
	WhenToUse  string      `json:"when_to_use"`
	Color      string      `json:"color,omitempty"`
	Source     AgentSource `json:"source"`
	BaseDir    string      `json:"base_dir,omitempty"`
	Filename   string      `json:"filename,omitempty"`
	PluginName string      `json:"plugin,omitempty"`

	// Tool surface.
	Tools           []string `json:"tools,omitempty"`
	DisallowedTools []string `json:"disallowed_tools,omitempty"`
	Skills          []string `json:"skills,omitempty"`

	// Model / effort / budget.
	Model    string           `json:"model,omitempty"` // "" | "inherit" | concrete id
	Effort   AgentEffortValue `json:"effort,omitempty"`
	MaxTurns int              `json:"max_turns,omitempty"`

	// Permission / execution overrides.
	PermissionMode string         `json:"permission_mode,omitempty"` // default/plan/acceptEdits/bypassPermissions/bubble
	Background     bool           `json:"background,omitempty"`
	Isolation      AgentIsolation `json:"isolation,omitempty"`

	// Memory / persistence.
	Memory                AgentMemoryScope            `json:"memory,omitempty"`
	PendingSnapshotUpdate *AgentPendingSnapshotUpdate `json:"pending_snapshot_update,omitempty"`
	OmitClaudeMd          bool                        `json:"omit_claude_md,omitempty"`

	// MCP / hooks (deferred typing — kept as interface{} to dodge import cycles;
	// Phase C materialises them via agent_mcp.go / agent_hooks.go).
	MCPServers         []AgentMcpServerSpec `json:"mcp_servers,omitempty"`
	Hooks              interface{}          `json:"hooks,omitempty"`
	RequiredMcpServers []string             `json:"required_mcp_servers,omitempty"`

	// Prompt overrides.
	InitialPrompt                      string `json:"initial_prompt,omitempty"`
	CriticalSystemReminderExperimental string `json:"critical_system_reminder_experimental,omitempty"`

	// Built-in extras.
	// Callback fires after a one-shot built-in completes (STATUSLINE_SETUP-style).
	Callback func() `json:"-"`

	// GetSystemPrompt lazily renders the agent's system prompt. MUST NOT be
	// nil for a well-formed definition. Loader closures capture the stored
	// prompt text; built-ins compose on the fly from shared snippets.
	GetSystemPrompt GetSystemPromptFn `json:"-"`
}

// IsBuiltIn mirrors the TS isBuiltInAgent type guard.
func (a *AgentDefinition) IsBuiltIn() bool {
	return a != nil && a.Source == AgentSourceBuiltIn
}

// IsPlugin mirrors the TS isPluginAgent type guard.
func (a *AgentDefinition) IsPlugin() bool {
	return a != nil && a.Source == AgentSourcePlugin
}

// IsCustom mirrors the TS isCustomAgent guard (user/project/policy).
func (a *AgentDefinition) IsCustom() bool {
	return a != nil && a.Source != AgentSourceBuiltIn && a.Source != AgentSourcePlugin
}

// RenderSystemPrompt invokes GetSystemPrompt guarding against nil receivers /
// nil closures, so callers don't need to null-check at every site.
func (a *AgentDefinition) RenderSystemPrompt(ctx SystemPromptCtx) string {
	if a == nil || a.GetSystemPrompt == nil {
		return ""
	}
	return a.GetSystemPrompt(ctx)
}

// ToLegacyDef returns the compact {Name,Type,Description} shape still used by
// prompt injection and UI listings. Kept until every caller is migrated to
// AgentDefinition directly.
func (a *AgentDefinition) ToLegacyDef() AgentDef {
	if a == nil {
		return AgentDef{}
	}
	return AgentDef{
		Name:        a.AgentType,
		Type:        a.AgentType,
		Description: a.WhenToUse,
	}
}

// AgentDefinitions holds the fully-resolved set of agents visible this
// session. Ports AgentDefinitionsResult from loadAgentsDir.ts.
//
// ActiveAgents is the de-duplicated working set (built-in < user <
// project < plugin < policy). AllAgents includes everything discovered
// before filtering (used by /agents for configuration UI).
// AllowedAgentTypes is populated when the Task tool is restricted via
// Agent(type1,type2) permission rules.
type AgentDefinitions struct {
	ActiveAgents      []*AgentDefinition `json:"-"`
	AllAgents         []*AgentDefinition `json:"-"`
	AllowedAgentTypes []string           `json:"allowed_agent_types,omitempty"`

	// Legacy projection retained for prompt injection that still uses
	// AgentDef. Kept in sync with ActiveAgents by ResolveActiveAgents in
	// agent_loader.go.
	ActiveLegacy []AgentDef `json:"active_agents,omitempty"`
	AllLegacy    []AgentDef `json:"all_agents,omitempty"`
}

// FindActive returns the active agent matching agentType (or nil).
func (d *AgentDefinitions) FindActive(agentType string) *AgentDefinition {
	if d == nil {
		return nil
	}
	for _, a := range d.ActiveAgents {
		if a != nil && a.AgentType == agentType {
			return a
		}
	}
	return nil
}

// FindAny searches ActiveAgents then AllAgents. Used by resume paths that
// may reference an agentType no longer in the active set.
func (d *AgentDefinitions) FindAny(agentType string) *AgentDefinition {
	if d == nil {
		return nil
	}
	if a := d.FindActive(agentType); a != nil {
		return a
	}
	for _, a := range d.AllAgents {
		if a != nil && a.AgentType == agentType {
			return a
		}
	}
	return nil
}

// ActiveTypes returns the list of active agent type names.
func (d *AgentDefinitions) ActiveTypes() []string {
	if d == nil {
		return nil
	}
	out := make([]string, 0, len(d.ActiveAgents))
	for _, a := range d.ActiveAgents {
		if a != nil {
			out = append(out, a.AgentType)
		}
	}
	return out
}

// RefreshLegacy recomputes the ActiveLegacy/AllLegacy projections from the
// canonical slices. Call after mutating ActiveAgents/AllAgents.
func (d *AgentDefinitions) RefreshLegacy() {
	if d == nil {
		return
	}
	d.ActiveLegacy = d.ActiveLegacy[:0]
	for _, a := range d.ActiveAgents {
		if a != nil {
			d.ActiveLegacy = append(d.ActiveLegacy, a.ToLegacyDef())
		}
	}
	d.AllLegacy = d.AllLegacy[:0]
	for _, a := range d.AllAgents {
		if a != nil {
			d.AllLegacy = append(d.AllLegacy, a.ToLegacyDef())
		}
	}
}

// AgentDef is the legacy compact representation used by prompt injection and
// the engine bootstrap surface. Keep this stable; do NOT add fields here —
// extend AgentDefinition instead.
type AgentDef struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Description string `json:"description,omitempty"`
}

// -----------------------------------------------------------------------------
// A02 · EnhanceSystemPromptWithEnvDetails placeholder
// -----------------------------------------------------------------------------
// Full implementation lands in B06 (agent_definition.go, append cwd/git/os/
// shell + path guidance). For now return the base prompt unchanged so that
// callers can compose without a circular dependency on prompt/*.

// EnhanceSystemPromptWithEnvDetails appends environment-specific guidance
// (cwd, OS, shell, absolute-path + emoji guardrails, optional additional
// working directories) to a base system prompt. Ports the external-build
// body of src/constants/prompts.ts::enhanceSystemPromptWithEnvDetails
// — the ant-specific branches (growthbook gates, bfs/ugrep hints) are
// omitted deliberately to keep the Go surface provider-agnostic.
func EnhanceSystemPromptWithEnvDetails(base string, ctx SystemPromptCtx) string {
	section := buildEnvDetailsSection(ctx)
	if section == "" {
		return base
	}
	if base == "" {
		return section
	}
	return base + "\n\n" + section
}

// buildEnvDetailsSection renders the appended env block. Kept simple — the
// section is treated as read-only context by the model.
func buildEnvDetailsSection(ctx SystemPromptCtx) string {
	var lines []string
	add := func(format string, args ...interface{}) {
		lines = append(lines, fmt.Sprintf(format, args...))
	}

	lines = append(lines, "Environment:")
	if ctx.Cwd != "" {
		add("- cwd: %s", ctx.Cwd)
	}
	if platform := runtimePlatform(); platform != "" {
		add("- Platform: %s", platform)
	}
	if shell := defaultShellLabel(); shell != "" {
		add("- Shell: %s", shell)
	}
	if ctx.Model != "" {
		add("- Model: %s", ctx.Model)
	}
	if ctx.IsNonInteractive {
		add("- Interactive: no (background/async run)")
	} else {
		add("- Interactive: yes")
	}
	if len(ctx.AdditionalWorkingDirectories) > 0 {
		lines = append(lines, "", "Additional working directories:")
		for _, p := range ctx.AdditionalWorkingDirectories {
			add("- %s", p)
		}
	}
	if len(ctx.Tools) > 0 {
		lines = append(lines, "", "Available tools:")
		add("- %s", joinSorted(ctx.Tools))
	}
	lines = append(lines, "", "Guidelines:",
		"- Prefer absolute paths in tool inputs and responses; relative paths break when other agents adopt your plan.",
		"- Do not use emojis in commit messages, pull-request bodies, or filenames.",
		"- Treat the environment above as read-only metadata — it is not a separate user message.",
	)
	return strings.Join(lines, "\n")
}

// runtimePlatform returns a short platform label: "darwin/arm64",
// "linux/amd64", "windows/amd64". Empty string when runtime package yields
// nothing meaningful (shouldn't happen in practice).
func runtimePlatform() string {
	if runtime.GOOS == "" {
		return ""
	}
	return runtime.GOOS + "/" + runtime.GOARCH
}

// defaultShellLabel produces a best-effort shell label based on common env
// vars. Windows looks at COMSPEC; unix reads SHELL. Returns "" when the env
// doesn't disclose anything useful.
func defaultShellLabel() string {
	if runtime.GOOS == "windows" {
		if comspec := strings.TrimSpace(os.Getenv("COMSPEC")); comspec != "" {
			return comspec
		}
		return "powershell"
	}
	if sh := strings.TrimSpace(os.Getenv("SHELL")); sh != "" {
		return sh
	}
	return ""
}

// joinSorted returns a deterministic comma-separated list of names.
func joinSorted(names []string) string {
	if len(names) == 0 {
		return ""
	}
	cp := make([]string, len(names))
	copy(cp, names)
	// Insertion sort — inputs are small tool lists (<50 items).
	for i := 1; i < len(cp); i++ {
		for j := i; j > 0 && cp[j] < cp[j-1]; j-- {
			cp[j], cp[j-1] = cp[j-1], cp[j]
		}
	}
	return strings.Join(cp, ", ")
}
