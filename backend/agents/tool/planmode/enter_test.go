package planmode

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/wall-ai/ubuilding/backend/agents"
)

func TestEnter_FromNormalToPlan(t *testing.T) {
	tc := &agents.ToolUseContext{Ctx: context.Background()}
	et := NewEnter()
	_, err := et.Call(context.Background(), json.RawMessage(`{}`), tc)
	if err != nil {
		t.Fatal(err)
	}
	if tc.PlanMode != ModePlan {
		t.Fatalf("PlanMode=%q", tc.PlanMode)
	}
}

func TestEnter_EmitsEvent(t *testing.T) {
	var got *agents.StreamEvent
	tc := &agents.ToolUseContext{
		Ctx:       context.Background(),
		EmitEvent: func(e agents.StreamEvent) { ev := e; got = &ev },
	}
	if _, err := NewEnter().Call(context.Background(), nil, tc); err != nil {
		t.Fatal(err)
	}
	if got == nil || got.Type != agents.EventPlanModeChange {
		t.Fatalf("event not emitted: %+v", got)
	}
}

func TestEnter_RefusesInAgent(t *testing.T) {
	tc := &agents.ToolUseContext{Ctx: context.Background(), AgentID: "sub-1"}
	_, err := NewEnter().Call(context.Background(), nil, tc)
	if err == nil || !strings.Contains(err.Error(), "agent") {
		t.Fatalf("want agent error, got %v", err)
	}
}

func TestEnter_AlreadyInPlanMode(t *testing.T) {
	tc := &agents.ToolUseContext{Ctx: context.Background(), PlanMode: ModePlan}
	_, err := NewEnter().Call(context.Background(), nil, tc)
	if err == nil {
		t.Fatal("expected already-in-plan error")
	}
}

func TestEnter_MapResultInjectsInstructions(t *testing.T) {
	cb := NewEnter().MapToolResultToParam(EnterOutput{From: "normal", To: "plan"}, "id1")
	s, _ := cb.Content.(string)
	if !strings.Contains(s, "Entered plan mode") || !strings.Contains(s, "ExitPlanMode") {
		t.Fatalf("content=%q", s)
	}
}

func TestEnter_RoundTripWithExit(t *testing.T) {
	tc := &agents.ToolUseContext{Ctx: context.Background()}
	if _, err := NewEnter().Call(context.Background(), nil, tc); err != nil {
		t.Fatal(err)
	}
	if _, err := New().Call(context.Background(), json.RawMessage(`{"plan":"do it"}`), tc); err != nil {
		t.Fatal(err)
	}
	if tc.PlanMode != ModeNormal {
		t.Fatalf("PlanMode=%q", tc.PlanMode)
	}
}
