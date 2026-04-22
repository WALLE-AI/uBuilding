package memory

import (
	"fmt"
	"os"

	"github.com/wall-ai/ubuilding/backend/agents"
)

// ---------------------------------------------------------------------------
// M14.I1 · AutoDream configuration.
//
// Ports `src/services/autoDream/config.ts`.
//
// The auto-dream (memory consolidation) subsystem periodically reviews
// recent sessions and reorganises the auto-memory directory. This file
// provides the gate check and tuning knobs.
// ---------------------------------------------------------------------------

// Environment variable names for auto-dream.
const (
	EnvAutoDreamEnabled     = "UBUILDING_ENABLE_AUTO_DREAM"
	EnvAutoDreamMinHours    = "UBUILDING_AUTO_DREAM_MIN_HOURS"
	EnvAutoDreamMinSessions = "UBUILDING_AUTO_DREAM_MIN_SESSIONS"
)

// AutoDreamConfig holds the thresholds that drive the auto-dream gate
// chain. All fields can be overridden via environment variables.
type AutoDreamConfig struct {
	// MinHours is the minimum number of hours since the last successful
	// consolidation before a new one may be attempted. Default: 24.
	MinHours int

	// MinSessions is the minimum number of sessions (with transcript
	// files touched since the last consolidation) required before a
	// new consolidation may be attempted. Default: 3.
	MinSessions int
}

// DefaultAutoDreamConfig mirrors the upstream TS defaults.
var DefaultAutoDreamConfig = AutoDreamConfig{
	MinHours:    24,
	MinSessions: 3,
}

// GetAutoDreamConfig returns the effective auto-dream configuration,
// applying any environment-variable overrides on top of the defaults.
func GetAutoDreamConfig() AutoDreamConfig {
	cfg := DefaultAutoDreamConfig

	if v := os.Getenv(EnvAutoDreamMinHours); v != "" {
		var n int
		if _, err := fmt.Sscanf(v, "%d", &n); err == nil && n > 0 {
			cfg.MinHours = n
		}
	}
	if v := os.Getenv(EnvAutoDreamMinSessions); v != "" {
		var n int
		if _, err := fmt.Sscanf(v, "%d", &n); err == nil && n > 0 {
			cfg.MinSessions = n
		}
	}

	return cfg
}

// IsAutoDreamEnabled reports whether the auto-dream consolidation
// subsystem is active. Auto-dream is a strict subset of auto-memory —
// when auto-memory is off, auto-dream is off regardless.
//
// Priority chain (first match wins):
//
//  1. UBUILDING_ENABLE_AUTO_DREAM env truthy/falsy → that value.
//  2. cfg.AutoDreamEnabled                         → that value.
//  3. Default                                      → false (opt-in).
func IsAutoDreamEnabled(cfg agents.EngineConfig, settings SettingsProvider) bool {
	if !IsAutoMemoryEnabled(cfg, settings) {
		return false
	}
	if v, ok := envBool(EnvAutoDreamEnabled); ok {
		return v
	}
	return cfg.AutoDreamEnabled
}
