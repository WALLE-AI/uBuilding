package agents

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
	FastModeEnabled      bool
	EmitToolUseSummaries bool
	HistorySnip          bool
	ContextCollapse      bool
	TokenBudget          bool
	CachedMicrocompact   bool
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
