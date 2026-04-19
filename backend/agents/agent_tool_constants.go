// Package agents — agent-scoped tool-name allow / deny sets.
//
// Task A16 · 1:1 port of the four constants + one-shot agent list from
// src/constants/tools.ts. These sets are consumed by resolveAgentTools
// (Task B01 / B08) and by the Agent tool's allow/deny filtering.
//
// The names are plain string literals rather than imports from each tool
// subpackage to avoid an import cycle (agents → tool/* → agents). They MUST
// stay in lockstep with the canonical `Name` / `EnterName` / etc. constants
// declared in each tool subpackage.
package agents

// ---------------------------------------------------------------------------
// Disallowed tool sets
// ---------------------------------------------------------------------------

// AllAgentDisallowedTools is the baseline set of tools no sub-agent may use.
// Mirrors ALL_AGENT_DISALLOWED_TOOLS in constants/tools.ts.
//
// Notes on parity:
//   - TaskOutput / TaskStop are main-thread-only: they inspect top-level
//     task state owned by the host.
//   - EnterPlanMode / ExitPlanMode are main-thread-only — plan mode is a
//     coordinator abstraction in claude-code.
//   - AskUserQuestion requires UI; sub-agents can't show prompts.
//   - The Agent (Task) tool is disallowed on non-ant builds to prevent
//     runaway recursion; Phase D's fork path keeps Task in the pool under
//     a runtime recursion guard instead.
//   - WorkflowTool is also blocked to prevent recursive workflow spawning.
var AllAgentDisallowedTools = makeNameSet([]string{
	"TaskOutput",
	"ExitPlanMode",
	"EnterPlanMode",
	"Task", // AGENT_TOOL_NAME — kept blocked on external builds
	"AskUserQuestion",
	"TaskStop",
	"WorkflowTool",
})

// CustomAgentDisallowedTools is the set of tools user/project/policy agents
// may not use. Currently identical to AllAgentDisallowedTools — the TS copy
// is defined as a spread so extra names can accrete without touching the
// baseline. We mirror that structure.
var CustomAgentDisallowedTools = unionNameSets(AllAgentDisallowedTools, nil)

// ---------------------------------------------------------------------------
// Allowed tool sets
// ---------------------------------------------------------------------------

// AsyncAgentAllowedTools enumerates every tool an async sub-agent may use.
// Mirrors ASYNC_AGENT_ALLOWED_TOOLS. Tools absent here are rejected by
// filterToolsForAgent when isAsync=true (unless white-listed through the
// in-process teammate carve-out below).
var AsyncAgentAllowedTools = makeNameSet([]string{
	"Read",
	"WebSearch",
	"TodoWrite",
	"Grep",
	"WebFetch",
	"Glob",
	"Bash",
	"PowerShell", // Windows alias — kept in the set for parity
	"Edit",
	"Write",
	"NotebookEdit",
	"Skill",
	"SyntheticOutput",
	"ToolSearch",
	"EnterWorktree",
	"ExitWorktree",
})

// InProcessTeammateAllowedTools is the additional carve-out for in-process
// teammate agents (TS: IN_PROCESS_TEAMMATE_ALLOWED_TOOLS). These tools are
// still blocked for generic async agents but let through when the host marks
// the ToolUseContext as belonging to a teammate.
var InProcessTeammateAllowedTools = makeNameSet([]string{
	"TaskCreate",
	"TaskGet",
	"TaskList",
	"TaskUpdate",
	"SendMessage",
	// AGENT_TRIGGERS-gated cron tools (opt-in).
	"CronCreate",
	"CronDelete",
	"CronList",
})

// CoordinatorModeAllowedTools mirrors COORDINATOR_MODE_ALLOWED_TOOLS. The
// coordinator itself is restricted to orchestration tools only.
var CoordinatorModeAllowedTools = makeNameSet([]string{
	"Task",
	"TaskStop",
	"SendMessage",
	"SyntheticOutput",
})

// ---------------------------------------------------------------------------
// One-shot built-in agents
// ---------------------------------------------------------------------------

// OneShotBuiltinAgentTypes lists the built-in agent types that terminate
// after a single round (e.g. the statusline-setup wizard). The sync
// invocation path skips the SendMessage trailer for these so a one-shot
// agent can't be "continued" after it completes.
//
// Mirrors src/tools/AgentTool/constants.ts::ONE_SHOT_BUILTIN_AGENT_TYPES.
var OneShotBuiltinAgentTypes = makeNameSet([]string{
	"statusline-setup",
})

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// makeNameSet allocates a set (map[string]struct{}) from a slice. Exposed
// only as an internal helper because Go has no set literal.
func makeNameSet(names []string) map[string]struct{} {
	out := make(map[string]struct{}, len(names))
	for _, n := range names {
		out[n] = struct{}{}
	}
	return out
}

// unionNameSets returns a new set containing every member of a and b.
// Nil arguments are treated as empty sets. Provided so derived sets (like
// CustomAgentDisallowedTools) can express "everything in X plus these extras"
// without mutating the base.
func unionNameSets(a, b map[string]struct{}) map[string]struct{} {
	out := make(map[string]struct{}, len(a)+len(b))
	for k := range a {
		out[k] = struct{}{}
	}
	for k := range b {
		out[k] = struct{}{}
	}
	return out
}

// IsOneShotAgent reports whether the named agent type is one-shot. Public
// wrapper so callers don't need to inspect the map directly.
func IsOneShotAgent(agentType string) bool {
	_, ok := OneShotBuiltinAgentTypes[agentType]
	return ok
}
