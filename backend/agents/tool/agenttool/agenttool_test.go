package agenttool

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/wall-ai/ubuilding/backend/agents"
	"github.com/wall-ai/ubuilding/backend/agents/tool"
)

func TestAgentTool_DispatchesToSpawn(t *testing.T) {
	var got agents.SubAgentParams
	tc := &agents.ToolUseContext{
		Ctx: context.Background(),
		SpawnSubAgent: func(_ context.Context, p agents.SubAgentParams) (string, error) {
			got = p
			return "final answer", nil
		},
	}
	tool := New()
	raw, _ := json.Marshal(Input{Prompt: "do x", Description: "sub", SubagentType: "researcher", MaxTurns: 3})
	res, err := tool.Call(context.Background(), raw, tc)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got.Prompt != "do x" || got.SubagentType != "researcher" || got.MaxTurns != 3 {
		t.Fatalf("params: %+v", got)
	}
	out := res.Data.(Output)
	if out.Result != "final answer" {
		t.Fatalf("result=%q", out.Result)
	}
}

func TestAgentTool_NoHandler(t *testing.T) {
	tool := New()
	raw, _ := json.Marshal(Input{Prompt: "x"})
	_, err := tool.Call(context.Background(), raw, &agents.ToolUseContext{Ctx: context.Background()})
	if err == nil || !strings.Contains(err.Error(), "SpawnSubAgent") {
		t.Fatalf("want no-handler err, got %v", err)
	}
}

func TestAgentTool_AllowList(t *testing.T) {
	tool := New(WithAllowedSubagentTypes("reviewer"))
	raw, _ := json.Marshal(Input{Prompt: "x", SubagentType: "hacker"})
	v := tool.ValidateInput(raw, nil)
	if v.Valid {
		t.Fatal("expected disallowed subagent_type to fail validation")
	}
	raw, _ = json.Marshal(Input{Prompt: "x", SubagentType: "reviewer"})
	if v := tool.ValidateInput(raw, nil); !v.Valid {
		t.Fatalf("allowed type rejected: %s", v.Message)
	}
}

func TestAgentTool_PropagatesError(t *testing.T) {
	tc := &agents.ToolUseContext{
		Ctx: context.Background(),
		SpawnSubAgent: func(context.Context, agents.SubAgentParams) (string, error) {
			return "", errors.New("boom")
		},
	}
	at := New()
	raw, _ := json.Marshal(Input{Prompt: "x"})
	if _, err := at.Call(context.Background(), raw, tc); err == nil {
		t.Fatal("expected error propagation")
	}
}

// A10 · Prompt() renders the agent catalog when the option is installed.
func TestAgentTool_PromptIncludesAgentCatalog(t *testing.T) {
	catalog := func() []*agents.AgentDefinition {
		return []*agents.AgentDefinition{
			{AgentType: "general-purpose", WhenToUse: "Fallback agent", Tools: []string{"*"}},
			{AgentType: "Explore", WhenToUse: "Read-only search", Tools: []string{"Read", "Grep"}, DisallowedTools: []string{"Write"}},
			{AgentType: "Plan", WhenToUse: "Design plans", DisallowedTools: []string{"Edit"}},
		}
	}
	at := New(WithAgentCatalog(catalog))
	got := at.Prompt(tool.PromptOptions{})
	for _, want := range []string{
		"- general-purpose: Fallback agent (Tools: *)",
		"- Explore: Read-only search (Tools: Read, Grep)",
		"- Plan: Design plans (Tools: All tools except Edit)",
		"Available agent types",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("Prompt missing %q; got:\n%s", want, got)
		}
	}
}

// A10 · No catalog installed → prompt still renders without the listing.
func TestAgentTool_PromptWithoutCatalog(t *testing.T) {
	at := New()
	got := at.Prompt(tool.PromptOptions{})
	if strings.Contains(got, "Available agent types") {
		t.Fatal("agent listing leaked without catalog")
	}
	if !strings.Contains(got, "Task tool launches specialized agents") {
		t.Fatal("base description lost")
	}
}

// A11 · AllowedAgentTypes from ToolUseContext are honoured when no explicit
// option is given.
func TestAgentTool_AllowListFromToolUseContext(t *testing.T) {
	at := New()
	tc := &agents.ToolUseContext{
		Options: agents.ToolUseOptions{
			AgentDefinitions: &agents.AgentDefinitions{AllowedAgentTypes: []string{"reviewer"}},
		},
	}
	// Allowed type passes.
	raw, _ := json.Marshal(Input{Prompt: "x", SubagentType: "reviewer"})
	if v := at.ValidateInput(raw, tc); !v.Valid {
		t.Fatalf("reviewer rejected: %s", v.Message)
	}
	// Disallowed type fails.
	raw, _ = json.Marshal(Input{Prompt: "x", SubagentType: "hacker"})
	if v := at.ValidateInput(raw, tc); v.Valid {
		t.Fatal("hacker should have been blocked by ctx allow list")
	}
	// Explicit option overrides the ctx list.
	at2 := New(WithAllowedSubagentTypes("hacker"))
	raw, _ = json.Marshal(Input{Prompt: "x", SubagentType: "hacker"})
	if v := at2.ValidateInput(raw, tc); !v.Valid {
		t.Fatalf("explicit option should take precedence: %s", v.Message)
	}
}

// A15 · discriminated Output + model pass-through.
func TestAgentTool_CallSetsStatusAndForwardsModel(t *testing.T) {
	var got agents.SubAgentParams
	tc := &agents.ToolUseContext{
		Ctx: context.Background(),
		SpawnSubAgent: func(_ context.Context, p agents.SubAgentParams) (string, error) {
			got = p
			return "done", nil
		},
	}
	at := New()
	raw, _ := json.Marshal(Input{
		Prompt: "count files", Description: "count", SubagentType: "general-purpose",
		Model: "haiku", RunInBackground: true, Name: "worker-a", TeamName: "t1",
		Mode: "plan", Isolation: "worktree", Cwd: "/workspace",
	})
	res, err := at.Call(context.Background(), raw, tc)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	out := res.Data.(Output)
	if out.Status != StatusCompleted {
		t.Errorf("Status = %q; want %q", out.Status, StatusCompleted)
	}
	if out.Result != "done" {
		t.Errorf("Result = %q", out.Result)
	}
	if got.Model != "haiku" || got.MaxTurns != 0 {
		t.Errorf("SubAgentParams pass-through: %+v", got)
	}
}

// A15 · schema advertises the extra multi-agent fields.
func TestAgentTool_InputSchemaAdvertisesMultiAgentFields(t *testing.T) {
	at := New()
	schema := at.InputSchema()
	for _, want := range []string{"model", "run_in_background", "name", "team_name", "mode", "isolation", "cwd"} {
		if _, ok := schema.Properties[want]; !ok {
			t.Errorf("schema missing property %q", want)
		}
	}
}
