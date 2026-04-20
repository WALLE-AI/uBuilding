package memory

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/wall-ai/ubuilding/backend/agents"
)

// ---------------------------------------------------------------------------
// M2.T1 · GetMemoryBaseDir env priority + caching
// ---------------------------------------------------------------------------

func TestGetMemoryBaseDir_EnvPriority(t *testing.T) {
	t.Setenv(EnvRemoteMemoryDir, "")
	ResetMemoryBaseDirCache()
	unset := GetMemoryBaseDir()
	if unset == "" {
		t.Fatal("expected non-empty default base dir (UserConfigDir fallback)")
	}
	if !strings.HasSuffix(unset, defaultConfigSub) {
		t.Errorf("expected default base to end in %q; got %q", defaultConfigSub, unset)
	}

	// Env override takes precedence.
	ResetMemoryBaseDirCache()
	t.Setenv(EnvRemoteMemoryDir, filepath.Join(t.TempDir(), "custom-base"))
	set := GetMemoryBaseDir()
	if set != os.Getenv(EnvRemoteMemoryDir) {
		t.Errorf("override failed: got %q want %q", set, os.Getenv(EnvRemoteMemoryDir))
	}

	// Empty string treated as unset.
	ResetMemoryBaseDirCache()
	t.Setenv(EnvRemoteMemoryDir, "   ")
	empty := GetMemoryBaseDir()
	if empty != unset {
		t.Errorf("whitespace env should fall through to default; got %q", empty)
	}

	// Caching: once computed, subsequent calls return the same value
	// even if the env changes, until ResetMemoryBaseDirCache is called.
	ResetMemoryBaseDirCache()
	t.Setenv(EnvRemoteMemoryDir, "/tmp/first")
	a := GetMemoryBaseDir()
	t.Setenv(EnvRemoteMemoryDir, "/tmp/second")
	b := GetMemoryBaseDir()
	if a != b {
		t.Errorf("sync.Once cache broken: %q vs %q", a, b)
	}
}

// ---------------------------------------------------------------------------
// M2.T2 · ValidateMemoryPath — 5 rejection classes + success
// ---------------------------------------------------------------------------

func TestValidateMemoryPath_Rejections(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		wantErr error
	}{
		{"empty", "", ErrPathEmpty},
		{"whitespace", "   ", ErrPathEmpty},
		{"null-byte", "/tmp/\x00foo", ErrPathNullByte},
		{"relative", "foo/bar", ErrPathRelative},
		{"relative-dot", "./foo", ErrPathRelative},
		{"unc-backslash", `\\server\share\path`, ErrPathUNC},
		{"unc-forward", "//server/share/path", ErrPathUNC},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ValidateMemoryPath(tc.in, false)
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("want err %v; got %v", tc.wantErr, err)
			}
		})
	}
}

func TestValidateMemoryPath_DriveRootRejection(t *testing.T) {
	// Bare drive root is always rejected — verified cross-platform by
	// testing the string directly. `filepath.IsAbs` recognises "C:" as
	// absolute on Windows only, so on Unix the path is rejected as
	// relative first. Both rejections are acceptable; the important
	// contract is "not allowed".
	_, err := ValidateMemoryPath("C:", false)
	if err == nil {
		t.Fatal("expected rejection for C:; got nil")
	}
	// Short-absolute-path rejection. On Unix "/a" is absolute; on
	// Windows `C:\` is the minimal absolute form and after stripping
	// the trailing separator collapses to "C:" (drive-root rejection).
	if runtime.GOOS == "windows" {
		if _, err := ValidateMemoryPath(`C:\`, false); !errors.Is(err, ErrPathDriveRoot) {
			t.Errorf("windows: want ErrPathDriveRoot; got %v", err)
		}
	} else {
		if _, err := ValidateMemoryPath("/a", false); !errors.Is(err, ErrPathTooShort) {
			t.Errorf("unix: want ErrPathTooShort; got %v", err)
		}
	}
}

func TestValidateMemoryPath_Success(t *testing.T) {
	in := t.TempDir()
	got, err := ValidateMemoryPath(in, false)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !strings.HasSuffix(got, string(os.PathSeparator)) {
		t.Errorf("expected trailing separator; got %q", got)
	}
	// Idempotent: re-validate the returned path.
	if _, err := ValidateMemoryPath(got, false); err != nil {
		t.Errorf("round-trip failed: %v", err)
	}
}

func TestValidateMemoryPath_TildeExpansion(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Skip("no home dir available in sandbox")
	}

	// Trivial remainders rejected.
	for _, in := range []string{"~", "~/", "~/.", "~/.."} {
		if _, err := ValidateMemoryPath(in, true); !errors.Is(err, ErrPathTildeTrivial) && !errors.Is(err, ErrPathRelative) {
			t.Errorf("%q: want trivial rejection; got %v", in, err)
		}
	}

	// Real expansion succeeds.
	got, err := ValidateMemoryPath("~/projects/memory", true)
	if err != nil {
		t.Fatalf("expansion failed: %v", err)
	}
	if !strings.HasPrefix(got, home) {
		t.Errorf("expected prefix %q; got %q", home, got)
	}
}

// ---------------------------------------------------------------------------
// M2.T3 · SanitizePathKey
// ---------------------------------------------------------------------------

func TestSanitizePathKey(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", "_"},
		{"/", "_"},
		{"/Users/a/proj", "_Users_a_proj"},
		{`C:\Users\a\proj`, "C__Users_a_proj"},
		{"with space/and-dash", "with_space_and_dash"},
		{"a.b.c", "a_b_c"},
		{"plugin:name:server", "plugin_name_server"},
	}
	for _, tc := range cases {
		got := SanitizePathKey(tc.in)
		if got != tc.want {
			t.Errorf("SanitizePathKey(%q) = %q; want %q", tc.in, got, tc.want)
		}
	}
}

func TestSanitizePathKey_LongPathTruncatesWithHash(t *testing.T) {
	long := strings.Repeat("a/", 200) + "end" // plenty over 200 bytes
	got := SanitizePathKey(long)
	if len(got) <= maxSanitizedLength {
		t.Fatalf("long path not truncated: len=%d", len(got))
	}
	if !strings.Contains(got[maxSanitizedLength:], "_") {
		t.Errorf("expected hash suffix separator; got %q", got[maxSanitizedLength:])
	}
	// Same input → same hash.
	got2 := SanitizePathKey(long)
	if got != got2 {
		t.Errorf("hash not deterministic: %q vs %q", got, got2)
	}
}

// ---------------------------------------------------------------------------
// M2.T4 · GetAutoMemPath priority
// ---------------------------------------------------------------------------

type fakeSettings struct {
	enabled        bool
	enabledPresent bool
	dir            string
}

func (f fakeSettings) AutoMemoryEnabled() (bool, bool) { return f.enabled, f.enabledPresent }
func (f fakeSettings) AutoMemoryDirectory() string     { return f.dir }

func TestGetAutoMemPath_Priority(t *testing.T) {
	// Isolate env + cache.
	ResetMemoryBaseDirCache()
	t.Setenv(EnvRemoteMemoryDir, filepath.Join(t.TempDir(), "base"))
	t.Setenv(EnvCoworkMemoryPathOverride, "")

	cwd := t.TempDir()

	// 3. Default: <base>/projects/<SanitizePathKey(cwd)>/memory/
	defaultPath := GetAutoMemPath(cwd, NopSettingsProvider)
	if defaultPath == "" {
		t.Fatal("default path empty")
	}
	if !strings.HasPrefix(defaultPath, os.Getenv(EnvRemoteMemoryDir)) {
		t.Errorf("default path %q not under base %q", defaultPath, os.Getenv(EnvRemoteMemoryDir))
	}
	if !strings.HasSuffix(defaultPath, string(os.PathSeparator)) {
		t.Errorf("missing trailing separator on %q", defaultPath)
	}

	// 2. settings.AutoMemoryDirectory overrides default.
	settingsDir := filepath.Join(t.TempDir(), "settings-dir")
	provider := fakeSettings{dir: settingsDir}
	viaSettings := GetAutoMemPath(cwd, provider)
	if !strings.HasPrefix(viaSettings, settingsDir) {
		t.Errorf("settings override failed: got %q want prefix %q", viaSettings, settingsDir)
	}

	// 1. Env override wins over everything.
	envOverride := filepath.Join(t.TempDir(), "env-override")
	t.Setenv(EnvCoworkMemoryPathOverride, envOverride)
	viaEnv := GetAutoMemPath(cwd, provider)
	if !strings.HasPrefix(viaEnv, envOverride) {
		t.Errorf("env override failed: got %q want prefix %q", viaEnv, envOverride)
	}

	if !HasAutoMemPathOverride() {
		t.Error("HasAutoMemPathOverride should be true with env set")
	}
}

func TestGetAutoMemPath_InvalidSettingsFallsThrough(t *testing.T) {
	ResetMemoryBaseDirCache()
	t.Setenv(EnvRemoteMemoryDir, filepath.Join(t.TempDir(), "base"))
	t.Setenv(EnvCoworkMemoryPathOverride, "")

	cwd := t.TempDir()
	// Invalid settings (relative path) → ignored, fall through to default.
	provider := fakeSettings{dir: "relative/foo"}
	got := GetAutoMemPath(cwd, provider)
	if strings.Contains(got, "relative") {
		t.Errorf("invalid settings must not be honoured: %q", got)
	}
	if !strings.HasPrefix(got, os.Getenv(EnvRemoteMemoryDir)) {
		t.Errorf("expected fallback to default base; got %q", got)
	}
}

// ---------------------------------------------------------------------------
// M2.I5 · GetAutoMemEntrypoint + GetAutoMemDailyLogPath
// ---------------------------------------------------------------------------

func TestGetAutoMemEntrypoint(t *testing.T) {
	ResetMemoryBaseDirCache()
	t.Setenv(EnvRemoteMemoryDir, filepath.Join(t.TempDir(), "base"))
	t.Setenv(EnvCoworkMemoryPathOverride, "")
	cwd := t.TempDir()
	ep := GetAutoMemEntrypoint(cwd, NopSettingsProvider)
	if filepath.Base(ep) != autoMemEntrypoint {
		t.Errorf("want basename %q; got %q", autoMemEntrypoint, filepath.Base(ep))
	}
	// entrypoint lives directly under the auto-mem dir.
	parent := filepath.Dir(ep) + string(os.PathSeparator)
	if parent != GetAutoMemPath(cwd, NopSettingsProvider) {
		t.Errorf("entrypoint parent mismatch:\n got  %q\n want %q", parent, GetAutoMemPath(cwd, NopSettingsProvider))
	}
}

func TestGetAutoMemDailyLogPath_DateOverride(t *testing.T) {
	ResetMemoryBaseDirCache()
	t.Setenv(EnvRemoteMemoryDir, filepath.Join(t.TempDir(), "base"))
	t.Setenv(EnvCoworkMemoryPathOverride, "")
	t.Setenv(EnvOverrideDate, "2025-02-07")
	cwd := t.TempDir()

	// Passing zero time triggers the env-driven override.
	got := GetAutoMemDailyLogPath(cwd, NopSettingsProvider, time.Time{})
	want := filepath.Join("logs", "2025", "02", "2025-02-07.md")
	if !strings.HasSuffix(got, want) {
		t.Errorf("want suffix %q; got %q", want, got)
	}

	// Explicit date wins over env.
	explicit := time.Date(2030, 11, 1, 12, 0, 0, 0, time.UTC)
	got = GetAutoMemDailyLogPath(cwd, NopSettingsProvider, explicit)
	want = filepath.Join("logs", "2030", "11", "2030-11-01.md")
	if !strings.HasSuffix(got, want) {
		t.Errorf("want suffix %q; got %q", want, got)
	}
}

// ---------------------------------------------------------------------------
// M2.T5 · IsAutoMemoryEnabled 5-level switch
// ---------------------------------------------------------------------------

func TestIsAutoMemoryEnabled_Priority(t *testing.T) {
	cfgOn := agents.EngineConfig{AutoMemoryEnabled: true}
	cfgOff := agents.EngineConfig{}
	settingsOn := fakeSettings{enabled: true, enabledPresent: true}
	settingsOff := fakeSettings{enabled: false, enabledPresent: true}
	settingsUnset := fakeSettings{enabledPresent: false}

	cases := []struct {
		name   string
		envEn  string
		envDis string
		cfg    agents.EngineConfig
		set    SettingsProvider
		want   bool
	}{
		{"default-off", "", "", cfgOff, nil, false},
		{"cfg-on", "", "", cfgOn, nil, true},
		{"settings-off-overrides-cfg-on", "", "", cfgOn, settingsOff, false},
		{"settings-on-overrides-cfg-off", "", "", cfgOff, settingsOn, true},
		{"settings-unset-defers-to-cfg", "", "", cfgOn, settingsUnset, true},
		{"env-enable-over-settings-off", "1", "", cfgOn, settingsOff, true},
		{"env-disable-beats-everything", "1", "1", cfgOn, settingsOn, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(EnvEnableAutoMemory, tc.envEn)
			t.Setenv(EnvDisableAutoMemory, tc.envDis)
			got := IsAutoMemoryEnabled(tc.cfg, tc.set)
			if got != tc.want {
				t.Errorf("got %v want %v", got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// FindCanonicalGitRoot — basic contract (fixture)
// ---------------------------------------------------------------------------

func TestFindCanonicalGitRoot_MainRepo(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	got, ok := FindCanonicalGitRoot(sub)
	if !ok {
		t.Fatal("expected to find git root")
	}
	if got != root {
		// Windows may canonicalise case; compare case-insensitively there.
		if runtime.GOOS == "windows" && strings.EqualFold(got, root) {
			return
		}
		t.Errorf("got %q; want %q", got, root)
	}
}

func TestFindCanonicalGitRoot_Worktree(t *testing.T) {
	if runtime.GOOS == "windows" {
		// filepath joins with backslashes; this test still works but keeps
		// assertion loose.
	}
	mainRepo := t.TempDir()
	mainGit := filepath.Join(mainRepo, ".git")
	if err := os.Mkdir(mainGit, 0o755); err != nil {
		t.Fatal(err)
	}
	worktreesDir := filepath.Join(mainGit, "worktrees", "feature-a")
	if err := os.MkdirAll(worktreesDir, 0o755); err != nil {
		t.Fatal(err)
	}

	worktree := t.TempDir()
	gitFile := filepath.Join(worktree, ".git")
	content := "gitdir: " + worktreesDir + "\n"
	if err := os.WriteFile(gitFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	got, ok := FindCanonicalGitRoot(worktree)
	if !ok {
		t.Fatal("expected to find worktree canonical root")
	}
	if got != mainRepo && !(runtime.GOOS == "windows" && strings.EqualFold(got, mainRepo)) {
		t.Errorf("got %q; want main repo %q", got, mainRepo)
	}
}

func TestFindCanonicalGitRoot_NoGit(t *testing.T) {
	if _, ok := FindCanonicalGitRoot(t.TempDir()); ok {
		t.Error("expected ok=false outside any git tree")
	}
}
