package memory

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/wall-ai/ubuilding/backend/agents"
)

// ---------------------------------------------------------------------------
// M3.I7 · processMemoryFile + M3.I10-I12 · GetMemoryFiles + helpers.
//
// The loader entry point is GetMemoryFiles. It walks the four
// CLAUDE.md tiers (Managed / User / Project / Local) plus, if
// enabled, AutoMem and TeamMem, producing an ordered []MemoryFileInfo
// suitable for rendering via BuildUserContextClaudeMd (M8.I1).
// ---------------------------------------------------------------------------

// Loader-facing constants.
const (
	// MaxIncludeDepth caps @include recursion to keep pathological
	// configurations from blowing the stack. Matches upstream TS.
	MaxIncludeDepth = 5

	// MaxMemoryCharacterCount is the soft-warning threshold upstream
	// uses to flag oversized CLAUDE.md sets. The loader logs via
	// slog.Warn when the combined content length exceeds this.
	MaxMemoryCharacterCount = 40000

	// ClaudeMdFilename is the primary project / additional-dir
	// instruction filename.
	ClaudeMdFilename = "CLAUDE.md"

	// ClaudeLocalMdFilename is the .gitignored per-repo override.
	ClaudeLocalMdFilename = "CLAUDE.local.md"

	// DotClaudeDir is the in-tree config directory.
	DotClaudeDir = ".claude"

	// RulesSubdir is where conditional / ungated rule files live
	// under DotClaudeDir.
	RulesSubdir = "rules"
)

// LoaderConfig is the input to GetMemoryFiles. Sensible defaults are
// resolved when fields are left at their zero value:
//
//   - Cwd defaults to os.Getwd().
//   - UserConfigHome defaults to GetMemoryBaseDir() so the user tier
//     lives at `<base>/CLAUDE.md` and `<base>/rules/`.
//   - ManagedClaudeMd / ManagedRulesDir default to "" (no managed
//     tier). Hosts that deploy MDM policies should set both.
//
// Every boolean defaults to the "more permissive" value expected by
// upstream TS, EXCEPT AutoMem/TeamMem which are gated on the matching
// EngineConfig flags so existing hosts see no behaviour change.
type LoaderConfig struct {
	// Cwd is the original working directory — the walk starts from
	// cwd and climbs to the filesystem root while collecting Project
	// and Local tier files.
	Cwd string

	// AdditionalDirs mirrors `--add-dir`. When EnableAdditionalDirs
	// is true, the loader walks each path identically to Cwd but only
	// at the leaf level (no upward walk).
	AdditionalDirs []string

	// EnableAdditionalDirs gates the AdditionalDirs walk — upstream
	// requires the `CLAUDE_CODE_ADDITIONAL_DIRECTORIES_CLAUDE_MD` env
	// var; hosts can set this directly for tests.
	EnableAdditionalDirs bool

	// Settings plugs the settings.json store; nil uses NopSettingsProvider.
	Settings SettingsProvider

	// Excludes is the claudeMdExcludes pattern list (typically sourced
	// from settings.json). Applied only to User/Project/Local tiers.
	Excludes []string

	// EngineConfig is consulted for AutoMemoryEnabled / TeamMemoryEnabled.
	// Hosts that do not have an EngineConfig on hand may leave this
	// at zero — auto/team tiers will then stay off.
	EngineConfig agents.EngineConfig

	// ManagedClaudeMd and ManagedRulesDir are the IDE/MDM-pushed
	// instruction paths. Leave empty to skip the Managed tier.
	ManagedClaudeMd string
	ManagedRulesDir string

	// UserConfigHome overrides where user-tier files are discovered.
	// Empty → GetMemoryBaseDir().
	UserConfigHome string

	// UserClaudeMd / UserRulesDir override the user tier entirely.
	// When set, UserConfigHome is ignored for that file.
	UserClaudeMd string
	UserRulesDir string

	// ForceIncludeExternal allows `@~/path` + `@/absolute` includes
	// that fall outside Cwd. In production this is toggled by the
	// user's "approve external includes" prompt; tests set it true
	// for convenience.
	ForceIncludeExternal bool

	// ExternalApproved mirrors hasClaudeMdExternalIncludesApproved
	// from the project config — merged with ForceIncludeExternal.
	ExternalApproved bool

	// DisableUserTier / DisableProjectTier / DisableLocalTier allow
	// a host to opt out of individual tiers (mirrors TS
	// isSettingSourceEnabled). Default zero value loads every tier.
	DisableUserTier    bool
	DisableProjectTier bool
	DisableLocalTier   bool

	// Logger lets the loader report soft warnings (oversized file
	// set, permission errors). Nil uses slog.Default().
	Logger *slog.Logger
}

func (c *LoaderConfig) logger() *slog.Logger {
	if c.Logger != nil {
		return c.Logger
	}
	return slog.Default()
}

// ---------------------------------------------------------------------------
// M3.I10 · GetMemoryFiles — top-level entry point.
// ---------------------------------------------------------------------------

// GetMemoryFiles walks the full CLAUDE.md hierarchy, resolves
// @includes, applies excludes, and returns the ordered list of
// memory files ready for prompt injection. The first element has
// the LOWEST priority (Managed) and the last has the highest
// (TeamMem, then AutoMem, then Local, then Project, then User,
// then Managed — upstream orders files such that more recent tiers
// render later in the prompt where the model pays more attention).
//
// The function is read-only: it performs no writes to disk.
func GetMemoryFiles(ctx context.Context, cfg LoaderConfig) ([]MemoryFileInfo, error) {
	if cfg.Cwd == "" {
		if wd, err := os.Getwd(); err == nil {
			cfg.Cwd = wd
		}
	}
	if cfg.Settings == nil {
		cfg.Settings = NopSettingsProvider
	}
	includeExternal := cfg.ForceIncludeExternal || cfg.ExternalApproved

	processed := make(map[string]struct{})
	result := make([]MemoryFileInfo, 0, 16)

	// Managed tier (no excludes, no external-include gating).
	if cfg.ManagedClaudeMd != "" {
		files := processMemoryFile(ctx, cfg, cfg.ManagedClaudeMd, MemoryTypeManaged,
			processed, true /*includeExternal*/, 0, "")
		result = append(result, files...)
	}
	if cfg.ManagedRulesDir != "" {
		result = append(result, processMdRules(ctx, cfg, cfg.ManagedRulesDir,
			MemoryTypeManaged, processed, true, false /*conditionalRule*/, nil)...)
	}

	// User tier.
	if !cfg.DisableUserTier {
		userMd, userRules := resolveUserPaths(cfg)
		if userMd != "" {
			result = append(result, processMemoryFile(ctx, cfg, userMd, MemoryTypeUser,
				processed, true /*user can always include external*/, 0, "")...)
		}
		if userRules != "" {
			result = append(result, processMdRules(ctx, cfg, userRules,
				MemoryTypeUser, processed, true, false, nil)...)
		}
	}

	// Project + Local walk from root → cwd.
	dirs := ascendingDirs(cfg.Cwd)
	for _, dir := range dirs {
		if !cfg.DisableProjectTier {
			result = append(result,
				processMemoryFile(ctx, cfg, filepath.Join(dir, ClaudeMdFilename),
					MemoryTypeProject, processed, includeExternal, 0, "")...)
			result = append(result,
				processMemoryFile(ctx, cfg, filepath.Join(dir, DotClaudeDir, ClaudeMdFilename),
					MemoryTypeProject, processed, includeExternal, 0, "")...)
			rulesDir := filepath.Join(dir, DotClaudeDir, RulesSubdir)
			result = append(result, processMdRules(ctx, cfg, rulesDir,
				MemoryTypeProject, processed, includeExternal, false, nil)...)
		}
		if !cfg.DisableLocalTier {
			result = append(result,
				processMemoryFile(ctx, cfg, filepath.Join(dir, ClaudeLocalMdFilename),
					MemoryTypeLocal, processed, includeExternal, 0, "")...)
		}
	}

	// --add-dir equivalents.
	if cfg.EnableAdditionalDirs {
		for _, d := range cfg.AdditionalDirs {
			result = append(result,
				processMemoryFile(ctx, cfg, filepath.Join(d, ClaudeMdFilename),
					MemoryTypeProject, processed, includeExternal, 0, "")...)
			result = append(result,
				processMemoryFile(ctx, cfg, filepath.Join(d, DotClaudeDir, ClaudeMdFilename),
					MemoryTypeProject, processed, includeExternal, 0, "")...)
			rulesDir := filepath.Join(d, DotClaudeDir, RulesSubdir)
			result = append(result, processMdRules(ctx, cfg, rulesDir,
				MemoryTypeProject, processed, includeExternal, false, nil)...)
		}
	}

	// Auto-memory entrypoint.
	if IsAutoMemoryEnabled(cfg.EngineConfig, cfg.Settings) {
		if ep := GetAutoMemEntrypoint(cfg.Cwd, cfg.Settings); ep != "" {
			files := processMemoryFile(ctx, cfg, ep, MemoryTypeAutoMem,
				processed, true, 0, "")
			result = append(result, files...)
		}
	}

	// Team memory entrypoint — resolved via M6 team_paths. Gated on
	// IsTeamMemoryEnabled so hosts that do not run the team-memory
	// subsystem incur no I/O.
	if IsTeamMemoryEnabled(cfg.EngineConfig, cfg.Settings) {
		if ep := GetTeamMemEntrypoint(cfg.Cwd, cfg.Settings); ep != "" {
			files := processMemoryFile(ctx, cfg, ep, MemoryTypeTeamMem,
				processed, true, 0, "")
			result = append(result, files...)
		}
	}

	// Soft warning when aggregate content exceeds the recommended cap.
	total := 0
	for _, f := range result {
		total += len(f.Content)
	}
	if total > MaxMemoryCharacterCount {
		cfg.logger().Warn("memory: total content exceeds recommended cap",
			"total_bytes", total, "cap", MaxMemoryCharacterCount,
			"file_count", len(result))
	}
	return result, nil
}

// ---------------------------------------------------------------------------
// M3.I7 · processMemoryFile — recursive read + include chase.
// ---------------------------------------------------------------------------

// processMemoryFile reads filePath (if it exists), parses it, and
// appends the main file plus any @-included files to the result
// slice. Missing files return nil silently. Callers pass the
// processed map by reference to share de-dup state across tiers.
func processMemoryFile(
	ctx context.Context,
	cfg LoaderConfig,
	filePath string,
	tier MemoryType,
	processed map[string]struct{},
	includeExternal bool,
	depth int,
	parent string,
) []MemoryFileInfo {
	if filePath == "" || depth >= MaxIncludeDepth {
		return nil
	}
	normPath := normalizePathForComparison(filePath)
	if _, ok := processed[normPath]; ok {
		return nil
	}
	if IsClaudeMdExcluded(filePath, tier, cfg.Excludes) {
		return nil
	}

	// Mark processed BEFORE reading so recursive includes that
	// reference the same file do not loop. Also mark the realpath
	// if distinct.
	processed[normPath] = struct{}{}
	if resolved, err := filepath.EvalSymlinks(filePath); err == nil && resolved != filePath {
		processed[normalizePathForComparison(resolved)] = struct{}{}
	}

	// Read.
	rawBytes, err := os.ReadFile(filePath)
	if err != nil {
		handleReadError(cfg, err, filePath)
		return nil
	}
	raw := string(rawBytes)

	info, includes := parseMemoryFileContent(raw, filePath, tier)
	if info == nil {
		return nil
	}
	if parent != "" {
		info.Parent = parent
	}
	// Skip empty content (after strip/truncate).
	if strings.TrimSpace(info.Content) == "" {
		return nil
	}

	out := make([]MemoryFileInfo, 0, 1+len(includes))
	out = append(out, *info)

	for _, inc := range includes {
		external := !pathUnderCwdOrAdditional(cfg, inc)
		if external && !includeExternal {
			continue
		}
		more := processMemoryFile(ctx, cfg, inc, tier, processed,
			includeExternal, depth+1, filePath)
		out = append(out, more...)
	}
	return out
}

// parseMemoryFileContent runs the frontmatter + strip + include-scan
// pipeline on raw content. tier controls whether MEMORY.md
// truncation kicks in. Returns (nil, nil) when the file is ignored
// entirely (e.g. binary extension).
func parseMemoryFileContent(raw, filePath string, tier MemoryType) (*MemoryFileInfo, []string) {
	ext := strings.ToLower(filepath.Ext(filePath))
	if ext != "" && !IsTextFileExtension(ext) {
		return nil, nil
	}

	parsed := ParseFrontmatter(raw)
	stripped, _ := StripHtmlComments(parsed.Content)
	final := stripped
	// Truncate entrypoints (AutoMem/TeamMem) to upstream caps. The
	// actual TruncateEntrypointContent ships with M4.I1; for now we
	// pass the content through untouched and let M4 wire truncation
	// in via a pluggable hook.
	if tier == MemoryTypeAutoMem || tier == MemoryTypeTeamMem {
		final = truncateEntrypointFn(final)
	}

	globs := SplitPathInFrontmatter(parsed.Frontmatter.Paths)
	// `**`-only globs collapse to "applies everywhere" per upstream.
	globs = stripTrailingMatchAll(globs)

	contentDiffers := final != raw
	info := &MemoryFileInfo{
		Path:                   filePath,
		Type:                   tier,
		Content:                final,
		Globs:                  globs,
		ContentDiffersFromDisk: contentDiffers,
	}
	if contentDiffers {
		info.RawContent = raw
	}

	includes := ExtractIncludePaths(final, filePath)
	return info, includes
}

// stripTrailingMatchAll drops `**` entries; if every pattern is `**`
// the result is nil so downstream treats the file as unconditional.
func stripTrailingMatchAll(globs []string) []string {
	if len(globs) == 0 {
		return nil
	}
	var out []string
	for _, g := range globs {
		if g == "" || g == "**" {
			continue
		}
		// Drop trailing /** suffix so the ignore library's "matches
		// self and descendants" rule works correctly. Mirror TS.
		g = strings.TrimSuffix(g, "/**")
		out = append(out, g)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// truncateEntrypointFn is a seam replaced by M4.I1 once
// TruncateEntrypointContent is available. Default is identity.
var truncateEntrypointFn = func(s string) string { return s }

// handleReadError mirrors TS handleMemoryFileReadError: ENOENT and
// EISDIR are silent; EACCES gets a warning; anything else bubbles
// up via slog.Debug so operators can inspect if curious.
func handleReadError(cfg LoaderConfig, err error, filePath string) {
	if errors.Is(err, fs.ErrNotExist) {
		return
	}
	var pe *os.PathError
	if errors.As(err, &pe) {
		if pe.Err == nil {
			return
		}
	}
	if errors.Is(err, fs.ErrPermission) {
		cfg.logger().Warn("memory: permission denied reading file",
			"path", filePath)
		return
	}
	cfg.logger().Debug("memory: read error", "path", filePath, "err", err)
}

// ---------------------------------------------------------------------------
// Path helpers.
// ---------------------------------------------------------------------------

// ascendingDirs returns [root, ..., cwd] so the walk order matches TS
// "reverse order of priority — latest files are highest priority".
func ascendingDirs(cwd string) []string {
	if cwd == "" {
		return nil
	}
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return []string{cwd}
	}
	var chain []string
	cur := abs
	for {
		chain = append(chain, cur)
		parent := filepath.Dir(cur)
		if parent == cur {
			break
		}
		cur = parent
	}
	// Reverse so root comes first, cwd last.
	for i, j := 0, len(chain)-1; i < j; i, j = i+1, j-1 {
		chain[i], chain[j] = chain[j], chain[i]
	}
	return chain
}

// pathUnderCwdOrAdditional returns true when filePath lives inside
// cfg.Cwd or any cfg.AdditionalDirs. External paths come from
// `@~/foo` or `@/absolute/outside/cwd` references.
func pathUnderCwdOrAdditional(cfg LoaderConfig, filePath string) bool {
	if pathUnder(filePath, cfg.Cwd) {
		return true
	}
	for _, d := range cfg.AdditionalDirs {
		if pathUnder(filePath, d) {
			return true
		}
	}
	return false
}

// pathUnder tests whether child lives under parent after cleaning.
// Both paths are compared after EvalSymlinks (best-effort — if
// resolution fails we fall back to the cleaned form).
func pathUnder(child, parent string) bool {
	if parent == "" {
		return false
	}
	p := filepath.Clean(parent)
	c := filepath.Clean(child)
	rp, err := filepath.EvalSymlinks(p)
	if err == nil {
		p = rp
	}
	rc, err := filepath.EvalSymlinks(c)
	if err == nil {
		c = rc
	}
	if !strings.HasSuffix(p, string(os.PathSeparator)) {
		p += string(os.PathSeparator)
	}
	cSep := c
	if !strings.HasSuffix(cSep, string(os.PathSeparator)) {
		cSep += string(os.PathSeparator)
	}
	return strings.HasPrefix(cSep, p)
}

// resolveUserPaths expands cfg's user-tier fields into concrete
// paths, consulting GetMemoryBaseDir when UserConfigHome is empty.
func resolveUserPaths(cfg LoaderConfig) (claudeMd, rulesDir string) {
	if cfg.UserClaudeMd != "" {
		claudeMd = cfg.UserClaudeMd
	}
	if cfg.UserRulesDir != "" {
		rulesDir = cfg.UserRulesDir
	}
	if claudeMd != "" && rulesDir != "" {
		return
	}
	home := cfg.UserConfigHome
	if home == "" {
		home = GetMemoryBaseDir()
	}
	if home == "" {
		return
	}
	if claudeMd == "" {
		claudeMd = filepath.Join(home, ClaudeMdFilename)
	}
	if rulesDir == "" {
		rulesDir = filepath.Join(home, RulesSubdir)
	}
	return
}

// ---------------------------------------------------------------------------
// M3.I11 · External include helpers.
// ---------------------------------------------------------------------------

// ExternalClaudeMdInclude describes an @-included memory file that
// lives outside the working directory — candidate for an approval
// prompt on first encounter.
type ExternalClaudeMdInclude struct {
	Path   string
	Parent string
}

// GetExternalClaudeMdIncludes mirrors upstream:
// returns every non-User file whose parent is set (i.e. it was
// @-included) and whose path is outside the working tree.
func GetExternalClaudeMdIncludes(cfg LoaderConfig, files []MemoryFileInfo) []ExternalClaudeMdInclude {
	var out []ExternalClaudeMdInclude
	for _, f := range files {
		if f.Type == MemoryTypeUser || f.Parent == "" {
			continue
		}
		if pathUnderCwdOrAdditional(cfg, f.Path) {
			continue
		}
		out = append(out, ExternalClaudeMdInclude{Path: f.Path, Parent: f.Parent})
	}
	return out
}

// HasExternalClaudeMdIncludes is a boolean shortcut.
func HasExternalClaudeMdIncludes(cfg LoaderConfig, files []MemoryFileInfo) bool {
	return len(GetExternalClaudeMdIncludes(cfg, files)) > 0
}

// ---------------------------------------------------------------------------
// M3.I12 · FilterInjectedMemoryFiles.
// ---------------------------------------------------------------------------

// FilterInjectedMemoryFiles drops entries whose Path was supplied via
// the `CLAUDE_CODE_ADDITIONAL_DIRECTORIES_CLAUDE_MD` env var AND
// matched an exclude pattern. Upstream has this as a late-stage
// sanity pass so AdditionalDirs never smuggle excluded content in.
func FilterInjectedMemoryFiles(cfg LoaderConfig, files []MemoryFileInfo) []MemoryFileInfo {
	if !cfg.EnableAdditionalDirs || len(cfg.Excludes) == 0 || len(cfg.AdditionalDirs) == 0 {
		return files
	}
	// Build a set of additional-dir prefixes.
	prefixes := make([]string, 0, len(cfg.AdditionalDirs))
	for _, d := range cfg.AdditionalDirs {
		prefixes = append(prefixes, filepath.Clean(d))
	}
	sort.Strings(prefixes)
	out := make([]MemoryFileInfo, 0, len(files))
	for _, f := range files {
		drop := false
		for _, prefix := range prefixes {
			if pathUnder(f.Path, prefix) && IsClaudeMdExcluded(f.Path, f.Type, cfg.Excludes) {
				drop = true
				break
			}
		}
		if !drop {
			out = append(out, f)
		}
	}
	return out
}
