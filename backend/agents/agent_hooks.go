// Package agents — per-agent frontmatter hooks.
//
// Tasks C03 · C04 · C05 · C10 · bridge the agent-scoped hook concept from
// src/tools/AgentTool/runAgent.ts::registerFrontmatterHooks into the Go
// ShellHookRegistry. Responsibilities:
//
//   - C03 / C10 · Execute SubagentStart hooks and thread their
//     AdditionalContexts into the sub-agent's initial messages.
//   - C04 · When an agent frontmatter declares a Stop hook, rewrite it to
//     SubagentStop so it fires only for that sub-agent.
//   - C05 · Register hooks under a scope id (typically the agentId). Sub-
//     sequent ClearSessionHooks removes exactly those entries without
//     touching engine-wide hooks.
//
// Scoped hook tracking uses a sidecar map keyed by scope id so multiple
// agents can coexist without re-registering one-shot hooks.
package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

// AgentFrontmatterHooks is the shape accepted from AgentDefinition.Hooks.
// Callers may pass:
//
//   - `map[HookEvent][]HookCommand` — typed form used internally.
//   - `map[string][]HookCommand` — YAML-decoded form (event as string).
//   - `map[string]interface{}` — raw YAML with []interface{} values that
//     decode into HookCommand via json round-trip.
//
// Anything else is ignored (the loader logs a warning via LoadError).
type AgentFrontmatterHooks map[HookEvent][]HookCommand

// ---------------------------------------------------------------------------
// Scoped registration
// ---------------------------------------------------------------------------

// scopedHookTracker records every (event, hook) pair added under a given
// scope id so ClearSessionHooks can remove them selectively.
type scopedHookTracker struct {
	mu     sync.Mutex
	scopes map[string]map[HookEvent][]HookCommand
}

// globalScopedHooks keeps sub-agent hook registrations isolated per engine.
// A real production host can substitute their own via WithHookTracker
// options (not yet wired; tests use the package-wide instance).
var globalScopedHooks = &scopedHookTracker{
	scopes: map[string]map[HookEvent][]HookCommand{},
}

// RegisterFrontmatterHooks installs an agent's frontmatter hooks under
// scopeID. When isAgent is true, `Stop` / `StopFailure` events are
// rewritten to `SubagentStop` / SubagentStopFailure equivalents so the
// hooks fire only when the sub-agent terminates (mirrors TS behaviour).
// Returns the normalized map that was registered (caller can inspect).
func RegisterFrontmatterHooks(
	registry *ShellHookRegistry,
	scopeID string,
	raw interface{},
	isAgent bool,
) (AgentFrontmatterHooks, error) {
	if registry == nil {
		return nil, fmt.Errorf("RegisterFrontmatterHooks: nil registry")
	}
	normalized, err := normalizeAgentHooks(raw)
	if err != nil {
		return nil, err
	}
	if len(normalized) == 0 {
		return nil, nil
	}
	if isAgent {
		normalized = rewriteStopForSubagent(normalized)
	}

	globalScopedHooks.mu.Lock()
	if _, ok := globalScopedHooks.scopes[scopeID]; !ok {
		globalScopedHooks.scopes[scopeID] = map[HookEvent][]HookCommand{}
	}
	for ev, cmds := range normalized {
		for _, cmd := range cmds {
			registry.Register(ev, cmd)
			globalScopedHooks.scopes[scopeID][ev] = append(globalScopedHooks.scopes[scopeID][ev], cmd)
		}
	}
	globalScopedHooks.mu.Unlock()
	return normalized, nil
}

// ClearSessionHooks removes every hook registered under scopeID without
// touching other registrations. Invoked by SpawnSubAgent's finally block.
//
// Duplicate-safe: when multiple scopes (or pre-existing entries) share the
// same command string, we drop only as many matching entries as the scope
// contributed, preserving the remainder. Ordering is preserved via a
// right-to-left sweep so the most recently added duplicates go first.
func ClearSessionHooks(registry *ShellHookRegistry, scopeID string) {
	if registry == nil {
		return
	}
	globalScopedHooks.mu.Lock()
	scoped := globalScopedHooks.scopes[scopeID]
	delete(globalScopedHooks.scopes, scopeID)
	globalScopedHooks.mu.Unlock()
	if len(scoped) == 0 {
		return
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	for ev, drop := range scoped {
		registry.hooks[ev] = dropHooksByCount(registry.hooks[ev], drop)
	}
}

// dropHooksByCount removes, for every cmd in `drop`, the last matching
// entry in `all` (if any). Equal-count duplicates in both slices leave the
// pre-existing hook intact.
func dropHooksByCount(all []HookCommand, drop []HookCommand) []HookCommand {
	if len(drop) == 0 || len(all) == 0 {
		return all
	}
	// Track which indices we've already removed.
	removed := make([]bool, len(all))
	for _, d := range drop {
		for i := len(all) - 1; i >= 0; i-- {
			if removed[i] {
				continue
			}
			if all[i].Command == d.Command && all[i].Matcher == d.Matcher && all[i].Shell == d.Shell {
				removed[i] = true
				break
			}
		}
	}
	out := all[:0]
	for i, h := range all {
		if !removed[i] {
			out = append(out, h)
		}
	}
	return append([]HookCommand(nil), out...)
}

// rewriteStopForSubagent swaps Stop / StopFailure keys to the SubagentStop
// equivalents. Mirrors isAgent=true in TS registerFrontmatterHooks.
func rewriteStopForSubagent(in AgentFrontmatterHooks) AgentFrontmatterHooks {
	out := make(AgentFrontmatterHooks, len(in))
	for ev, cmds := range in {
		target := ev
		switch ev {
		case HookEventStop:
			target = HookEventSubagentStop
		case HookEventStopFailure:
			// Preserve the failure variant as SubagentStop — TS doesn't have
			// a SubagentStopFailure; we fold them together.
			target = HookEventSubagentStop
		}
		out[target] = append(out[target], cmds...)
	}
	return out
}

// normalizeAgentHooks converts any of the accepted Hooks shapes into a
// typed AgentFrontmatterHooks map.
func normalizeAgentHooks(raw interface{}) (AgentFrontmatterHooks, error) {
	if raw == nil {
		return nil, nil
	}
	switch v := raw.(type) {
	case AgentFrontmatterHooks:
		return cloneHooks(v), nil
	case map[HookEvent][]HookCommand:
		return cloneHooks(v), nil
	case map[string][]HookCommand:
		out := make(AgentFrontmatterHooks, len(v))
		for k, cmds := range v {
			out[HookEvent(k)] = append(out[HookEvent(k)], cmds...)
		}
		return out, nil
	case map[string]interface{}:
		return decodeInterfaceHooks(v)
	}
	return nil, fmt.Errorf("unsupported Hooks shape %T", raw)
}

func cloneHooks(src AgentFrontmatterHooks) AgentFrontmatterHooks {
	out := make(AgentFrontmatterHooks, len(src))
	for k, v := range src {
		out[k] = append([]HookCommand(nil), v...)
	}
	return out
}

// decodeInterfaceHooks handles the YAML-decoded map where values are
// []interface{} of map[string]interface{}. Uses a json round-trip to
// shore up the field mapping.
func decodeInterfaceHooks(raw map[string]interface{}) (AgentFrontmatterHooks, error) {
	out := AgentFrontmatterHooks{}
	for eventKey, val := range raw {
		list, ok := val.([]interface{})
		if !ok {
			return nil, fmt.Errorf("hook %q: expected list, got %T", eventKey, val)
		}
		for _, entry := range list {
			buf, err := json.Marshal(entry)
			if err != nil {
				return nil, fmt.Errorf("hook %q: marshal: %w", eventKey, err)
			}
			var cmd HookCommand
			if err := json.Unmarshal(buf, &cmd); err != nil {
				return nil, fmt.Errorf("hook %q: unmarshal: %w", eventKey, err)
			}
			if strings.TrimSpace(cmd.Command) == "" {
				continue
			}
			out[HookEvent(eventKey)] = append(out[HookEvent(eventKey)], cmd)
		}
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// SubagentStart / Stop dispatch
// ---------------------------------------------------------------------------

// ExecuteSubagentStartHooks fires every SubagentStart hook in the registry
// and returns the aggregated AdditionalContexts. The caller then wraps
// these in a `hook_additional_context` user message prepended to the
// sub-agent's initial transcript.
func ExecuteSubagentStartHooks(
	ctx context.Context,
	registry *ShellHookRegistry,
	agentID, agentType string,
) ([]string, error) {
	if registry == nil {
		return nil, nil
	}
	input := HookInput{
		AgentID:   agentID,
		AgentType: agentType,
	}
	agg, err := RunHooksForEvent(ctx, registry, HookEventSubagentStart, input, "")
	if err != nil {
		return nil, err
	}
	if agg == nil || len(agg.AdditionalContexts) == 0 {
		return nil, nil
	}
	return append([]string(nil), agg.AdditionalContexts...), nil
}

// ExecuteSubagentStopHooks fires SubagentStop hooks at the end of a sub-
// agent lifecycle. Unlike Start hooks, Stop hooks currently produce only
// StopReason + SystemMessage signals — callers typically forward the
// SystemMessage into the transcript.
func ExecuteSubagentStopHooks(
	ctx context.Context,
	registry *ShellHookRegistry,
	agentID, agentType string,
) (*AggregatedHookResult, error) {
	if registry == nil {
		return nil, nil
	}
	input := HookInput{
		AgentID:   agentID,
		AgentType: agentType,
	}
	return RunHooksForEvent(ctx, registry, HookEventSubagentStop, input, "")
}

// AttachSubagentStartContext returns a user-role Message suitable for
// prepending to a sub-agent's initial messages. `contexts` is the slice
// returned by ExecuteSubagentStartHooks. Nil/empty ⇒ nil Message.
func AttachSubagentStartContext(agentID, agentType string, contexts []string) *Message {
	if len(contexts) == 0 {
		return nil
	}
	body := "<hook_additional_context source=\"SubagentStart\">\n" +
		strings.Join(contexts, "\n\n") +
		"\n</hook_additional_context>"
	return &Message{
		Type:    MessageTypeUser,
		Subtype: "hook_additional_context",
		UUID:    "hook-" + agentID,
		Content: []ContentBlock{{Type: ContentBlockText, Text: body}},
	}
}
