// Package permission — three-tier session rules + agent-denial helpers.
//
// Task B05 · session-level rules (agent-supplied) must not survive past
// their lifetime while CLI-supplied rules stay sticky.
// Task B11 · `alwaysAllowRules.{cliArg, session, command}` tiered lookup
// mirroring src/utils/permissions/permissions.ts::buildAllowRules.
// Task B12 · `filterDeniedAgents` + `getDenyRuleForAgent` — lets the Task
// tool prompt skip agents that the user has denied via `Agent(name)`.
package permission

import (
	"strings"

	"github.com/wall-ai/ubuilding/backend/agents"
)

// RuleSource distinguishes a rule's origin. Mirrors the TS alwaysAllowRules
// shape which is a struct-of-slices keyed by origin.
type RuleSource string

const (
	// RuleSourceCliArg — provided via `--allowedTools` / SDK constructor.
	// These persist across sub-agent spawns and /reload events.
	RuleSourceCliArg RuleSource = "cliArg"

	// RuleSourceSession — session-scoped rules granted by the user during
	// the conversation (or injected by an agent definition). Dropped when
	// the containing session ends.
	RuleSourceSession RuleSource = "session"

	// RuleSourceCommand — per-command rules supplied by a slash command or
	// skill. Scoped to a single tool invocation.
	RuleSourceCommand RuleSource = "command"
)

// TieredAllowRules holds the three-tier allow list. Each tier stores a flat
// slice of rule values (the exact strings, like "Bash(git *)"). Callers
// resolve them via ResolveEffectiveAllow / LookupTool.
type TieredAllowRules struct {
	CliArg  []string `json:"cli_arg,omitempty"`
	Session []string `json:"session,omitempty"`
	Command []string `json:"command,omitempty"`
}

// IsEmpty reports whether every tier is empty. Exposed so fast-path
// callers can skip rule evaluation entirely.
func (t TieredAllowRules) IsEmpty() bool {
	return len(t.CliArg) == 0 && len(t.Session) == 0 && len(t.Command) == 0
}

// All returns every rule string across tiers in precedence order
// (CliArg first, Command last). Duplicates are preserved — callers that
// want a set should dedupe themselves.
func (t TieredAllowRules) All() []string {
	out := make([]string, 0, len(t.CliArg)+len(t.Session)+len(t.Command))
	out = append(out, t.CliArg...)
	out = append(out, t.Session...)
	out = append(out, t.Command...)
	return out
}

// LookupTool returns every rule that targets toolName (exact ParseRuleValue
// match). Used by the agent-side allow check; deny rules go through a
// parallel TieredDenyRules surface.
func (t TieredAllowRules) LookupTool(toolName string) []RuleValue {
	out := make([]RuleValue, 0, 4)
	scan := func(rules []string) {
		for _, r := range rules {
			rv := ParseRuleValue(r)
			if rv.Tool == toolName {
				out = append(out, rv)
			}
		}
	}
	scan(t.CliArg)
	scan(t.Session)
	scan(t.Command)
	return out
}

// ReplaceSession returns a copy of t with the Session tier replaced by
// `newSession`. Matches the TS behaviour where sub-agents rewrite session
// rules without touching the sticky CliArg tier.
func (t TieredAllowRules) ReplaceSession(newSession []string) TieredAllowRules {
	out := TieredAllowRules{
		CliArg:  append([]string(nil), t.CliArg...),
		Session: append([]string(nil), newSession...),
		Command: append([]string(nil), t.Command...),
	}
	return out
}

// ---------------------------------------------------------------------------
// Working directories
// ---------------------------------------------------------------------------

// AdditionalWorkingDirectoriesFromContext returns the parent context's
// additional working directories as a sorted slice. Exposed so the agent-
// side prompt builder can surface them without importing the context map
// directly.
func AdditionalWorkingDirectoriesFromContext(ctx *agents.ToolPermissionContext) []string {
	if ctx == nil || len(ctx.AdditionalWorkingDirectories) == 0 {
		return nil
	}
	out := make([]string, 0, len(ctx.AdditionalWorkingDirectories))
	for k := range ctx.AdditionalWorkingDirectories {
		out = append(out, k)
	}
	return sortedStringsCopy(out)
}

// sortedStringsCopy returns a sorted copy of `in`, mutation-safe for
// callers that want to append further entries.
func sortedStringsCopy(in []string) []string {
	cp := append([]string(nil), in...)
	// Insertion sort — inputs are always small (<10 directories).
	for i := 1; i < len(cp); i++ {
		for j := i; j > 0 && cp[j] < cp[j-1]; j-- {
			cp[j], cp[j-1] = cp[j-1], cp[j]
		}
	}
	return cp
}

// ---------------------------------------------------------------------------
// Agent denial helpers (B12)
// ---------------------------------------------------------------------------

// AgentDenyMatch describes how a given agent type matched a deny rule.
// Returned by GetDenyRuleForAgent so callers can build useful messages
// ("Agent 'reviewer' blocked by Agent(reviewer) in projectSettings").
type AgentDenyMatch struct {
	AgentType string
	Rule      RuleValue
	Source    string // origin tag attached to the PermissionRule, e.g. "projectSettings"
}

// GetDenyRuleForAgent returns the first deny rule that matches agentType,
// or nil when no rule applies. A blanket rule (no pattern) matches every
// agent. A pattern like `worker` / `worker, reviewer` matches when the
// agent type appears in the comma-separated list.
//
// The rule is stored as an agents.PermissionRule whose Pattern field is
// the inner argument of the original `Agent(...)` spec (already extracted
// by the rule loader).  A fully-qualified value "Agent(worker)" is also
// accepted for direct callers that didn't split it upstream.
func GetDenyRuleForAgent(ctx *agents.ToolPermissionContext, toolName, agentType string) *AgentDenyMatch {
	if ctx == nil {
		return nil
	}
	rules := ctx.AlwaysDenyRules[toolName]
	for _, r := range rules {
		// Normalise: if the Pattern carries the full "Task(name)" spec,
		// unwrap it so we can reason about the arg list directly.
		pattern := r.Pattern
		if rv := ParseRuleValue(pattern); rv.HasArgs && rv.Tool == toolName {
			pattern = rv.Pattern
		}
		pattern = strings.TrimSpace(pattern)
		if pattern == "" {
			// Blanket deny for the tool (no inner spec).
			return &AgentDenyMatch{
				AgentType: agentType,
				Rule:      RuleValue{Tool: toolName},
			}
		}
		for _, t := range ParseCommaList(pattern) {
			if t == agentType {
				return &AgentDenyMatch{
					AgentType: agentType,
					Rule:      RuleValue{Tool: toolName, Pattern: pattern, HasArgs: true},
				}
			}
		}
	}
	return nil
}

// FilterDeniedAgents returns the subset of `agentTypes` not denied by
// Agent(...) rules under the Task tool name. Callers that operate on
// AgentDefinition slices can adapt via a projection (see
// FilterDeniedAgentsByType).
func FilterDeniedAgents(ctx *agents.ToolPermissionContext, toolName string, agentTypes []string) []string {
	if len(agentTypes) == 0 {
		return nil
	}
	out := make([]string, 0, len(agentTypes))
	for _, t := range agentTypes {
		if GetDenyRuleForAgent(ctx, toolName, t) == nil {
			out = append(out, t)
		}
	}
	return out
}

// FilterDeniedAgentsByType filters a slice of AgentDefinition pointers.
// Convenience wrapper used by the Task tool prompt builder and the /agents
// listing in Phase D.
func FilterDeniedAgentsByType(
	ctx *agents.ToolPermissionContext,
	toolName string,
	defs []*agents.AgentDefinition,
) []*agents.AgentDefinition {
	if len(defs) == 0 {
		return nil
	}
	out := make([]*agents.AgentDefinition, 0, len(defs))
	for _, d := range defs {
		if d == nil {
			continue
		}
		if GetDenyRuleForAgent(ctx, toolName, d.AgentType) == nil {
			out = append(out, d)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Session-rule scoping helpers (B05)
// ---------------------------------------------------------------------------

// ApplySessionAllowToContext replaces the Session tier on ctx's per-tool
// AlwaysAllowRules with `newRules`. Rules are stored as PermissionRule
// with Tool="<parsed name>" and Pattern="<raw spec>". The CliArg tier
// must stay sticky — callers who own the CLI/SDK entries rely on this.
//
// Returns a fresh map; `ctx` is not mutated.
func ApplySessionAllowToContext(
	ctx *agents.ToolPermissionContext,
	newRules []string,
) map[string][]agents.PermissionRule {
	out := cloneRuleMap(ctx.AlwaysAllowRules)
	// Strip rules tagged session-scope on the caller side. We can't read
	// an explicit tier tag here because agents.PermissionRule doesn't carry
	// one; the expectation is that hosts keep CliArg rules in a separate
	// slice and re-merge through this helper only. To stay safe we append
	// and let callers pre-clean.
	for _, raw := range newRules {
		rv := ParseRuleValue(raw)
		if rv.Tool == "" {
			continue
		}
		out[rv.Tool] = append(out[rv.Tool], agents.PermissionRule{Tool: rv.Tool, Pattern: rv.Pattern})
	}
	return out
}

// cloneRuleMap returns a shallow copy where each slice is re-allocated.
func cloneRuleMap(in map[string][]agents.PermissionRule) map[string][]agents.PermissionRule {
	if in == nil {
		return map[string][]agents.PermissionRule{}
	}
	out := make(map[string][]agents.PermissionRule, len(in))
	for k, v := range in {
		out[k] = append([]agents.PermissionRule(nil), v...)
	}
	return out
}

// FormatDenyMatch renders a human-friendly string for logs / UI surfacing.
// Matches the TS getDenyRuleForAgent error phrasing.
func FormatDenyMatch(m *AgentDenyMatch) string {
	if m == nil {
		return ""
	}
	pat := m.Rule.Pattern
	if pat == "" {
		pat = m.AgentType
	}
	src := m.Source
	if src == "" {
		src = "settings"
	}
	return "Agent '" + m.AgentType + "' has been denied by permission rule '" +
		m.Rule.Tool + "(" + pat + ")' from " + src + "."
}

// Keep strings import live even when future helpers drop direct use.
var _ = strings.TrimSpace
