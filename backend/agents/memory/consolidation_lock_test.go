package memory

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// M14.T2 · Consolidation lock tests.
// ---------------------------------------------------------------------------

func TestReadLastConsolidatedAt_NoFile(t *testing.T) {
	dir := t.TempDir()
	ts, err := ReadLastConsolidatedAt(dir)
	assert.NoError(t, err)
	assert.True(t, ts.IsZero(), "should be zero when no lock file exists")
}

func TestReadLastConsolidatedAt_WithFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, consolidationLockFile)
	require.NoError(t, os.WriteFile(path, []byte("1234"), 0o644))

	ts, err := ReadLastConsolidatedAt(dir)
	assert.NoError(t, err)
	assert.False(t, ts.IsZero(), "should return file mtime")
	assert.WithinDuration(t, time.Now(), ts, 5*time.Second)
}

func TestTryAcquireConsolidationLock_FirstAcquire(t *testing.T) {
	dir := t.TempDir()
	subDir := filepath.Join(dir, "memory")
	// subDir does not exist yet — acquire should create it.

	priorMtime, err := TryAcquireConsolidationLock(subDir)
	require.NoError(t, err)
	require.NotNil(t, priorMtime, "should succeed on first acquire")
	assert.True(t, priorMtime.IsZero(), "prior mtime should be zero for first acquire")

	// Lock file should contain our PID.
	raw, readErr := os.ReadFile(lockPath(subDir))
	require.NoError(t, readErr)
	pid, parseErr := strconv.Atoi(string(raw))
	require.NoError(t, parseErr)
	assert.Equal(t, os.Getpid(), pid)
}

func TestTryAcquireConsolidationLock_ReclaimStale(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, consolidationLockFile)

	// Write a fake stale lock with a bogus PID and old mtime.
	require.NoError(t, os.WriteFile(path, []byte("999999999"), 0o644))
	past := time.Now().Add(-2 * time.Hour)
	require.NoError(t, os.Chtimes(path, past, past))

	priorMtime, err := TryAcquireConsolidationLock(dir)
	require.NoError(t, err)
	require.NotNil(t, priorMtime, "should reclaim stale lock")
	assert.WithinDuration(t, past, *priorMtime, 2*time.Second)
}

func TestTryAcquireConsolidationLock_BlockedByLiveProcess(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, consolidationLockFile)

	// Write our own PID as holder with fresh mtime — simulates
	// another goroutine of the same process holding the lock.
	require.NoError(t, os.WriteFile(path, []byte(strconv.Itoa(os.Getpid())), 0o644))

	// Try to acquire — our PID is live and lock is fresh.
	// The function should read the file, see it's our PID, write our PID again,
	// and verify — which will succeed (same PID). So this actually acquires.
	// To truly block, we'd need a different PID. Let's use PPID instead.
	ppid := os.Getppid()
	require.NoError(t, os.WriteFile(path, []byte(strconv.Itoa(ppid)), 0o644))

	priorMtime, err := TryAcquireConsolidationLock(dir)
	require.NoError(t, err)

	if ppid > 0 && isProcessRunning(ppid) {
		// Parent is alive → should be blocked.
		assert.Nil(t, priorMtime, "should be blocked by live parent process")
	} else {
		// Parent not alive → reclaim succeeds.
		assert.NotNil(t, priorMtime, "should reclaim when holder PID is dead")
	}
}

func TestRollbackConsolidationLock_RemoveOnZero(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, consolidationLockFile)
	require.NoError(t, os.WriteFile(path, []byte("1234"), 0o644))

	zero := time.Time{}
	err := RollbackConsolidationLock(dir, &zero)
	require.NoError(t, err)

	_, statErr := os.Stat(path)
	assert.True(t, os.IsNotExist(statErr), "lock file should be removed")
}

func TestRollbackConsolidationLock_RestoreMtime(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, consolidationLockFile)
	require.NoError(t, os.WriteFile(path, []byte(strconv.Itoa(os.Getpid())), 0o644))

	prior := time.Now().Add(-24 * time.Hour)
	err := RollbackConsolidationLock(dir, &prior)
	require.NoError(t, err)

	// Body should be cleared.
	raw, readErr := os.ReadFile(path)
	require.NoError(t, readErr)
	assert.Empty(t, string(raw), "PID body should be cleared")

	// Mtime should be restored.
	info, statErr := os.Stat(path)
	require.NoError(t, statErr)
	assert.WithinDuration(t, prior, info.ModTime(), 2*time.Second)
}

func TestRollbackConsolidationLock_NilPrior(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, consolidationLockFile)
	require.NoError(t, os.WriteFile(path, []byte("1234"), 0o644))

	err := RollbackConsolidationLock(dir, nil)
	require.NoError(t, err)

	_, statErr := os.Stat(path)
	assert.True(t, os.IsNotExist(statErr), "lock file should be removed on nil prior")
}

func TestRecordConsolidation(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "memory")

	err := RecordConsolidation(dir)
	require.NoError(t, err)

	// Should exist with our PID.
	raw, readErr := os.ReadFile(lockPath(dir))
	require.NoError(t, readErr)
	pid, parseErr := strconv.Atoi(string(raw))
	require.NoError(t, parseErr)
	assert.Equal(t, os.Getpid(), pid)
}

func TestListSessionsTouchedSince_Empty(t *testing.T) {
	sessions, err := ListSessionsTouchedSince(t.TempDir(), time.Now().Add(-1*time.Hour))
	assert.NoError(t, err)
	assert.Empty(t, sessions)
}

func TestListSessionsTouchedSince_NonExistent(t *testing.T) {
	sessions, err := ListSessionsTouchedSince(filepath.Join(t.TempDir(), "nope"), time.Now())
	assert.NoError(t, err)
	assert.Nil(t, sessions)
}

func TestListSessionsTouchedSince_Filters(t *testing.T) {
	dir := t.TempDir()

	// Create some session transcript files.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "session-a.jsonl"), []byte("{}"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "session-b.jsonl"), []byte("{}"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "notes.md"), []byte("not jsonl"), 0o644))

	// Set session-a to be old.
	old := time.Now().Add(-48 * time.Hour)
	require.NoError(t, os.Chtimes(filepath.Join(dir, "session-a.jsonl"), old, old))

	since := time.Now().Add(-1 * time.Hour)
	sessions, err := ListSessionsTouchedSince(dir, since)
	require.NoError(t, err)

	assert.Contains(t, sessions, "session-b")
	assert.NotContains(t, sessions, "session-a", "old session should be excluded")
	assert.NotContains(t, sessions, "notes", "non-jsonl should be excluded")
}

func TestIsProcessRunning_Self(t *testing.T) {
	assert.True(t, isProcessRunning(os.Getpid()), "own process should be running")
}

func TestIsProcessRunning_Zero(t *testing.T) {
	assert.False(t, isProcessRunning(0))
}

func TestIsProcessRunning_Negative(t *testing.T) {
	assert.False(t, isProcessRunning(-1))
}
