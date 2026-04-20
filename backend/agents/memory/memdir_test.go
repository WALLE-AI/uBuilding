package memory

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// M4.T1 · Truncate — line boundary.
// ---------------------------------------------------------------------------

func TestTruncateEntrypointContent_LineBoundary(t *testing.T) {
	// Exactly MaxEntrypointLines — no truncation.
	exact := strings.Repeat("a\n", MaxEntrypointLines)
	// Trim trailing "\n" so line count stays exactly MaxEntrypointLines.
	exact = strings.TrimRight(exact, "\n")
	got := TruncateEntrypointContent(exact)
	if got.WasLineTruncated || got.WasByteTruncated {
		t.Errorf("exact line count should NOT trigger truncation: %+v", got)
	}

	// MaxEntrypointLines + 1 — line truncation fires.
	over := exact + "\noverflow"
	got = TruncateEntrypointContent(over)
	if !got.WasLineTruncated {
		t.Errorf("expected line truncation; got %+v", got)
	}
	if !strings.Contains(got.Content, "WARNING: MEMORY.md is") {
		t.Errorf("warning block missing: %q", got.Content)
	}
	if !strings.Contains(got.Content, "lines (limit: 200)") {
		t.Errorf("reason text wrong: %q", got.Content)
	}

	// Zero lines / empty input.
	got = TruncateEntrypointContent("")
	if got.Content != "" || got.WasLineTruncated || got.WasByteTruncated {
		t.Errorf("empty input should pass through: %+v", got)
	}

	// Single line under cap.
	got = TruncateEntrypointContent("only one line")
	if got.WasLineTruncated || got.WasByteTruncated {
		t.Errorf("single line should not truncate: %+v", got)
	}
}

// ---------------------------------------------------------------------------
// M4.T2 · Truncate — byte boundary.
// ---------------------------------------------------------------------------

func TestTruncateEntrypointContent_ByteBoundary(t *testing.T) {
	// Exactly MaxEntrypointBytes (no over-cap) — no truncation.
	under := strings.Repeat("x", MaxEntrypointBytes)
	got := TruncateEntrypointContent(under)
	if got.WasLineTruncated || got.WasByteTruncated {
		t.Errorf("exact byte count must NOT trigger truncation: %+v", got)
	}

	// MaxEntrypointBytes + 1 — byte truncation fires.
	over := strings.Repeat("x", MaxEntrypointBytes+1)
	got = TruncateEntrypointContent(over)
	if !got.WasByteTruncated {
		t.Errorf("expected byte truncation")
	}
	if !strings.Contains(got.Content, "index entries are too long") {
		t.Errorf("byte-only reason missing: %q", got.Content)
	}
	// Content (minus warning) must be <= byte cap.
	// Extract the truncated prefix before the warning.
	warning := "\n\n> WARNING:"
	prefix := got.Content
	if idx := strings.Index(prefix, warning); idx >= 0 {
		prefix = prefix[:idx]
	}
	if len(prefix) > MaxEntrypointBytes {
		t.Errorf("truncated prefix exceeds cap: len=%d", len(prefix))
	}

	// Multi-byte char near boundary — byte-truncation must cut at a
	// newline, not mid-rune. This uses `日` (3 bytes) repeated.
	mb := strings.Repeat("日\n", (MaxEntrypointBytes/4)+10)
	got = TruncateEntrypointContent(mb)
	if !got.WasByteTruncated {
		t.Errorf("expected byte truncation for multi-byte content; got %+v", got)
	}
}

// ---------------------------------------------------------------------------
// M4.T2 · formatFileSize parity.
// ---------------------------------------------------------------------------

func TestFormatFileSize(t *testing.T) {
	cases := []struct {
		in   int
		want string
	}{
		{0, "0 bytes"},
		{1, "1 bytes"},
		{1023, "1023 bytes"},
		{1024, "1KB"},
		{1536, "1.5KB"},
		{1024 * 1024, "1MB"},
		{1024 * 1024 * 3, "3MB"},
	}
	for _, tc := range cases {
		got := formatFileSize(tc.in)
		if got != tc.want {
			t.Errorf("formatFileSize(%d) = %q; want %q", tc.in, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// M4.T3 · LoadAutoMemEntrypoint — missing file returns nil,nil.
// ---------------------------------------------------------------------------

func TestLoadAutoMemEntrypoint_Missing(t *testing.T) {
	ResetMemoryBaseDirCache()
	t.Setenv(EnvRemoteMemoryDir, filepath.Join(t.TempDir(), "base"))
	t.Setenv(EnvCoworkMemoryPathOverride, "")

	cwd := t.TempDir()
	info, err := LoadAutoMemEntrypoint(context.Background(), cwd, NopSettingsProvider)
	if err != nil {
		t.Fatalf("missing file should not error: %v", err)
	}
	if info != nil {
		t.Errorf("missing file should return nil; got %+v", info)
	}
}

func TestLoadAutoMemEntrypoint_Success(t *testing.T) {
	ResetMemoryBaseDirCache()
	base := filepath.Join(t.TempDir(), "base")
	t.Setenv(EnvRemoteMemoryDir, base)
	t.Setenv(EnvCoworkMemoryPathOverride, "")

	cwd := t.TempDir()
	// Create the entrypoint file manually.
	dir := GetAutoMemPath(cwd, NopSettingsProvider)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	ep := GetAutoMemEntrypoint(cwd, NopSettingsProvider)
	if err := os.WriteFile(ep, []byte("- [Note](note.md) — a note\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	info, err := LoadAutoMemEntrypoint(context.Background(), cwd, NopSettingsProvider)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if info == nil {
		t.Fatal("expected non-nil info")
	}
	if info.Type != MemoryTypeAutoMem {
		t.Errorf("type: %s; want AutoMem", info.Type)
	}
	if !strings.Contains(info.Content, "Note") {
		t.Errorf("content missing note: %q", info.Content)
	}
	// Truncation trims leading/trailing whitespace so the content
	// technically differs from disk whenever the raw file has a
	// trailing newline. RawContent must then hold the original bytes.
	if info.ContentDiffersFromDisk {
		if info.RawContent == "" {
			t.Errorf("RawContent should be set when ContentDiffersFromDisk is true")
		}
	}
}

// ---------------------------------------------------------------------------
// M4.I3 · EnsureMemoryDirExists.
// ---------------------------------------------------------------------------

func TestEnsureMemoryDirExists_Idempotent(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "mem", "dir", "deep")
	if err := EnsureMemoryDirExists(dir); err != nil {
		t.Fatalf("first call: %v", err)
	}
	// Second call must succeed.
	if err := EnsureMemoryDirExists(dir); err != nil {
		t.Errorf("second call (idempotent): %v", err)
	}
	// Directory must exist.
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		t.Errorf("dir not created: %v", err)
	}
}

func TestEnsureMemoryDirExists_Empty(t *testing.T) {
	if err := EnsureMemoryDirExists(""); err == nil {
		t.Error("empty path should error")
	}
}

// ---------------------------------------------------------------------------
// Truncation wired into the loader (integration).
// ---------------------------------------------------------------------------

func TestLoaderAppliesEntrypointTruncation(t *testing.T) {
	ResetMemoryBaseDirCache()
	base := filepath.Join(t.TempDir(), "base")
	t.Setenv(EnvRemoteMemoryDir, base)
	t.Setenv(EnvCoworkMemoryPathOverride, "")
	t.Setenv(EnvEnableAutoMemory, "1")

	cwd := t.TempDir()
	dir := GetAutoMemPath(cwd, NopSettingsProvider)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	ep := GetAutoMemEntrypoint(cwd, NopSettingsProvider)
	// Write >200 lines so truncation fires.
	bigContent := strings.Repeat("- line\n", MaxEntrypointLines+5)
	if err := os.WriteFile(ep, []byte(bigContent), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	files, err := GetMemoryFiles(context.Background(), LoaderConfig{Cwd: cwd})
	if err != nil {
		t.Fatalf("loader: %v", err)
	}
	var auto *MemoryFileInfo
	for i := range files {
		if files[i].Type == MemoryTypeAutoMem {
			auto = &files[i]
			break
		}
	}
	if auto == nil {
		t.Fatalf("AutoMem not loaded: %+v", files)
	}
	if !strings.Contains(auto.Content, "WARNING: MEMORY.md is") {
		t.Errorf("truncation warning missing: %q", auto.Content)
	}
	if !auto.ContentDiffersFromDisk {
		t.Errorf("ContentDiffersFromDisk should be true after truncation")
	}
}
