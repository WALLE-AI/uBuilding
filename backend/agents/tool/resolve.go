// Package tool — sub-agent tool resolution.
//
// Tasks B01 / B08 · port src/tools/AgentTool/agentToolUtils.ts's
// resolveAgentTools + filterToolsForAgent pair. These are the two gates
// every spawned sub-agent passes through before its Task-query begins:
//
//  1. filterToolsForAgent enforces the baseline disallow / async / teammate
//     carve-outs (agent_tool_constants.go defines the sets).
//  2. resolveAgentTools then applies the agent-specific allow list
//     (default `*` / whitelist) followed by its disallow list, producing
//     the final pool the worker sees.
//
// `Agent(type1,type2)` allow-list entries carry metadata, not a tool —
// they populate allowedAgentTypes so the Task tool can restrict which
// subagent_type the child can spawn next.
package tool

import (
	"strings"

	"github.com/wall-ai/ubuilding/backend/agents"
	"github.com/wall-ai/ubuilding/backend/agents/permission"
)

// FilterToolsForAgentOpts controls filterToolsForAgent.
type FilterToolsForAgentOpts struct {
	// IsBuiltIn: whether the calling agent is a built-in definition.
	// Custom agents gain an additional disallow set
	// (CustomAgentDisallowedTools) that built-ins bypass.
	IsBuiltIn bool

	// IsAsync: whether the spawn is async (background / fork-default path).
	// Async agents must pick from ASYNC_AGENT_ALLOWED_TOOLS only, with the
	// in-process teammate carve-out allowed through IsInProcessTeammate.
	IsAsync bool

	// PermissionMode hook: plan-mode agents are granted ExitPlanMode even
	// if the baseline disallow list would strip it.
	PermissionMode permission.Mode

	// IsInProcessTeammate allows teammates to keep a handful of task-graph
	// and SendMessage tools that are otherwise stripped for generic async
	// agents.
	IsInProcessTeammate bool
}

// FilterToolsForAgent mirrors src/tools/AgentTool/agentToolUtils.ts::
// filterToolsForAgent. The output preserves input order.
func FilterToolsForAgent(tools Tools, opts FilterToolsForAgentOpts) Tools {
	out := make(Tools, 0, len(tools))
	mode := permission.NormalizeMode(opts.PermissionMode)
	for _, t := range tools {
		name := t.Name()

		// MCP tools bypass every disallow set.
		if isMCPTool(name) {
			out = append(out, t)
			continue
		}

		// Plan mode grants ExitPlanMode back even for agents that would
		// normally have it stripped (e.g. in-process teammates that
		// surface the plan UI to the main thread).
		if mode == permission.ModePlan && name == "ExitPlanMode" {
			out = append(out, t)
			continue
		}

		// Baseline disallow set applies to every agent.
		if _, blocked := agents.AllAgentDisallowedTools[name]; blocked {
			continue
		}
		// Custom agents also drop CUSTOM-only blocks.
		if !opts.IsBuiltIn {
			if _, blocked := agents.CustomAgentDisallowedTools[name]; blocked {
				continue
			}
		}

		// Async agents must be in the allow set (or in the teammate
		// carve-out when applicable).
		if opts.IsAsync {
			if _, ok := agents.AsyncAgentAllowedTools[name]; !ok {
				if !opts.IsInProcessTeammate {
					continue
				}
				if _, teammate := agents.InProcessTeammateAllowedTools[name]; !teammate {
					continue
				}
			}
		}
		out = append(out, t)
	}
	return out
}

// ResolvedAgentTools is the result of ResolveAgentTools. Mirrors the TS
// ResolvedAgentTools shape: valid/invalid entries from the frontmatter,
// the effective tool pool, and the parsed `Agent(type,…)` allowed list.
type ResolvedAgentTools struct {
	HasWildcard       bool
	ValidTools        []string
	InvalidTools      []string
	ResolvedTools     Tools
	AllowedAgentTypes []string
}

// ResolveAgentTools produces the effective tool pool for a sub-agent
// spawn. Steps (ordered to match agentToolUtils.ts::resolveAgentTools):
//
//  1. Apply FilterToolsForAgent unless isMainThread is true.
//  2. Apply the agent's DisallowedTools list (parsed via
//     permission.ParseRuleValue so `Bash(…)` patterns map back to the tool
//     name).
//  3. Apply the agent's Tools allow list. Empty or ["*"] means "allow all
//     remaining"; otherwise match exact names. `Agent(a,b)` entries
//     populate AllowedAgentTypes and are dropped from the resolved pool.
//  4. Deduplicate resolved tools by name (built-ins win over later
//     duplicates — same ordering guarantee the TS version provides via a
//     Set).
//
// `isMainThread` is kept for parity with TS's main-thread path; leave it
// false for every sub-agent spawn.
func ResolveAgentTools(
	available Tools,
	agent *agents.AgentDefinition,
	isAsync bool,
	isMainThread bool,
) ResolvedAgentTools {
	if agent == nil {
		return ResolvedAgentTools{ResolvedTools: append(Tools(nil), available...)}
	}

	filtered := available
	if !isMainThread {
		filtered = FilterToolsForAgent(filtered, FilterToolsForAgentOpts{
			IsBuiltIn:      agent.IsBuiltIn(),
			IsAsync:        isAsync,
			PermissionMode: permission.Mode(agent.PermissionMode),
		})
	}

	// Step 2 — deny list.
	denyLookup := make(map[string]struct{}, len(agent.DisallowedTools))
	for _, spec := range agent.DisallowedTools {
		denyLookup[permission.ParseRuleValue(spec).Tool] = struct{}{}
	}
	afterDeny := make(Tools, 0, len(filtered))
	for _, t := range filtered {
		if _, denied := denyLookup[t.Name()]; denied {
			continue
		}
		afterDeny = append(afterDeny, t)
	}

	// Step 3 — allow list.
	hasWildcard := len(agent.Tools) == 0 ||
		(len(agent.Tools) == 1 && strings.TrimSpace(agent.Tools[0]) == "*")
	if hasWildcard {
		return ResolvedAgentTools{
			HasWildcard:   true,
			ResolvedTools: afterDeny,
		}
	}

	// Index available tools by name for O(1) lookup.
	byName := make(map[string]Tool, len(afterDeny))
	for _, t := range afterDeny {
		byName[t.Name()] = t
	}

	out := ResolvedAgentTools{}
	seen := make(map[string]struct{}, len(agent.Tools))
	for _, spec := range agent.Tools {
		rv := permission.ParseRuleValue(spec)
		// Special-case Agent(x,y) — metadata, not a tool.
		if rv.Tool == "Task" || rv.Tool == "Agent" {
			if rv.HasArgs {
				out.AllowedAgentTypes = append(out.AllowedAgentTypes, permission.ParseCommaList(rv.Pattern)...)
			}
			// For sub-agents the Task tool is already stripped by
			// filterToolsForAgent, so skip resolution.
			if !isMainThread {
				out.ValidTools = append(out.ValidTools, spec)
				continue
			}
		}
		t, ok := byName[rv.Tool]
		if !ok {
			out.InvalidTools = append(out.InvalidTools, spec)
			continue
		}
		out.ValidTools = append(out.ValidTools, spec)
		if _, already := seen[rv.Tool]; already {
			continue
		}
		seen[rv.Tool] = struct{}{}
		out.ResolvedTools = append(out.ResolvedTools, t)
	}
	return out
}
