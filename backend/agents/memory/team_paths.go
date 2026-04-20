package memory

import (
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/wall-ai/ubuilding/backend/agents"
	"golang.org/x/text/unicode/norm"
)

// ---------------------------------------------------------------------------
// M6 · Team memory paths & validation.
//
// Ports `src/memdir/teamMemPaths.ts` — the sibling of paths.go that
// extends the per-project auto-memory directory with a `team/`
// subtree shared via git. Because write-paths originate from the
// model's tool calls we defend against:
//
//   - Null-byte truncation of C syscalls
//   - URL-encoded traversal (%2e%2e%2f = ../)
//   - Unicode-fullwidth traversal (U+FF0E U+FF0F → ../)
//   - Backslash traversal (Windows native separator)
//   - Absolute-path injection (leading /)
//   - Symlink escape (PSR M22186 — a pre-existing symlink inside
//     teamDir pointing out of the tree)
//
// Exported API:
//
//   - PathTraversalError
//   - IsTeamMemoryEnabled(EngineConfig, SettingsProvider) bool
//   - GetTeamMemPath(cwd, settings) string
//   - GetTeamMemEntrypoint(cwd, settings) string
//   - IsTeamMemPath(filePath, cwd, settings) bool
//   - IsTeamMemFile(filePath, cwd, settings, cfg) bool
//   - ValidateTeamMemWritePath(filePath, cwd, settings) (string, error)
//   - ValidateTeamMemKey(relativeKey, cwd, settings) (string, error)
// ---------------------------------------------------------------------------

// EnvEnableTeamMemory is the boolean gate mirroring the TS growthbook
// `tengu_herring_clock` flag. When unset, TeamMemoryEnabled on the
// EngineConfig is the source of truth; when set to a truthy/falsy
// value it overrides the config (truthy wins, falsy forces-off).
const EnvEnableTeamMemory = "UBUILDING_ENABLE_TEAM_MEMORY"

// teamSubdir is the fixed subdirectory under the auto-memory tree
// that hosts team-shared memory. Git-synced by the user outside of
// this package.
const teamSubdir = "team"

// PathTraversalError reports a detected path-traversal or injection
// vector. Callers treat this as a skip-this-entry signal rather than
// an abort so batch operations surface only the offending files.
type PathTraversalError struct{ Msg string }

func (e *PathTraversalError) Error() string { return e.Msg }

// newPathTraversalError is a tiny constructor used in the file so
// format calls stay compact and the error type lookup is one hop.
func newPathTraversalError(format string, args ...interface{}) *PathTraversalError {
	return &PathTraversalError{Msg: fmt.Sprintf(format, args...)}
}

// ---------------------------------------------------------------------------
// M6.I1 · IsTeamMemoryEnabled + path accessors.
// ---------------------------------------------------------------------------

// IsTeamMemoryEnabled reports whether the team-memory subsystem is
// active. Team memory is a strict subset of auto memory — when
// auto-memory is off, team is off regardless of config. Environment
// variable UBUILDING_ENABLE_TEAM_MEMORY overrides the EngineConfig
// when set.
func IsTeamMemoryEnabled(cfg agents.EngineConfig, settings SettingsProvider) bool {
	if !IsAutoMemoryEnabled(cfg, settings) {
		return false
	}
	if v, ok := envBool(EnvEnableTeamMemory); ok {
		return v
	}
	return cfg.TeamMemoryEnabled
}

// envBool reads name from the environment and interprets it as a
// boolean. Returns (value, true) when present and parsable, else
// (false, false).
func envBool(name string) (bool, bool) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return false, false
	}
	switch strings.ToLower(raw) {
	case "1", "true", "yes", "on":
		return true, true
	case "0", "false", "no", "off":
		return false, true
	}
	return false, false
}

// GetTeamMemPath returns the team-memory directory for cwd. Format:
//
//	<auto-mem-path>/team/
//
// The trailing separator is preserved so prefix-containment checks
// correctly reject paths like `<auto>/team-evil/`. Returns "" when
// the parent auto-memory path cannot be resolved.
func GetTeamMemPath(cwd string, settings SettingsProvider) string {
	autoDir := GetAutoMemPath(cwd, settings)
	if autoDir == "" {
		return ""
	}
	// Normalise via NFC mirroring upstream's `.normalize('NFC')` so
	// callers that compare paths byte-wise match regardless of how
	// the underlying filesystem normalises Unicode filenames.
	out := filepath.Join(autoDir, teamSubdir) + string(os.PathSeparator)
	return norm.NFC.String(out)
}

// GetTeamMemEntrypoint returns `<team-mem-path>/MEMORY.md`.
func GetTeamMemEntrypoint(cwd string, settings SettingsProvider) string {
	dir := GetTeamMemPath(cwd, settings)
	if dir == "" {
		return ""
	}
	return filepath.Join(strings.TrimRight(dir, string(os.PathSeparator)),
		autoMemEntrypoint)
}

// ---------------------------------------------------------------------------
// M6.I2 · Key sanitisation.
// ---------------------------------------------------------------------------

// sanitizeTeamMemKey rejects relative keys that carry injection
// payloads. The check set mirrors TS:
//
//   1. Null bytes (C-syscall truncation).
//   2. URL-encoded traversal — if decoded value differs from raw AND
//      the decoded value contains `..` or `/`.
//   3. NFKC-normalised traversal — defence-in-depth for downstream
//      filesystems that silently normalise fullwidth separators.
//   4. Backslashes (always rejected — Windows separator is a
//      traversal vector when keys are joined against a POSIX-flavoured
//      teamDir and vice-versa).
//   5. Absolute paths (leading `/` or `C:\` etc.).
//
// Returns the untouched key on success so callers can re-use the
// validated string directly.
func sanitizeTeamMemKey(key string) (string, error) {
	if strings.ContainsRune(key, 0) {
		return "", newPathTraversalError("Null byte in path key: %q", key)
	}
	if decoded, err := url.QueryUnescape(key); err == nil &&
		decoded != key &&
		(strings.Contains(decoded, "..") || strings.Contains(decoded, "/")) {
		return "", newPathTraversalError("URL-encoded traversal in path key: %q", key)
	}
	if nfkc := norm.NFKC.String(key); nfkc != key {
		if strings.Contains(nfkc, "..") ||
			strings.Contains(nfkc, "/") ||
			strings.Contains(nfkc, `\`) ||
			strings.ContainsRune(nfkc, 0) {
			return "", newPathTraversalError("Unicode-normalized traversal in path key: %q", key)
		}
	}
	if strings.Contains(key, `\`) {
		return "", newPathTraversalError("Backslash in path key: %q", key)
	}
	if strings.HasPrefix(key, "/") || filepath.IsAbs(key) {
		return "", newPathTraversalError("Absolute path key: %q", key)
	}
	return key, nil
}

// ---------------------------------------------------------------------------
// M6.I3 · realpathDeepestExisting.
// ---------------------------------------------------------------------------

// realpathDeepestExisting walks up absPath until filepath.EvalSymlinks
// succeeds, rejoining the tail (non-existing segments) onto the
// resolved ancestor. Detects dangling symlinks and symlink loops as
// PathTraversalError. Follows upstream's two-phase approach where
// phase 1 is string-level containment (performed by the caller) and
// phase 2 (this function) is the realpath-aware pass.
func realpathDeepestExisting(absPath string) (string, error) {
	var tail []string
	current := absPath
	for {
		parent := filepath.Dir(current)
		if parent == current {
			// Reached the filesystem root — rare in practice; fall
			// back to the input so the caller's containment check
			// rejects this pathologically deep case.
			return absPath, nil
		}
		real, err := filepath.EvalSymlinks(current)
		if err == nil {
			if len(tail) == 0 {
				return real, nil
			}
			// Reverse the tail (deepest-first on the way in → outer-first here).
			for i, j := 0, len(tail)-1; i < j; i, j = i+1, j-1 {
				tail[i], tail[j] = tail[j], tail[i]
			}
			return filepath.Join(append([]string{real}, tail...)...), nil
		}
		if isLoopErr(err) {
			return "", newPathTraversalError("Symlink loop detected in path: %q", current)
		}
		if errors.Is(err, fs.ErrNotExist) {
			// Distinguish between "truly non-existent" and "dangling
			// symlink": a dangling symlink's entry exists per
			// os.Lstat even though the target does not. Abort
			// validation for dangling symlinks — writeFile would
			// follow the link and land outside teamDir.
			if st, lerr := os.Lstat(current); lerr == nil {
				if st.Mode()&os.ModeSymlink != 0 {
					return "", newPathTraversalError(
						"Dangling symlink detected (target does not exist): %q",
						current)
				}
			}
		} else if !errors.Is(err, fs.ErrNotExist) && !isNotDirErr(err) {
			// EACCES / EIO / etc. — cannot verify containment, fail
			// closed so the caller skips this key.
			return "", newPathTraversalError(
				"Cannot verify path containment (%v): %q", err, current)
		}
		leaf := current[len(parent)+len(string(os.PathSeparator)):]
		tail = append(tail, leaf)
		current = parent
	}
}

// isLoopErr tries to detect ELOOP / "too many links" regardless of
// the OS-specific error wording. On POSIX, Go's errno surfaces as
// syscall.ELOOP, but on Windows and on wrapped errors we fall back
// to substring matching.
func isLoopErr(err error) bool {
	s := err.Error()
	return strings.Contains(s, "too many links") ||
		strings.Contains(s, "loop") ||
		strings.Contains(s, "ELOOP")
}

// isNotDirErr mirrors ENOTDIR detection in the TS reference — on Go
// we also accept wrapped errors that match fs.ErrInvalid because
// Windows sometimes surfaces "not a directory" that way.
func isNotDirErr(err error) bool {
	s := err.Error()
	return strings.Contains(s, "not a directory") ||
		strings.Contains(s, "ENOTDIR")
}

// ---------------------------------------------------------------------------
// M6.I4 · isRealPathWithinTeamDir.
// ---------------------------------------------------------------------------

// isRealPathWithinTeamDir returns true iff realCandidate (already
// symlink-resolved) is the same as, or lives strictly beneath, the
// realpath of the team memory directory. When the team dir does not
// exist this function returns true — a symlink escape requires a
// pre-existing symlink inside teamDir, so without the dir there is
// no attack surface.
func isRealPathWithinTeamDir(realCandidate, teamDir string) bool {
	// Strip trailing separator for EvalSymlinks which rejects them
	// on some platforms.
	stripped := strings.TrimRight(teamDir, string(os.PathSeparator))
	realTeam, err := filepath.EvalSymlinks(stripped)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) || isNotDirErr(err) {
			return true
		}
		return false // EACCES etc. — fail closed.
	}
	if realCandidate == realTeam {
		return true
	}
	return strings.HasPrefix(realCandidate, realTeam+string(os.PathSeparator))
}

// ---------------------------------------------------------------------------
// M6.I5 · IsTeamMemPath + IsTeamMemFile.
// ---------------------------------------------------------------------------

// IsTeamMemPath reports whether filePath resolves (via filepath.Clean
// → filepath.Abs) to a location under the team memory directory.
// Does NOT traverse symlinks — use ValidateTeamMemWritePath for the
// full symlink-aware check.
func IsTeamMemPath(filePath, cwd string, settings SettingsProvider) bool {
	teamDir := GetTeamMemPath(cwd, settings)
	if teamDir == "" {
		return false
	}
	abs, err := filepath.Abs(filePath)
	if err != nil {
		return false
	}
	abs = filepath.Clean(abs)
	return strings.HasPrefix(abs+string(os.PathSeparator), teamDir) || abs+string(os.PathSeparator) == teamDir
}

// IsTeamMemFile combines IsTeamMemoryEnabled + IsTeamMemPath.
func IsTeamMemFile(
	filePath, cwd string,
	settings SettingsProvider,
	cfg agents.EngineConfig,
) bool {
	return IsTeamMemoryEnabled(cfg, settings) && IsTeamMemPath(filePath, cwd, settings)
}

// ---------------------------------------------------------------------------
// M6.I6 · ValidateTeamMemWritePath.
// ---------------------------------------------------------------------------

// ValidateTeamMemWritePath verifies that filePath is safe to write
// inside the team memory directory. The two-phase check follows
// upstream:
//
//  1. filepath.Abs + filepath.Clean → string-level containment vs
//     teamDir (rejects plain `..` traversal without touching FS).
//  2. realpathDeepestExisting + isRealPathWithinTeamDir → symlink-
//     aware containment (rejects the PSR M22186 symlink escape).
//
// Returns the string-level resolved path on success so callers can
// pass it straight to os.WriteFile.
func ValidateTeamMemWritePath(
	filePath, cwd string,
	settings SettingsProvider,
) (string, error) {
	if strings.ContainsRune(filePath, 0) {
		return "", newPathTraversalError("Null byte in path: %q", filePath)
	}
	teamDir := GetTeamMemPath(cwd, settings)
	if teamDir == "" {
		return "", newPathTraversalError("Team memory directory is unresolved (no cwd?)")
	}
	abs, err := filepath.Abs(filePath)
	if err != nil {
		return "", newPathTraversalError("cannot compute abs path: %v", err)
	}
	resolved := filepath.Clean(abs)
	if !strings.HasPrefix(resolved+string(os.PathSeparator), teamDir) &&
		resolved+string(os.PathSeparator) != teamDir {
		return "", newPathTraversalError("Path escapes team memory directory: %q", filePath)
	}
	realCandidate, err := realpathDeepestExisting(resolved)
	if err != nil {
		return "", err // already a PathTraversalError
	}
	if !isRealPathWithinTeamDir(realCandidate, teamDir) {
		return "", newPathTraversalError(
			"Path escapes team memory directory via symlink: %q", filePath)
	}
	return resolved, nil
}

// ---------------------------------------------------------------------------
// M6.I7 · ValidateTeamMemKey.
// ---------------------------------------------------------------------------

// ValidateTeamMemKey sanitises a relative key (typically supplied
// by a tool invocation) and returns the absolute safe path within
// teamDir. The sanitiser rejects obvious injection vectors up front;
// the realpath pass catches symlink escapes.
func ValidateTeamMemKey(
	relativeKey, cwd string,
	settings SettingsProvider,
) (string, error) {
	if _, err := sanitizeTeamMemKey(relativeKey); err != nil {
		return "", err
	}
	teamDir := GetTeamMemPath(cwd, settings)
	if teamDir == "" {
		return "", newPathTraversalError("Team memory directory is unresolved (no cwd?)")
	}
	full := filepath.Join(strings.TrimRight(teamDir, string(os.PathSeparator)), relativeKey)
	resolved := filepath.Clean(full)
	if !strings.HasPrefix(resolved+string(os.PathSeparator), teamDir) &&
		resolved+string(os.PathSeparator) != teamDir {
		return "", newPathTraversalError("Key escapes team memory directory: %q", relativeKey)
	}
	realCandidate, err := realpathDeepestExisting(resolved)
	if err != nil {
		return "", err
	}
	if !isRealPathWithinTeamDir(realCandidate, teamDir) {
		return "", newPathTraversalError(
			"Key escapes team memory directory via symlink: %q", relativeKey)
	}
	return resolved, nil
}
