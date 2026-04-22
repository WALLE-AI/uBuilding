package session_memory

import (
	"os"
	"sync"
	"time"

	"github.com/wall-ai/ubuilding/backend/agents"
)

// ---------------------------------------------------------------------------
// M10.I1 · SessionMemory config — defaults, getters/setters, enablement.
//
// Ports `src/services/SessionMemory/sessionMemoryUtils.ts`.
// ---------------------------------------------------------------------------

// Environment variable names for session memory.
const (
	EnvEnableSessionMemory = "UBUILDING_ENABLE_SESSION_MEMORY"
)

// Extraction wait/stale constants (mirrors TS).
const (
	ExtractionWaitTimeout  = 15 * time.Second
	ExtractionStaleThreshold = 60 * time.Second
)

// DefaultSessionMemoryConfig mirrors TS DEFAULT_SESSION_MEMORY_CONFIG.
var DefaultSessionMemoryConfig = SessionMemoryConfig{
	MinimumMessageTokensToInit: 10_000,
	MinimumTokensBetweenUpdate: 5_000,
	ToolCallsBetweenUpdates:    3,
}

var (
	smConfigMu sync.RWMutex
	smConfig   = DefaultSessionMemoryConfig
)

// GetSessionMemoryConfig returns a copy of the current config.
func GetSessionMemoryConfig() SessionMemoryConfig {
	smConfigMu.RLock()
	defer smConfigMu.RUnlock()
	return smConfig
}

// SetSessionMemoryConfig merges the given partial config into the
// current one. Only positive values override defaults (matches TS).
func SetSessionMemoryConfig(partial SessionMemoryConfig) {
	smConfigMu.Lock()
	defer smConfigMu.Unlock()
	if partial.MinimumMessageTokensToInit > 0 {
		smConfig.MinimumMessageTokensToInit = partial.MinimumMessageTokensToInit
	}
	if partial.MinimumTokensBetweenUpdate > 0 {
		smConfig.MinimumTokensBetweenUpdate = partial.MinimumTokensBetweenUpdate
	}
	if partial.ToolCallsBetweenUpdates > 0 {
		smConfig.ToolCallsBetweenUpdates = partial.ToolCallsBetweenUpdates
	}
}

// ResetSessionMemoryConfig restores the default configuration.
// Primarily for testing.
func ResetSessionMemoryConfig() {
	smConfigMu.Lock()
	defer smConfigMu.Unlock()
	smConfig = DefaultSessionMemoryConfig
}

// IsSessionMemoryEnabled reports whether the session-memory subsystem
// is active. Priority chain (first match wins):
//
//  1. UBUILDING_ENABLE_SESSION_MEMORY env truthy/falsy → that value.
//  2. cfg.SessionMemoryEnabled                         → that value.
//  3. Default                                          → false (opt-in).
func IsSessionMemoryEnabled(cfg agents.EngineConfig) bool {
	if v := os.Getenv(EnvEnableSessionMemory); v != "" {
		return isEnvTruthy(v)
	}
	return cfg.SessionMemoryEnabled
}

// HasMetInitializationThreshold checks if the current token count
// exceeds the minimum required to initialize session memory.
func HasMetInitializationThreshold(currentTokenCount int) bool {
	cfg := GetSessionMemoryConfig()
	return currentTokenCount >= cfg.MinimumMessageTokensToInit
}

// HasMetUpdateThreshold checks if enough tokens have accumulated
// since the last extraction to warrant a new update.
func HasMetUpdateThreshold(currentTokenCount, tokensAtLastExtraction int) bool {
	cfg := GetSessionMemoryConfig()
	return (currentTokenCount - tokensAtLastExtraction) >= cfg.MinimumTokensBetweenUpdate
}

// GetToolCallsBetweenUpdates returns the configured minimum tool calls
// between session memory updates.
func GetToolCallsBetweenUpdates() int {
	cfg := GetSessionMemoryConfig()
	return cfg.ToolCallsBetweenUpdates
}

// isEnvTruthy mirrors the package-level helper (not exported from
// memory package, so we replicate locally).
func isEnvTruthy(raw string) bool {
	switch {
	case raw == "1" || raw == "true" || raw == "yes" || raw == "on":
		return true
	case raw == "0" || raw == "false" || raw == "no" || raw == "off":
		return false
	}
	return false
}
