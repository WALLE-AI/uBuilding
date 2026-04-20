// Package session_memory implements the session-memory subsystem ported
// from claude-code-main's `src/services/SessionMemory/*` and the
// orchestration half of `src/services/compact/sessionMemoryCompact.ts`.
//
// Session memory is a long-running, self-maintained scratchpad the
// model edits to preserve salient context across turns. It differs from
// the auto-memory / team-memory systems under `backend/agents/memory`:
//
//   - memory/       : instruction-layer CLAUDE.md + persistent fact-layer
//                     MEMORY.md files the *user* (and model) curate.
//   - session_memory: per-session notes.md + background extraction hook
//                     + compact-time integration.
//
// Sub-areas:
//
//   - types.go      : shared data model (M1).
//   - config.go     : SessionMemoryConfig defaults + getters/setters (M10).
//   - state.go      : per-session mutable state + WaitForExtraction (M10).
//   - paths.go      : session-scoped path resolution (M10).
//   - template.go   : default notes.md template + analysis helpers (M10).
//   - extractor.go  : ShouldExtractMemory + PostSamplingHook (M11).
//   - can_use_tool.go : Edit-only canUseTool factory (M11).
//   - compact_bridge.go : SM-compact orchestration (M12).
//
// All capabilities are opt-in via `EngineConfig.SessionMemoryEnabled`
// and the `UBUILDING_ENABLE_SESSION_MEMORY` env variable; the default
// zero value keeps the engine running exactly as before.
//
// Dependencies on the `agents` package are one-way: this package imports
// `agents` for `Message` / `ToolUseContext` / `EngineConfig`, and
// `agents` MUST NOT import `session_memory` back — integration happens
// through callable fields on `EngineConfig`.
package session_memory
