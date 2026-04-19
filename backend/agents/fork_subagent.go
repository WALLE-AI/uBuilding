// Package agents — fork sub-agent runtime.
//
// Tasks D01 · D02 · D03 · D04 · D05 · D11 · port the externally-reusable
// pieces of src/tools/AgentTool/forkSubagent.ts.
//
//   - D01 · Feature flag `ForkSubagentEnabled` (env + hard toggles for
//     coordinator / non-interactive).
//   - D02 · BuildForkedMessages + FORK_BOILERPLATE constants so fork
//     requests keep byte-identical prefixes with the parent.
//   - D03 · IsInForkChild recursion guard (fork-inside-fork rejection).
//   - D04 · AgentTool schema support for optional subagent_type (handled
//     in the agenttool package once forks land; this file exposes the
//     predicate hosts check).
//   - D05 · RunForkedAgent default runner: drives a child QueryEngine with
//     useExactTools=true, inheriting parent toolset + rendered system
//     prompt so the Anthropic cache key stays stable.
//   - D11 · exports fork-prefix fingerprint helper so tests can assert
//     byte-identical prefixes between two fork dispatches.
package agents

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"strings"
	"sync/atomic"
	"time"
)

// atomicDepsCell wraps an atomic.Value so DefaultForkRunner can thread a
// QueryDeps pointer without explicit synchronisation.
type atomicDepsCell struct{ v atomic.Value }

// Store sets the current deps (nil clears).
func (c *atomicDepsCell) Store(d QueryDeps) {
	if d == nil {
		c.v.Store((*depsHolder)(nil))
		return
	}
	c.v.Store(&depsHolder{deps: d})
}

// Load returns the current deps (or nil).
func (c *atomicDepsCell) Load() interface{} {
	v := c.v.Load()
	if v == nil {
		return nil
	}
	h, _ := v.(*depsHolder)
	if h == nil {
		return nil
	}
	return h.deps
}

// depsHolder is a shim that lets atomic.Value hold an interface nil-safely.
type depsHolder struct{ deps QueryDeps }

// ForkBoilerplateTag wraps the directive block so IsInForkChild can
// detect recursive fork attempts.
const ForkBoilerplateTag = "fork_directive"

// ForkDirectivePrefix tags the per-child directive text block — allows
// future heuristics (e.g. routing the directive to a summariser) to find
// the boundary without re-parsing the tag.
const ForkDirectivePrefix = "[FORK_DIRECTIVE]"

// ForkPlaceholderResult is the placeholder string written into every
// tool_result block when constructing the forked conversation. Must be
// identical across fork children for prompt-cache sharing.
const ForkPlaceholderResult = "Fork started — processing in background"

// ForkAgentType is the synthetic agent type name used for fork dispatches.
// The fork path never registers this in the active agent registry — it's
// used only for analytics + querySource gating.
const ForkAgentType = "fork"

// ForkSubagentEnabled mirrors isForkSubagentEnabled: only active when the
// env var is truthy AND we're not in coordinator mode AND the session is
// interactive. The coordinator/ and non-interactive branches are read
// from ambient env, which hosts can override via EngineConfig (not wired
// yet — Phase D coordinator mode lives in Wave 3).
func ForkSubagentEnabled() bool {
	if !isEnvTruthy(os.Getenv("UBUILDING_FORK_SUBAGENT")) {
		return false
	}
	if isEnvTruthy(os.Getenv("UBUILDING_COORDINATOR_MODE")) {
		return false
	}
	if isEnvTruthy(os.Getenv("UBUILDING_NON_INTERACTIVE")) {
		return false
	}
	return true
}

// BuildForkedMessages produces the message slice a fork child runs against.
//
// Layout:
//
//	[...ctxHistory, lastAssistant, user{ tool_results..., directive }]
//
// `lastAssistant` MUST be the parent's most recent assistant message whose
// tool_use blocks we're "placeholding". When the assistant has no tool_use
// blocks we fall back to a single user message with the directive.
func BuildForkedMessages(directive string, lastAssistant *Message) []Message {
	if lastAssistant == nil {
		return []Message{{
			Type:    MessageTypeUser,
			UUID:    "fork-user",
			Content: []ContentBlock{{Type: ContentBlockText, Text: buildChildDirective(directive)}},
		}}
	}

	// Extract every tool_use block from the assistant to mirror into
	// placeholder tool_result blocks.
	var toolUses []ContentBlock
	for _, blk := range lastAssistant.Content {
		if blk.Type == ContentBlockToolUse {
			toolUses = append(toolUses, blk)
		}
	}

	assistantCopy := *lastAssistant
	assistantCopy.UUID = lastAssistant.UUID + "-fork"
	assistantCopy.Content = append([]ContentBlock(nil), lastAssistant.Content...)

	var result []Message
	result = append(result, assistantCopy)

	if len(toolUses) == 0 {
		result = append(result, Message{
			Type:    MessageTypeUser,
			UUID:    "fork-user",
			Content: []ContentBlock{{Type: ContentBlockText, Text: buildChildDirective(directive)}},
		})
		return result
	}

	var userBlocks []ContentBlock
	for _, use := range toolUses {
		userBlocks = append(userBlocks, ContentBlock{
			Type:      ContentBlockToolResult,
			ToolUseID: use.ID,
			Content:   ForkPlaceholderResult,
		})
	}
	userBlocks = append(userBlocks, ContentBlock{
		Type: ContentBlockText,
		Text: buildChildDirective(directive),
	})

	result = append(result, Message{
		Type:    MessageTypeUser,
		UUID:    "fork-user",
		Content: userBlocks,
	})
	return result
}

// buildChildDirective wraps the directive in the fork boilerplate so the
// child's system prompt text is stable across children.
func buildChildDirective(directive string) string {
	return "<" + ForkBoilerplateTag + ">\n" +
		"STOP. READ THIS FIRST.\n\n" +
		"You are a forked worker process. You are NOT the main agent.\n\n" +
		"RULES:\n" +
		"1. Do NOT spawn sub-agents; execute directly.\n" +
		"2. Do NOT editorialize or add meta-commentary.\n" +
		"3. USE your tools directly (Read, Write, Bash, etc.).\n" +
		"4. Commit any file changes before reporting.\n" +
		"5. Report structured facts, then stop.\n" +
		"</" + ForkBoilerplateTag + ">\n\n" +
		ForkDirectivePrefix + directive
}

// IsInForkChild returns true when messages contain the fork boilerplate
// marker — used by AgentTool.Call to reject fork-inside-fork recursion.
func IsInForkChild(messages []Message) bool {
	marker := "<" + ForkBoilerplateTag + ">"
	for _, m := range messages {
		if m.Type != MessageTypeUser {
			continue
		}
		for _, blk := range m.Content {
			if blk.Type == ContentBlockText && strings.Contains(blk.Text, marker) {
				return true
			}
		}
	}
	return false
}

// ForkPrefixFingerprint returns a deterministic hex digest over every
// field that must stay byte-identical across fork children for prompt-
// cache reuse. D11 uses this in tests to assert parity.
func ForkPrefixFingerprint(params CacheSafeParams) string {
	h := sha256.New()
	h.Write([]byte(params.SystemPrompt))
	for _, k := range sortedKeys(params.UserContext) {
		h.Write([]byte("u:" + k + "=" + params.UserContext[k] + "\n"))
	}
	for _, k := range sortedKeys(params.SystemContext) {
		h.Write([]byte("s:" + k + "=" + params.SystemContext[k] + "\n"))
	}
	for _, m := range params.ForkContextMessages {
		h.Write([]byte("m:" + string(m.Type) + ":" + m.UUID + "\n"))
		for _, blk := range m.Content {
			h.Write([]byte("b:" + string(blk.Type) + ":"))
			if blk.Text != "" {
				h.Write([]byte(blk.Text))
			}
			if blk.ID != "" {
				h.Write([]byte("|id=" + blk.ID))
			}
			if blk.ToolUseID != "" {
				h.Write([]byte("|tu=" + blk.ToolUseID))
			}
			h.Write([]byte("\n"))
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}

func sortedKeys(m map[string]string) []string {
	if len(m) == 0 {
		return nil
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	// Inline insertion sort — inputs are tiny.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// D05 · default fork runner.
// ---------------------------------------------------------------------------

// DefaultForkRunner drives a new QueryEngine against the supplied cache-
// safe params. The engine reuses the parent's SystemPrompt bytes and does
// not recompute env details — byte identity is the point.
//
// Hosts with deeper integration (streaming progress, transcript writes)
// can register a bespoke runner via RegisterForkRunner before SpawnSubAgent.
func DefaultForkRunner(ctx context.Context, params ForkedAgentParams) (*ForkedAgentResult, error) {
	parent := params.CacheSafeParams.ToolUseContext
	if parent == nil {
		return nil, errForkRunnerUnavailable
	}

	// Build a child engine whose BaseSystemPrompt is exactly the parent's
	// rendered prompt — no enhancement applied.
	cfg := EngineConfig{
		UserSpecifiedModel: parent.Options.MainLoopModel,
		BaseSystemPrompt:   params.CacheSafeParams.SystemPrompt,
		Cwd:                "", // child inherits cwd via its host-supplied deps
		MaxTurns:           params.MaxTurns,
	}
	if params.MaxTurns == 0 {
		cfg.MaxTurns = 20
	}

	// Seed the child with the parent context + prompt messages so the
	// prefix matches `[...ctxHistory, lastAssistant, user{results+directive}]`.
	initial := append([]Message(nil), params.CacheSafeParams.ForkContextMessages...)
	initial = append(initial, params.PromptMessages...)

	// Locate a QueryDeps to drive the child. The ForkedAgentParams route
	// doesn't currently thread deps — callers must register a runner
	// on an engine instance (see engineForkRunner below) to pick them up.
	deps, ok := deps4Fork.Load().(QueryDeps)
	if !ok || deps == nil {
		return nil, errForkRunnerUnavailable
	}
	child := NewQueryEngine(cfg, deps)
	child.UpdateMessages(initial[:len(initial)-1]) // last prompt message goes through SubmitMessage

	last := initial[len(initial)-1]
	var promptText string
	for _, blk := range last.Content {
		if blk.Type == ContentBlockText {
			promptText += blk.Text
		}
	}
	start := time.Now()
	final, err := drainSubagentStream(ctx, child.SubmitMessage(ctx, promptText))

	// E05 · emit fork analytics regardless of error so hosts can track
	// failure rates too.
	event := ForkAnalyticsEvent{
		SessionID:  child.GetSessionID(),
		DurationMs: time.Since(start).Milliseconds(),
		Usage:      child.GetUsage(),
	}
	if parent.QueryTracking != nil {
		event.ChainID = parent.QueryTracking.ChainID
		event.Depth = parent.QueryTracking.Depth + 1
	}
	if parent.AgentType != "" {
		event.AgentType = parent.AgentType
	}
	if err != nil {
		event.Error = err.Error()
	}
	LogForkAgentQueryEvent(event)

	if err != nil {
		return nil, err
	}
	return &ForkedAgentResult{
		Messages:  child.GetMessages(),
		FinalText: final,
	}, nil
}

// deps4Fork is a package-level atomic pointer so DefaultForkRunner can
// access a QueryDeps without API changes to RunForkedAgent (ForkedAgentParams
// doesn't yet carry one). Hosts set this once during engine bootstrap via
// EnableEngineDrivenForkRunner; tests poke it directly.
var deps4Fork atomicDepsCell

// EnableEngineDrivenForkRunner installs the DefaultForkRunner with the
// supplied deps. Returns a cleanup closure that unregisters the runner
// (useful in tests).
func EnableEngineDrivenForkRunner(deps QueryDeps) func() {
	deps4Fork.Store(deps)
	RegisterForkRunner(DefaultForkRunner)
	return func() {
		RegisterForkRunner(nil)
		deps4Fork.Store(nil)
	}
}
