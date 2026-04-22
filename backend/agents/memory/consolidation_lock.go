package memory

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// ---------------------------------------------------------------------------
// M14.I2 · Consolidation lock — file-based mutual exclusion for
// auto-dream consolidation runs.
//
// Ports `src/services/autoDream/consolidationLock.ts`.
//
// The lock file's mtime doubles as the `lastConsolidatedAt` timestamp.
// Its body holds the PID of the holder so stale-lock detection can
// verify whether the process is still alive.
// ---------------------------------------------------------------------------

// consolidationLockFile is the name of the lock file inside the
// auto-memory directory.
const consolidationLockFile = ".consolidate-lock"

// HolderStaleMs is the maximum age (in milliseconds) of a lock before
// it is considered stale even if the holder PID is alive. Guards
// against PID reuse on long-lived machines.
const HolderStaleMs = 60 * 60 * 1000 // 1 hour

// lockPath returns the absolute path to the consolidation lock file.
func lockPath(autoMemPath string) string {
	return filepath.Join(strings.TrimRight(autoMemPath, string(os.PathSeparator)), consolidationLockFile)
}

// ReadLastConsolidatedAt returns the mtime of the lock file, which
// represents the timestamp of the last successful consolidation.
// Returns zero time if the lock file does not exist.
func ReadLastConsolidatedAt(autoMemPath string) (time.Time, error) {
	info, err := os.Stat(lockPath(autoMemPath))
	if err != nil {
		if os.IsNotExist(err) {
			return time.Time{}, nil
		}
		return time.Time{}, err
	}
	return info.ModTime(), nil
}

// TryAcquireConsolidationLock attempts to acquire the consolidation
// lock by writing the current PID to the lock file.
//
// Returns the prior mtime (for rollback) on success, or nil if
// acquisition failed (another live process holds the lock).
//
// Acquisition semantics:
//  1. If no lock file exists → acquire (prior mtime = zero).
//  2. If lock exists and mtime < HolderStaleMs ago AND holder PID is
//     alive → blocked, return nil.
//  3. Otherwise (stale or dead PID) → reclaim the lock.
//  4. After writing PID, re-read to verify ownership (race guard).
func TryAcquireConsolidationLock(autoMemPath string) (*time.Time, error) {
	path := lockPath(autoMemPath)

	// Read existing lock state.
	var priorMtime time.Time
	var holderPID int
	hasPrior := false

	if info, err := os.Stat(path); err == nil {
		priorMtime = info.ModTime()
		hasPrior = true

		if raw, readErr := os.ReadFile(path); readErr == nil {
			if parsed, parseErr := strconv.Atoi(strings.TrimSpace(string(raw))); parseErr == nil {
				holderPID = parsed
			}
		}
	}

	// Check if current holder is still live and the lock is fresh.
	if hasPrior {
		ageMs := time.Since(priorMtime).Milliseconds()
		if ageMs < int64(HolderStaleMs) && holderPID > 0 && isProcessRunning(holderPID) {
			return nil, nil // blocked
		}
	}

	// Ensure directory exists.
	if err := os.MkdirAll(strings.TrimRight(autoMemPath, string(os.PathSeparator)), 0o755); err != nil {
		return nil, fmt.Errorf("consolidation lock: mkdir: %w", err)
	}

	// Write our PID.
	myPID := os.Getpid()
	if err := os.WriteFile(path, []byte(strconv.Itoa(myPID)), 0o644); err != nil {
		return nil, fmt.Errorf("consolidation lock: write: %w", err)
	}

	// Re-read to verify we won the race.
	verify, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("consolidation lock: verify read: %w", err)
	}
	if parsed, parseErr := strconv.Atoi(strings.TrimSpace(string(verify))); parseErr != nil || parsed != myPID {
		return nil, nil // lost the race
	}

	if hasPrior {
		return &priorMtime, nil
	}
	zero := time.Time{}
	return &zero, nil
}

// RollbackConsolidationLock restores the lock file to its pre-acquire
// state after a failed consolidation run.
//
// If priorMtime is zero → delete the lock file (restore no-file state).
// Otherwise → clear the PID body and restore the prior mtime.
func RollbackConsolidationLock(autoMemPath string, priorMtime *time.Time) error {
	path := lockPath(autoMemPath)

	if priorMtime == nil || priorMtime.IsZero() {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("consolidation lock: rollback remove: %w", err)
		}
		return nil
	}

	// Clear PID body so our still-running process doesn't appear as holder.
	if err := os.WriteFile(path, []byte(""), 0o644); err != nil {
		return fmt.Errorf("consolidation lock: rollback write: %w", err)
	}

	// Restore prior mtime. os.Chtimes takes atime + mtime.
	if err := os.Chtimes(path, *priorMtime, *priorMtime); err != nil {
		return fmt.Errorf("consolidation lock: rollback chtimes: %w", err)
	}

	return nil
}

// RecordConsolidation stamps the lock file with the current time,
// recording a successful consolidation. Used by manual /dream commands.
func RecordConsolidation(autoMemPath string) error {
	path := lockPath(autoMemPath)
	dir := strings.TrimRight(autoMemPath, string(os.PathSeparator))

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("consolidation lock: record mkdir: %w", err)
	}

	return os.WriteFile(path, []byte(strconv.Itoa(os.Getpid())), 0o644)
}

// ListSessionsTouchedSince scans projectDir for *.jsonl transcript
// files whose mtime is after sinceTime and returns their base names
// (without extension) as session identifiers.
func ListSessionsTouchedSince(projectDir string, sinceTime time.Time) ([]string, error) {
	entries, err := os.ReadDir(projectDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("consolidation lock: list sessions: %w", err)
	}

	var sessions []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".jsonl") {
			continue
		}
		info, infoErr := e.Info()
		if infoErr != nil {
			continue
		}
		if info.ModTime().After(sinceTime) {
			sessions = append(sessions, strings.TrimSuffix(name, ".jsonl"))
		}
	}
	return sessions, nil
}

// isProcessRunning checks whether a process with the given PID is alive.
//
//   - Unix:    Signal(0) returns nil for alive, ESRCH for dead.
//   - Windows: OpenProcess with PROCESS_QUERY_LIMITED_INFORMATION;
//     if it succeeds the PID is live. We close the handle immediately.
func isProcessRunning(pid int) bool {
	if pid <= 0 {
		return false
	}

	if runtime.GOOS == "windows" {
		return isProcessRunningWindows(pid)
	}

	// Unix path.
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return p.Signal(syscall.Signal(0)) == nil
}

// isProcessRunningWindows uses the Windows API to check process liveness.
func isProcessRunningWindows(pid int) bool {
	const PROCESS_QUERY_LIMITED_INFORMATION = 0x1000
	h, err := syscall.OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	syscall.CloseHandle(h)
	return true
}
