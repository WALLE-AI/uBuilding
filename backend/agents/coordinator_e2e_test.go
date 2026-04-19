package agents_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/wall-ai/ubuilding/backend/agents"
)

// TestCoordinator_E2E exercises the full Wave 3 coordinator loop:
//   - BuildCoordinatorEngineConfig restricts the top-level tool pool.
//   - RouteTaskToAsync spawns a sub-agent asynchronously via the task
//     manager.
//   - NewCoordinatorInbox feeds the resulting <task-notification> user
//     message back into the coordinator's conversation.
//
// A real coordinator would then read from `inbox`, append the returned
// Message to its transcript via QueryEngine.UpdateMessages, and emit the
// next user turn that references the notification. This test verifies
// the plumbing end-to-end without needing a real LLM.
func TestCoordinator_E2E(t *testing.T) {
	// Base config with a mix of tools — the coordinator should see only
	// Task / SendMessage after filtering.
	base := agents.EngineConfig{
		Tools: []interface{}{
			stubNamedTool{name: "Task"},
			stubNamedTool{name: "Bash"},
			stubNamedTool{name: "SendMessage"},
		},
	}
	spy := &subagentSpy{responses: []agents.Message{textAssistant("task result")}}
	coordCfg := agents.BuildCoordinatorEngineConfig(base, agents.CoordinatorConfig{})

	// Sanity-check the coordinator config surface before handing it to the
	// engine. The engine itself doesn't expose its config, so we verify
	// the derived config directly.
	if !coordCfg.IsCoordinatorMode {
		t.Fatal("coordCfg not in coordinator mode")
	}
	if len(coordCfg.Tools) != 2 {
		t.Fatalf("expected 2 tools after filtering, got %d", len(coordCfg.Tools))
	}

	engine := agents.NewQueryEngine(coordCfg, spy)

	// QueryEngine.Config() surfaces the derived config for introspection.
	if !engine.Config().IsCoordinatorMode {
		t.Fatal("engine.Config() reports coordinator mode off")
	}

	mgr := agents.NewLocalAgentTaskManager()
	inbox, cleanup, err := agents.NewCoordinatorInbox(mgr, 8, nil)
	if err != nil {
		t.Fatalf("inbox: %v", err)
	}
	defer cleanup()

	// Dispatch a task as the coordinator would via RouteTaskToAsync.
	handle, err := agents.RouteTaskToAsync(context.Background(), engine, mgr,
		agents.SubAgentParams{Prompt: "inspect main.go", Description: "audit"}, false)
	if err != nil {
		t.Fatalf("route: %v", err)
	}

	// Read the <task-notification> message back from the inbox.
	var msg *agents.Message
	select {
	case msg = <-inbox:
	case <-time.After(2 * time.Second):
		t.Fatal("inbox never received notification")
	}
	if msg == nil {
		t.Fatal("nil inbox message")
	}
	if msg.Type != agents.MessageTypeUser || msg.Subtype != "task_notification" {
		t.Fatalf("wrong shape: %+v", msg)
	}
	body := msg.Content[0].Text
	for _, want := range []string{
		"<task-notification>",
		"<status>completed</status>",
		"<result>task result</result>",
		"<summary>audit</summary>",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in body:\n%s", want, body)
		}
	}
	if !strings.Contains(msg.UUID, handle.TaskID) {
		t.Fatalf("message uuid %q should reference task id %q", msg.UUID, handle.TaskID)
	}

	// A follow-up dispatch should produce a second notification.
	handle2, err := agents.RouteTaskToAsync(context.Background(), engine, mgr,
		agents.SubAgentParams{Prompt: "review diffs"}, false)
	if err != nil {
		t.Fatalf("route 2: %v", err)
	}
	select {
	case msg := <-inbox:
		if !strings.Contains(msg.UUID, handle2.TaskID) {
			t.Fatalf("second message uuid %q should reference %q", msg.UUID, handle2.TaskID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("second inbox delivery timed out")
	}
}

// stubNamedTool exposes just Name() so BuildCoordinatorEngineConfig can
// classify it. It doesn't need to implement the full tool.Tool surface
// because coordinator filtering only inspects the name.
type stubNamedTool struct{ name string }

func (s stubNamedTool) Name() string { return s.name }
