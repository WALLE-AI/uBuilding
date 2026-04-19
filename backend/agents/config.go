package agents

import (
	"os"
	"strings"
)

// QueryConfig is an immutable snapshot of environment and feature gates
// taken at the start of each query loop iteration. Corresponds to
// TypeScript's buildQueryConfig() in query/config.ts.
type QueryConfig struct {
	// SessionID is the current session identifier.
	SessionID string

	// Gates holds feature gate values snapshotted at query start.
	Gates QueryGates
}

// QueryGates holds boolean feature flags for the query loop.
// These are snapshotted once at query start to ensure consistent behavior.
type QueryGates struct {
	FastModeEnabled        bool
	EmitToolUseSummaries   bool
	HistorySnip            bool
	ContextCollapse        bool
	TokenBudget            bool
	CachedMicrocompact     bool
	StreamingToolExecution bool
	ReactiveCompact        bool
	IsAnt                  bool
}

// BuildQueryConfig creates a new immutable QueryConfig snapshot.
// In the TypeScript codebase, this reads from GrowthBook feature flags;
// here we accept explicit values or read from configuration.
func BuildQueryConfig(sessionID string, gates QueryGates) QueryConfig {
	return QueryConfig{
		SessionID: sessionID,
		Gates:     gates,
	}
}

// BuildQueryConfigFromEngine derives a QueryConfig from an EngineConfig,
// applying environment variable overrides where applicable.
func BuildQueryConfigFromEngine(sessionID string, cfg EngineConfig) QueryConfig {
	gates := QueryGates{
		FastModeEnabled:        !isEnvTruthy(os.Getenv("CLAUDE_CODE_DISABLE_FAST_MODE")),
		EmitToolUseSummaries:   isEnvTruthy(os.Getenv("CLAUDE_CODE_EMIT_TOOL_USE_SUMMARIES")),
		HistorySnip:            true,
		ContextCollapse:        false, // opt-in
		TokenBudget:            cfg.TaskBudget != nil,
		CachedMicrocompact:     false, // opt-in
		StreamingToolExecution: true,  // default on
		ReactiveCompact:        true,  // default on
		IsAnt:                  isEnvTruthy(os.Getenv("USER_TYPE_ANT")),
	}

	// Allow env overrides
	if isEnvTruthy(os.Getenv("CLAUDE_CODE_DISABLE_STREAMING_TOOL_EXECUTION")) {
		gates.StreamingToolExecution = false
	}
	if isEnvTruthy(os.Getenv("DISABLE_AUTO_COMPACT")) {
		gates.ReactiveCompact = false
	}

	return BuildQueryConfig(sessionID, gates)
}

// isEnvTruthy returns true if the value is "1", "true", "yes", or "on"
// (case-insensitive, surrounding whitespace ignored). Mirrors TS
// isEnvTruthy. Shared by config and agent-builtin callers.
func isEnvTruthy(val string) bool {
	switch strings.ToLower(strings.TrimSpace(val)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}
