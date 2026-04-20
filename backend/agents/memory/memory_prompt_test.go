package memory

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wall-ai/ubuilding/backend/agents"
)

// ---------------------------------------------------------------------------
// M7.T1 · BuildMemoryLines — individual mode.
// ---------------------------------------------------------------------------

func TestBuildMemoryLines_ContainsRequiredSections(t *testing.T) {
	lines := BuildMemoryLines(AutoMemDisplayName, "/tmp/mem", nil, false)
	joined := strings.Join(lines, "\n")

	must := []string{
		"# " + AutoMemDisplayName,
		"`/tmp/mem`",
		DirExistsGuidance,
		"## Types of memory",
		"## What NOT to save in memory",
		"## How to save memories",
		"## When to access memories",
		"## Before recommending from memory",
		"## Memory and other forms of persistence",
		"Step 1",
		"Step 2",
	}
	for _, s := range must {
		if !strings.Contains(joined, s) {
			t.Errorf("output missing %q", s)
		}
	}
}

func TestBuildMemoryLines_SkipIndex(t *testing.T) {
	lines := BuildMemoryLines(AutoMemDisplayName, "/tmp/mem", nil, true /*skipIndex*/)
	joined := strings.Join(lines, "\n")
	// skipIndex drops the two-step Step1/Step2 wording.
	if strings.Contains(joined, "Step 1") || strings.Contains(joined, "Step 2") {
		t.Error("skipIndex=true should omit Step 1/Step 2 wording")
	}
	// Must still carry the frontmatter example + types + guidance.
	for _, s := range []string{
		"```markdown",
		"## Types of memory",
		"## What NOT to save in memory",
	} {
		if !strings.Contains(joined, s) {
			t.Errorf("skipIndex: missing %q", s)
		}
	}
}

func TestBuildMemoryLines_ExtraGuidelinesAppended(t *testing.T) {
	lines := BuildMemoryLines(AutoMemDisplayName, "/tmp/mem",
		[]string{"- extra rule about PII"}, false)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "extra rule about PII") {
		t.Error("extra guideline missing from output")
	}
}

// ---------------------------------------------------------------------------
// M7.T2 · BuildCombinedMemoryPrompt — team mode.
// ---------------------------------------------------------------------------

func TestBuildCombinedMemoryPrompt_HasScopeSection(t *testing.T) {
	prompt := BuildCombinedMemoryPrompt("/auto", "/team", nil, false)

	must := []string{
		"# Memory",
		"`/auto`",
		"`/team`",
		DirsExistGuidance,
		"## Memory scope",
		"- private:",
		"- team:",
		"<scope>always private</scope>", // user type scope
		"<scope>usually team</scope>",   // reference type scope
		"never save API keys or user credentials", // team-only secret-rule
		"## Memory and other forms of persistence",
	}
	for _, s := range must {
		if !strings.Contains(prompt, s) {
			t.Errorf("combined prompt missing %q", s)
		}
	}
}

func TestBuildCombinedMemoryPrompt_SkipIndex(t *testing.T) {
	prompt := BuildCombinedMemoryPrompt("/auto", "/team", nil, true)
	if strings.Contains(prompt, "Step 1") {
		t.Error("skipIndex=true should omit Step 1 wording")
	}
}

func TestBuildCombinedMemoryPrompt_NoIndividualScopeBlocks(t *testing.T) {
	// Combined prompt uses TypesSectionCombined; never the individual variant.
	prompt := BuildCombinedMemoryPrompt("/a", "/b", nil, false)
	// Individual variant has no <scope> tags — combined MUST have them.
	if !strings.Contains(prompt, "<scope>") {
		t.Error("combined prompt should contain <scope> tags")
	}
}

// ---------------------------------------------------------------------------
// M7.T3 · LoadMemoryPrompt dispatcher.
// ---------------------------------------------------------------------------

func TestLoadMemoryPrompt_Disabled(t *testing.T) {
	ResetMemoryBaseDirCache()
	t.Setenv(EnvRemoteMemoryDir, filepath.Join(t.TempDir(), "base"))
	t.Setenv(EnvDisableAutoMemory, "1")
	t.Setenv(EnvEnableAutoMemory, "")
	t.Setenv(EnvEnableTeamMemory, "")

	prompt, err := LoadMemoryPrompt(context.Background(), t.TempDir(),
		agents.EngineConfig{}, NopSettingsProvider)
	if err != nil {
		t.Fatal(err)
	}
	if prompt != "" {
		t.Errorf("disabled Auto should yield empty prompt; got %d bytes", len(prompt))
	}
}

func TestLoadMemoryPrompt_IndividualMode(t *testing.T) {
	ResetMemoryBaseDirCache()
	base := filepath.Join(t.TempDir(), "base")
	t.Setenv(EnvRemoteMemoryDir, base)
	t.Setenv(EnvEnableAutoMemory, "1")
	t.Setenv(EnvEnableTeamMemory, "0")

	cwd := t.TempDir()
	prompt, err := LoadMemoryPrompt(context.Background(), cwd,
		agents.EngineConfig{}, NopSettingsProvider)
	if err != nil {
		t.Fatal(err)
	}
	if prompt == "" {
		t.Fatal("expected non-empty individual prompt")
	}
	if !strings.Contains(prompt, "# "+AutoMemDisplayName) {
		t.Errorf("expected individual heading; got: %s", prompt[:min(300, len(prompt))])
	}
	if strings.Contains(prompt, "## Memory scope") {
		t.Error("individual prompt must NOT contain team-mode scope section")
	}
}

func TestLoadMemoryPrompt_CombinedMode(t *testing.T) {
	ResetMemoryBaseDirCache()
	base := filepath.Join(t.TempDir(), "base")
	t.Setenv(EnvRemoteMemoryDir, base)
	t.Setenv(EnvEnableAutoMemory, "1")
	t.Setenv(EnvEnableTeamMemory, "1")

	cwd := t.TempDir()
	prompt, err := LoadMemoryPrompt(context.Background(), cwd,
		agents.EngineConfig{AutoMemoryEnabled: true, TeamMemoryEnabled: true},
		NopSettingsProvider)
	if err != nil {
		t.Fatal(err)
	}
	if prompt == "" {
		t.Fatal("expected combined prompt")
	}
	if !strings.Contains(prompt, "# Memory") {
		t.Errorf("expected '# Memory' heading")
	}
	if !strings.Contains(prompt, "## Memory scope") {
		t.Error("combined prompt must include scope section")
	}
}

func TestLoadMemoryPrompt_CoworkExtraGuidelines(t *testing.T) {
	ResetMemoryBaseDirCache()
	base := filepath.Join(t.TempDir(), "base")
	t.Setenv(EnvRemoteMemoryDir, base)
	t.Setenv(EnvEnableAutoMemory, "1")
	t.Setenv(EnvCoworkMemoryExtraGuidelines, "- company-specific rule")

	cwd := t.TempDir()
	prompt, err := LoadMemoryPrompt(context.Background(), cwd,
		agents.EngineConfig{}, NopSettingsProvider)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompt, "company-specific rule") {
		t.Error("extra guideline from env var missing from prompt")
	}
}

// ---------------------------------------------------------------------------
// M7.T4 · SearchingPastContextBuilder hook.
// ---------------------------------------------------------------------------

func TestBuildMemoryLines_SearchingPastContextHook(t *testing.T) {
	prev := SearchingPastContextBuilder
	SearchingPastContextBuilder = func(dir string) []string {
		return []string{"## Searching past context", "grep " + dir}
	}
	t.Cleanup(func() { SearchingPastContextBuilder = prev })

	lines := BuildMemoryLines(AutoMemDisplayName, "/mem", nil, false)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "## Searching past context") {
		t.Error("hook output not appended")
	}
	if !strings.Contains(joined, "grep /mem") {
		t.Error("hook did not receive memoryDir")
	}
}
