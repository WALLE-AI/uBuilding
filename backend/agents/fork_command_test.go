package agents

import (
	"strings"
	"testing"
)

// E03 · skill/command fork prep expands the template + picks an agent.
func TestPrepareForkedCommandContext_Defaults(t *testing.T) {
	cmd := &Command{Name: "verify"}
	active := []*AgentDefinition{
		{AgentType: "reviewer", WhenToUse: "review"},
	}
	ctx, err := PrepareForkedCommandContext(cmd, "run tests", active, func(c *Command, args string) string {
		return "/" + c.Name + " " + args
	})
	if err != nil {
		t.Fatal(err)
	}
	if ctx.SkillContent != "/verify run tests" {
		t.Fatalf("content = %q", ctx.SkillContent)
	}
	if ctx.BaseAgent == nil || ctx.BaseAgent.AgentType != "reviewer" {
		t.Fatalf("agent = %+v", ctx.BaseAgent)
	}
	if len(ctx.PromptMessages) != 1 || ctx.PromptMessages[0].Type != MessageTypeUser {
		t.Fatalf("prompt = %+v", ctx.PromptMessages)
	}
}

// E03 · `agent=<type>` alias targets a specific definition.
func TestPrepareForkedCommandContext_AgentAlias(t *testing.T) {
	cmd := &Command{Name: "commit", Aliases: []string{"agent=reviewer"}}
	active := []*AgentDefinition{
		{AgentType: "hacker"},
		{AgentType: "reviewer"},
	}
	ctx, err := PrepareForkedCommandContext(cmd, "all", active, nil)
	if err != nil {
		t.Fatal(err)
	}
	if ctx.BaseAgent.AgentType != "reviewer" {
		t.Fatalf("alias routing broken: %s", ctx.BaseAgent.AgentType)
	}
	// Expand fallback: "" expand → raw name.
	if !strings.Contains(ctx.SkillContent, "commit") {
		t.Fatalf("fallback expand lost: %q", ctx.SkillContent)
	}
}

func TestPrepareForkedCommandContext_NilCommand(t *testing.T) {
	if _, err := PrepareForkedCommandContext(nil, "", nil, nil); err == nil {
		t.Fatal("nil command should error")
	}
}

func TestApplyAllowedTools_AppendsSafely(t *testing.T) {
	base := PreparedForkedContext{}
	out := base.ApplyAllowedTools([]string{"Bash"})
	if len(out.AllowedTools) != 1 || out.AllowedTools[0] != "Bash" {
		t.Fatalf("AllowedTools = %v", out.AllowedTools)
	}
	// Caller mutates returned slice — base stays empty.
	out.AllowedTools[0] = "Hijacked"
	if len(base.AllowedTools) != 0 {
		t.Fatal("base was mutated")
	}
}

// E04 · ApplyOverridesToContext mutates every known field.
func TestApplyOverridesToContext_FullSet(t *testing.T) {
	tc := &ToolUseContext{}
	msgs := []Message{{Type: MessageTypeUser, UUID: "u"}}
	ApplyOverridesToContext(tc, SubagentContextOverrides{
		AgentID:                      "A1",
		AgentType:                    "reviewer",
		ToolUseID:                    "tu-9",
		RenderedSystemPrompt:         "rendered",
		ContentReplacementState:      map[string]string{"tu-9": "ok"},
		AgentPermissionMode:          "plan",
		ShouldAvoidPermissionPrompts: true,
		Messages:                     msgs,
	})
	if tc.AgentID != "A1" || tc.AgentType != "reviewer" || tc.ToolUseID != "tu-9" {
		t.Fatalf("basic fields lost: %+v", tc)
	}
	if tc.RenderedSystemPrompt != "rendered" {
		t.Fatal("RenderedSystemPrompt lost")
	}
	if tc.Options.AgentPermissionMode != "plan" {
		t.Fatal("permission mode lost")
	}
	if !tc.Options.ShouldAvoidPermissionPromptsOverride {
		t.Fatal("avoid-prompts lost")
	}
	if tc.ContentReplacementState == nil {
		t.Fatal("replacement state lost")
	}
	if len(tc.Messages) != 1 || tc.Messages[0].UUID != "u" {
		t.Fatal("messages lost")
	}
}

// E04 · nil context is a no-op (hosts sometimes call pre-init).
func TestApplyOverridesToContext_NilSafe(t *testing.T) {
	ApplyOverridesToContext(nil, SubagentContextOverrides{AgentID: "X"})
}
