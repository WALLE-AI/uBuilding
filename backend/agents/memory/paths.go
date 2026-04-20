package memory

import (
	"errors"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wall-ai/ubuilding/backend/agents"
)

// ---------------------------------------------------------------------------
// M2 · paths.go — auto-memory / memdir path resolution.
//
// Ports the relevant half of `src/memdir/paths.ts`:
//
//   - getMemoryBaseDir         → GetMemoryBaseDir
//   - validateMemoryPath       → ValidateMemoryPath
//   - sanitizePath             → SanitizePathKey
//   - getAutoMemPath           → GetAutoMemPath
//   - getAutoMemEntrypoint     → GetAutoMemEntrypoint
//   - getAutoMemDailyLogPath   → GetAutoMemDailyLogPath
//   - isAutoMemoryEnabled      → IsAutoMemoryEnabled
//
// Default posture differs from upstream: auto-memory is OPT-IN here
// (default off) to preserve backwards compatibility with existing Go
// callers; set `EngineConfig.AutoMemoryEnabled` or
// `UBUILDING_ENABLE_AUTO_MEMORY=1` to turn it on.
// ---------------------------------------------------------------------------

// Environment variable names.
const (
	EnvRemoteMemoryDir          = "UBUILDING_REMOTE_MEMORY_DIR"
	EnvCoworkMemoryPathOverride = "UBUILDING_COWORK_MEMORY_PATH_OVERRIDE"
	EnvEnableAutoMemory         = "UBUILDING_ENABLE_AUTO_MEMORY"
	EnvDisableAutoMemory        = "UBUILDING_DISABLE_AUTO_MEMORY"
	EnvOverrideDate             = "UBUILDING_OVERRIDE_DATE"

	// Legacy CLAUDE_CODE_OVERRIDE_DATE is honoured for parity with
	// upstream's assistant-mode log path testing.
	EnvLegacyOverrideDate = "CLAUDE_CODE_OVERRIDE_DATE"
)

// Directory layout constants (mirror paths.ts).
const (
	autoMemDirname       = "memory"
	autoMemEntrypoint    = "MEMORY.md"
	autoMemProjectsChild = "projects"
	autoMemLogsChild     = "logs"
	defaultConfigSub     = "ubuilding"
)

// Sentinel errors returned by ValidateMemoryPath. Tests assert on
// these via errors.Is, so downstream callers can distinguish the
// rejection kind without string matching.
var (
	ErrPathEmpty         = errors.New("memory path: empty")
	ErrPathNullByte      = errors.New("memory path: contains null byte")
	ErrPathRelative      = errors.New("memory path: not absolute")
	ErrPathTooShort      = errors.New("memory path: length < 3")
	ErrPathDriveRoot     = errors.New("memory path: bare drive root")
	ErrPathUNC           = errors.New("memory path: UNC path (network share)")
	ErrPathTildeTrivial  = errors.New("memory path: trivial tilde remainder")
	ErrGitRootUnresolved = errors.New("memory path: could not resolve git root")
)

// ---------------------------------------------------------------------------
// M2.I1 · GetMemoryBaseDir
// ---------------------------------------------------------------------------

var (
	baseDirOnce  sync.Once
	baseDirCache string
)

// GetMemoryBaseDir returns the base directory for persistent memory
// storage. Resolution order:
//
//  1. `UBUILDING_REMOTE_MEMORY_DIR` env var when set to a non-empty
//     value. Empty strings are treated as "not set" so a stray
//     “export UBUILDING_REMOTE_MEMORY_DIR=“ in a shell profile does
//     not silently redirect memory to the current directory.
//  2. `<os.UserConfigDir()>/ubuilding` — e.g. `%AppData%/ubuilding` on
//     Windows, `~/.config/ubuilding` on Linux, `~/Library/Application
//     Support/ubuilding` on macOS.
//  3. "" when neither is resolvable (callers must handle this).
//
// Cached via sync.Once; tests may call ResetMemoryBaseDirCache to
// observe env changes mid-test.
func GetMemoryBaseDir() string {
	baseDirOnce.Do(func() { baseDirCache = computeMemoryBaseDir() })
	return baseDirCache
}

func computeMemoryBaseDir() string {
	if v := strings.TrimSpace(os.Getenv(EnvRemoteMemoryDir)); v != "" {
		return v
	}
	if dir, err := os.UserConfigDir(); err == nil && dir != "" {
		return filepath.Join(dir, defaultConfigSub)
	}
	return ""
}

// ResetMemoryBaseDirCache clears the sync.Once cache used by
// GetMemoryBaseDir. Intended for tests only.
func ResetMemoryBaseDirCache() {
	baseDirOnce = sync.Once{}
	baseDirCache = ""
}

// ---------------------------------------------------------------------------
// M2.I2 · ValidateMemoryPath
// ---------------------------------------------------------------------------

// ValidateMemoryPath normalises raw and rejects paths that would be
// dangerous as a read-allowlist root:
//
//   - empty / whitespace only       → ErrPathEmpty
//   - contains NUL byte             → ErrPathNullByte
//   - relative after normalize      → ErrPathRelative
//   - length < 3                    → ErrPathTooShort
//   - bare drive root ("C:")         → ErrPathDriveRoot
//   - UNC (`\\server\share` or `//…`) → ErrPathUNC
//
// When expandTilde is true a leading `~/` or `~\` is expanded to the
// user's home directory. Trivial remainders (`~`, `~/`, `~/.`,
// `~/..`) are rejected with ErrPathTildeTrivial so they cannot match
// all of $HOME.
//
// On success the returned string is an absolute path ending in
// exactly one separator, suitable as a prefix for containment checks.
func ValidateMemoryPath(raw string, expandTilde bool) (string, error) {
	candidate := strings.TrimSpace(raw)
	if candidate == "" {
		return "", ErrPathEmpty
	}

	if expandTilde && (strings.HasPrefix(candidate, "~/") || strings.HasPrefix(candidate, `~\`)) {
		rest := candidate[2:]
		probe := rest
		if probe == "" {
			probe = "."
		}
		restNorm := filepath.Clean(probe)
		if restNorm == "." || restNorm == ".." {
			return "", ErrPathTildeTrivial
		}
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return "", fmt.Errorf("memory path: cannot resolve home: %w", err)
		}
		candidate = filepath.Join(home, rest)
	}

	// NUL byte check must happen BEFORE Clean because Clean may drop it
	// on some platforms (unlikely but belt-and-braces).
	if strings.ContainsRune(candidate, '\x00') {
		return "", ErrPathNullByte
	}

	cleaned := filepath.Clean(candidate)

	// UNC / drive-root checks run BEFORE the IsAbs gate. Cleaning a
	// Windows path like `C:\` preserves the trailing slash, but once
	// we strip it to do prefix-containment, `filepath.IsAbs("C:")`
	// returns false — making a naive IsAbs check incorrectly reject
	// the path as relative instead of as a drive root.
	if strings.HasPrefix(cleaned, `\\`) || strings.HasPrefix(cleaned, "//") {
		return "", ErrPathUNC
	}
	stripped := strings.TrimRight(cleaned, `/\`)
	if isBareDriveRoot(stripped) {
		return "", ErrPathDriveRoot
	}

	// Now enforce absoluteness on the original cleaned form so `C:\sub`
	// (absolute on Windows) is accepted while `C:sub` (relative on
	// Windows) is rejected.
	if !filepath.IsAbs(cleaned) {
		return "", ErrPathRelative
	}
	if len(stripped) < 3 {
		return "", ErrPathTooShort
	}

	// Append exactly one separator so callers can do prefix containment
	// without worrying about the "teamDir" vs "teamDir-evil" confusion.
	return stripped + string(os.PathSeparator), nil
}

func isBareDriveRoot(p string) bool {
	// Accept either "C:" or "c:" — Windows shells are case-insensitive
	// for drive letters. Length must be exactly 2.
	if len(p) != 2 {
		return false
	}
	c := p[0]
	if !((c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z')) {
		return false
	}
	return p[1] == ':'
}

// ---------------------------------------------------------------------------
// M2.I3 · SanitizePathKey
// ---------------------------------------------------------------------------

// maxSanitizedLength mirrors `sessionStoragePortable.MAX_SANITIZED_LENGTH`.
// Components longer than this get truncated and a stable hash appended.
const maxSanitizedLength = 200

// SanitizePathKey turns a git-root / project path into a stable
// filesystem-safe namespace key. Every non-alphanumeric rune becomes
// '_' so the result is safe on every supported OS.
//
// Examples:
//
//	"/Users/a/proj"    → "_Users_a_proj"
//	"C:\\Users\\a\\proj" → "C__Users_a_proj"
//
// NB: this function does NOT trim leading separators. Dropping the
// leading "/" would collapse "/foo" and "foo" into the same key which
// would be a footgun for isAutoManagedMemoryFile containment checks.
//
// Paths longer than 200 bytes are truncated and suffixed with a
// stable FNV-1a / base36 hash so the key stays unique without
// exceeding individual filesystem component limits.
func SanitizePathKey(projectRoot string) string {
	if projectRoot == "" {
		return "_"
	}
	var b strings.Builder
	b.Grow(len(projectRoot))
	for _, r := range projectRoot {
		switch {
		case r >= '0' && r <= '9',
			r >= 'A' && r <= 'Z',
			r >= 'a' && r <= 'z':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	s := b.String()
	if s == "" {
		return "_"
	}
	if len(s) <= maxSanitizedLength {
		return s
	}
	h := fnv.New64a()
	_, _ = h.Write([]byte(projectRoot))
	return s[:maxSanitizedLength] + "_" + strconv.FormatUint(h.Sum64(), 36)
}

// ---------------------------------------------------------------------------
// M2.I4 · GetAutoMemPath + SettingsProvider
// ---------------------------------------------------------------------------

// SettingsProvider is the minimal interface needed by the auto-memory
// path resolver. Hosts can plug any settings store that exposes these
// two lookups. Pass nil when no settings store is available; the
// resolver then falls back to env + default.
type SettingsProvider interface {
	// AutoMemoryEnabled returns (value, present). present=false means
	// the settings file did not contain the key at all.
	AutoMemoryEnabled() (enabled bool, present bool)

	// AutoMemoryDirectory returns "" when unset. Paths are subject to
	// ValidateMemoryPath(expandTilde=true) and rejected on failure.
	AutoMemoryDirectory() string
}

// nopSettings implements SettingsProvider and reports nothing.
type nopSettings struct{}

func (nopSettings) AutoMemoryEnabled() (bool, bool) { return false, false }
func (nopSettings) AutoMemoryDirectory() string     { return "" }

// NopSettingsProvider is a convenience no-op settings provider for
// callers that have no settings store wired up yet.
var NopSettingsProvider SettingsProvider = nopSettings{}

// GetAutoMemPath returns the absolute auto-memory directory for cwd
// with a trailing separator. Resolution order mirrors paths.ts:
//
//  1. `UBUILDING_COWORK_MEMORY_PATH_OVERRIDE` env var (full-path override,
//     no tilde expansion — intended for programmatic callers).
//  2. settings.AutoMemoryDirectory() (tilde-expanded, validated).
//  3. `<base>/projects/<SanitizePathKey(gitRoot)>/memory/` where base
//     comes from GetMemoryBaseDir() and gitRoot from
//     FindCanonicalGitRoot(cwd) (falling back to cwd itself).
//
// Returns "" when no resolution succeeds — callers must handle the
// empty case (typically by disabling memory features).
func GetAutoMemPath(cwd string, settings SettingsProvider) string {
	if override, err := ValidateMemoryPath(os.Getenv(EnvCoworkMemoryPathOverride), false); err == nil && override != "" {
		return override
	}
	if settings != nil {
		if setting := settings.AutoMemoryDirectory(); setting != "" {
			if norm, err := ValidateMemoryPath(setting, true); err == nil && norm != "" {
				return norm
			}
		}
	}
	base := GetMemoryBaseDir()
	if base == "" {
		return ""
	}
	root := autoMemBase(cwd)
	if root == "" {
		return ""
	}
	dir := filepath.Join(base, autoMemProjectsChild, SanitizePathKey(root), autoMemDirname)
	return dir + string(os.PathSeparator)
}

// HasAutoMemPathOverride reports whether the env-var override is set
// to a valid path. Used by tool carve-outs that must distinguish
// "user chose a custom dir" from "we defaulted" — per the paths.ts
// doc block.
func HasAutoMemPathOverride() bool {
	_, err := ValidateMemoryPath(os.Getenv(EnvCoworkMemoryPathOverride), false)
	return err == nil && strings.TrimSpace(os.Getenv(EnvCoworkMemoryPathOverride)) != ""
}

// autoMemBase picks the project root that keys the auto-memory dir.
// Uses the canonical git root so every worktree of a repo shares one
// memory directory (anthropics/claude-code#24382). Falls back to cwd
// when cwd is not inside a git tree.
func autoMemBase(cwd string) string {
	if root, ok := FindCanonicalGitRoot(cwd); ok {
		return root
	}
	if cwd == "" {
		return ""
	}
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return cwd
	}
	return abs
}

// FindCanonicalGitRoot walks up from cwd looking for a `.git`
// directory or a `.git` file (worktree pointer). Returns (root,
// true) when found and ("", false) otherwise.
//
// Worktree handling: when `.git` is a regular file with content
// `gitdir: <path>/.git/worktrees/<name>`, the canonical root is the
// directory two levels above that path (i.e. the main-repo work
// tree). This mirrors upstream's findCanonicalGitRoot behaviour so
// every worktree shares one memory dir.
func FindCanonicalGitRoot(cwd string) (string, bool) {
	if cwd == "" {
		return "", false
	}
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return "", false
	}
	cur := abs
	for {
		gitPath := filepath.Join(cur, ".git")
		info, err := os.Lstat(gitPath)
		if err == nil {
			if info.IsDir() {
				return cur, true
			}
			if info.Mode().IsRegular() {
				if root, ok := parseWorktreeGitdir(gitPath, cur); ok {
					return root, true
				}
			}
			return cur, true
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return "", false
		}
		cur = parent
	}
}

// parseWorktreeGitdir reads a `.git` file of the form
// `gitdir: /abs/path/.git/worktrees/<name>` (or a relative form) and
// returns the main repo's work-tree directory.
func parseWorktreeGitdir(gitFile, worktreeRoot string) (string, bool) {
	data, err := os.ReadFile(gitFile)
	if err != nil {
		return "", false
	}
	line := strings.TrimSpace(string(data))
	const prefix = "gitdir:"
	if !strings.HasPrefix(line, prefix) {
		return "", false
	}
	rel := strings.TrimSpace(strings.TrimPrefix(line, prefix))
	if rel == "" {
		return "", false
	}
	if !filepath.IsAbs(rel) {
		rel = filepath.Join(worktreeRoot, rel)
	}
	rel = filepath.Clean(rel)
	// rel should look like .../.git/worktrees/<name>. Climb two levels
	// to reach `.../.git`, then one more to reach the main work tree.
	worktreesDir := filepath.Dir(rel)
	if filepath.Base(worktreesDir) != "worktrees" {
		return "", false
	}
	gitDir := filepath.Dir(worktreesDir)
	if filepath.Base(gitDir) != ".git" {
		return "", false
	}
	return filepath.Dir(gitDir), true
}

// ---------------------------------------------------------------------------
// M2.I5 · GetAutoMemEntrypoint + GetAutoMemDailyLogPath
// ---------------------------------------------------------------------------

// GetAutoMemEntrypoint joins GetAutoMemPath with MEMORY.md. Returns ""
// when the auto-memory path itself cannot be resolved.
func GetAutoMemEntrypoint(cwd string, settings SettingsProvider) string {
	dir := GetAutoMemPath(cwd, settings)
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, autoMemEntrypoint)
}

// GetAutoMemDailyLogPath returns the daily-log file path for the
// given date, shaped as `<autoMemPath>/logs/YYYY/MM/YYYY-MM-DD.md`.
// When date is zero the current system date is used, unless
// `UBUILDING_OVERRIDE_DATE` / `CLAUDE_CODE_OVERRIDE_DATE` is set to
// `YYYY-MM-DD` — in which case the env override wins.
func GetAutoMemDailyLogPath(cwd string, settings SettingsProvider, date time.Time) string {
	dir := GetAutoMemPath(cwd, settings)
	if dir == "" {
		return ""
	}
	if date.IsZero() {
		date = resolveOverrideDate(time.Now())
	}
	yyyy := strconv.Itoa(date.Year())
	mm := fmt.Sprintf("%02d", int(date.Month()))
	dd := fmt.Sprintf("%02d", date.Day())
	return filepath.Join(dir, autoMemLogsChild, yyyy, mm, fmt.Sprintf("%s-%s-%s.md", yyyy, mm, dd))
}

// resolveOverrideDate lets tests (and assistant-mode scenarios) pin
// the "today" value by setting an env var. Returns fallback when the
// env var is empty or unparseable.
func resolveOverrideDate(fallback time.Time) time.Time {
	for _, name := range []string{EnvOverrideDate, EnvLegacyOverrideDate} {
		raw := strings.TrimSpace(os.Getenv(name))
		if raw == "" {
			continue
		}
		if t, err := time.Parse("2006-01-02", raw); err == nil {
			return t
		}
	}
	return fallback
}

// ---------------------------------------------------------------------------
// M2.I6 · IsAutoMemoryEnabled
// ---------------------------------------------------------------------------

// IsAutoMemoryEnabled reports whether auto-memory should be active
// for this engine. Priority chain (first match wins):
//
//  1. `UBUILDING_DISABLE_AUTO_MEMORY` env truthy → false.
//  2. `UBUILDING_ENABLE_AUTO_MEMORY` env truthy  → true.
//  3. settings.AutoMemoryEnabled() when present → that value.
//  4. cfg.AutoMemoryEnabled == true              → true.
//  5. Default                                    → false (opt-in).
//
// Pass `NopSettingsProvider` when no settings store is available.
func IsAutoMemoryEnabled(cfg agents.EngineConfig, settings SettingsProvider) bool {
	if isEnvTruthy(os.Getenv(EnvDisableAutoMemory)) {
		return false
	}
	if isEnvTruthy(os.Getenv(EnvEnableAutoMemory)) {
		return true
	}
	if settings != nil {
		if enabled, present := settings.AutoMemoryEnabled(); present {
			return enabled
		}
	}
	return cfg.AutoMemoryEnabled
}

// isEnvTruthy mirrors upstream isEnvTruthy: "1"/"true"/"yes"/"on"
// (case-insensitive) are truthy. Anything else is false.
func isEnvTruthy(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// isEnvFalsy is the dual used where a tri-state "unset / true / false"
// matters. Currently unused internally but exported for future M11/M12
// bridges. Kept unexported to avoid API churn.
func isEnvFalsy(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "0", "false", "no", "off":
		return true
	}
	return false
}

// Compile-time use so the helper is not dead when no caller wires up
// explicit tri-state logic yet — used once the M11/M12 env toggles
// land. Keep as a build-time reference; removing it would break
// future opt-in wiring.
var _ = isEnvFalsy
