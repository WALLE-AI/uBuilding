package memory

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wall-ai/ubuilding/backend/agents"
)

// ---------------------------------------------------------------------------
// M14.T4 · AutoDream service tests.
// ---------------------------------------------------------------------------

// setupAutoDreamEnv prepares a temp environment for auto-dream tests.
func setupAutoDreamEnv(t *testing.T) (cwd string, cfg agents.EngineConfig) {
	t.Helper()
	ResetMemoryBaseDirCache()

	base := filepath.Join(t.TempDir(), "base")
	cwd = t.TempDir()

	t.Setenv(EnvRemoteMemoryDir, base)
	t.Setenv(EnvEnableAutoMemory, "1")
	t.Setenv(EnvAutoDreamEnabled, "1")

	cfg = agents.EngineConfig{
		AutoMemoryEnabled: true,
		AutoDreamEnabled:  true,
	}
	return cwd, cfg
}

func TestAutoDream_DisabledGate(t *testing.T) {
	cwd, cfg := setupAutoDreamEnv(t)
	t.Setenv(EnvAutoDreamEnabled, "0")

	svc := NewAutoDreamService(cwd, "session-1", cfg, NopSettingsProvider, nil)
	result, err := svc.ExecuteAutoDream(context.Background(), nil)

	require.NoError(t, err)
	assert.True(t, result.Skipped)
	assert.Equal(t, "auto_dream_disabled", result.SkipReason)
}

func TestAutoDream_TimeGate(t *testing.T) {
	cwd, cfg := setupAutoDreamEnv(t)

	// Create auto-mem dir and a recent lock (consolidation happened just now).
	autoMemPath := GetAutoMemPath(cwd, NopSettingsProvider)
	require.NotEmpty(t, autoMemPath)
	require.NoError(t, os.MkdirAll(autoMemPath, 0o755))
	require.NoError(t, RecordConsolidation(autoMemPath))

	svc := NewAutoDreamService(cwd, "session-1", cfg, NopSettingsProvider, nil)
	result, err := svc.ExecuteAutoDream(context.Background(), nil)

	require.NoError(t, err)
	assert.True(t, result.Skipped)
	assert.Contains(t, result.SkipReason, "time_gate")
}

func TestAutoDream_SessionGate(t *testing.T) {
	cwd, cfg := setupAutoDreamEnv(t)

	// Create auto-mem dir with an old lock file.
	autoMemPath := GetAutoMemPath(cwd, NopSettingsProvider)
	require.NotEmpty(t, autoMemPath)
	require.NoError(t, os.MkdirAll(autoMemPath, 0o755))

	lockFile := filepath.Join(autoMemPath, consolidationLockFile)
	past := time.Now().Add(-48 * time.Hour)
	require.NoError(t, os.WriteFile(lockFile, []byte("0"), 0o644))
	require.NoError(t, os.Chtimes(lockFile, past, past))

	// No transcript files → session gate should block.
	svc := NewAutoDreamService(cwd, "session-1", cfg, NopSettingsProvider, nil)
	result, err := svc.ExecuteAutoDream(context.Background(), nil)

	require.NoError(t, err)
	assert.True(t, result.Skipped)
	assert.Contains(t, result.SkipReason, "session_gate")
}

func TestAutoDream_ScanThrottle(t *testing.T) {
	cwd, cfg := setupAutoDreamEnv(t)

	autoMemPath := GetAutoMemPath(cwd, NopSettingsProvider)
	require.NotEmpty(t, autoMemPath)
	require.NoError(t, os.MkdirAll(autoMemPath, 0o755))

	// Old lock so time gate passes.
	lockFile := filepath.Join(autoMemPath, consolidationLockFile)
	past := time.Now().Add(-48 * time.Hour)
	require.NoError(t, os.WriteFile(lockFile, []byte("0"), 0o644))
	require.NoError(t, os.Chtimes(lockFile, past, past))

	svc := NewAutoDreamService(cwd, "session-1", cfg, NopSettingsProvider, nil)
	// First call triggers session scan and skips due to no sessions.
	_, _ = svc.ExecuteAutoDream(context.Background(), nil)
	// Second call should hit scan throttle.
	result, err := svc.ExecuteAutoDream(context.Background(), nil)

	require.NoError(t, err)
	assert.True(t, result.Skipped)
	assert.Equal(t, "scan_throttle", result.SkipReason)
}

func TestAutoDream_FullRun(t *testing.T) {
	cwd, cfg := setupAutoDreamEnv(t)

	autoMemPath := GetAutoMemPath(cwd, NopSettingsProvider)
	require.NotEmpty(t, autoMemPath)
	require.NoError(t, os.MkdirAll(autoMemPath, 0o755))

	// Old lock so time gate passes.
	lockFile := filepath.Join(autoMemPath, consolidationLockFile)
	past := time.Now().Add(-48 * time.Hour)
	require.NoError(t, os.WriteFile(lockFile, []byte("0"), 0o644))
	require.NoError(t, os.Chtimes(lockFile, past, past))

	// Create transcript dir with enough sessions.
	transcriptDir := resolveTranscriptDir(cwd)
	require.NotEmpty(t, transcriptDir)
	require.NoError(t, os.MkdirAll(transcriptDir, 0o755))
	for _, name := range []string{"session-a.jsonl", "session-b.jsonl", "session-c.jsonl"} {
		require.NoError(t, os.WriteFile(filepath.Join(transcriptDir, name), []byte("{}"), 0o644))
	}

	// Inject a mock SideQueryFn.
	old := DefaultSideQueryFn
	var capturedPrompt string
	DefaultSideQueryFn = func(_ context.Context, system, user string) (string, error) {
		capturedPrompt = system
		return "Consolidation complete. Updated: preferences.md\nCreated: project_overview.md", nil
	}
	defer func() { DefaultSideQueryFn = old }()

	svc := NewAutoDreamService(cwd, "other-session", cfg, NopSettingsProvider, nil)
	result, err := svc.ExecuteAutoDream(context.Background(), nil)

	require.NoError(t, err)
	assert.False(t, result.Skipped)
	assert.GreaterOrEqual(t, result.SessionCount, 3)
	assert.Contains(t, capturedPrompt, "Phase 1")
	assert.Contains(t, result.FilesTouched, "preferences.md")
	assert.Contains(t, result.FilesTouched, "project_overview.md")
}

func TestAutoDream_NoSideQueryFn(t *testing.T) {
	cwd, cfg := setupAutoDreamEnv(t)

	autoMemPath := GetAutoMemPath(cwd, NopSettingsProvider)
	require.NotEmpty(t, autoMemPath)
	require.NoError(t, os.MkdirAll(autoMemPath, 0o755))

	// Old lock so time gate passes.
	lockFile := filepath.Join(autoMemPath, consolidationLockFile)
	past := time.Now().Add(-48 * time.Hour)
	require.NoError(t, os.WriteFile(lockFile, []byte("0"), 0o644))
	require.NoError(t, os.Chtimes(lockFile, past, past))

	// Create enough sessions.
	transcriptDir := resolveTranscriptDir(cwd)
	require.NotEmpty(t, transcriptDir)
	require.NoError(t, os.MkdirAll(transcriptDir, 0o755))
	for _, name := range []string{"s1.jsonl", "s2.jsonl", "s3.jsonl"} {
		require.NoError(t, os.WriteFile(filepath.Join(transcriptDir, name), []byte("{}"), 0o644))
	}

	// nil SideQueryFn should cause error and rollback.
	old := DefaultSideQueryFn
	DefaultSideQueryFn = nil
	defer func() { DefaultSideQueryFn = old }()

	svc := NewAutoDreamService(cwd, "other-session", cfg, NopSettingsProvider, nil)
	result, err := svc.ExecuteAutoDream(context.Background(), nil)

	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "no SideQueryFn")
}

func TestExtractTouchedFiles(t *testing.T) {
	response := `Summary of changes:
Created: new_patterns.md
Updated: project_overview.md
Nothing else changed.
Created: new_patterns.md`

	files := extractTouchedFiles(response)
	assert.Contains(t, files, "new_patterns.md")
	assert.Contains(t, files, "project_overview.md")
	// Deduplication.
	count := 0
	for _, f := range files {
		if f == "new_patterns.md" {
			count++
		}
	}
	assert.Equal(t, 1, count, "should deduplicate")
}

func TestBuildDreamExtra(t *testing.T) {
	extra := buildDreamExtra([]string{"session-a", "session-b"})
	assert.Contains(t, extra, "Tool constraints")
	assert.Contains(t, extra, "Sessions since last consolidation (2)")
	assert.Contains(t, extra, "- session-a")
	assert.Contains(t, extra, "- session-b")
}

func TestResolveTranscriptDir(t *testing.T) {
	ResetMemoryBaseDirCache()
	base := filepath.Join(t.TempDir(), "base")
	t.Setenv(EnvRemoteMemoryDir, base)

	cwd := t.TempDir()
	dir := resolveTranscriptDir(cwd)
	assert.NotEmpty(t, dir)
	assert.Contains(t, dir, "projects")
}
