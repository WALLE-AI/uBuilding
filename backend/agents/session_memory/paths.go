package session_memory

import (
	"os"
	"path/filepath"
)

// ---------------------------------------------------------------------------
// M10.I3 · SessionMemory paths — session-scoped path resolution.
//
// Ports `src/utils/permissions/filesystem.ts` session memory paths.
// ---------------------------------------------------------------------------

// SessionMemoryDirName is the directory name under the session storage
// root where session memory files live.
const SessionMemoryDirName = "session-memory"

// SessionMemoryFileName is the notes file managed by session memory.
const SessionMemoryFileName = "notes.md"

// GetSessionMemoryDir returns the directory for session memory files,
// given the session storage root (typically <configHome>/projects/<key>/<sessionID>).
func GetSessionMemoryDir(sessionStorageRoot string) string {
	return filepath.Join(sessionStorageRoot, SessionMemoryDirName)
}

// GetSessionMemoryPath returns the full path to the session notes file.
func GetSessionMemoryPath(sessionStorageRoot string) string {
	return filepath.Join(GetSessionMemoryDir(sessionStorageRoot), SessionMemoryFileName)
}

// GetSessionMemoryContent reads the current session memory content.
// Returns "" if the file does not exist or cannot be read.
func GetSessionMemoryContent(sessionStorageRoot string) string {
	path := GetSessionMemoryPath(sessionStorageRoot)
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(data)
}
