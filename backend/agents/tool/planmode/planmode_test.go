package planmode

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/wall-ai/ubuilding/backend/agents"
)

func TestExitPlanMode_Success(t *testing.T) {
	var emitted agents.StreamEvent
	tc := &agents.ToolUseContext{
		Ctx:      context.Background(),
		PlanMode: ModePlan,
		EmitEvent: func(e agents.StreamEvent) { emitted = e },
	}
	tool := New()
	raw, _ := json.Marshal(Input{Plan: "do the thing"})
	res, err := tool.Call(context.Background(), raw, tc)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if tc.PlanMode != ModeNormal {
		t.Fatalf("plan mode not flipped: %s", tc.PlanMode)
	}
	if emitted.Type != agents.EventPlanModeChange {
		t.Fatalf("no plan_mode_change event")
	}
	out := res.Data.(Output)
	if out.From != ModePlan || out.To != ModeNormal {
		t.Fatalf("bad output: %+v", out)
	}
}

func TestExitPlanMode_NotInPlanMode(t *testing.T) {
	tool := New()
	tc := &agents.ToolUseContext{Ctx: context.Background(), PlanMode: ModeNormal}
	raw, _ := json.Marshal(Input{Plan: "x"})
	if _, err := tool.Call(context.Background(), raw, tc); err == nil {
		t.Fatal("expected error when not in plan mode")
	}
}

func TestExitPlanMode_Validation(t *testing.T) {
	tool := New()
	raw, _ := json.Marshal(Input{Plan: ""})
	v := tool.ValidateInput(raw, nil)
	if v.Valid {
		t.Fatal("empty plan must be invalid")
	}
}
