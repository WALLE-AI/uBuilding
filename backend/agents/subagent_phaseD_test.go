package agents_test

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wall-ai/ubuilding/backend/agents"
)

// TestPhaseD_ForkRejectedInsideFork · D03 recursion guard: once the parent
// transcript contains the fork boilerplate, IsInForkChild returns true, so
// a downstream AgentTool invocation must not re-enter.
func TestPhaseD_ForkRejectedInsideFork(t *testing.T) {
	parent := &agents.Message{Type: agents.MessageTypeAssistant, UUID: "a1"}
	forkMsgs := agents.BuildForkedMessages("work it", parent)
	if !agents.IsInForkChild(forkMsgs) {
		t.Fatal("fork child should be detected")
	}
	// Non-fork transcript → safe.
	plain := []agents.Message{
		{Type: agents.MessageTypeUser, Content: []agents.ContentBlock{{Type: agents.ContentBlockText, Text: "normal"}}},
	}
	if agents.IsInForkChild(plain) {
		t.Fatal("plain transcript misidentified as fork")
	}
}

// TestPhaseD_SidechainResume covers D06+D07+D08+D13:
//   - Write a transcript + metadata.
//   - ResumeAgent rehydrates both, applying D13 filters.
func TestPhaseD_SidechainResume(t *testing.T) {
	tmp := t.TempDir()
	_ = filepath.Join(tmp, "ignored")

	msgs := []agents.Message{
		{Type: agents.MessageTypeUser, UUID: "u1", Content: []agents.ContentBlock{{Type: agents.ContentBlockText, Text: "start"}}},
		// thinking-only → dropped by D13.
		{Type: agents.MessageTypeAssistant, UUID: "a-think", Content: []agents.ContentBlock{{Type: agents.ContentBlockThinking, Thinking: "hmm"}}},
		{Type: agents.MessageTypeAssistant, UUID: "a1", Content: []agents.ContentBlock{{Type: agents.ContentBlockText, Text: "ok"}}},
	}
	if _, err := agents.RecordSidechainTranscript(tmp, "agt-d", msgs, ""); err != nil {
		t.Fatalf("record: %v", err)
	}
	if err := agents.WriteAgentMetadata(tmp, "agt-d", agents.AgentMetadata{AgentType: "Explore", Description: "d"}); err != nil {
		t.Fatalf("meta: %v", err)
	}

	res, err := agents.ResumeAgent(agents.ResumeAgentOptions{Cwd: tmp, AgentID: "agt-d", Prompt: "carry on"})
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if res.Metadata.AgentType != "Explore" {
		t.Fatalf("metadata lost: %+v", res.Metadata)
	}
	// D13 filters removed the thinking-only turn.
	if len(res.InitialMessages) != 2 {
		t.Fatalf("expected 2 messages after filter, got %d", len(res.InitialMessages))
	}
	for _, m := range res.InitialMessages {
		if m.UUID == "a-think" {
			t.Fatalf("thinking-only survived")
		}
	}
	if res.ContinuePrompt != "carry on" {
		t.Fatalf("prompt = %q", res.ContinuePrompt)
	}
}

// TestPhaseD_AsyncSpawn_NotifiesWithXML exercises D09+D16+D17:
//   - SpawnAsyncSubAgent returns immediately with a handle.
//   - The completion notification serialises to the <task-notification>
//     XML blob the coordinator consumes.
func TestPhaseD_AsyncSpawn_NotifiesWithXML(t *testing.T) {
	spy := &subagentSpy{responses: []agents.Message{textAssistant("42")}}
	engine := agents.NewQueryEngine(agents.EngineConfig{}, spy)

	mgr := agents.NewLocalAgentTaskManager()
	var (
		wg   sync.WaitGroup
		note agents.TaskNotification
	)
	wg.Add(1)
	mgr.OnNotification = func(n agents.TaskNotification) {
		note = n
		wg.Done()
	}
	handle, err := engine.SpawnAsyncSubAgent(context.Background(), mgr, agents.SubAgentParams{Description: "compute", Prompt: "go"})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	if handle.TaskID == "" {
		t.Fatal("handle missing id")
	}

	// Wait for the notification.
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("notification never fired")
	}

	if note.Status != agents.TaskStateCompleted {
		t.Fatalf("state = %s", note.Status)
	}
	blob, err := agents.EncodeTaskNotification(note)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	for _, want := range []string{
		"<task-notification>",
		"<status>completed</status>",
		"<summary>compute</summary>",
		"<result>42</result>",
	} {
		if !strings.Contains(blob, want) {
			t.Errorf("missing %q in:\n%s", want, blob)
		}
	}
}
