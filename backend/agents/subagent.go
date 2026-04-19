// Package agents — sub-agent orchestration.
//
// Tasks A08 · A09 · A12 · A20 · ports the minimal runtime surface needed to
// spawn a sub-query from the Task tool, mirroring runAgent.ts's sync branch.
// This file intentionally does NOT yet ship fork / async / worktree / MCP
// wiring — those arrive in Phases B+D.
//
// The high-level flow is:
//
//  1. Resolve the target AgentDefinition (SubagentType, or default).
//  2. Enforce a recursion depth cap via a ctx-scoped counter (A12).
//  3. Render the agent's system prompt (A02 placeholder enhance).
//  4. Build a child QueryEngine that shares the parent's deps but carries
//     its own message history and system prompt.
//  5. Drain the child's SubmitMessage stream; capture the last assistant
//     text as the sub-agent's final answer (A20 recordable-message filter
//     decides which events accumulate into the response).
//
// The child engine is a full QueryEngine — NOT a new sub-runner — so every
// downstream invariant (compression, hooks, stop-reason handling) applies
// unchanged to the sub-query.
package agents

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
)

// -----------------------------------------------------------------------------
// Errors (A12)
// -----------------------------------------------------------------------------

// ErrSubagentDepthExceeded is returned when the requested SpawnSubAgent call
// would exceed the configured nesting cap.
var ErrSubagentDepthExceeded = errors.New("subagent recursion depth exceeded")

// ErrUnknownSubagentType is returned when SubagentType cannot be resolved to
// any active agent (and the fallback general-purpose is unavailable).
var ErrUnknownSubagentType = errors.New("unknown subagent_type")

// DefaultMaxSubagentDepth is the hard cap when EngineConfig does not set one.
const DefaultMaxSubagentDepth = 3

// -----------------------------------------------------------------------------
// Depth tracking (A12)
// -----------------------------------------------------------------------------

type subagentDepthKey struct{}

// SubagentDepthFromContext reports the current sub-agent nesting depth
// carried on ctx. Top-level invocations return 0.
func SubagentDepthFromContext(ctx context.Context) int {
	v := ctx.Value(subagentDepthKey{})
	if v == nil {
		return 0
	}
	d, _ := v.(int)
	return d
}

// withIncrementedSubagentDepth returns a child context whose depth counter
// is parent+1. Always returns a non-nil ctx so callers can defer cleanup
// without nil-checking.
func withIncrementedSubagentDepth(ctx context.Context) context.Context {
	return context.WithValue(ctx, subagentDepthKey{}, SubagentDepthFromContext(ctx)+1)
}

// WithSubagentDepth returns a child ctx with the depth counter set to the
// given value. Exposed so hosts / tests that bridge through a different
// runtime (e.g. HTTP) can seed depth before handing the ctx to SpawnSubAgent.
func WithSubagentDepth(ctx context.Context, depth int) context.Context {
	if depth < 0 {
		depth = 0
	}
	return context.WithValue(ctx, subagentDepthKey{}, depth)
}

// -----------------------------------------------------------------------------
// SpawnSubAgent (A08)
// -----------------------------------------------------------------------------

// SpawnSubAgent executes a sub-query synchronously and returns the final
// assistant text. It is safe to assign to ToolUseContext.SpawnSubAgent —
// the Task tool calls it verbatim (see tool/agenttool/agenttool.go).
//
// Recursion guard (A12):
//   - Depth counter on ctx increments by 1 on each spawn.
//   - When the post-increment depth exceeds MaxSubagentDepth (or the default
//     DefaultMaxSubagentDepth), the call returns ErrSubagentDepthExceeded.
//   - MaxSubagentDepth < 0 disables the guard entirely.
func (e *QueryEngine) SpawnSubAgent(ctx context.Context, params SubAgentParams) (string, error) {
	if strings.TrimSpace(params.Prompt) == "" {
		return "", errors.New("SpawnSubAgent: prompt required")
	}

	// ---- Depth guard ----------------------------------------------------
	cap := e.subagentDepthCap()
	if cap >= 0 {
		newDepth := SubagentDepthFromContext(ctx) + 1
		if newDepth > cap {
			return "", fmt.Errorf("%w (depth=%d cap=%d)", ErrSubagentDepthExceeded, newDepth, cap)
		}
	}
	ctx = withIncrementedSubagentDepth(ctx)

	// ---- Agent resolution -----------------------------------------------
	agents := e.Agents()
	agentType := strings.TrimSpace(params.SubagentType)
	if agentType == "" {
		agentType = GeneralPurposeAgent.AgentType
	}
	def := agents.FindActive(agentType)
	if def == nil {
		// Fallback to built-in general-purpose regardless of loader state —
		// without this, engines configured with only custom agents would
		// reject `{subagent_type: ""}`.
		if agentType == GeneralPurposeAgent.AgentType {
			gp := GeneralPurposeAgent
			def = &gp
		} else {
			return "", fmt.Errorf("%w: %s", ErrUnknownSubagentType, agentType)
		}
	}

	// ---- Build the child QueryEngine ------------------------------------
	childCfg := e.buildChildEngineConfig(def, params)
	child := NewQueryEngine(childCfg, e.deps, WithLogger(e.logger))

	// ---- C02 · per-agent MCP bootstrap ----------------------------------
	mcpBundle, mcpErr := InitializeAgentMcpServers(ctx, e.config.MCPConnector, child.GetSessionID(), def)
	if mcpErr != nil {
		return "", fmt.Errorf("sub-agent %q MCP init: %w", def.AgentType, mcpErr)
	}
	defer func() { _ = mcpBundle.Cleanup(ctx) }()

	// ---- C05 · frontmatter hooks scoped to this agent ------------------
	if def.Hooks != nil && e.config.HookRegistry != nil {
		if _, regErr := RegisterFrontmatterHooks(e.config.HookRegistry, child.GetSessionID(), def.Hooks, true); regErr != nil {
			// Non-fatal: log via slog and move on, matching TS warn path.
			if e.logger != nil {
				e.logger.Warn("agent frontmatter hooks registration failed",
					"agent", def.AgentType, "err", regErr)
			}
		}
		defer ClearSessionHooks(e.config.HookRegistry, child.GetSessionID())
	}

	// ---- C03 · SubagentStart hooks + initial message pre-fill ----------
	var preludeMessages []Message
	if e.config.HookRegistry != nil {
		if contexts, hookErr := ExecuteSubagentStartHooks(ctx, e.config.HookRegistry, child.GetSessionID(), def.AgentType); hookErr == nil {
			if msg := AttachSubagentStartContext(child.GetSessionID(), def.AgentType, contexts); msg != nil {
				preludeMessages = append(preludeMessages, *msg)
			}
		}
	}

	// ---- C11 · flush per-agent skill invocation log on return ----------
	if e.config.SkillInvocationLog != nil {
		defer e.config.SkillInvocationLog.ClearInvokedSkillsForAgent(child.GetSessionID())
	}

	// ---- C06 · skills preload (content blocks as meta user messages) ---
	if len(def.Skills) > 0 && e.config.ResolveAgentSkill != nil {
		for _, skillName := range def.Skills {
			blocks, skillErr := e.config.ResolveAgentSkill(ctx, def.AgentType, skillName)
			if skillErr != nil {
				return "", fmt.Errorf("sub-agent %q skill %q: %w", def.AgentType, skillName, skillErr)
			}
			if len(blocks) == 0 {
				continue
			}
			preludeMessages = append(preludeMessages, Message{
				Type:    MessageTypeUser,
				Subtype: "skill_preload",
				UUID:    "skill-" + def.AgentType + "-" + skillName,
				Content: blocks,
			})
		}
	}

	// Seed the child's history with the prelude so the first SubmitMessage
	// call sees them as prior user context.
	if len(preludeMessages) > 0 {
		child.UpdateMessages(preludeMessages)
	}

	// C02 · forward agent-only MCP tools into the child's options. Tools
	// are thread-safe reads so sharing the MCPTool slice is fine.
	if len(mcpBundle.Tools) > 0 {
		// Reserve: the mcp tools are already represented in the parent's
		// pool when named references hit the shared cache; for inline
		// servers this is the only place the child sees them. We expose
		// them via the engine's child config for downstream wiring.
		// (The actual tool objects live with the host; we record the names
		// for analytics today and revisit wiring in Phase D fork path.)
		if e.logger != nil {
			names := make([]string, 0, len(mcpBundle.Tools))
			for _, t := range mcpBundle.Tools {
				names = append(names, t.Name)
			}
			e.logger.Debug("agent mcp tools ready", "agent", def.AgentType, "tools", names)
		}
	}

	// ---- Drain the child stream & collect the final assistant text ------
	final, err := drainSubagentStream(ctx, child.SubmitMessage(ctx, params.Prompt))
	if err != nil {
		return "", fmt.Errorf("sub-agent %q failed: %w", def.AgentType, err)
	}

	// ---- C04 · SubagentStop hooks fire on clean completion --------------
	if e.config.HookRegistry != nil {
		if _, stopErr := ExecuteSubagentStopHooks(ctx, e.config.HookRegistry, child.GetSessionID(), def.AgentType); stopErr != nil && e.logger != nil {
			e.logger.Warn("SubagentStop hooks reported error", "agent", def.AgentType, "err", stopErr)
		}
	}
	return final, nil
}

// subagentDepthCap returns the effective depth cap from config. Kept on the
// engine so tests can tweak config without going through helpers.
func (e *QueryEngine) subagentDepthCap() int {
	if e == nil {
		return DefaultMaxSubagentDepth
	}
	if e.config.MaxSubagentDepth < 0 {
		return -1 // disabled
	}
	if e.config.MaxSubagentDepth == 0 {
		return DefaultMaxSubagentDepth
	}
	return e.config.MaxSubagentDepth
}

// buildChildEngineConfig derives an EngineConfig for the sub-agent's own
// QueryEngine. Mirrors runAgent.ts's param assembly: model resolution,
// system prompt, max turns, and (B03) pre-filtered tool pool.
func (e *QueryEngine) buildChildEngineConfig(def *AgentDefinition, params SubAgentParams) EngineConfig {
	cfg := e.config
	// Child carries its own agent registry pointer (same slice) so nested
	// spawns still see the full catalog.
	cfg.Agents = e.Agents()

	// Model — GetAgentModel handles "inherit" + alias-matching (A17). The
	// SubAgentParams.Model takes precedence over the agent's frontmatter
	// model (matches TS AgentTool: tool-arg > agent definition).
	cfg.UserSpecifiedModel = GetAgentModel(def.Model, e.config.UserSpecifiedModel, params.Model, def.PermissionMode)

	// Tools — apply B01/B08 resolver so the child sees only the allowed
	// pool. Falls back to the parent list untouched when no resolver is
	// configured (keeps A-phase tests working).
	if e.config.ResolveSubagentTools != nil {
		cfg.Tools = e.config.ResolveSubagentTools(e.config.Tools, def, def.Background)
	}

	// System prompt — agent definition renders with enhancement placeholder.
	promptCtx := SystemPromptCtx{
		Model:                        cfg.UserSpecifiedModel,
		Tools:                        collectToolNames(cfg.Tools),
		AdditionalWorkingDirectories: e.config.AdditionalWorkingDirs,
		Cwd:                          e.config.Cwd,
	}
	base := def.RenderSystemPrompt(promptCtx)

	// C08 · persistent agent memory injection. Fires only when the agent
	// definition opts in with a non-empty scope; BuildMemoryPrompt is
	// side-effect safe (creates the directory if missing, no-ops otherwise).
	if def.Memory != AgentMemoryScopeNone {
		memCfg := e.config.AgentMemoryConfig
		if memCfg.Cwd == "" {
			memCfg.Cwd = e.config.Cwd
		}
		if mem := BuildMemoryPrompt(def.AgentType, def.Memory, memCfg); mem != "" {
			if base == "" {
				base = mem
			} else {
				base = base + "\n\n" + mem
			}
		}
	}

	cfg.BaseSystemPrompt = EnhanceSystemPromptWithEnvDetails(base, promptCtx)

	// The child should NOT re-run the parent's prompt builder — fall back
	// to the legacy single-layer prompt path.
	cfg.BuildSystemPromptFn = nil
	cfg.AgentSystemPrompt = ""
	cfg.CoordinatorSystemPrompt = ""
	cfg.OverrideSystemPrompt = ""

	// Max turns priority: params → agent → DefaultSubagentMaxTurns → parent.
	switch {
	case params.MaxTurns > 0:
		cfg.MaxTurns = params.MaxTurns
	case def.MaxTurns > 0:
		cfg.MaxTurns = def.MaxTurns
	case e.config.DefaultSubagentMaxTurns > 0:
		cfg.MaxTurns = e.config.DefaultSubagentMaxTurns
	}

	// Child never inherits parent's committed budget — keep a fresh cost
	// window so a long child doesn't immediately trip the parent's cap.
	cfg.MaxBudgetUSD = 0
	cfg.TaskBudget = nil

	// Pre-query hooks that only make sense for the top-level loop must be
	// cleared so child doesn't re-run /-command parsing etc.
	cfg.LoadMemories = nil
	cfg.DiscoverSkills = nil
	cfg.OnCompactBoundary = nil

	// B04 · propagate agent permission mode to the child's
	// ToolUseContext.Options.AgentPermissionMode via defaultToolUseContext.
	cfg.SubagentPermissionMode = def.PermissionMode

	// A19 · surface OmitClaudeMd on the child config so host-wired
	// BuildSystemPromptFn implementations can skip the CLAUDE.md / git
	// injections. The legacy BaseSystemPrompt path (what this package
	// produces by default) already skips those since it renders solely
	// from the AgentDefinition.
	cfg.SubagentOmitClaudeMd = def.OmitClaudeMd

	// Force a non-interactive session for sub-agents — they cannot show UI.
	// (permission hooks check this flag via isNonInteractive in Phase B.)
	// Note: we keep MaxSubagentDepth so a child spawn still consults the
	// same cap, but the ctx-scoped counter is the actual enforcer.
	return cfg
}

// collectToolNames plucks Name() from a []interface{} of tool values. Used
// only to feed SystemPromptCtx.Tools; values that don't implement Name()
// are ignored.
func collectToolNames(pool []interface{}) []string {
	if len(pool) == 0 {
		return nil
	}
	type named interface{ Name() string }
	out := make([]string, 0, len(pool))
	for _, t := range pool {
		if n, ok := t.(named); ok {
			out = append(out, n.Name())
		}
	}
	return out
}

// Agents returns the engine's resolved AgentDefinitions. It is safe to call
// before the first SubmitMessage — lazy initialisation mirrors TS's
// memoized loadAgentsDir.
func (e *QueryEngine) Agents() *AgentDefinitions {
	if e == nil {
		return &AgentDefinitions{}
	}
	e.mu.RLock()
	if e.config.Agents != nil {
		defs := e.config.Agents
		e.mu.RUnlock()
		return defs
	}
	loader := e.config.AgentsLoader
	e.mu.RUnlock()
	if loader != nil {
		e.mu.Lock()
		// Re-check after acquiring the write lock.
		if e.config.Agents == nil {
			defs, errs := loader()
			if defs == nil {
				defs = &AgentDefinitions{}
			}
			e.config.Agents = defs
			if e.logger != nil {
				for _, err := range errs {
					e.logger.Warn("agent loader error", "path", err.Path, "err", err.Err)
				}
			}
		}
		defs := e.config.Agents
		e.mu.Unlock()
		return defs
	}
	// No loader / no registry — synthesize the built-in set so Task still works.
	defs := &AgentDefinitions{ActiveAgents: DefaultBuiltInAgents()}
	for _, a := range defs.ActiveAgents {
		defs.AllAgents = append(defs.AllAgents, a)
	}
	defs.RefreshLegacy()
	e.mu.Lock()
	if e.config.Agents == nil {
		e.config.Agents = defs
	} else {
		defs = e.config.Agents
	}
	e.mu.Unlock()
	return defs
}

// -----------------------------------------------------------------------------
// Stream collection (A20)
// -----------------------------------------------------------------------------

// drainSubagentStream consumes every event emitted by the child's
// SubmitMessage and returns the final assistant text. Events that aren't
// "recordable" (stream deltas, progress, etc.) are discarded here — they
// belong to the child's internal accounting. Matches the TS
// isRecordableMessage gate in runAgent.ts.
func drainSubagentStream(ctx context.Context, stream <-chan StreamEvent) (string, error) {
	final := ""
	lastStopReason := ""
	isError := false
	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case ev, ok := <-stream:
			if !ok {
				// Channel closed → child completed.
				if isError {
					return "", fmt.Errorf("sub-agent ended with error (stop_reason=%s)", lastStopReason)
				}
				return strings.TrimSpace(final), nil
			}
			if !isRecordableEvent(ev) {
				continue
			}
			switch ev.Type {
			case EventAssistant:
				if ev.Message != nil {
					if ev.Message.IsApiError {
						isError = true
					}
					if text := extractAssistantText(ev.Message); text != "" {
						final = text
					}
					if ev.Message.StopReason != "" {
						lastStopReason = ev.Message.StopReason
					}
				}
			case EventAttachment:
				if ev.Message != nil && ev.Message.Attachment != nil &&
					ev.Message.Attachment.Type == "max_turns_reached" {
					// The child already emitted an error result; drain
					// remaining events so the channel closes cleanly.
					isError = true
				}
			case EventError:
				isError = true
				if ev.Error != "" {
					lastStopReason = ev.Error
				}
			}
		}
	}
}

// isRecordableEvent mirrors isRecordableMessage from runAgent.ts — we record
// assistant/user/progress/attachment/result events but drop stream deltas
// and raw system init events so the sub-agent text accumulator isn't
// polluted by transient updates.
func isRecordableEvent(ev StreamEvent) bool {
	switch ev.Type {
	case EventAssistant, EventUser, EventAttachment, EventProgress,
		EventResult, EventCompactBoundary, EventDone, EventError:
		return true
	default:
		return false
	}
}

// extractAssistantText concatenates the text blocks of an assistant message
// (sub-agents are expected to end with a pure-text turn; tool-call-only
// turns mean work is still in flight).
func extractAssistantText(msg *Message) string {
	if msg == nil {
		return ""
	}
	var b strings.Builder
	for _, block := range msg.Content {
		if block.Type == ContentBlockText && block.Text != "" {
			if b.Len() > 0 {
				b.WriteString("\n")
			}
			b.WriteString(block.Text)
		}
	}
	return b.String()
}

// -----------------------------------------------------------------------------
// Logger helper (keep slog import usage localised)
// -----------------------------------------------------------------------------

// _ensure slog import stays live even if future refactors drop the warn.
var _ = slog.LevelDebug
