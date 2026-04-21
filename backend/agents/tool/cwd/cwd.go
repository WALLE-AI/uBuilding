// Package cwd provides a global workspace directory state shared by all tools.
// It mirrors the architecture of claude-code-main's bootstrap/state.ts (STATE.cwd)
// combined with utils/cwd.ts (pwd()/getCwd()). Tools call Get() at execution
// time to obtain the current workspace — workspace changes via the API take
// effect immediately without requiring tool reconstruction.
package cwd

import "sync"

var (
	mu  sync.RWMutex
	dir string
)

// Get returns the current workspace directory.
// An empty string means "not set — callers should fall back to os.Getwd()".
func Get() string {
	mu.RLock()
	defer mu.RUnlock()
	return dir
}

// Set updates the workspace directory atomically.
func Set(path string) {
	mu.Lock()
	dir = path
	mu.Unlock()
}
