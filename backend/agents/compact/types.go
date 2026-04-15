package compact

import (
	"github.com/wall-ai/ubuilding/backend/agents"
)

// CompactResult holds the outcome of any compaction pass.
type CompactResult struct {
	Messages    []agents.Message `json:"messages"`
	TokensSaved int              `json:"tokens_saved"`
	Applied     bool             `json:"applied"`
	Summary     string           `json:"summary,omitempty"`
}

// CompactBoundary represents a compaction boundary marker in the message history.
// Messages before the boundary are compacted; messages after are preserved.
type CompactBoundary struct {
	Index   int    `json:"index"`
	Summary string `json:"summary"`
}

// Compactor defines the interface for context compression strategies.
type Compactor interface {
	// Compact applies the compression strategy to the given messages.
	// Returns nil if no compaction was applied.
	Compact(messages []agents.Message, systemPrompt string) *CompactResult
}

// AutoCompactTrackingState holds mutable state for auto-compaction across turns.
// This corresponds to TS autoCompactTrackingState.
type AutoCompactTrackingState struct {
	Compacted           bool   `json:"compacted"`
	TurnID              string `json:"turn_id"`
	TurnCounter         int    `json:"turn_counter"`
	ConsecutiveFailures int    `json:"consecutive_failures"`
}

// Thresholds for triggering compaction (from query.ts).
const (
	// AutoCompactThreshold triggers LLM-based summarization at 80% of context window.
	AutoCompactThreshold = 0.80

	// TokenWarningThreshold triggers a warning message at 85%.
	TokenWarningThreshold = 0.85

	// TokenBlockingThreshold prevents new queries at 95%.
	TokenBlockingThreshold = 0.95

	// DefaultContextWindow is the default assumed context window size in tokens.
	DefaultContextWindow = 200000

	// MaxConsecutiveCompactFailures is the circuit breaker threshold.
	MaxConsecutiveCompactFailures = 3
)
