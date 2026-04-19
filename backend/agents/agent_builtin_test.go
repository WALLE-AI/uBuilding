package agents

import (
	"strings"
	"testing"
)

func TestDefaultBuiltInAgents_Contents(t *testing.T) {
	t.Setenv("UBUILDING_DISABLE_BUILTIN_AGENTS", "")
	agents := DefaultBuiltInAgents()
	if len(agents) != 3 {
		t.Fatalf("expected 3 built-in agents, got %d: %+v", len(agents), agents)
	}
	byType := map[string]*AgentDefinition{}
	for _, a := range agents {
		byType[a.AgentType] = a
	}
	for _, tp := range []string{"general-purpose", "Explore", "Plan"} {
		if _, ok := byType[tp]; !ok {
			t.Fatalf("missing built-in agent %q", tp)
		}
	}

	// Every built-in must have a non-empty whenToUse and a prompt closure.
	for _, a := range agents {
		if strings.TrimSpace(a.WhenToUse) == "" {
			t.Errorf("%s: empty WhenToUse", a.AgentType)
		}
		if a.GetSystemPrompt == nil {
			t.Errorf("%s: GetSystemPrompt is nil", a.AgentType)
		} else if got := a.GetSystemPrompt(SystemPromptCtx{}); strings.TrimSpace(got) == "" {
			t.Errorf("%s: system prompt is empty", a.AgentType)
		}
		if a.Source != AgentSourceBuiltIn {
			t.Errorf("%s: source = %v; want built-in", a.AgentType, a.Source)
		}
	}

	// Plan must enforce plan mode and omit ClaudeMd.
	if byType["Plan"].PermissionMode != "plan" {
		t.Errorf("Plan.PermissionMode = %q; want plan", byType["Plan"].PermissionMode)
	}
	if !byType["Plan"].OmitClaudeMd || !byType["Explore"].OmitClaudeMd {
		t.Error("Plan and Explore must set OmitClaudeMd")
	}
	// Explore/Plan disallow writes.
	for _, name := range []string{"Explore", "Plan"} {
		disallow := map[string]bool{}
		for _, d := range byType[name].DisallowedTools {
			disallow[d] = true
		}
		for _, blocked := range []string{"Edit", "Write", "NotebookEdit"} {
			if !disallow[blocked] {
				t.Errorf("%s must disallow %s", name, blocked)
			}
		}
	}
}

func TestDefaultBuiltInAgents_EnvDisable(t *testing.T) {
	t.Setenv("UBUILDING_DISABLE_BUILTIN_AGENTS", "1")
	if got := DefaultBuiltInAgents(); len(got) != 0 {
		t.Fatalf("env disable should produce 0 agents, got %d", len(got))
	}
}

func TestIsEnvTruthy(t *testing.T) {
	for _, v := range []string{"1", "true", "TRUE", "yes", "On"} {
		if !isEnvTruthy(v) {
			t.Errorf("%q should be truthy", v)
		}
	}
	for _, v := range []string{"", "0", "false", "off", "no"} {
		if isEnvTruthy(v) {
			t.Errorf("%q should not be truthy", v)
		}
	}
}
