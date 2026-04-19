package agents

import (
	"os"
	"testing"
)

func TestGetDefaultSubagentModel(t *testing.T) {
	if got := GetDefaultSubagentModel(); got != "inherit" {
		t.Fatalf("default subagent model = %q; want inherit", got)
	}
}

func TestGetAgentModel_ResolutionChain(t *testing.T) {
	// Ensure no env leakage from other tests.
	t.Setenv("CLAUDE_CODE_SUBAGENT_MODEL", "")
	_ = os.Unsetenv("CLAUDE_CODE_SUBAGENT_MODEL")

	const parent = "claude-sonnet-4-20250514"

	cases := []struct {
		name                        string
		agent, tool, parent, permMo string
		env                         string
		want                        string
	}{
		{
			name:   "env override wins",
			env:    "claude-haiku-test",
			agent:  "opus",
			tool:   "sonnet",
			parent: parent,
			want:   "claude-haiku-test",
		},
		{
			name:   "tool-specified takes precedence over frontmatter",
			agent:  "opus",
			tool:   "haiku",
			parent: parent,
			want:   "haiku",
		},
		{
			name:   "tool alias matches parent tier → inherit parent verbatim",
			agent:  "",
			tool:   "sonnet",
			parent: parent,
			want:   parent,
		},
		{
			name:   "agent=inherit → parent model",
			agent:  "inherit",
			parent: parent,
			want:   parent,
		},
		{
			name:   "agent blank → default(inherit) → parent",
			parent: parent,
			want:   parent,
		},
		{
			name:   "agent concrete id passes through",
			agent:  "claude-opus-4",
			parent: parent,
			want:   "claude-opus-4",
		},
		{
			name:   "agent alias = opus vs sonnet parent → alias passes through",
			agent:  "opus",
			parent: parent,
			want:   "opus",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.env != "" {
				t.Setenv("CLAUDE_CODE_SUBAGENT_MODEL", tc.env)
			} else {
				t.Setenv("CLAUDE_CODE_SUBAGENT_MODEL", "")
			}
			got := GetAgentModel(tc.agent, tc.parent, tc.tool, tc.permMo)
			if got != tc.want {
				t.Fatalf("GetAgentModel(%q, %q, %q, %q) = %q; want %q",
					tc.agent, tc.parent, tc.tool, tc.permMo, got, tc.want)
			}
		})
	}
}

func TestGetQuerySourceForAgent(t *testing.T) {
	cases := []struct{ agentType, want string; builtIn bool }{
		{"general-purpose", "agent:builtin:general-purpose", true},
		{"reviewer", "agent:user:reviewer", false},
		{"", "agent:builtin", true},
		{"", "agent:user", false},
	}
	for _, tc := range cases {
		if got := GetQuerySourceForAgent(tc.agentType, tc.builtIn); got != tc.want {
			t.Errorf("GetQuerySourceForAgent(%q, %v) = %q; want %q", tc.agentType, tc.builtIn, got, tc.want)
		}
	}
}

func TestIsAgentModelAlias(t *testing.T) {
	for _, a := range []string{"sonnet", "Opus", "HAIKU", "inherit"} {
		if !IsAgentModelAlias(a) {
			t.Errorf("%q should be an alias", a)
		}
	}
	for _, a := range []string{"", "claude-sonnet-4", "foo"} {
		if IsAgentModelAlias(a) {
			t.Errorf("%q should not be an alias", a)
		}
	}
}
