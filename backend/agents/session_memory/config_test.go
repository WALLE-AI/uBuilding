package session_memory

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/wall-ai/ubuilding/backend/agents"
)

// ---------------------------------------------------------------------------
// M10.T1 · SessionMemory config tests.
// ---------------------------------------------------------------------------

func TestGetSessionMemoryConfig_Defaults(t *testing.T) {
	ResetSessionMemoryConfig()
	cfg := GetSessionMemoryConfig()
	assert.Equal(t, 10_000, cfg.MinimumMessageTokensToInit)
	assert.Equal(t, 5_000, cfg.MinimumTokensBetweenUpdate)
	assert.Equal(t, 3, cfg.ToolCallsBetweenUpdates)
}

func TestSetSessionMemoryConfig_PartialOverride(t *testing.T) {
	ResetSessionMemoryConfig()
	defer ResetSessionMemoryConfig()

	SetSessionMemoryConfig(SessionMemoryConfig{
		MinimumMessageTokensToInit: 20_000,
	})
	cfg := GetSessionMemoryConfig()
	assert.Equal(t, 20_000, cfg.MinimumMessageTokensToInit)
	assert.Equal(t, 5_000, cfg.MinimumTokensBetweenUpdate, "untouched field keeps default")
	assert.Equal(t, 3, cfg.ToolCallsBetweenUpdates, "untouched field keeps default")
}

func TestSetSessionMemoryConfig_ZeroIgnored(t *testing.T) {
	ResetSessionMemoryConfig()
	defer ResetSessionMemoryConfig()

	SetSessionMemoryConfig(SessionMemoryConfig{
		MinimumMessageTokensToInit: 0, // should not override
		ToolCallsBetweenUpdates:    5,
	})
	cfg := GetSessionMemoryConfig()
	assert.Equal(t, 10_000, cfg.MinimumMessageTokensToInit, "zero should not override")
	assert.Equal(t, 5, cfg.ToolCallsBetweenUpdates)
}

func TestIsSessionMemoryEnabled_EnvOverride(t *testing.T) {
	t.Setenv(EnvEnableSessionMemory, "1")
	cfg := agents.EngineConfig{SessionMemoryEnabled: false}
	assert.True(t, IsSessionMemoryEnabled(cfg), "env=1 overrides config=false")
}

func TestIsSessionMemoryEnabled_EnvOverrideFalse(t *testing.T) {
	t.Setenv(EnvEnableSessionMemory, "0")
	cfg := agents.EngineConfig{SessionMemoryEnabled: true}
	assert.False(t, IsSessionMemoryEnabled(cfg), "env=0 overrides config=true")
}

func TestIsSessionMemoryEnabled_ConfigFallback(t *testing.T) {
	// No env set.
	cfgOn := agents.EngineConfig{SessionMemoryEnabled: true}
	assert.True(t, IsSessionMemoryEnabled(cfgOn))

	cfgOff := agents.EngineConfig{SessionMemoryEnabled: false}
	assert.False(t, IsSessionMemoryEnabled(cfgOff))
}

func TestIsSessionMemoryEnabled_DefaultOff(t *testing.T) {
	cfg := agents.EngineConfig{}
	assert.False(t, IsSessionMemoryEnabled(cfg), "default should be off")
}

func TestHasMetInitializationThreshold(t *testing.T) {
	ResetSessionMemoryConfig()
	assert.False(t, HasMetInitializationThreshold(9_999))
	assert.True(t, HasMetInitializationThreshold(10_000))
	assert.True(t, HasMetInitializationThreshold(15_000))
}

func TestHasMetUpdateThreshold(t *testing.T) {
	ResetSessionMemoryConfig()
	assert.False(t, HasMetUpdateThreshold(14_999, 10_000))
	assert.True(t, HasMetUpdateThreshold(15_000, 10_000))
	assert.True(t, HasMetUpdateThreshold(20_000, 10_000))
}

func TestGetToolCallsBetweenUpdates(t *testing.T) {
	ResetSessionMemoryConfig()
	assert.Equal(t, 3, GetToolCallsBetweenUpdates())
}
