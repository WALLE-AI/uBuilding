package agenttool

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/wall-ai/ubuilding/backend/agents"
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
	tool := New()
	raw, _ := json.Marshal(Input{Prompt: "x"})
	if _, err := tool.Call(context.Background(), raw, tc); err == nil {
		t.Fatal("expected error propagation")
	}
}
