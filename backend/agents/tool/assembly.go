package tool

import (
	"sort"
	"strings"

	"github.com/wall-ai/ubuilding/backend/agents"
)

// ---------------------------------------------------------------------------
// Tool assembly + filtering — maps to claude-code-main's
// getTools() / assembleToolPool() / filterToolsByDenyRules() pipeline.
//
// Design goals:
//   1. Keep built-in tools as a contiguous prefix in the final Tools slice so
//      that Anthropic prompt-cache breakpoints line up on a stable boundary
//      regardless of which MCP servers connect.
//   2. Apply deny rules identically at assembly time and at permission-check
//      time — one matcher, two call sites.
// ---------------------------------------------------------------------------

// MCPToolNamePrefix is the prefix shared by all MCP tools. Name layout:
// `mcp__<server>__<tool>`.
const MCPToolNamePrefix = "mcp__"

// isMCPTool returns true when the tool name follows the mcp__server__name
// convention used by claude-code's MCP gateway.
func isMCPTool(name string) bool { return strings.HasPrefix(name, MCPToolNamePrefix) }

// mcpServerOf extracts the `<server>` segment from `mcp__<server>__<tool>`.
// Returns "" for non-MCP names or malformed inputs.
func mcpServerOf(name string) string {
	if !isMCPTool(name) {
		return ""
	}
	rest := name[len(MCPToolNamePrefix):]
	idx := strings.Index(rest, "__")
	if idx <= 0 {
		return rest // server-only, no tool suffix
	}
	return rest[:idx]
}

// FilterByDenyRules returns the subset of tools that survive the deny-rule
// pass. A rule with empty Pattern means "blanket deny" (entire tool or entire
// MCP server). For MCP tools the check additionally honors
// `mcp__<server>` blanket deny (removes every tool from that server).
func FilterByDenyRules(tools Tools, permCtx *agents.ToolPermissionContext) Tools {
	if permCtx == nil || len(permCtx.AlwaysDenyRules) == 0 {
		return tools
	}
	result := make(Tools, 0, len(tools))
	for _, t := range tools {
		if isDeniedByRules(t, permCtx) {
			continue
		}
		result = append(result, t)
	}
	return result
}

// isDeniedByRules is the single matcher shared by FilterByDenyRules and any
// future runtime permission check.
func isDeniedByRules(t Tool, permCtx *agents.ToolPermissionContext) bool {
	name := t.Name()
	// Direct blanket deny on the tool name.
	if rules, ok := permCtx.AlwaysDenyRules[name]; ok {
		for _, r := range rules {
			if r.Pattern == "" { // blanket
				return true
			}
		}
	}
	// MCP server-wide blanket deny. Match `mcp__<server>` as a rule key.
	if isMCPTool(name) {
		server := mcpServerOf(name)
		if server == "" {
			return false
		}
		key := MCPToolNamePrefix + server
		if rules, ok := permCtx.AlwaysDenyRules[key]; ok {
			for _, r := range rules {
				if r.Pattern == "" {
					return true
				}
			}
		}
	}
	return false
}

// AssembleToolPool combines the Registry's built-in tools with externally
// supplied MCP tools, applies deny-rule filtering, keeps built-ins as a
// contiguous alphabetical prefix, and deduplicates by name (built-in wins).
//
// Mirrors claude-code-main's assembleToolPool() in src/tools.ts.
func AssembleToolPool(r *Registry, permCtx *agents.ToolPermissionContext, mcpTools Tools) Tools {
	if r == nil {
		return nil
	}
	// Built-ins from the registry, already filtered by deny and IsEnabled.
	builtins := r.GetToolsForPermCtx(permCtx)
	sortedBuiltins := make(Tools, len(builtins))
	copy(sortedBuiltins, builtins)
	sort.SliceStable(sortedBuiltins, func(i, j int) bool {
		return sortedBuiltins[i].Name() < sortedBuiltins[j].Name()
	})

	// MCP, after deny filtering.
	allowedMCP := FilterByDenyRules(mcpTools, permCtx)
	sortedMCP := make(Tools, len(allowedMCP))
	copy(sortedMCP, allowedMCP)
	sort.SliceStable(sortedMCP, func(i, j int) bool {
		return sortedMCP[i].Name() < sortedMCP[j].Name()
	})

	// Merge with built-ins as the prefix; drop any MCP tool whose name
	// collides with an already-present built-in (built-in wins).
	seen := make(map[string]struct{}, len(sortedBuiltins)+len(sortedMCP))
	out := make(Tools, 0, len(sortedBuiltins)+len(sortedMCP))
	for _, t := range sortedBuiltins {
		if _, dup := seen[t.Name()]; dup {
			continue
		}
		seen[t.Name()] = struct{}{}
		out = append(out, t)
	}
	for _, t := range sortedMCP {
		if _, dup := seen[t.Name()]; dup {
			continue
		}
		seen[t.Name()] = struct{}{}
		out = append(out, t)
	}
	return out
}
