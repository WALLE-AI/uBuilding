package memory

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/wall-ai/ubuilding/backend/agents"
)

// ---------------------------------------------------------------------------
// M14.T1 · AutoDream config tests.
// ---------------------------------------------------------------------------

func TestGetAutoDreamConfig_Defaults(t *testing.T) {
	cfg := GetAutoDreamConfig()
	assert.Equal(t, 24, cfg.MinHours, "default MinHours")
	assert.Equal(t, 3, cfg.MinSessions, "default MinSessions")
}

func TestGetAutoDreamConfig_EnvOverride(t *testing.T) {
	t.Setenv(EnvAutoDreamMinHours, "48")
	t.Setenv(EnvAutoDreamMinSessions, "5")

	cfg := GetAutoDreamConfig()
	assert.Equal(t, 48, cfg.MinHours)
	assert.Equal(t, 5, cfg.MinSessions)
}

func TestGetAutoDreamConfig_EnvInvalidIgnored(t *testing.T) {
	t.Setenv(EnvAutoDreamMinHours, "not_a_number")
	t.Setenv(EnvAutoDreamMinSessions, "-1")

	cfg := GetAutoDreamConfig()
	assert.Equal(t, DefaultAutoDreamConfig.MinHours, cfg.MinHours)
	assert.Equal(t, DefaultAutoDreamConfig.MinSessions, cfg.MinSessions)
}

func TestIsAutoDreamEnabled_GatedOnAutoMemory(t *testing.T) {
	ResetMemoryBaseDirCache()
	t.Setenv(EnvRemoteMemoryDir, filepath.Join(t.TempDir(), "base"))
	// Auto memory off → auto dream must also be off.
	t.Setenv(EnvEnableAutoMemory, "0")
	t.Setenv(EnvAutoDreamEnabled, "1")

	cfg := agents.EngineConfig{AutoDreamEnabled: true}
	assert.False(t, IsAutoDreamEnabled(cfg, NopSettingsProvider),
		"auto dream must be disabled when auto memory is off")
}

func TestIsAutoDreamEnabled_EnvOverrideTrue(t *testing.T) {
	ResetMemoryBaseDirCache()
	t.Setenv(EnvRemoteMemoryDir, filepath.Join(t.TempDir(), "base"))
	t.Setenv(EnvEnableAutoMemory, "1")
	t.Setenv(EnvAutoDreamEnabled, "true")

	cfg := agents.EngineConfig{AutoDreamEnabled: false}
	assert.True(t, IsAutoDreamEnabled(cfg, NopSettingsProvider),
		"env override=true should beat config=false")
}

func TestIsAutoDreamEnabled_EnvOverrideFalse(t *testing.T) {
	ResetMemoryBaseDirCache()
	t.Setenv(EnvRemoteMemoryDir, filepath.Join(t.TempDir(), "base"))
	t.Setenv(EnvEnableAutoMemory, "1")
	t.Setenv(EnvAutoDreamEnabled, "false")

	cfg := agents.EngineConfig{AutoDreamEnabled: true}
	assert.False(t, IsAutoDreamEnabled(cfg, NopSettingsProvider),
		"env override=false should beat config=true")
}

func TestIsAutoDreamEnabled_ConfigFallback(t *testing.T) {
	ResetMemoryBaseDirCache()
	t.Setenv(EnvRemoteMemoryDir, filepath.Join(t.TempDir(), "base"))
	t.Setenv(EnvEnableAutoMemory, "1")
	// No env override for auto dream.

	cfgOn := agents.EngineConfig{AutoDreamEnabled: true}
	assert.True(t, IsAutoDreamEnabled(cfgOn, NopSettingsProvider))

	cfgOff := agents.EngineConfig{AutoDreamEnabled: false}
	assert.False(t, IsAutoDreamEnabled(cfgOff, NopSettingsProvider))
}

func TestIsAutoDreamEnabled_DefaultOff(t *testing.T) {
	ResetMemoryBaseDirCache()
	t.Setenv(EnvRemoteMemoryDir, filepath.Join(t.TempDir(), "base"))
	t.Setenv(EnvEnableAutoMemory, "1")

	cfg := agents.EngineConfig{}
	assert.False(t, IsAutoDreamEnabled(cfg, NopSettingsProvider),
		"default should be off (opt-in)")
}
