// Package fileio implements the FileRead / FileEdit / FileWrite tools, along
// with shared path and state helpers.
package fileio

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/wall-ai/ubuilding/backend/agents"
)

// EnsureAbsolute returns an error when p is not an absolute path.
func EnsureAbsolute(p string) error {
	if p == "" {
		return errors.New("path must not be empty")
	}
	if !filepath.IsAbs(p) {
		return fmt.Errorf("path must be absolute: %s", p)
	}
	return nil
}

// Resolve cleans the path and resolves symlinks (if the path exists).
// Non-existent paths are returned cleaned without error so creation paths
// (FileWrite) can still flow through.
func Resolve(p string) (string, error) {
	clean := filepath.Clean(p)
	if info, err := os.Lstat(clean); err == nil && info.Mode()&os.ModeSymlink != 0 {
		resolved, err := filepath.EvalSymlinks(clean)
		if err == nil {
			return resolved, nil
		}
	}
	return clean, nil
}

// EnsureInWorkspace rejects paths that escape the given roots. Each root must
// itself be absolute. An empty roots slice skips the containment check.
func EnsureInWorkspace(path string, roots []string) error {
	if len(roots) == 0 {
		return nil
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	abs = filepath.Clean(abs)
	for _, r := range roots {
		if r == "" {
			continue
		}
		root := filepath.Clean(r)
		if pathHasPrefix(abs, root) {
			return nil
		}
	}
	return fmt.Errorf("path %s is outside allowed workspace roots", path)
}

func pathHasPrefix(p, prefix string) bool {
	sep := string(os.PathSeparator)
	p = strings.TrimRight(p, sep)
	prefix = strings.TrimRight(prefix, sep)
	// Case-insensitive on Windows.
	if os.PathSeparator == '\\' {
		p = strings.ToLower(p)
		prefix = strings.ToLower(prefix)
	}
	if p == prefix {
		return true
	}
	return strings.HasPrefix(p, prefix+sep)
}

// SHA256File returns the hex-encoded SHA-256 of the file at path.
func SHA256File(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// RecordReadState caches the current file state in toolCtx.ReadFileState.
// Returns the recorded FileState so callers can inspect it.
func RecordReadState(toolCtx *agents.ToolUseContext, path string) (*agents.FileState, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	hash, err := SHA256File(path)
	if err != nil {
		return nil, err
	}
	st := &agents.FileState{
		Path:         path,
		LastModified: info.ModTime().UnixNano(),
		Size:         info.Size(),
		ContentHash:  hash,
	}
	if toolCtx != nil && toolCtx.ReadFileState != nil {
		toolCtx.ReadFileState.Set(path, st)
	}
	return st, nil
}

// HasFreshRead reports whether toolCtx.ReadFileState has a cached entry for
// path whose mtime+size+hash still match the file on disk. This is the gate
// FileEdit/FileWrite (overwrite) use to refuse edits on files the model has
// not read recently.
func HasFreshRead(toolCtx *agents.ToolUseContext, path string) (bool, error) {
	if toolCtx == nil || toolCtx.ReadFileState == nil {
		return false, nil
	}
	cached, ok := toolCtx.ReadFileState.Get(path)
	if !ok {
		return false, nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return false, err
	}
	if info.Size() != cached.Size || info.ModTime().UnixNano() != cached.LastModified {
		return false, nil
	}
	return true, nil
}
