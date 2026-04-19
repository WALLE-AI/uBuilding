// Package agents — fork infrastructure.
//
// Task E01 · CacheSafeParams shared channel.
// Task E02 · runForkedAgent generic API.
//
// These correspond to `src/utils/forkedAgent.ts::{saveCacheSafeParams,
// getLastCacheSafeParams,runForkedAgent}`. The fork path proper — recursive
// guards, tombstone rewrites, prompt-cache-identical prefix — lives in Phase
// D. Wave 1 ships the minimal plumbing so callers (session memory
// summaries, /btw, etc.) can start writing against a stable API while the
// runtime semantics catch up.
package agents

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
)

// CacheSafeParams captures the inputs that must stay identical between
// parent and fork to preserve the parent's prompt cache. Mirrors the TS
// CacheSafeParams struct; opaque fields stay interface{} to avoid import
// cycles with compact/ and prompt/.
type CacheSafeParams struct {
	// SystemPrompt is the already-rendered system prompt string. Byte-
	// identical reuse is what preserves the Anthropic prompt cache.
	SystemPrompt string

	// UserContext is the per-turn context map (CLAUDE.md, directory tree,
	// etc.). Kept as map[string]string for parity with TS.
	UserContext map[string]string

	// SystemContext carries stable environment facts appended to the system
	// prompt (cwd, git status). Same key/value shape as UserContext.
	SystemContext map[string]string

	// ToolUseContext is the fully populated context that the forked query
	// will run under. Callers clone with CreateSubagentContext before
	// handing the value here.
	ToolUseContext *ToolUseContext

	// ForkContextMessages is the parent conversation slice the fork
	// inherits. When non-empty, the fork replays these messages before its
	// own prompt. Empty = fresh fork (Phase D uses this for the "explicit
	// subagent_type" path).
	ForkContextMessages []Message
}

// cacheSafeParamsSlot is the package-wide "last stored" cache. Post-turn
// hooks write into it so background forks (summaries, /btw) can recover
// the parent's cache without threading params through every call-site.
var cacheSafeParamsSlot struct {
	mu  sync.RWMutex
	val *CacheSafeParams
}

// SaveCacheSafeParams records p as the most-recent cache-safe snapshot.
// Passing nil clears the slot; concurrent callers see a consistent value.
func SaveCacheSafeParams(p *CacheSafeParams) {
	cacheSafeParamsSlot.mu.Lock()
	defer cacheSafeParamsSlot.mu.Unlock()
	cacheSafeParamsSlot.val = p
}

// GetLastCacheSafeParams returns the most-recent snapshot (nil if none).
// Returns a shallow copy so callers can mutate top-level fields without
// stepping on subsequent writes, but shared pointers inside the struct
// (ToolUseContext, Messages) remain shared by design.
func GetLastCacheSafeParams() *CacheSafeParams {
	cacheSafeParamsSlot.mu.RLock()
	defer cacheSafeParamsSlot.mu.RUnlock()
	if cacheSafeParamsSlot.val == nil {
		return nil
	}
	cp := *cacheSafeParamsSlot.val
	return &cp
}

// -----------------------------------------------------------------------------
// E02 · runForkedAgent generic API
// -----------------------------------------------------------------------------

// ForkedAgentParams controls RunForkedAgent. Mirrors the TS type of the
// same name — optional fields leave zero-values; required fields are
// documented inline.
type ForkedAgentParams struct {
	// PromptMessages is the fork-specific prompt appended to the parent
	// conversation. Required.
	PromptMessages []Message

	// CacheSafeParams carries the parent's system prompt + context and is
	// required for cache-identical prefixes. Callers typically source this
	// from GetLastCacheSafeParams().
	CacheSafeParams CacheSafeParams

	// QuerySource tags the fork for analytics (mirrors TS `QuerySource`).
	QuerySource string

	// ForkLabel labels the fork in logs/events (e.g. "session_memory").
	ForkLabel string

	// Overrides lets callers tweak the child ToolUseContext (share
	// callbacks, permission mode, etc.).
	Overrides SubagentContextOverrides

	// MaxOutputTokens caps the fork's single-turn output budget. Zero =
	// engine default.
	MaxOutputTokens int

	// MaxTurns caps the fork's loop length. Zero = engine default.
	MaxTurns int

	// OnMessage receives every forwarded Message. Useful for streaming UI.
	OnMessage func(Message)

	// SkipTranscript disables sidechain transcript recording (Phase D
	// hook; no-op in Wave 1).
	SkipTranscript bool

	// SkipCacheWrite marks the fork as fire-and-forget; the prompt cache
	// entry must not be written back (Phase D hook; no-op in Wave 1).
	SkipCacheWrite bool
}

// ForkedAgentResult is the output of RunForkedAgent.
type ForkedAgentResult struct {
	// Messages is the forked conversation (parent context + fork output).
	Messages []Message

	// FinalText is the concatenation of the final assistant message's text
	// blocks. Empty when the fork produced no assistant reply.
	FinalText string

	// TotalUsage accumulates usage across the fork's API calls.
	TotalUsage Usage
}

// ForkRunner executes the forked sub-query. Wave 1 exposes only the
// signature; Phase D swaps in the production implementation that drives
// query() directly with byte-identical prefixes. Hosts can register a
// custom runner to integrate with their own engine wiring.
type ForkRunner func(ctx context.Context, params ForkedAgentParams) (*ForkedAgentResult, error)

var (
	forkRunnerMu sync.RWMutex
	forkRunner   ForkRunner
)

// RegisterForkRunner installs the ForkRunner used by RunForkedAgent. Pass
// nil to clear (restores the default "not wired" error). Tests use this to
// inject a deterministic runner.
func RegisterForkRunner(fn ForkRunner) {
	forkRunnerMu.Lock()
	defer forkRunnerMu.Unlock()
	forkRunner = fn
}

// RunForkedAgent dispatches to the registered ForkRunner. Returns an error
// when no runner is installed — callers are expected to guard with
// HasForkRunner() (or register a default runner during engine bootstrap).
func RunForkedAgent(ctx context.Context, params ForkedAgentParams) (*ForkedAgentResult, error) {
	forkRunnerMu.RLock()
	runner := forkRunner
	forkRunnerMu.RUnlock()
	if runner == nil {
		return nil, errForkRunnerUnavailable
	}
	if err := validateForkedAgentParams(params); err != nil {
		return nil, err
	}
	return runner(ctx, params)
}

// HasForkRunner reports whether a ForkRunner has been installed.
func HasForkRunner() bool {
	forkRunnerMu.RLock()
	defer forkRunnerMu.RUnlock()
	return forkRunner != nil
}

// errForkRunnerUnavailable is returned when no ForkRunner is installed.
var errForkRunnerUnavailable = errors.New("RunForkedAgent: no ForkRunner registered (Phase D wires the default)")

// validateForkedAgentParams performs lightweight sanity checks that every
// ForkRunner implementation can rely on.
func validateForkedAgentParams(p ForkedAgentParams) error {
	if len(p.PromptMessages) == 0 {
		return errors.New("RunForkedAgent: PromptMessages required")
	}
	if strings.TrimSpace(p.CacheSafeParams.SystemPrompt) == "" {
		return errors.New("RunForkedAgent: CacheSafeParams.SystemPrompt required")
	}
	if p.CacheSafeParams.ToolUseContext == nil {
		return errors.New("RunForkedAgent: CacheSafeParams.ToolUseContext required")
	}
	return nil
}

// ---------------------------------------------------------------------------
// E03 · prepareForkedCommandContext
// ---------------------------------------------------------------------------

// PreparedForkedContext bundles everything a caller needs to drive a
// forked slash command / skill invocation. Mirrors TS
// PreparedForkedContext (forkedAgent.ts).
type PreparedForkedContext struct {
	// SkillContent is the expanded skill / command body with arguments
	// substituted (the $ARGUMENTS replacement happens upstream).
	SkillContent string

	// AllowedTools extends the child ToolUseContext's session allow rules
	// so the forked command can drive tools the parent didn't already
	// allow. Empty = no extra allow.
	AllowedTools []string

	// BaseAgent is the definition the fork runner should use. Callers
	// typically pass GeneralPurposeAgent; slash commands can override via
	// their `agent` frontmatter field.
	BaseAgent *AgentDefinition

	// PromptMessages is the initial conversation for the fork: a single
	// user message with SkillContent as its text body.
	PromptMessages []Message
}

// PrepareForkedCommandContext expands `command` with `args` and bundles
// the inputs a host needs to call RunForkedAgent. Nil / empty slots are
// allowed; the returned struct always has a non-nil BaseAgent.
//
// The `expand` callback lets callers plug in their own prompt template
// logic (e.g. $ARGUMENTS substitution). Returning "" falls back to cmd's
// raw text (via FetchRawCommand).
func PrepareForkedCommandContext(
	cmd *Command,
	args string,
	activeAgents []*AgentDefinition,
	expand func(cmd *Command, args string) string,
) (*PreparedForkedContext, error) {
	if cmd == nil {
		return nil, errors.New("PrepareForkedCommandContext: nil command")
	}
	body := ""
	if expand != nil {
		body = expand(cmd, args)
	}
	if body == "" {
		body = strings.TrimSpace(cmd.Name) // fallback: at least route to command
	}

	base := resolveCommandAgent(cmd, activeAgents)
	msgs := []Message{
		{
			Type:    MessageTypeUser,
			Subtype: "fork_command",
			UUID:    "cmd-" + cmd.Name,
			Content: []ContentBlock{{Type: ContentBlockText, Text: body}},
		},
	}
	return &PreparedForkedContext{
		SkillContent:   body,
		AllowedTools:   nil, // hosts can append via ApplyAllowedTools
		BaseAgent:      base,
		PromptMessages: msgs,
	}, nil
}

// ApplyAllowedTools returns a shallow copy of ctx with AllowedTools set to
// the supplied slice. Convenient helper so callers stay single-line.
func (p PreparedForkedContext) ApplyAllowedTools(tools []string) PreparedForkedContext {
	p.AllowedTools = append([]string(nil), tools...)
	return p
}

// resolveCommandAgent prefers an explicit frontmatter agent, otherwise the
// built-in general-purpose definition.
func resolveCommandAgent(cmd *Command, active []*AgentDefinition) *AgentDefinition {
	agentName := ""
	if cmd != nil {
		// Commands carry a free-form map; upstream code may stash an
		// `agent` frontmatter value here. We support `cmd.Aliases` as a
		// common carrier in the external port; slash-command frontmatter
		// parsers can populate it with "agent=<type>".
		for _, alias := range cmd.Aliases {
			if strings.HasPrefix(alias, "agent=") {
				agentName = strings.TrimSpace(strings.TrimPrefix(alias, "agent="))
				break
			}
		}
	}
	for _, a := range active {
		if a == nil {
			continue
		}
		if agentName != "" && a.AgentType == agentName {
			return a
		}
	}
	// Fallback: first active agent, otherwise the built-in general-purpose.
	for _, a := range active {
		if a != nil {
			return a
		}
	}
	gp := GeneralPurposeAgent
	return &gp
}

// ---------------------------------------------------------------------------
// E04 · SubagentContextOverrides completeness
// ---------------------------------------------------------------------------

// ApplyOverridesToContext copies every override field onto tc that the
// Wave 2 surface supports. Kept as a free function so callers can re-use
// the logic outside CreateSubagentContext — fork helpers, test harnesses,
// or E03 runners mutate an existing context to match a different agent
// definition's expectations.
func ApplyOverridesToContext(tc *ToolUseContext, over SubagentContextOverrides) {
	if tc == nil {
		return
	}
	if over.AgentID != "" {
		tc.AgentID = over.AgentID
	}
	if over.AgentType != "" {
		tc.AgentType = over.AgentType
	}
	if over.ToolUseID != "" {
		tc.ToolUseID = over.ToolUseID
	}
	if over.RenderedSystemPrompt != "" {
		tc.RenderedSystemPrompt = over.RenderedSystemPrompt
	}
	if over.ContentReplacementState != nil {
		tc.ContentReplacementState = over.ContentReplacementState
	}
	if over.AgentPermissionMode != "" {
		tc.Options.AgentPermissionMode = over.AgentPermissionMode
	}
	if over.ShouldAvoidPermissionPrompts {
		tc.Options.ShouldAvoidPermissionPromptsOverride = true
	}
	if len(over.Messages) > 0 {
		tc.Messages = append([]Message(nil), over.Messages...)
	}
}

// ExtractForkFinalText mirrors the TS helper of the same name: return the
// last assistant message's text blocks concatenated with newlines, or
// defaultText when no assistant message is found.
func ExtractForkFinalText(messages []Message, defaultText string) string {
	for i := len(messages) - 1; i >= 0; i-- {
		m := messages[i]
		if m.Type != MessageTypeAssistant {
			continue
		}
		var parts []string
		for _, block := range m.Content {
			if block.Type == ContentBlockText && block.Text != "" {
				parts = append(parts, block.Text)
			}
		}
		if len(parts) > 0 {
			return strings.Join(parts, "\n")
		}
	}
	return defaultText
}

// formatForkErr wraps an error with fork context. Exposed so ForkRunner
// implementations can emit consistent error messages.
func formatForkErr(label string, err error) error {
	return fmt.Errorf("fork[%s]: %w", label, err)
}

// keep formatForkErr referenced so future unused-warning lints stay quiet.
var _ = formatForkErr
