package memory

import (
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/wall-ai/ubuilding/backend/agents"
)

// ---------------------------------------------------------------------------
// M9 · Memory file detection predicates.
//
// Ports `src/utils/memoryFileDetection.ts`:
//
//   - IsAutoMemPath           — path starts with getAutoMemPath()
//   - IsAutoMemFile           — enabled guard + IsAutoMemPath
//   - MemoryScopeForPath      — "personal" / "team" / nil
//   - IsAutoManagedMemoryFile — union of auto, team, session, agent
//   - IsMemoryDirectory       — directory-level classification
//   - DetectSessionFileType   — session_memory / session_transcript
//   - DetectSessionPatternType — glob/pattern-level variant
//   - IsShellCommandTargetingMemory — shell command string analysis
//   - IsAutoManagedMemoryPattern — pattern-level auto-managed check
//
// All predicates are designed for use from tool permission and
// collapse/badge logic without importing the tool layer.
// ---------------------------------------------------------------------------

// MemoryScope distinguishes personal vs team memory stores.
type MemoryScope string

const (
	MemoryScopePersonal MemoryScope = "personal"
	MemoryScopeTeam     MemoryScope = "team"
)

// SessionFileType identifies the kind of session-related file.
type SessionFileType string

const (
	SessionFileMemory     SessionFileType = "session_memory"
	SessionFileTranscript SessionFileType = "session_transcript"
)

// ---------------------------------------------------------------------------
// Path containment helpers (platform-aware string comparison).
// ---------------------------------------------------------------------------

// toComparable converts a path to a stable string-comparable form:
// forward-slash separated, and on Windows, lowercased.
func toComparable(p string) string {
	posixForm := strings.ReplaceAll(p, `\`, "/")
	if runtime.GOOS == "windows" {
		return strings.ToLower(posixForm)
	}
	return posixForm
}

// ---------------------------------------------------------------------------
// M9.I1 · IsAutoMemPath
// ---------------------------------------------------------------------------

// IsAutoMemPath reports whether absolutePath lives under the auto-memory
// directory. Normalizes the path to prevent traversal bypasses via "..".
// Mirrors TS `isAutoMemPath` in `src/memdir/paths.ts`.
func IsAutoMemPath(absolutePath, cwd string, settings SettingsProvider) bool {
	normalizedPath := filepath.Clean(absolutePath)
	autoDir := GetAutoMemPath(cwd, settings)
	if autoDir == "" {
		return false
	}
	return pathStartsWith(normalizedPath, autoDir)
}

// ---------------------------------------------------------------------------
// M9.I2 · IsAutoMemFile
// ---------------------------------------------------------------------------

// IsAutoMemFile reports whether filePath is within the auto-memory directory
// AND auto-memory is currently enabled.
func IsAutoMemFile(
	filePath, cwd string,
	settings SettingsProvider,
	cfg agents.EngineConfig,
) bool {
	if !IsAutoMemoryEnabled(cfg, settings) {
		return false
	}
	return IsAutoMemPath(filePath, cwd, settings)
}

// ---------------------------------------------------------------------------
// M9.I3 · MemoryScopeForPath
// ---------------------------------------------------------------------------

// MemoryScopeForPath determines which memory store (if any) a path belongs
// to. Team dir is a subdirectory of auto-mem dir, so team paths match both —
// check team first. Returns nil-equivalent empty string when the path is not
// a memory file.
func MemoryScopeForPath(
	filePath, cwd string,
	settings SettingsProvider,
	cfg agents.EngineConfig,
) MemoryScope {
	if IsTeamMemoryEnabled(cfg, settings) && IsTeamMemFile(filePath, cwd, settings, cfg) {
		return MemoryScopeTeam
	}
	if IsAutoMemFile(filePath, cwd, settings, cfg) {
		return MemoryScopePersonal
	}
	return ""
}

// ---------------------------------------------------------------------------
// M9.I4 · DetectSessionFileType
// ---------------------------------------------------------------------------

// DetectSessionFileType detects if a file path is a session-related file
// under the config home directory. Returns the type or "".
// Mirrors TS `detectSessionFileType` in memoryFileDetection.ts.
func DetectSessionFileType(filePath string) SessionFileType {
	configDir := GetMemoryBaseDir()
	if configDir == "" {
		return ""
	}
	normalized := toComparable(filePath)
	configDirCmp := toComparable(configDir)
	if !strings.HasPrefix(normalized, configDirCmp) {
		return ""
	}
	if strings.Contains(normalized, "/session-memory/") && strings.HasSuffix(normalized, ".md") {
		return SessionFileMemory
	}
	if strings.Contains(normalized, "/projects/") && strings.HasSuffix(normalized, ".jsonl") {
		return SessionFileTranscript
	}
	return ""
}

// ---------------------------------------------------------------------------
// M9.I5 · DetectSessionPatternType
// ---------------------------------------------------------------------------

// DetectSessionPatternType checks if a glob/pattern string indicates session
// file access intent. Used for Grep/Glob tools where we check patterns.
func DetectSessionPatternType(pattern string) SessionFileType {
	normalized := strings.ReplaceAll(pattern, `\`, "/")
	if strings.Contains(normalized, "session-memory") &&
		(strings.Contains(normalized, ".md") || strings.HasSuffix(normalized, "*")) {
		return SessionFileMemory
	}
	if strings.Contains(normalized, ".jsonl") ||
		(strings.Contains(normalized, "projects") && strings.Contains(normalized, "*.jsonl")) {
		return SessionFileTranscript
	}
	return ""
}

// ---------------------------------------------------------------------------
// M9.I6 · IsAutoManagedMemoryFile
// ---------------------------------------------------------------------------

// IsAutoManagedMemoryFile reports whether a file is Claude-managed memory
// (NOT user-managed instruction files). Includes: auto-memory (memdir),
// team memory, session memory/transcripts. Excludes: CLAUDE.md,
// CLAUDE.local.md, .claude/rules/*.md (user-managed).
// Used for collapse/badge logic.
func IsAutoManagedMemoryFile(
	filePath, cwd string,
	settings SettingsProvider,
	cfg agents.EngineConfig,
) bool {
	if IsAutoMemFile(filePath, cwd, settings, cfg) {
		return true
	}
	if IsTeamMemoryEnabled(cfg, settings) && IsTeamMemFile(filePath, cwd, settings, cfg) {
		return true
	}
	if DetectSessionFileType(filePath) != "" {
		return true
	}
	return false
}

// ---------------------------------------------------------------------------
// M9.I7 · IsMemoryDirectory
// ---------------------------------------------------------------------------

// IsMemoryDirectory reports whether a directory path is memory-related.
// Used by Grep/Glob which take a directory `path` rather than a file.
// Mirrors TS `isMemoryDirectory` in memoryFileDetection.ts.
func IsMemoryDirectory(
	dirPath, cwd string,
	settings SettingsProvider,
	cfg agents.EngineConfig,
) bool {
	normalizedPath := filepath.Clean(dirPath)
	normalizedCmp := toComparable(normalizedPath)

	// Team memory directories.
	if IsTeamMemoryEnabled(cfg, settings) && IsTeamMemPath(normalizedPath, cwd, settings) {
		return true
	}

	// Auto-memory path (incl. override).
	if IsAutoMemoryEnabled(cfg, settings) {
		autoMemPath := GetAutoMemPath(cwd, settings)
		if autoMemPath != "" {
			autoMemDirCmp := toComparable(strings.TrimRight(autoMemPath, `/\`))
			autoMemPathCmp := toComparable(autoMemPath)
			if normalizedCmp == autoMemDirCmp || strings.HasPrefix(normalizedCmp, autoMemPathCmp) {
				return true
			}
		}
	}

	configDirCmp := toComparable(GetMemoryBaseDir())
	if configDirCmp == "" {
		return false
	}

	underConfig := strings.HasPrefix(normalizedCmp, configDirCmp)
	if !underConfig {
		return false
	}
	if strings.Contains(normalizedCmp, "/session-memory/") {
		return true
	}
	if strings.Contains(normalizedCmp, "/projects/") {
		return true
	}
	if IsAutoMemoryEnabled(cfg, settings) && strings.Contains(normalizedCmp, "/memory/") {
		return true
	}
	return false
}

// ---------------------------------------------------------------------------
// M9.I8 · IsShellCommandTargetingMemory
// ---------------------------------------------------------------------------

// absPathRe extracts absolute path-like tokens from a shell command.
// Unix: /foo/bar, Windows: C:\foo, C:/foo, MinGW: /c/foo.
var absPathRe = regexp.MustCompile(`(?:[A-Za-z]:[/\\]|/)[^\s'"]+`)

// IsShellCommandTargetingMemory checks if a shell command string targets
// memory files by extracting absolute path tokens and checking them against
// memory detection functions.
func IsShellCommandTargetingMemory(
	command, cwd string,
	settings SettingsProvider,
	cfg agents.EngineConfig,
) bool {
	autoMemDir := ""
	if IsAutoMemoryEnabled(cfg, settings) {
		autoMemDir = strings.TrimRight(GetAutoMemPath(cwd, settings), `/\`)
	}
	configDir := GetMemoryBaseDir()

	// Quick check: does the command mention any memory-related directory?
	commandCmp := toComparable(command)
	dirs := []string{configDir, autoMemDir}
	matchesAny := false
	for _, d := range dirs {
		if d == "" {
			continue
		}
		if strings.Contains(commandCmp, toComparable(d)) {
			matchesAny = true
			break
		}
	}
	if !matchesAny {
		return false
	}

	matches := absPathRe.FindAllString(command, -1)
	if len(matches) == 0 {
		return false
	}

	for _, m := range matches {
		// Strip trailing shell metacharacters.
		cleanPath := strings.TrimRight(m, ",;|&>")
		if IsAutoManagedMemoryFile(cleanPath, cwd, settings, cfg) || IsMemoryDirectory(cleanPath, cwd, settings, cfg) {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// M9.I9 · IsAutoManagedMemoryPattern
// ---------------------------------------------------------------------------

// IsAutoManagedMemoryPattern checks if a glob/pattern targets auto-managed
// memory files only. Used for collapse badge logic.
func IsAutoManagedMemoryPattern(pattern string) bool {
	return DetectSessionPatternType(pattern) != ""
}

// ---------------------------------------------------------------------------
// Helpers.
// ---------------------------------------------------------------------------

// pathStartsWith checks if child path starts with parent path, handling
// trailing separators and platform-specific case sensitivity.
func pathStartsWith(child, parent string) bool {
	childCmp := toComparable(child)
	parentCmp := toComparable(parent)
	// Ensure parent ends with separator for prefix match.
	if !strings.HasSuffix(parentCmp, "/") {
		parentCmp += "/"
	}
	return strings.HasPrefix(childCmp+"/", parentCmp) || childCmp == strings.TrimSuffix(parentCmp, "/")
}
