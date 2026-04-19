package bg

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/wall-ai/ubuilding/backend/agents"
	"github.com/wall-ai/ubuilding/backend/agents/tool"
)

func TestOutputTool_PromptKeywords(t *testing.T) {
	p := NewOutputTool().Prompt(tool.PromptOptions{})
	for _, want := range []string{
		"DEPRECATED",
		"Read tool on the task's output file path",
		"<task-notification>",
		"`task_id`",
		"`bash_id`",
		"`agentId`",
		"`incremental=true`",
		"background shells, async agents, and remote sessions",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("TaskOutput prompt missing %q", want)
		}
	}
}

func TestOutputTool_DescriptionDeprecationMarker(t *testing.T) {
	d := NewOutputTool().Description(nil)
	if !strings.Contains(d, "[Deprecated]") {
		t.Errorf("description missing deprecation marker: %q", d)
	}
}

func TestOutputTool_Aliases(t *testing.T) {
	al := NewOutputTool().Aliases()
	for _, want := range []string{"AgentOutputTool", "BashOutputTool"} {
		found := false
		for _, a := range al {
			if a == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("OutputTool Aliases missing %q; got %v", want, al)
		}
	}
}

func TestStopTool_PromptKeywords(t *testing.T) {
	p := NewStopTool().Prompt(tool.PromptOptions{})
	for _, want := range []string{
		"Cancels an in-progress background task",
		"bg-shell id",
		"task-graph node id",
		`("bg" or "graph")`,
		"terminate a long-running task",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("TaskStop prompt missing %q", want)
		}
	}
}

func TestStopTool_DescriptionBullets(t *testing.T) {
	d := NewStopTool().Description(nil)
	for _, want := range []string{
		"Stops a running background task by its ID",
		"task_id parameter identifying the task to stop",
		"success or failure status",
		"long-running task",
	} {
		if !strings.Contains(d, want) {
			t.Errorf("TaskStop description missing %q", want)
		}
	}
}

func TestStopTool_Aliases(t *testing.T) {
	al := NewStopTool().Aliases()
	if len(al) != 1 || al[0] != "KillShell" {
		t.Errorf("StopTool Aliases = %v; want [KillShell]", al)
	}
}

func TestOutputTool_AcceptsTaskIDAlias(t *testing.T) {
	m := NewManager()
	id, err := m.Start(context.Background(), "t", func(ctx context.Context, write func(string)) (int, error) {
		write("done")
		return 0, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.WaitForTerminal(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	tc := &agents.ToolUseContext{Ctx: context.Background(), TaskManager: m}
	ot := NewOutputTool()

	// Upstream canonical name.
	raw, _ := json.Marshal(map[string]string{"task_id": id})
	if v := ot.ValidateInput(raw, tc); !v.Valid {
		t.Fatalf("task_id alias rejected: %s", v.Message)
	}
	res, err := ot.Call(context.Background(), raw, tc)
	if err != nil {
		t.Fatalf("call(task_id): %v", err)
	}
	if r := res.Data.(OutputResult); r.BashID != id {
		t.Fatalf("BashID=%s want %s", r.BashID, id)
	}

	// Legacy AgentOutputTool alias.
	raw, _ = json.Marshal(map[string]string{"agentId": id})
	if v := ot.ValidateInput(raw, tc); !v.Valid {
		t.Fatalf("agentId alias rejected: %s", v.Message)
	}

	// Missing id of any flavour is rejected.
	raw, _ = json.Marshal(map[string]string{})
	if v := ot.ValidateInput(raw, tc); v.Valid {
		t.Fatal("missing id should fail validation")
	}
}
