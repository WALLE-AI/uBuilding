package memory

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

// ---------------------------------------------------------------------------
// M3.I6 · claudeMdExcludes matching.
//
// Mirrors `isClaudeMdExcluded` + `resolveExcludePatterns` in
// `src/utils/claudemd.ts`. Only the User / Project / Local tiers can
// be excluded; Managed / AutoMem / TeamMem files are always loaded.
//
// Pattern semantics follow doublestar/v4 (a close cousin of picomatch).
// Patterns that begin with `/` are treated as absolute filesystem
// paths and their static prefix is additionally realpath-resolved so
// symlinked roots (e.g. `/tmp` → `/private/tmp` on macOS) still
// match.
// ---------------------------------------------------------------------------

// IsClaudeMdExcluded reports whether filePath should be dropped from
// the loaded-memory set given a list of user-supplied glob patterns.
// Behaviour specifics:
//
//   - Only User / Project / Local tiers are subject to excludes; other
//     tiers (Managed, AutoMem, TeamMem) always return false so excludes
//     cannot silence policy-owned instruction files.
//   - patterns with backslashes are normalised to forward slashes so a
//     Windows-authored `C:\proj\CLAUDE.md` pattern still matches
//     against the filePath after normalisation.
//   - When a pattern is absolute and its static prefix exists on the
//     filesystem, the realpath-resolved variant is checked as well.
func IsClaudeMdExcluded(filePath string, tier MemoryType, patterns []string) bool {
	if tier != MemoryTypeUser && tier != MemoryTypeProject && tier != MemoryTypeLocal {
		return false
	}
	if len(patterns) == 0 {
		return false
	}
	normPath := strings.ReplaceAll(filePath, `\`, "/")
	expanded := resolveExcludePatterns(patterns)
	for _, p := range expanded {
		if p == "" {
			continue
		}
		matched, _ := doublestar.PathMatch(p, normPath)
		if matched {
			return true
		}
	}
	return false
}

// globMetaRe locates the first glob metacharacter in a pattern; used
// to find the "static prefix" that can be realpath-resolved.
var globMetaRe = regexp.MustCompile(`[*?\[{]`)

// resolveExcludePatterns takes user-supplied patterns and appends
// realpath-resolved variants for any that start with `/` AND whose
// static prefix points to an existing directory. Backslashes are
// normalised to forward slashes up front.
func resolveExcludePatterns(patterns []string) []string {
	out := make([]string, 0, len(patterns)*2)
	for _, p := range patterns {
		norm := strings.ReplaceAll(p, `\`, "/")
		out = append(out, norm)
		if !strings.HasPrefix(norm, "/") {
			continue
		}
		loc := globMetaRe.FindStringIndex(norm)
		staticPrefix := norm
		if loc != nil {
			staticPrefix = norm[:loc[0]]
		}
		dirToResolve := filepath.Dir(staticPrefix)
		resolved, err := filepath.EvalSymlinks(dirToResolve)
		if err != nil {
			continue
		}
		resolvedNorm := strings.ReplaceAll(resolved, `\`, "/")
		if resolvedNorm == dirToResolve {
			continue
		}
		suffix := norm[len(dirToResolve):]
		out = append(out, resolvedNorm+suffix)
	}
	return out
}

// normalizePathForComparison lower-cases a path on Windows (for the
// drive-letter casing problem) and replaces backslashes with slashes
// everywhere. Used as a processed-set key so the same file discovered
// via symlink vs direct walk collapses to a single entry.
func normalizePathForComparison(p string) string {
	s := strings.ReplaceAll(p, `\`, "/")
	if isWindows() {
		s = strings.ToLower(s)
	}
	return s
}

// isWindows reports whether the runtime OS is Windows. Isolated in
// its own function so the loader stays portable without compile-time
// build tags cluttering logic files.
func isWindows() bool {
	return os.PathSeparator == '\\'
}
