package session_memory

import (
	"time"

	"github.com/wall-ai/ubuilding/backend/agents/compact"
)

// ---------------------------------------------------------------------------
// M1.I2 · Shared types for the session-memory subsystem.
//
// SessionMemoryCompactConfig is re-exported verbatim from the compact
// package so callers can import only `session_memory` when wiring the
// compact bridge in M12. The SessionMemoryConfig / SessionState types
// are declared here as skeletons; the full implementation lives in
// config.go (M10.I1) and state.go (M10.I2).
// ---------------------------------------------------------------------------

// SessionMemoryCompactConfig re-exports compact.SessionMemoryCompactConfig
// so M12 callers do not need a direct compact import.
type SessionMemoryCompactConfig = compact.SessionMemoryCompactConfig

// DefaultSessionMemoryCompactConfig mirrors compact's default values —
// re-export kept for API ergonomics.
var DefaultSessionMemoryCompactConfig = compact.DefaultSessionMemoryCompactConfig

// SessionMemoryConfig holds the thresholds that drive the
// post-sampling extraction hook. Defaults are populated by
// DefaultSessionMemoryConfig (M10.I1).
//
// Field semantics mirror TypeScript's DEFAULT_SESSION_MEMORY_CONFIG:
//
//	MinimumMessageTokensToInit — tokens required before we attempt the
//	    first extraction (10 000 in TS).
//	MinimumTokensBetweenUpdate — tokens that must accumulate between
//	    consecutive extractions (5 000 in TS).
//	ToolCallsBetweenUpdates    — minimum number of tool calls since the
//	    last update before we re-run extraction (3 in TS).
type SessionMemoryConfig struct {
	MinimumMessageTokensToInit int `json:"minimum_message_tokens_to_init"`
	MinimumTokensBetweenUpdate int `json:"minimum_tokens_between_update"`
	ToolCallsBetweenUpdates    int `json:"tool_calls_between_updates"`
}

// SessionState holds per-session mutable state for the extraction
// pipeline. Every field is guarded by the Mutex defined alongside the
// state.go implementation (M10.I2).
//
// The struct is declared here so other files (extractor.go,
// compact_bridge.go) can take a *SessionState parameter without an
// import cycle or type redeclaration.
type SessionState struct {
	// LastSummarizedMessageID tracks the message UUID that was last
	// successfully summarised into notes.md. Compact uses this as the
	// starting point when it recomputes messages-to-keep.
	LastSummarizedMessageID string

	// ExtractionStartedAt is set when an extraction goroutine begins
	// and cleared on completion. It is used to coalesce concurrent
	// triggers (only one extraction at a time per session).
	ExtractionStartedAt time.Time

	// TokensAtLastExtraction records the rolling token count at the
	// moment the last extraction ran. The extractor compares the
	// current count against this value plus MinimumTokensBetweenUpdate
	// to decide whether an update is due.
	TokensAtLastExtraction int

	// Initialized flips to true after the first successful setup of
	// notes.md. It is used to distinguish "first-time init" from
	// "update" threshold calculations.
	Initialized bool
}
