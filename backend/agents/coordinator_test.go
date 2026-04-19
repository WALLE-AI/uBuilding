package agents

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// --- stubs ------------------------------------------------------------------

type coordStubTool struct{ n string }

func (s coordStubTool) Name() string { return s.n }

// --- BuildCoordinatorEngineConfig ------------------------------------------

func TestBuildCoordinatorEngineConfig_DefaultsAndFiltering(t *testing.T) {
	base := EngineConfig{
		Tools: []interface{}{
			coordStubTool{n: "Task"},
			coordStubTool{n: "Bash"},
			coordStubTool{n: "SendMessage"},
			coordStubTool{n: "Read"},
		},
	}
	out := BuildCoordinatorEngineConfig(base, CoordinatorConfig{})

	if !out.IsCoordinatorMode {
		t.Fatal("IsCoordinatorMode not set")
	}
	if strings.TrimSpace(out.CoordinatorSystemPrompt) == "" {
		t.Fatal("prompt empty")
	}
	if !strings.Contains(out.CoordinatorSystemPrompt, "coordinator agent") {
		t.Fatalf("default prompt missing signature text: %q", out.CoordinatorSystemPrompt)
	}

	// Only orchestration tools survive.
	names := []string{}
	for _, tl := range out.Tools {
		names = append(names, tl.(coordStubTool).n)
	}
	want := []string{"Task", "SendMessage"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("filtered tools = %v; want %v", names, want)
	}

	// Base config must not have been mutated.
	if len(base.Tools) != 4 {
		t.Fatalf("base mutated: %d tools", len(base.Tools))
	}
	if base.IsCoordinatorMode {
		t.Fatal("base.IsCoordinatorMode mutated")
	}
}

func TestBuildCoordinatorEngineConfig_CustomPromptAndAllowList(t *testing.T) {
	allowed := map[string]struct{}{"Task": {}, "Custom": {}}
	base := EngineConfig{
		Tools: []interface{}{
			coordStubTool{n: "Task"},
			coordStubTool{n: "Custom"},
			coordStubTool{n: "Bash"},
		},
	}
	out := BuildCoordinatorEngineConfig(base, CoordinatorConfig{
		SystemPrompt:       "hand-written",
		AppendSystemPrompt: "ALSO",
		AllowedTools:       allowed,
	})
	if out.CoordinatorSystemPrompt != "hand-written" {
		t.Fatalf("prompt = %q", out.CoordinatorSystemPrompt)
	}
	if out.AppendSystemPrompt != "ALSO" {
		t.Fatalf("append = %q", out.AppendSystemPrompt)
	}
	if len(out.Tools) != 2 {
		t.Fatalf("tools = %d", len(out.Tools))
	}
}

// --- RouteTaskToAsync -----------------------------------------------------

func TestRouteTaskToAsync_CoordinatorModeForcesAsync(t *testing.T) {
	spy := &subagentSpy{responses: []Message{textAssistantInPkg("async-result")}}
	engine := NewQueryEngine(EngineConfig{IsCoordinatorMode: true}, spy)

	mgr := NewLocalAgentTaskManager()
	var done sync.WaitGroup
	done.Add(1)
	mgr.OnNotification = func(TaskNotification) { done.Done() }

	handle, err := RouteTaskToAsync(context.Background(), engine, mgr, SubAgentParams{Prompt: "go"}, false)
	if err != nil {
		t.Fatalf("route: %v", err)
	}
	if handle.TaskID == "" {
		t.Fatal("handle.TaskID empty — coordinator path should produce a task id")
	}
	waitTimeout(t, &done, 2*time.Second, "notification never fired")
}

func TestRouteTaskToAsync_SyncFallbackStillRegistersTask(t *testing.T) {
	spy := &subagentSpy{responses: []Message{textAssistantInPkg("sync-result")}}
	engine := NewQueryEngine(EngineConfig{}, spy) // coordinator mode OFF

	mgr := NewLocalAgentTaskManager()
	handle, err := RouteTaskToAsync(context.Background(), engine, mgr, SubAgentParams{Prompt: "go"}, false)
	if err != nil {
		t.Fatalf("route: %v", err)
	}
	task := mgr.Get(handle.TaskID)
	if task == nil {
		t.Fatal("task not registered in sync fallback")
	}
	if task.State != TaskStateCompleted {
		t.Fatalf("state = %s", task.State)
	}
	if task.FinalText != "sync-result" {
		t.Fatalf("final = %q", task.FinalText)
	}
}

func TestRouteTaskToAsync_CoordinatorModeRequiresManager(t *testing.T) {
	engine := NewQueryEngine(EngineConfig{IsCoordinatorMode: true}, &subagentSpy{})
	if _, err := RouteTaskToAsync(context.Background(), engine, nil, SubAgentParams{Prompt: "go"}, false); err == nil {
		t.Fatal("coordinator mode w/ nil manager must error")
	}
}

// --- NotifyCoordinator ----------------------------------------------------

func TestNotifyCoordinator_WrapsNotificationAsUserMessage(t *testing.T) {
	note := TaskNotification{
		TaskID:    "t-42",
		AgentType: "Plan",
		Status:    TaskStateCompleted,
		Result:    "all good",
	}
	msg, err := NotifyCoordinator(note)
	if err != nil {
		t.Fatalf("NotifyCoordinator: %v", err)
	}
	if msg.Type != MessageTypeUser || msg.Subtype != "task_notification" {
		t.Fatalf("wrong shape: %+v", msg)
	}
	if !strings.Contains(msg.Content[0].Text, "<task-notification>") {
		t.Fatal("xml missing from body")
	}
	if !strings.Contains(msg.Content[0].Text, "<result>all good</result>") {
		t.Fatal("result missing from body")
	}
	if msg.UUID != "task-t-42" {
		t.Fatalf("UUID = %q", msg.UUID)
	}
}

// --- LocalAgentTaskManager observers --------------------------------------

func TestTaskManager_AddObserver_FanOut(t *testing.T) {
	mgr := NewLocalAgentTaskManager()
	var a, b int32
	unsubA := mgr.AddObserver(func(TaskNotification) { atomic.AddInt32(&a, 1) })
	_ = mgr.AddObserver(func(TaskNotification) { atomic.AddInt32(&b, 1) })
	if mgr.ObserverCount() != 2 {
		t.Fatalf("count = %d", mgr.ObserverCount())
	}
	task := mgr.Register(&LocalAgentTask{AgentType: "x", State: TaskStateRunning})
	mgr.complete(task, "ok")

	if atomic.LoadInt32(&a) != 1 || atomic.LoadInt32(&b) != 1 {
		t.Fatalf("fan-out broken: a=%d b=%d", a, b)
	}

	// Unsubscribe A, then fire another terminal notification.
	unsubA()
	if mgr.ObserverCount() != 1 {
		t.Fatalf("unsubscribe failed, count = %d", mgr.ObserverCount())
	}
	task2 := mgr.Register(&LocalAgentTask{AgentType: "y", State: TaskStateRunning})
	mgr.complete(task2, "ok2")
	if atomic.LoadInt32(&a) != 1 {
		t.Fatalf("unsubscribed observer still received events: a=%d", a)
	}
	if atomic.LoadInt32(&b) != 2 {
		t.Fatalf("remaining observer missed event: b=%d", b)
	}
}

func TestTaskManager_AddObserver_NilSafe(t *testing.T) {
	mgr := NewLocalAgentTaskManager()
	cancel := mgr.AddObserver(nil)
	if cancel == nil {
		t.Fatal("nil observer should still return a no-op cancel")
	}
	cancel() // must not panic
	if mgr.ObserverCount() != 0 {
		t.Fatal("nil observer should not be tracked")
	}
}

// --- NewCoordinatorInbox --------------------------------------------------

func TestNewCoordinatorInbox_DeliversMessagesAndCleansUp(t *testing.T) {
	mgr := NewLocalAgentTaskManager()
	inbox, cleanup, err := NewCoordinatorInbox(mgr, 4, nil)
	if err != nil {
		t.Fatalf("inbox: %v", err)
	}
	defer cleanup()

	task := mgr.Register(&LocalAgentTask{AgentType: "x", State: TaskStateRunning})
	mgr.complete(task, "done")

	select {
	case msg := <-inbox:
		if msg == nil {
			t.Fatal("nil message")
		}
		if !strings.Contains(msg.Content[0].Text, "<status>completed</status>") {
			t.Fatalf("missing status: %s", msg.Content[0].Text)
		}
	case <-time.After(time.Second):
		t.Fatal("inbox never received the notification")
	}
}

func TestNewCoordinatorInbox_OverflowInvokesCallback(t *testing.T) {
	mgr := NewLocalAgentTaskManager()
	var overflow int32
	inbox, cleanup, err := NewCoordinatorInbox(mgr, 1, func(TaskNotification) {
		atomic.AddInt32(&overflow, 1)
	})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	// Fill the buffer, then exceed it.
	for i := 0; i < 3; i++ {
		task := mgr.Register(&LocalAgentTask{AgentType: "x", State: TaskStateRunning})
		mgr.complete(task, "done")
	}
	// Drain exactly one message to confirm buffer worked, then verify
	// overflow fired at least once.
	select {
	case <-inbox:
	case <-time.After(time.Second):
		t.Fatal("should have at least one buffered message")
	}
	if atomic.LoadInt32(&overflow) == 0 {
		t.Fatal("overflow callback never fired")
	}
}

func TestNewCoordinatorInbox_RejectsNilManager(t *testing.T) {
	if _, _, err := NewCoordinatorInbox(nil, 4, nil); err == nil {
		t.Fatal("nil manager should error")
	}
}

// --- helpers ---

func waitTimeout(t *testing.T, wg *sync.WaitGroup, d time.Duration, msg string) {
	t.Helper()
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(d):
		t.Fatal(msg)
	}
}
