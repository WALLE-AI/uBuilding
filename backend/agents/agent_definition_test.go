package agents

import (
	"encoding/json"
	"testing"
)

func TestAgentDefinition_ZeroValueAndGuards(t *testing.T) {
	var nilDef *AgentDefinition
	if nilDef.IsBuiltIn() || nilDef.IsPlugin() || nilDef.IsCustom() {
		t.Fatal("nil AgentDefinition guards must all return false")
	}
	if got := nilDef.RenderSystemPrompt(SystemPromptCtx{}); got != "" {
		t.Fatalf("nil RenderSystemPrompt = %q; want empty", got)
	}
	if got := nilDef.ToLegacyDef(); got.Type != "" || got.Name != "" {
		t.Fatalf("nil ToLegacyDef: got %+v", got)
	}
}

func TestAgentDefinition_SourceGuards(t *testing.T) {
	cases := []struct {
		src                     AgentSource
		builtIn, plugin, custom bool
		trusted                 bool
	}{
		{AgentSourceBuiltIn, true, false, false, true},
		{AgentSourcePlugin, false, true, false, true},
		{AgentSourcePolicy, false, false, true, true},
		{AgentSourceUser, false, false, true, false},
		{AgentSourceProject, false, false, true, false},
	}
	for _, tc := range cases {
		a := &AgentDefinition{Source: tc.src}
		if a.IsBuiltIn() != tc.builtIn {
			t.Errorf("%s IsBuiltIn=%v want %v", tc.src, a.IsBuiltIn(), tc.builtIn)
		}
		if a.IsPlugin() != tc.plugin {
			t.Errorf("%s IsPlugin=%v want %v", tc.src, a.IsPlugin(), tc.plugin)
		}
		if a.IsCustom() != tc.custom {
			t.Errorf("%s IsCustom=%v want %v", tc.src, a.IsCustom(), tc.custom)
		}
		if tc.src.IsAdminTrusted() != tc.trusted {
			t.Errorf("%s IsAdminTrusted=%v want %v", tc.src, tc.src.IsAdminTrusted(), tc.trusted)
		}
	}
}

func TestAgentDefinition_RenderAndToLegacy(t *testing.T) {
	called := 0
	a := &AgentDefinition{
		AgentType: "general-purpose",
		WhenToUse: "General purpose description",
		Source:    AgentSourceBuiltIn,
		GetSystemPrompt: func(ctx SystemPromptCtx) string {
			called++
			if ctx.Model == "" {
				return "no-model"
			}
			return "model=" + ctx.Model
		},
	}
	if got := a.RenderSystemPrompt(SystemPromptCtx{Model: "claude-sonnet"}); got != "model=claude-sonnet" {
		t.Fatalf("RenderSystemPrompt: got %q", got)
	}
	if called != 1 {
		t.Fatalf("GetSystemPrompt calls = %d", called)
	}
	leg := a.ToLegacyDef()
	if leg.Name != "general-purpose" || leg.Type != "general-purpose" || leg.Description != "General purpose description" {
		t.Fatalf("ToLegacyDef: %+v", leg)
	}
}

func TestAgentDefinitions_FindAndRefresh(t *testing.T) {
	a := &AgentDefinition{AgentType: "alpha", WhenToUse: "A", Source: AgentSourceBuiltIn}
	b := &AgentDefinition{AgentType: "beta", WhenToUse: "B", Source: AgentSourceUser}
	all := &AgentDefinitions{
		ActiveAgents: []*AgentDefinition{a},
		AllAgents:    []*AgentDefinition{a, b},
	}
	if got := all.FindActive("alpha"); got != a {
		t.Fatalf("FindActive(alpha) = %v", got)
	}
	if got := all.FindActive("beta"); got != nil {
		t.Fatalf("FindActive(beta) should be nil; got %+v", got)
	}
	if got := all.FindAny("beta"); got != b {
		t.Fatalf("FindAny(beta) = %v", got)
	}
	if got := all.FindAny("gamma"); got != nil {
		t.Fatalf("FindAny(gamma) should be nil")
	}
	types := all.ActiveTypes()
	if len(types) != 1 || types[0] != "alpha" {
		t.Fatalf("ActiveTypes = %+v", types)
	}

	all.RefreshLegacy()
	if len(all.ActiveLegacy) != 1 || all.ActiveLegacy[0].Type != "alpha" {
		t.Fatalf("ActiveLegacy = %+v", all.ActiveLegacy)
	}
	if len(all.AllLegacy) != 2 || all.AllLegacy[1].Type != "beta" {
		t.Fatalf("AllLegacy = %+v", all.AllLegacy)
	}
}

// A14 fields are optional — verify they round-trip through JSON without data
// loss (documenting what a loader is expected to populate).
func TestAgentDefinition_JSONRoundTrip(t *testing.T) {
	in := AgentDefinition{
		AgentType:                          "reviewer",
		WhenToUse:                          "Review code",
		Source:                             AgentSourceProject,
		BaseDir:                            "/repo/.claude/agents",
		Filename:                           "reviewer.md",
		Tools:                              []string{"Read", "Grep"},
		DisallowedTools:                    []string{"Bash"},
		Skills:                             []string{"lint", "typecheck"},
		Model:                              "inherit",
		Effort:                             AgentEffortValue{Name: "medium"},
		MaxTurns:                           42,
		PermissionMode:                     "plan",
		Background:                         true,
		Isolation:                          AgentIsolationWorktree,
		Memory:                             AgentMemoryScopeProject,
		RequiredMcpServers:                 []string{"slack"},
		InitialPrompt:                      "Start by reading the PR diff.",
		CriticalSystemReminderExperimental: "stay under 500 words",
		OmitClaudeMd:                       true,
		MCPServers: []AgentMcpServerSpec{
			{ByName: "slack"},
			{Inline: map[string]interface{}{"slack-dev": map[string]interface{}{"type": "stdio"}}},
		},
	}
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out AgentDefinition
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	// Spot-check a subset of fields survived the round trip.
	if out.AgentType != in.AgentType || out.Memory != in.Memory || out.Isolation != in.Isolation {
		t.Fatalf("roundtrip mismatch: %+v", out)
	}
	if len(out.MCPServers) != 2 || out.MCPServers[0].ByName != "slack" {
		t.Fatalf("mcp roundtrip: %+v", out.MCPServers)
	}
	if !out.Background || !out.OmitClaudeMd {
		t.Fatal("bool fields lost")
	}
	if out.CriticalSystemReminderExperimental != in.CriticalSystemReminderExperimental {
		t.Fatal("critical reminder lost")
	}
}

// B06 · enhance now appends an Environment: section.
func TestEnhanceSystemPromptWithEnvDetails_AppendsSection(t *testing.T) {
	const base = "You are an agent."
	got := EnhanceSystemPromptWithEnvDetails(base, SystemPromptCtx{
		Model: "claude-sonnet",
		Tools: []string{"Read", "Grep"},
		Cwd:   "/tmp/work",
	})
	if !stringsContains(got, base) {
		t.Fatalf("base prompt must be preserved: %q", got)
	}
	for _, want := range []string{
		"Environment:",
		"- cwd: /tmp/work",
		"- Platform: ",
		"- Model: claude-sonnet",
		"Guidelines:",
		"absolute paths",
		"Do not use emojis",
	} {
		if !stringsContains(got, want) {
			t.Errorf("enhance output missing %q; got:\n%s", want, got)
		}
	}
}

// B06 · empty context still emits the Guidelines block.
func TestEnhanceSystemPromptWithEnvDetails_EmptyBase(t *testing.T) {
	got := EnhanceSystemPromptWithEnvDetails("", SystemPromptCtx{})
	if got == "" || !stringsContains(got, "Guidelines:") {
		t.Fatalf("expected Guidelines-only body, got: %q", got)
	}
}

// B06 · non-interactive flag flips the wording.
func TestEnhanceSystemPromptWithEnvDetails_NonInteractive(t *testing.T) {
	got := EnhanceSystemPromptWithEnvDetails("", SystemPromptCtx{IsNonInteractive: true})
	if !stringsContains(got, "Interactive: no") {
		t.Fatalf("non-interactive flag not reflected: %q", got)
	}
}

func stringsContains(s, substr string) bool {
	return len(s) >= len(substr) && indexOf(s, substr) >= 0
}

// indexOf is a tiny substring check to avoid importing strings at test
// scope (the helper isn't exported from the Go standard library without
// reaching for strings).
func indexOf(s, sub string) int {
	if sub == "" {
		return 0
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
