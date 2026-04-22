package memory

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wall-ai/ubuilding/backend/agents"
)

// ---------------------------------------------------------------------------
// D4.T2 · Team sync tests.
// ---------------------------------------------------------------------------

func setupTeamSyncEnv(t *testing.T) (cwd string, cfg agents.EngineConfig) {
	t.Helper()
	ResetMemoryBaseDirCache()

	base := filepath.Join(t.TempDir(), "base")
	cwd = t.TempDir()

	t.Setenv(EnvRemoteMemoryDir, base)
	t.Setenv(EnvEnableAutoMemory, "1")
	t.Setenv(EnvEnableTeamMemory, "1")

	cfg = agents.EngineConfig{
		AutoMemoryEnabled: true,
		TeamMemoryEnabled: true,
	}
	return cwd, cfg
}

func TestBuildPushPayload_EmptyDir(t *testing.T) {
	cwd, cfg := setupTeamSyncEnv(t)

	syncer := NewTeamMemorySyncer(cwd, cfg, NopSettingsProvider, nil)
	data, skipped, err := syncer.BuildPushPayload()
	require.NoError(t, err)
	assert.Empty(t, data.Files)
	assert.Empty(t, skipped)
}

func TestBuildPushPayload_WithFiles(t *testing.T) {
	cwd, cfg := setupTeamSyncEnv(t)

	teamDir := GetTeamMemPath(cwd, NopSettingsProvider)
	require.NotEmpty(t, teamDir)
	require.NoError(t, os.MkdirAll(teamDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(teamDir, "prefs.md"), []byte("# Prefs"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(teamDir, "readme.txt"), []byte("not md"), 0o644))

	syncer := NewTeamMemorySyncer(cwd, cfg, NopSettingsProvider, nil)
	data, skipped, err := syncer.BuildPushPayload()
	require.NoError(t, err)
	assert.Equal(t, 1, len(data.Files), "only .md files")
	assert.Equal(t, "prefs.md", data.Files[0].RelativePath)
	assert.NotEmpty(t, data.Files[0].Hash)
	assert.Empty(t, skipped)
}

func TestBuildPushPayload_SkipsSecrets(t *testing.T) {
	cwd, cfg := setupTeamSyncEnv(t)

	teamDir := GetTeamMemPath(cwd, NopSettingsProvider)
	require.NotEmpty(t, teamDir)
	require.NoError(t, os.MkdirAll(teamDir, 0o755))

	// Write a file with something that looks like an AWS key.
	secretContent := "AKIAIOSFODNN7EXAMPLE"
	require.NoError(t, os.WriteFile(filepath.Join(teamDir, "secrets.md"), []byte(secretContent), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(teamDir, "safe.md"), []byte("# Safe"), 0o644))

	syncer := NewTeamMemorySyncer(cwd, cfg, NopSettingsProvider, nil)
	data, skipped, err := syncer.BuildPushPayload()
	require.NoError(t, err)

	// Safe file should be in payload.
	assert.Equal(t, 1, len(data.Files))
	assert.Equal(t, "safe.md", data.Files[0].RelativePath)

	// Secret file should be skipped.
	assert.Equal(t, 1, len(skipped))
	assert.Equal(t, "secrets.md", skipped[0].RelativePath)
}

func TestApplyFetchResult_WritesFiles(t *testing.T) {
	cwd, cfg := setupTeamSyncEnv(t)

	syncer := NewTeamMemorySyncer(cwd, cfg, NopSettingsProvider, nil)
	data := &TeamMemoryData{
		Files: []TeamMemoryContentStorage{
			{RelativePath: "fetched.md", Content: "# Fetched content"},
		},
	}
	written, err := syncer.ApplyFetchResult(data)
	require.NoError(t, err)
	assert.Equal(t, 1, written)

	teamDir := GetTeamMemPath(cwd, NopSettingsProvider)
	content, readErr := os.ReadFile(filepath.Join(teamDir, "fetched.md"))
	require.NoError(t, readErr)
	assert.Equal(t, "# Fetched content", string(content))
}

func TestApplyFetchResult_RejectsUnsafePaths(t *testing.T) {
	cwd, cfg := setupTeamSyncEnv(t)

	syncer := NewTeamMemorySyncer(cwd, cfg, NopSettingsProvider, nil)
	data := &TeamMemoryData{
		Files: []TeamMemoryContentStorage{
			{RelativePath: "../escape.md", Content: "bad"},
			{RelativePath: "safe.md", Content: "good"},
		},
	}
	written, err := syncer.ApplyFetchResult(data)
	require.NoError(t, err)
	assert.Equal(t, 1, written, "only safe path should be written")
}

func TestApplyFetchResult_NilData(t *testing.T) {
	cwd, cfg := setupTeamSyncEnv(t)
	syncer := NewTeamMemorySyncer(cwd, cfg, NopSettingsProvider, nil)

	written, err := syncer.ApplyFetchResult(nil)
	assert.NoError(t, err)
	assert.Equal(t, 0, written)
}

func TestRecordPush(t *testing.T) {
	syncer := NewTeamMemorySyncer("", agents.EngineConfig{}, NopSettingsProvider, nil)
	syncer.RecordPush(0)
	assert.False(t, syncer.Status().LastPushAt.IsZero())
	assert.Empty(t, syncer.Status().LastError)

	syncer.RecordPush(2)
	assert.Contains(t, syncer.Status().LastError, "skipped")
}

func TestRecordError(t *testing.T) {
	syncer := NewTeamMemorySyncer("", agents.EngineConfig{}, NopSettingsProvider, nil)
	syncer.RecordError(assert.AnError)
	assert.NotEmpty(t, syncer.Status().LastError)
}
