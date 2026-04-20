package memory

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/wall-ai/ubuilding/backend/agents"
)

// setupTeamMemEnv configures env vars so GetTeamMemPath resolves
// deterministically for the duration of the test.
func setupTeamMemEnv(t *testing.T) (base, cwd string) {
	t.Helper()
	ResetMemoryBaseDirCache()
	base = filepath.Join(t.TempDir(), "base")
	cwd = t.TempDir()
	t.Setenv(EnvRemoteMemoryDir, base)
	t.Setenv(EnvCoworkMemoryPathOverride, "")
	t.Setenv(EnvEnableAutoMemory, "1")
	t.Setenv(EnvEnableTeamMemory, "1")
	return base, cwd
}

// ---------------------------------------------------------------------------
// M6.T1 · IsTeamMemoryEnabled + path accessors.
// ---------------------------------------------------------------------------

func TestIsTeamMemoryEnabled_GatedOnAuto(t *testing.T) {
	ResetMemoryBaseDirCache()
	t.Setenv(EnvRemoteMemoryDir, filepath.Join(t.TempDir(), "base"))
	t.Setenv(EnvEnableAutoMemory, "0")
	t.Setenv(EnvEnableTeamMemory, "1")
	// Auto disabled → Team disabled regardless of TeamMemoryEnabled flag.
	cfg := agents.EngineConfig{TeamMemoryEnabled: true}
	if IsTeamMemoryEnabled(cfg, NopSettingsProvider) {
		t.Error("Team must be disabled when Auto is disabled")
	}
}

func TestIsTeamMemoryEnabled_EnvOverride(t *testing.T) {
	ResetMemoryBaseDirCache()
	t.Setenv(EnvRemoteMemoryDir, filepath.Join(t.TempDir(), "base"))
	t.Setenv(EnvEnableAutoMemory, "1")
	// Env override false wins over config true.
	t.Setenv(EnvEnableTeamMemory, "false")
	cfg := agents.EngineConfig{TeamMemoryEnabled: true}
	if IsTeamMemoryEnabled(cfg, NopSettingsProvider) {
		t.Error("env override=false should beat config=true")
	}
	// Env override true wins over config false.
	t.Setenv(EnvEnableTeamMemory, "true")
	cfg.TeamMemoryEnabled = false
	if !IsTeamMemoryEnabled(cfg, NopSettingsProvider) {
		t.Error("env override=true should beat config=false")
	}
}

func TestGetTeamMemPath_Format(t *testing.T) {
	_, cwd := setupTeamMemEnv(t)
	teamDir := GetTeamMemPath(cwd, NopSettingsProvider)
	if teamDir == "" {
		t.Fatal("teamDir empty")
	}
	if !strings.HasSuffix(teamDir, string(os.PathSeparator)) {
		t.Errorf("teamDir must end with separator (prefix-attack protection); got %q", teamDir)
	}
	if !strings.Contains(teamDir, teamSubdir) {
		t.Errorf("teamDir missing %q subdir: %q", teamSubdir, teamDir)
	}

	ep := GetTeamMemEntrypoint(cwd, NopSettingsProvider)
	if filepath.Base(ep) != autoMemEntrypoint {
		t.Errorf("entrypoint base = %q; want %q", filepath.Base(ep), autoMemEntrypoint)
	}
}

// ---------------------------------------------------------------------------
// M6.T2 · sanitizeTeamMemKey — injection vectors.
// ---------------------------------------------------------------------------

func TestSanitizeTeamMemKey(t *testing.T) {
	cases := []struct {
		name string
		key  string
		ok   bool
	}{
		{"plain", "note.md", true},
		{"nested", "sub/dir/note.md", true},
		{"null-byte", "bad\x00name", false},
		{"url-encoded-traversal", "%2e%2e%2fetc/passwd", false},
		{"backslash", `a\b.md`, false},
		{"absolute-posix", "/etc/passwd", false},
		{"unicode-fullwidth", "\uFF0E\uFF0E\uFF0Fpasswd", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := sanitizeTeamMemKey(tc.key)
			if tc.ok && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if !tc.ok {
				if err == nil {
					t.Errorf("expected error for %q", tc.key)
					return
				}
				var pe *PathTraversalError
				if !errors.As(err, &pe) {
					t.Errorf("expected PathTraversalError; got %T: %v", err, err)
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// M6.T3 · ValidateTeamMemWritePath + containment.
// ---------------------------------------------------------------------------

func TestIsTeamMemPath(t *testing.T) {
	_, cwd := setupTeamMemEnv(t)
	teamDir := GetTeamMemPath(cwd, NopSettingsProvider)

	inside := filepath.Join(teamDir, "note.md")
	if !IsTeamMemPath(inside, cwd, NopSettingsProvider) {
		t.Errorf("inside path should match; got false for %q", inside)
	}

	// Prefix-attack: `<auto>/team-evil/foo.md` must NOT match because
	// teamDir ends in a separator.
	trimmed := strings.TrimRight(teamDir, string(os.PathSeparator))
	evil := trimmed + "-evil" + string(os.PathSeparator) + "foo.md"
	if IsTeamMemPath(evil, cwd, NopSettingsProvider) {
		t.Errorf("prefix attack leaked: %q accepted", evil)
	}

	// Traversal attempts inside the team dir must collapse via Clean.
	traverse := filepath.Join(teamDir, "sub", "..", "..", "outside", "x.md")
	if IsTeamMemPath(traverse, cwd, NopSettingsProvider) {
		t.Errorf("traversal leaked: %q accepted", traverse)
	}
}

func TestValidateTeamMemWritePath_Containment(t *testing.T) {
	_, cwd := setupTeamMemEnv(t)
	teamDir := GetTeamMemPath(cwd, NopSettingsProvider)

	// Create the team dir on disk so realpathDeepestExisting resolves.
	if err := os.MkdirAll(teamDir, 0o700); err != nil {
		t.Fatalf("mkdir teamDir: %v", err)
	}

	// Happy path.
	ok := filepath.Join(teamDir, "note.md")
	resolved, err := ValidateTeamMemWritePath(ok, cwd, NopSettingsProvider)
	if err != nil {
		t.Fatalf("inside path should validate: %v", err)
	}
	if resolved == "" {
		t.Error("resolved path empty")
	}

	// Null byte.
	if _, err := ValidateTeamMemWritePath("team/\x00note.md", cwd, NopSettingsProvider); err == nil {
		t.Error("null-byte path should fail validation")
	}

	// Traversal.
	outside := filepath.Join(teamDir, "..", "..", "escape.md")
	if _, err := ValidateTeamMemWritePath(outside, cwd, NopSettingsProvider); err == nil {
		t.Error("traversal path should fail validation")
	}
}

func TestValidateTeamMemKey_Valid(t *testing.T) {
	_, cwd := setupTeamMemEnv(t)
	teamDir := GetTeamMemPath(cwd, NopSettingsProvider)
	if err := os.MkdirAll(teamDir, 0o700); err != nil {
		t.Fatalf("mkdir teamDir: %v", err)
	}
	resolved, err := ValidateTeamMemKey("note.md", cwd, NopSettingsProvider)
	if err != nil {
		t.Fatalf("key should validate: %v", err)
	}
	if !strings.HasSuffix(resolved, "note.md") {
		t.Errorf("unexpected resolved path: %q", resolved)
	}
}

func TestValidateTeamMemKey_Rejections(t *testing.T) {
	_, cwd := setupTeamMemEnv(t)
	teamDir := GetTeamMemPath(cwd, NopSettingsProvider)
	if err := os.MkdirAll(teamDir, 0o700); err != nil {
		t.Fatalf("mkdir teamDir: %v", err)
	}
	bad := []string{
		"../escape.md",
		"/etc/passwd",
		`C:\Windows\System32\foo.md`,
		"foo\x00.md",
		"%2e%2e/passwd",
	}
	for _, k := range bad {
		if _, err := ValidateTeamMemKey(k, cwd, NopSettingsProvider); err == nil {
			t.Errorf("key %q should have failed", k)
		}
	}
}

// ---------------------------------------------------------------------------
// M6.T4 · Symlink escape detection (POSIX only — Windows lstat/
// symlink semantics differ and CI may not support unprivileged
// symlink creation).
// ---------------------------------------------------------------------------

func TestValidateTeamMemKey_SymlinkEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires elevated privileges on Windows CI")
	}
	_, cwd := setupTeamMemEnv(t)
	teamDir := GetTeamMemPath(cwd, NopSettingsProvider)
	if err := os.MkdirAll(teamDir, 0o700); err != nil {
		t.Fatal(err)
	}
	outsideDir := filepath.Join(filepath.Dir(strings.TrimRight(teamDir, string(os.PathSeparator))), "outside")
	if err := os.MkdirAll(outsideDir, 0o700); err != nil {
		t.Fatal(err)
	}
	// Create symlink `team/escape` → `outside/`.
	link := filepath.Join(teamDir, "escape")
	if err := os.Symlink(outsideDir, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	// Now ValidateTeamMemKey("escape/foo.md") should fail the
	// realpath containment check even though the string-level check
	// passes (the link sits inside teamDir).
	if _, err := ValidateTeamMemKey("escape/foo.md", cwd, NopSettingsProvider); err == nil {
		t.Error("symlink escape should fail validation")
	} else {
		var pe *PathTraversalError
		if !errors.As(err, &pe) {
			t.Errorf("expected PathTraversalError; got %T: %v", err, err)
		}
	}
}

// ---------------------------------------------------------------------------
// M6.T5 · Loader integration — TeamMem tier loads the entrypoint.
// ---------------------------------------------------------------------------

func TestLoader_LoadsTeamMemEntrypoint(t *testing.T) {
	_, cwd := setupTeamMemEnv(t)
	teamDir := GetTeamMemPath(cwd, NopSettingsProvider)
	if err := os.MkdirAll(teamDir, 0o700); err != nil {
		t.Fatal(err)
	}
	ep := GetTeamMemEntrypoint(cwd, NopSettingsProvider)
	if err := os.WriteFile(ep, []byte("# team memory\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Loader must pick up the TeamMem tier once the flag is on.
	files, err := GetMemoryFiles(context.Background(), LoaderConfig{
		Cwd: cwd,
		// AutoMem must also be enabled implicitly — env var sets it.
		EngineConfig: agents.EngineConfig{
			AutoMemoryEnabled: true,
			TeamMemoryEnabled: true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var hasTeam bool
	for _, f := range files {
		if f.Type == MemoryTypeTeamMem {
			hasTeam = true
			break
		}
	}
	if !hasTeam {
		t.Errorf("TeamMem tier missing from loader output: %v", collectTypes(files))
	}
}
