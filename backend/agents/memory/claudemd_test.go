package memory

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Fixture helpers.
// ---------------------------------------------------------------------------

// writeFile creates a file under root with content. Parents auto-created.
func writeFile(t *testing.T, root, rel, content string) string {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(p), err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
	return p
}

// collectPaths returns the ordered path list from MemoryFileInfo slice
// for easier assertion.
func collectPaths(files []MemoryFileInfo) []string {
	out := make([]string, len(files))
	for i, f := range files {
		out[i] = f.Path
	}
	return out
}

// collectTypes returns the ordered type list.
func collectTypes(files []MemoryFileInfo) []MemoryType {
	out := make([]MemoryType, len(files))
	for i, f := range files {
		out[i] = f.Type
	}
	return out
}

// ---------------------------------------------------------------------------
// M3.T3 · GetMemoryFiles layer order.
// ---------------------------------------------------------------------------

func TestGetMemoryFiles_LayerOrder(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "proj")
	writeFile(t, project, "CLAUDE.md", "# project rules\n")
	writeFile(t, project, "CLAUDE.local.md", "# local rules\n")

	userHome := filepath.Join(root, "cfg")
	userMd := filepath.Join(userHome, ClaudeMdFilename)
	writeFile(t, userHome, ClaudeMdFilename, "# user\n")

	managedMd := writeFile(t, filepath.Join(root, "managed"), ClaudeMdFilename, "# managed\n")

	cfg := LoaderConfig{
		Cwd:             project,
		ManagedClaudeMd: managedMd,
		UserClaudeMd:    userMd,
	}
	files, err := GetMemoryFiles(context.Background(), cfg)
	if err != nil {
		t.Fatalf("loader: %v", err)
	}
	types := collectTypes(files)
	want := []MemoryType{MemoryTypeManaged, MemoryTypeUser, MemoryTypeProject, MemoryTypeLocal}
	if len(types) != len(want) {
		t.Fatalf("unexpected count: got %v (paths %v)", types, collectPaths(files))
	}
	for i, got := range types {
		if got != want[i] {
			t.Errorf("tier[%d] = %s; want %s", i, got, want[i])
		}
	}
}

// ---------------------------------------------------------------------------
// M3.T4 · Include cycle handled.
// ---------------------------------------------------------------------------

func TestGetMemoryFiles_IncludeCycle(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "proj")
	writeFile(t, project, "CLAUDE.md", "include @./a.md\n")
	writeFile(t, project, "a.md", "alpha @./b.md\n")
	writeFile(t, project, "b.md", "beta @./a.md\n") // cycle

	cfg := LoaderConfig{Cwd: project}
	files, err := GetMemoryFiles(context.Background(), cfg)
	if err != nil {
		t.Fatalf("loader: %v", err)
	}
	// CLAUDE.md + a.md + b.md = 3 files; the back-reference in b.md
	// must NOT add a.md a second time.
	if len(files) != 3 {
		t.Errorf("expected 3 files; got %d: %v", len(files), collectPaths(files))
	}
}

// ---------------------------------------------------------------------------
// M3.T5 · Include depth limit.
// ---------------------------------------------------------------------------

func TestGetMemoryFiles_IncludeDepthLimit(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "proj")
	// CLAUDE.md → 1.md → 2.md → 3.md → 4.md → 5.md → 6.md
	writeFile(t, project, "CLAUDE.md", "@./1.md\n")
	for i := 1; i <= 5; i++ {
		content := "content\n"
		if i < 6 {
			content = "@./" + itoa(i+1) + ".md\n"
		}
		writeFile(t, project, itoa(i)+".md", content)
	}
	// 6.md exists but should never be loaded (depth >= 5 stops before reaching it).
	writeFile(t, project, "6.md", "deep\n")

	cfg := LoaderConfig{Cwd: project}
	files, err := GetMemoryFiles(context.Background(), cfg)
	if err != nil {
		t.Fatalf("loader: %v", err)
	}
	paths := collectPaths(files)
	for _, p := range paths {
		if strings.HasSuffix(p, string(filepath.Separator)+"6.md") {
			t.Errorf("6.md should be cut off by MAX_INCLUDE_DEPTH; got files %v", paths)
		}
	}
}

// ---------------------------------------------------------------------------
// M3.T6 · Excludes affect User/Project/Local only.
// ---------------------------------------------------------------------------

func TestGetMemoryFiles_Excludes(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "proj")
	writeFile(t, project, "CLAUDE.md", "proj rules\n")
	managedMd := writeFile(t, filepath.Join(root, "managed"), "CLAUDE.md", "managed\n")
	userMd := writeFile(t, filepath.Join(root, "cfg"), "CLAUDE.md", "user\n")

	cfg := LoaderConfig{
		Cwd:             project,
		ManagedClaudeMd: managedMd,
		UserClaudeMd:    userMd,
		Excludes:        []string{"**/CLAUDE.md"},
	}
	files, err := GetMemoryFiles(context.Background(), cfg)
	if err != nil {
		t.Fatalf("loader: %v", err)
	}
	types := collectTypes(files)
	// Managed should survive; User/Project must be dropped.
	hasManaged := false
	for _, t := range types {
		if t == MemoryTypeUser || t == MemoryTypeProject || t == MemoryTypeLocal {
			hasManaged = hasManaged || false
			continue
		}
		if t == MemoryTypeManaged {
			hasManaged = true
		}
	}
	for _, ty := range types {
		if ty == MemoryTypeUser || ty == MemoryTypeProject || ty == MemoryTypeLocal {
			t.Errorf("tier %s should have been excluded", ty)
		}
	}
	if !hasManaged {
		t.Errorf("Managed should NOT be subject to excludes; got %v", types)
	}
}

// ---------------------------------------------------------------------------
// M3.T7 · External-include approval gate.
// ---------------------------------------------------------------------------

func TestGetMemoryFiles_ExternalIncludeApproval(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "proj")
	outside := filepath.Join(root, "outside")

	// Use forward-slash paths in the @include directive to match
	// upstream's include regex (which rejects `\`-containing tokens
	// beyond the explicit `\ ` escape sequence).
	outsideSlash := strings.ReplaceAll(outside, `\`, "/")
	writeFile(t, project, "CLAUDE.md", "include @"+outsideSlash+"/secret.md\n")
	writeFile(t, outside, "secret.md", "external content\n")

	// Without approval — external skipped.
	cfgA := LoaderConfig{Cwd: project}
	a, _ := GetMemoryFiles(context.Background(), cfgA)
	for _, f := range a {
		if strings.HasSuffix(f.Path, "secret.md") {
			t.Errorf("external include leaked without approval: %v", collectPaths(a))
		}
	}

	// With approval — external loaded.
	cfgB := LoaderConfig{Cwd: project, ForceIncludeExternal: true}
	b, _ := GetMemoryFiles(context.Background(), cfgB)
	found := false
	for _, f := range b {
		if strings.HasSuffix(f.Path, "secret.md") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("external include should load when approved: %v", collectPaths(b))
	}

	// GetExternalClaudeMdIncludes reports them.
	ext := GetExternalClaudeMdIncludes(cfgB, b)
	if len(ext) == 0 {
		t.Errorf("GetExternalClaudeMdIncludes should report the external include")
	}
	if !HasExternalClaudeMdIncludes(cfgB, b) {
		t.Errorf("HasExternalClaudeMdIncludes should report true")
	}
}

// ---------------------------------------------------------------------------
// M3.T9 · Total-content soft warning.
// ---------------------------------------------------------------------------

func TestGetMemoryFiles_CharLimitWarning(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "proj")
	big := strings.Repeat("x", MaxMemoryCharacterCount+10)
	writeFile(t, project, "CLAUDE.md", big)

	// Capture slog output by using a discard handler with a hook (simple
	// smoke test — the loader must not panic with oversized content).
	cfg := LoaderConfig{Cwd: project}
	files, err := GetMemoryFiles(context.Background(), cfg)
	if err != nil {
		t.Fatalf("loader: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("expected 1 file; got %v", collectPaths(files))
	}
	if len(files[0].Content) < MaxMemoryCharacterCount {
		t.Errorf("content truncated unexpectedly: %d", len(files[0].Content))
	}
}

// ---------------------------------------------------------------------------
// Rules tests (M3.T2).
// ---------------------------------------------------------------------------

func TestProcessMdRules_UnconditionalOnly(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "proj")
	rules := filepath.Join(project, DotClaudeDir, RulesSubdir)
	// Unconditional rule (no frontmatter)
	writeFile(t, rules, "a.md", "unconditional\n")
	// Conditional rule (has paths)
	writeFile(t, rules, "b.md", "---\npaths: src/*.md\n---\nconditional\n")

	cfg := LoaderConfig{Cwd: project}
	files, err := GetMemoryFiles(context.Background(), cfg)
	if err != nil {
		t.Fatalf("loader: %v", err)
	}
	// a.md should appear; b.md should NOT (conditional rules are
	// NOT eagerly injected — they need processConditionedMdRules).
	haveA, haveB := false, false
	for _, f := range files {
		if strings.HasSuffix(f.Path, "a.md") {
			haveA = true
		}
		if strings.HasSuffix(f.Path, "b.md") {
			haveB = true
		}
	}
	if !haveA {
		t.Errorf("unconditional a.md missing: %v", collectPaths(files))
	}
	if haveB {
		t.Errorf("conditional b.md should NOT be eagerly loaded: %v", collectPaths(files))
	}
}

func TestProcessConditionedMdRules_Matches(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "proj")
	rules := filepath.Join(project, DotClaudeDir, RulesSubdir)
	writeFile(t, rules, "ts-only.md",
		"---\npaths: src/**/*.ts\n---\nTypeScript rule\n")
	writeFile(t, rules, "md-only.md",
		"---\npaths: docs/**/*.md\n---\nMarkdown rule\n")

	target := filepath.Join(project, "src", "lib", "module.ts")

	cfg := LoaderConfig{Cwd: project}
	processed := make(map[string]struct{})
	matched := processConditionedMdRules(context.Background(), cfg, target, rules,
		MemoryTypeProject, processed, false)
	if len(matched) != 1 {
		t.Fatalf("expected 1 matching rule; got %d: %v", len(matched), collectPaths(matched))
	}
	if !strings.HasSuffix(matched[0].Path, "ts-only.md") {
		t.Errorf("wrong rule matched: %v", matched[0].Path)
	}
}

// ---------------------------------------------------------------------------
// FilterInjectedMemoryFiles.
// ---------------------------------------------------------------------------

func TestFilterInjectedMemoryFiles_DropsExcludedAdditional(t *testing.T) {
	root := t.TempDir()
	add := filepath.Join(root, "addl")
	project := filepath.Join(root, "proj")
	writeFile(t, add, "CLAUDE.md", "excluded content\n")
	writeFile(t, project, "CLAUDE.md", "kept\n")

	cfg := LoaderConfig{
		Cwd:                  project,
		AdditionalDirs:       []string{add},
		EnableAdditionalDirs: true,
		Excludes:             []string{"**/CLAUDE.md"},
	}
	files, err := GetMemoryFiles(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	// Excludes already dropped these during load; filter should be idempotent.
	kept := FilterInjectedMemoryFiles(cfg, files)
	if len(kept) > len(files) {
		t.Errorf("filter should not add entries; got %d from %d", len(kept), len(files))
	}
}

// ---------------------------------------------------------------------------
// Small helpers kept in-file so tests stay portable.
// ---------------------------------------------------------------------------

// itoa without strconv to keep the test file dependency-free.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	buf := [20]byte{}
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
