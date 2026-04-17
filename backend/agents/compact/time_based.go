package compact

import "time"

// ---------------------------------------------------------------------------
// Time-based Microcompact Configuration
// Maps to TypeScript src/services/compact/timeBasedMCConfig.ts
//
// Triggers content-clearing microcompact when the gap since the last main-loop
// assistant message exceeds a threshold — the server-side prompt cache has
// almost certainly expired by then, so clearing old tool results before the
// next request shrinks the rewrite without forcing an additional miss.
// ---------------------------------------------------------------------------

// TimeBasedMCConfig controls time-based microcompaction.
type TimeBasedMCConfig struct {
	// Enabled is the master switch. When false, time-based microcompact is a no-op.
	Enabled bool

	// GapThresholdMinutes triggers a clear when (now - last assistant timestamp)
	// exceeds this many minutes. 60 is the safe default — Anthropic's 1h cache
	// TTL is guaranteed expired at that point.
	GapThresholdMinutes int

	// KeepRecent is the number of most-recent compactable tool results to keep.
	// Older results are cleared. When 0, falls back to DefaultKeepRecent.
	KeepRecent int
}

// Defaults mirror TS TIME_BASED_MC_CONFIG_DEFAULTS.
const (
	DefaultTimeBasedGapMinutes = 60
	DefaultKeepRecent          = 5
)

// DefaultTimeBasedMCConfig returns a disabled config with safe defaults.
// Maps to TIME_BASED_MC_CONFIG_DEFAULTS.
func DefaultTimeBasedMCConfig() TimeBasedMCConfig {
	return TimeBasedMCConfig{
		Enabled:             false,
		GapThresholdMinutes: DefaultTimeBasedGapMinutes,
		KeepRecent:          DefaultKeepRecent,
	}
}

// ShouldTrigger returns true when the config is enabled AND the gap since
// lastAssistantAt exceeds the configured threshold. lastAssistantAt zero
// means no prior assistant message — in that case triggering is skipped.
func (c TimeBasedMCConfig) ShouldTrigger(now, lastAssistantAt time.Time) bool {
	if !c.Enabled {
		return false
	}
	if lastAssistantAt.IsZero() {
		return false
	}
	threshold := c.GapThresholdMinutes
	if threshold <= 0 {
		threshold = DefaultTimeBasedGapMinutes
	}
	gap := now.Sub(lastAssistantAt)
	return gap >= time.Duration(threshold)*time.Minute
}

// EffectiveKeepRecent returns the configured KeepRecent, or the default when 0.
func (c TimeBasedMCConfig) EffectiveKeepRecent() int {
	if c.KeepRecent <= 0 {
		return DefaultKeepRecent
	}
	return c.KeepRecent
}
