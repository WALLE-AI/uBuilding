package memory

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

// ---------------------------------------------------------------------------
// M3.I8 · processMdRules — walk `.claude/rules/**/*.md`.
// M3.I9 · processConditionedMdRules — filter by frontmatter `paths:`.
// ---------------------------------------------------------------------------

// processMdRules reads every `*.md` file under rulesDir (recursively)
// and returns MemoryFileInfo entries appropriate for the given
// conditional mode.
//
//   - conditionalRule=false: include files WITHOUT a frontmatter `paths:`
//     entry (unconditional rules) in alphabetical order of filename.
//   - conditionalRule=true:  include files WITH  a frontmatter `paths:`
//     entry. The caller post-filters against the target path via
//     processConditionedMdRules.
//
// visitedDirs guards against symlink cycles; pass nil on the
// top-level call.
func processMdRules(
	ctx context.Context,
	cfg LoaderConfig,
	rulesDir string,
	tier MemoryType,
	processed map[string]struct{},
	includeExternal bool,
	conditionalRule bool,
	visitedDirs map[string]struct{},
) []MemoryFileInfo {
	if rulesDir == "" {
		return nil
	}
	if visitedDirs == nil {
		visitedDirs = make(map[string]struct{})
	}
	normDir := normalizePathForComparison(rulesDir)
	if _, seen := visitedDirs[normDir]; seen {
		return nil
	}
	resolvedDir := rulesDir
	if r, err := filepath.EvalSymlinks(rulesDir); err == nil {
		resolvedDir = r
		visitedDirs[normalizePathForComparison(r)] = struct{}{}
	}
	visitedDirs[normDir] = struct{}{}

	entries, err := os.ReadDir(resolvedDir)
	if err != nil {
		// ENOENT / EACCES / ENOTDIR all silently return [] per upstream.
		return nil
	}
	// Sort for deterministic ordering (upstream relies on readdir
	// ordering which is driver-dependent).
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})

	var out []MemoryFileInfo
	for _, entry := range entries {
		name := entry.Name()
		entryPath := filepath.Join(resolvedDir, name)
		if entry.IsDir() {
			out = append(out, processMdRules(ctx, cfg, entryPath, tier,
				processed, includeExternal, conditionalRule, visitedDirs)...)
			continue
		}
		if !strings.HasSuffix(strings.ToLower(name), ".md") {
			continue
		}
		files := processMemoryFile(ctx, cfg, entryPath, tier, processed,
			includeExternal, 0, "")
		for _, f := range files {
			if conditionalRule {
				if len(f.Globs) > 0 {
					out = append(out, f)
				}
			} else {
				if len(f.Globs) == 0 {
					out = append(out, f)
				}
			}
		}
	}
	return out
}

// processConditionedMdRules filters the conditional rule files
// produced by processMdRules(conditionalRule=true) down to just those
// whose `paths:` globs match targetPath. Invoked on demand when a
// tool operates on a specific file and the host wants per-file
// instruction context.
//
// For Project tier the globs are relative to the directory that
// contains `.claude/rules/` (two levels above rulesDir). For Managed
// and User tiers the globs are relative to cfg.Cwd, matching upstream
// processConditionedMdRules behaviour.
func processConditionedMdRules(
	ctx context.Context,
	cfg LoaderConfig,
	targetPath string,
	rulesDir string,
	tier MemoryType,
	processed map[string]struct{},
	includeExternal bool,
) []MemoryFileInfo {
	candidates := processMdRules(ctx, cfg, rulesDir, tier, processed,
		includeExternal, true /*conditionalRule*/, nil)
	if len(candidates) == 0 {
		return nil
	}
	var baseDir string
	switch tier {
	case MemoryTypeProject:
		// rulesDir → .../.claude/rules ; baseDir → .../(parent of .claude)
		baseDir = filepath.Dir(filepath.Dir(rulesDir))
	default:
		baseDir = cfg.Cwd
	}
	rel, ok := relOrEmpty(baseDir, targetPath)
	if !ok {
		return nil
	}
	// doublestar expects forward-slash separated paths.
	relNorm := strings.ReplaceAll(rel, `\`, "/")
	if relNorm == "" || strings.HasPrefix(relNorm, "..") {
		return nil
	}
	var out []MemoryFileInfo
	for _, f := range candidates {
		for _, g := range f.Globs {
			pat := strings.ReplaceAll(g, `\`, "/")
			if matched, _ := doublestar.PathMatch(pat, relNorm); matched {
				out = append(out, f)
				break
			}
		}
	}
	return out
}

// relOrEmpty returns the baseDir → target relative path, and false
// when target is outside baseDir (upstream rejects `..` paths).
func relOrEmpty(baseDir, target string) (string, bool) {
	if baseDir == "" || target == "" {
		return "", false
	}
	rel, err := filepath.Rel(baseDir, target)
	if err != nil {
		return "", false
	}
	if strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return "", false
	}
	return rel, true
}
