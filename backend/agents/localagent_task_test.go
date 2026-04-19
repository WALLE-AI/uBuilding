package agents

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestLocalAgentTaskManager_RegisterAndKill(t *testing.T) {
	mgr := NewLocalAgentTaskManager()
	task := mgr.Register(&LocalAgentTask{AgentType: "Explore", Description: "scan"})
	if task.ID == "" {
		t.Fatal("ID should be auto-assigned")
	}
	if mgr.Get(task.ID) != task {
		t.Fatal("Get lookup broken")
	}
	if len(mgr.List()) != 1 {
		t.Fatal("List should have 1 entry")
	}
	if err := mgr.Kill(task.ID); err != nil {
		t.Fatalf("Kill: %v", err)
	}
	if task.State != TaskStateKilled {
		t.Fatalf("state = %s", task.State)
	}
	if err := mgr.Kill("ghost"); err == nil {
		t.Fatal("kill unknown should error")
	}
}

// D17 · XML encoding contains expected fields.
func TestEncodeTaskNotification(t *testing.T) {
	blob, err := EncodeTaskNotification(TaskNotification{
		TaskID:    "t-1",
		AgentType: "Plan",
		Status:    TaskStateCompleted,
		Summary:   "done",
		Result:    "42",
		Usage:     TaskUsageSummary{TotalTokens: 100, ToolUses: 3, DurationMs: 250},
	})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	for _, want := range []string{
		"<task-notification>",
		"<task-id>t-1</task-id>",
		"<agent-type>Plan</agent-type>",
		"<status>completed</status>",
		"<result>42</result>",
		"<total_tokens>100</total_tokens>",
	} {
		if !strings.Contains(blob, want) {
			t.Errorf("missing %q in:\n%s", want, blob)
		}
	}
}

// D09 · SpawnAsyncSubAgent returns immediately and fires a completion
// notification once the child finishes.
func TestSpawnAsyncSubAgent_CompletesAndNotifies(t *testing.T) {
	spy := &subagentSpy{responses: []Message{textAssistantInPkg("async done")}}
	engine := NewQueryEngine(EngineConfig{}, spy)

	mgr := NewLocalAgentTaskManager()
	var notified TaskNotification
	var wg sync.WaitGroup
	wg.Add(1)
	mgr.OnNotification = func(n TaskNotification) {
		notified = n
		wg.Done()
	}

	handle, err := engine.SpawnAsyncSubAgent(context.Background(), mgr, SubAgentParams{Prompt: "go"})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	if handle.TaskID == "" || handle.AgentType != "general-purpose" {
		t.Fatalf("handle: %+v", handle)
	}

	// Wait up to 2s for the goroutine to finish.
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("notification never fired")
	}

	if notified.Status != TaskStateCompleted {
		t.Fatalf("status = %s", notified.Status)
	}
	if notified.Result != "async done" {
		t.Fatalf("result = %q", notified.Result)
	}
	if notified.TaskID != handle.TaskID {
		t.Fatalf("task id mismatch")
	}
}

// D09 · SpawnAsyncSubAgent Kill short-circuits the goroutine cleanly.
func TestSpawnAsyncSubAgent_Kill(t *testing.T) {
	spy := &subagentSpy{responses: []Message{textAssistantInPkg("late")}}
	engine := NewQueryEngine(EngineConfig{}, spy)

	mgr := NewLocalAgentTaskManager()
	handle, err := engine.SpawnAsyncSubAgent(context.Background(), mgr, SubAgentParams{Prompt: "go"})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	if err := mgr.Kill(handle.TaskID); err != nil {
		t.Fatalf("Kill: %v", err)
	}
	time.Sleep(50 * time.Millisecond) // give goroutine a chance to observe ctx cancel
	task := mgr.Get(handle.TaskID)
	if task.State != TaskStateKilled {
		t.Fatalf("state = %s", task.State)
	}
}
