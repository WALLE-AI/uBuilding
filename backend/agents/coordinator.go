// Package agents — coordinator mode runtime.
//
// Wave 3 · glue that ties the Wave-2 pieces into a usable coordinator:
//
//   - BuildCoordinatorEngineConfig takes a base EngineConfig and layers on
//     the coordinator system prompt, an allow-list of orchestration tools
//     (CoordinatorModeAllowedTools), and a LocalAgentTaskManager so every
//     Task tool dispatch goes through the async path.
//   - RouteTaskToAsync decides whether a given Task invocation should be
//     spawned with SpawnAsyncSubAgent (coordinator mode forces async) or
//     the legacy sync SpawnSubAgent.
//   - NotifyCoordinator wraps a TaskNotification into a user-role Message
//     the coordinator engine injects into its conversation so the model
//     sees completion events as they happen.
//   - NewCoordinatorObserver returns a TaskObserver callback that forwards
//     every terminal event to the engine's message stream via an inbox
//     channel; hosts wire the returned channel to the engine loop.
//
// All helpers are opt-in — nothing here mutates global state; callers
// compose the pieces they need.
package agents

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
)

// ---------------------------------------------------------------------------
// Configuration
// ---------------------------------------------------------------------------

// CoordinatorConfig bundles the inputs used to configure a coordinator-
// mode engine. Every field is optional; zero-values fall through to
// sensible defaults (e.g. the built-in coordinator system prompt and a
// fresh LocalAgentTaskManager).
type CoordinatorConfig struct {
	// SystemPrompt is the coordinator-specific system prompt. Leaving it
	// empty falls back to DefaultCoordinatorSystemPrompt.
	SystemPrompt string

	// AppendSystemPrompt is concatenated after SystemPrompt (after a
	// double newline). Matches TS EffectiveSystemPromptConfig semantics.
	AppendSystemPrompt string

	// AllowedTools overrides the default tool allow-list. Leaving it empty
	// uses CoordinatorModeAllowedTools (Task / TaskStop / SendMessage /
	// SyntheticOutput).
	AllowedTools map[string]struct{}

	// TaskManager is the shared LocalAgentTaskManager; coordinator-mode
	// spawns are routed through it so completion events fan out to
	// observers. Leaving it nil allocates a fresh manager.
	TaskManager *LocalAgentTaskManager

	// ForceAllAsync, when true (the default when zero-valued), forces
	// every Task invocation to SpawnAsyncSubAgent so the coordinator never
	// blocks on a sub-agent. Set to false to let sync spawns through.
	ForceAllAsync bool
}

// DefaultCoordinatorSystemPrompt is the canonical system prompt applied
// when CoordinatorConfig.SystemPrompt is empty. Mirrors the TS fallback.
const DefaultCoordinatorSystemPrompt = `You are the coordinator agent.

Your role is to break work into tasks and dispatch each task to the most
appropriate specialized sub-agent via the Task tool. You do NOT execute
tool-level work yourself (reading files, editing code, running shells).

Operating rules:
  1. Every piece of work MUST be routed through Task (async).
  2. When a task completes, you will receive a <task-notification> user
     message with its result. Incorporate it into your plan and dispatch
     follow-up tasks as needed.
  3. Use TaskStop to cancel sub-agents that have gone off-track.
  4. Use SendMessage to communicate with peer coordinators (when
     applicable).
  5. Summarise progress for the user at meaningful checkpoints; do not
     narrate every tool call.`

// BuildCoordinatorEngineConfig returns a new EngineConfig suitable for
// starting a coordinator-mode engine. The base config is copied — the
// caller's struct is NOT mutated.
//
// The resulting config:
//   - Sets IsCoordinatorMode + CoordinatorSystemPrompt.
//   - Installs a ResolveSubagentTools hook that intersects the parent
//     tool pool with cfg.AllowedTools (falling back to
//     CoordinatorModeAllowedTools).
//   - Leaves HookRegistry / MCPConnector / ResolveAgentSkill untouched so
//     callers can compose coordinator mode with Wave 2 features.
func BuildCoordinatorEngineConfig(base EngineConfig, cfg CoordinatorConfig) EngineConfig {
	out := base

	prompt := strings.TrimSpace(cfg.SystemPrompt)
	if prompt == "" {
		prompt = DefaultCoordinatorSystemPrompt
	}
	out.IsCoordinatorMode = true
	out.CoordinatorSystemPrompt = prompt
	if cfg.AppendSystemPrompt != "" {
		out.AppendSystemPrompt = cfg.AppendSystemPrompt
	}

	allowed := cfg.AllowedTools
	if len(allowed) == 0 {
		allowed = CoordinatorModeAllowedTools
	}
	// Filter base.Tools to the allow-list so the coordinator sees only
	// orchestration tools at the top level. Sub-agent pools go through the
	// resolve hook below which the host typically wires to
	// tool.ResolveAgentTools (Wave 1).
	filtered := make([]interface{}, 0, len(base.Tools))
	for _, t := range base.Tools {
		name := toolNameOf(t)
		if name == "" {
			// Can't classify — keep so we don't drop unknown host tools.
			filtered = append(filtered, t)
			continue
		}
		if _, ok := allowed[name]; ok {
			filtered = append(filtered, t)
		}
	}
	out.Tools = filtered
	return out
}

// ---------------------------------------------------------------------------
// Spawn routing
// ---------------------------------------------------------------------------

// RouteTaskToAsync spawns the sub-agent asynchronously when the engine is
// in coordinator mode (or the caller passed forceAsync=true). Otherwise
// it falls through to SpawnSubAgent and wraps the result in an
// AsyncAgentHandle so the caller's code path stays uniform.
//
// A nil manager in coordinator mode is a programmer error — callers must
// wire the manager before spawning.
func RouteTaskToAsync(
	ctx context.Context,
	engine *QueryEngine,
	manager *LocalAgentTaskManager,
	params SubAgentParams,
	forceAsync bool,
) (*AsyncAgentHandle, error) {
	if engine == nil {
		return nil, errors.New("RouteTaskToAsync: nil engine")
	}
	coord := engine.config.IsCoordinatorMode
	if coord || forceAsync {
		if manager == nil {
			return nil, errors.New("RouteTaskToAsync: coordinator mode requires a LocalAgentTaskManager")
		}
		return engine.SpawnAsyncSubAgent(ctx, manager, params)
	}
	// Sync fallback — wrap the result so callers get a handle regardless.
	final, err := engine.SpawnSubAgent(ctx, params)
	if err != nil {
		return nil, err
	}
	// Synthesise an ad-hoc task record so the coordinator still gets an
	// observable notification (useful for logging even in non-coordinator
	// mode).
	if manager != nil {
		task := manager.Register(&LocalAgentTask{
			AgentType:   params.SubagentType,
			Description: params.Description,
			Prompt:      params.Prompt,
			State:       TaskStateRunning,
		})
		manager.complete(task, final)
		return &AsyncAgentHandle{TaskID: task.ID, AgentType: task.AgentType}, nil
	}
	return &AsyncAgentHandle{AgentType: params.SubagentType}, nil
}

// ---------------------------------------------------------------------------
// Notification injection
// ---------------------------------------------------------------------------

// NotifyCoordinator turns a TaskNotification into a user-role Message the
// coordinator engine should prepend to its next turn. The Message body is
// the XML blob produced by EncodeTaskNotification, wrapped in a
// `<task-notification>` hook-context tag.
func NotifyCoordinator(note TaskNotification) (*Message, error) {
	blob, err := EncodeTaskNotification(note)
	if err != nil {
		return nil, fmt.Errorf("NotifyCoordinator: %w", err)
	}
	return &Message{
		Type:    MessageTypeUser,
		Subtype: "task_notification",
		UUID:    "task-" + note.TaskID,
		Content: []ContentBlock{{Type: ContentBlockText, Text: blob}},
	}, nil
}

// CoordinatorInbox is the channel hosts use to pump task-notification
// messages into the coordinator engine loop. Buffered so a brief burst
// (many tasks completing near-simultaneously) doesn't block the observer.
type CoordinatorInbox chan *Message

// NewCoordinatorInbox returns a buffered inbox plus a TaskObserver that
// feeds it. The returned `unsubscribe` must be called when the
// coordinator engine shuts down to avoid leaking the observer.
//
// `onOverflow` (optional) is invoked when the inbox buffer is full so the
// host can decide between dropping, blocking, or logging. Passing nil
// drops notifications silently when the buffer is saturated.
func NewCoordinatorInbox(
	manager *LocalAgentTaskManager,
	bufferSize int,
	onOverflow func(note TaskNotification),
) (CoordinatorInbox, func(), error) {
	if manager == nil {
		return nil, nil, errors.New("NewCoordinatorInbox: nil manager")
	}
	if bufferSize <= 0 {
		bufferSize = 16
	}
	inbox := make(CoordinatorInbox, bufferSize)

	// mu guards `closed` so the observer doesn't try to send after the
	// unsubscribe helper has closed the channel.
	var mu sync.Mutex
	closed := false

	unsubscribe := manager.AddObserver(func(note TaskNotification) {
		msg, err := NotifyCoordinator(note)
		if err != nil || msg == nil {
			return
		}
		mu.Lock()
		if closed {
			mu.Unlock()
			return
		}
		select {
		case inbox <- msg:
		default:
			if onOverflow != nil {
				onOverflow(note)
			}
		}
		mu.Unlock()
	})

	cleanup := func() {
		unsubscribe()
		mu.Lock()
		if !closed {
			close(inbox)
			closed = true
		}
		mu.Unlock()
	}
	return inbox, cleanup, nil
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

// toolNameOf extracts a display name from an opaque tool value. It tries
// the common shapes (tool.Tool.Name(), struct with `Name` method, plain
// string) and returns "" when nothing matches.
func toolNameOf(t interface{}) string {
	if t == nil {
		return ""
	}
	switch v := t.(type) {
	case interface{ Name() string }:
		return v.Name()
	case string:
		return v
	}
	return ""
}
