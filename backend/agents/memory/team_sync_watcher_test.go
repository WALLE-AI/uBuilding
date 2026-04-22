package memory

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// D4.T3 · Team sync watcher tests.
// ---------------------------------------------------------------------------

func TestTeamSyncWatcher_ScanOnce_InitialSeed(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.md"), []byte("hi"), 0o644))

	w := NewTeamSyncWatcher(dir, nil, nil)

	// First scan seeds baseline — all files are "new".
	changed := w.ScanOnce()
	assert.Equal(t, []string{"a.md"}, changed)

	// Second scan with no changes — nothing reported.
	changed = w.ScanOnce()
	assert.Empty(t, changed)
}

func TestTeamSyncWatcher_ScanOnce_DetectsModification(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "b.md")
	require.NoError(t, os.WriteFile(path, []byte("v1"), 0o644))

	w := NewTeamSyncWatcher(dir, nil, nil)
	w.ScanOnce() // seed

	// Modify the file (ensure different mtime).
	time.Sleep(10 * time.Millisecond)
	require.NoError(t, os.WriteFile(path, []byte("v2"), 0o644))

	changed := w.ScanOnce()
	assert.Contains(t, changed, "b.md")
}

func TestTeamSyncWatcher_ScanOnce_DetectsDeletion(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "c.md")
	require.NoError(t, os.WriteFile(path, []byte("x"), 0o644))

	w := NewTeamSyncWatcher(dir, nil, nil)
	w.ScanOnce() // seed

	require.NoError(t, os.Remove(path))

	changed := w.ScanOnce()
	assert.Contains(t, changed, "c.md")
}

func TestTeamSyncWatcher_ScanOnce_IgnoresNonMd(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "readme.txt"), []byte("hi"), 0o644))

	w := NewTeamSyncWatcher(dir, nil, nil)
	changed := w.ScanOnce()
	assert.Empty(t, changed)
}

func TestTeamSyncWatcher_OnChangeCallback(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "d.md"), []byte("x"), 0o644))

	var mu sync.Mutex
	var captured []string
	onChange := func(changed []string) {
		mu.Lock()
		captured = append(captured, changed...)
		mu.Unlock()
	}

	w := NewTeamSyncWatcher(dir, onChange, nil)
	w.SetInterval(10 * time.Millisecond)
	w.Start()

	// Wait for at least one scan cycle.
	time.Sleep(100 * time.Millisecond)
	w.Stop()

	mu.Lock()
	defer mu.Unlock()
	assert.Contains(t, captured, "d.md")
}

func TestTeamSyncWatcher_GetTrackedFiles(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "e.md"), []byte("x"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "f.md"), []byte("y"), 0o644))

	w := NewTeamSyncWatcher(dir, nil, nil)
	w.ScanOnce()

	tracked := w.GetTrackedFiles()
	assert.Equal(t, 2, len(tracked))
}

func TestTeamSyncWatcher_MissingDir(t *testing.T) {
	w := NewTeamSyncWatcher(filepath.Join(t.TempDir(), "nope"), nil, nil)
	changed := w.ScanOnce()
	assert.Empty(t, changed)
}
