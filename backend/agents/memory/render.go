package memory

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

// ---------------------------------------------------------------------------
// M8 · Render helpers used by the prompt / context layer.
//
//   - M8.I1 · BuildUserContextClaudeMd (mirrors TS getClaudeMds)
//   - M8.I1 · GetLargeMemoryFiles    (diagnostics)
//
// The rendered content is threaded into UserContext.ClaudeMd upstream
// of the system prompt so each CLAUDE.md tier gets a labelled fence
// that the model can cite. Order inside the rendered block follows
// tier priority (Managed → User → Project → Local → AutoMem → TeamMem)
// matching upstream's getClaudeMds with the defaults.
// ---------------------------------------------------------------------------

// ClaudeMdRenderOptions controls which tiers are rendered and how
// paths are labelled. Unset fields use sensible defaults:
//
//   - FilterType=nil accepts every tier.
//   - RelativeTo empty leaves paths absolute.
//   - SkipProjectLevel=false keeps Project + Local tiers.
type ClaudeMdRenderOptions struct {
	// FilterType is consulted per file; returning false drops the
	// entry from the rendered string. Mirrors TS's optional filter
	// argument to getClaudeMds.
	FilterType func(MemoryType) bool

	// RelativeTo, when set, causes path labels to be rendered
	// relative to this directory (typically the engine cwd). Files
	// outside this root fall back to their absolute path.
	RelativeTo string

	// SkipProjectLevel mirrors the TS `tengu_moth_copse` skip-index
	// feature flag — when true, Project + Local tiers are dropped.
	SkipProjectLevel bool
}

// BuildUserContextClaudeMd renders the loaded memory files into a
// single string suitable for injecting into UserContext.ClaudeMd.
// Empty output is returned as "" (not a single fence) so the
// downstream builder can omit the claudeMd entry entirely.
//
// Output format per file:
//
//	Contents of <label> (<tier>):
//
//	<content>
//
// Files are separated by blank lines — upstream uses no extra
// demarcation and relies on the header line for per-file attribution.
func BuildUserContextClaudeMd(files []MemoryFileInfo, opts ClaudeMdRenderOptions) string {
	if len(files) == 0 {
		return ""
	}
	ordered := orderByTier(files)
	var b strings.Builder
	first := true
	for _, f := range ordered {
		if opts.FilterType != nil && !opts.FilterType(f.Type) {
			continue
		}
		if opts.SkipProjectLevel && (f.Type == MemoryTypeProject || f.Type == MemoryTypeLocal) {
			continue
		}
		content := strings.TrimRight(f.Content, "\n")
		if content == "" {
			continue
		}
		if !first {
			b.WriteString("\n\n")
		}
		first = false
		label := displayPath(f.Path, opts.RelativeTo)
		fmt.Fprintf(&b, "Contents of %s (%s):\n\n%s", label, f.Type, content)
	}
	return b.String()
}

// orderByTier returns files sorted so tiers are rendered in a
// deterministic priority order. Within a tier the original order is
// preserved (loader emits them in walk order).
func orderByTier(files []MemoryFileInfo) []MemoryFileInfo {
	priority := map[MemoryType]int{
		MemoryTypeManaged: 0,
		MemoryTypeUser:    1,
		MemoryTypeProject: 2,
		MemoryTypeLocal:   3,
		MemoryTypeAutoMem: 4,
		MemoryTypeTeamMem: 5,
	}
	idx := make([]int, len(files))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool {
		return priority[files[idx[a]].Type] < priority[files[idx[b]].Type]
	})
	out := make([]MemoryFileInfo, len(files))
	for n, i := range idx {
		out[n] = files[i]
	}
	return out
}

// displayPath returns a cwd-relative path when feasible, else the
// absolute path. Keeps rendered prompts portable across machines.
func displayPath(path, relativeTo string) string {
	if relativeTo == "" {
		return path
	}
	if rel, err := filepath.Rel(relativeTo, path); err == nil &&
		!strings.HasPrefix(rel, "..") &&
		!filepath.IsAbs(rel) {
		return rel
	}
	return path
}

// GetLargeMemoryFiles returns the subset of files whose Content
// length exceeds MaxMemoryCharacterCount. Used by the /context
// diagnostics and status-bar notices upstream.
func GetLargeMemoryFiles(files []MemoryFileInfo) []MemoryFileInfo {
	var out []MemoryFileInfo
	for _, f := range files {
		if len(f.Content) > MaxMemoryCharacterCount {
			out = append(out, f)
		}
	}
	return out
}
