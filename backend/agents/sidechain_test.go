package agents

import (
	"path/filepath"
	"strings"
	"testing"
)

// D06 · record + read round trip.
func TestSidechainTranscript_RoundTrip(t *testing.T) {
	tmp := t.TempDir()
	messages := []Message{
		{Type: MessageTypeUser, UUID: "u1", Content: []ContentBlock{{Type: ContentBlockText, Text: "hi"}}},
		{Type: MessageTypeAssistant, UUID: "a1", Content: []ContentBlock{{Type: ContentBlockText, Text: "hey"}}},
	}
	n, err := RecordSidechainTranscript(tmp, "agt-1", messages, "")
	if err != nil || n != 2 {
		t.Fatalf("record: n=%d err=%v", n, err)
	}
	// Append more without losing the earlier ones.
	second := []Message{
		{Type: MessageTypeUser, UUID: "u2", Content: []ContentBlock{{Type: ContentBlockText, Text: "again"}}},
	}
	if _, err := RecordSidechainTranscript(tmp, "agt-1", second, "a1"); err != nil {
		t.Fatalf("append: %v", err)
	}

	got, err := GetAgentTranscript(tmp, "agt-1")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d messages; want 3", len(got))
	}
	if got[0].UUID != "u1" || got[2].UUID != "u2" {
		t.Fatalf("order wrong: %+v", got)
	}
}

func TestSidechainTranscript_MissingReturnsEmpty(t *testing.T) {
	tmp := t.TempDir()
	got, err := GetAgentTranscript(tmp, "missing")
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatalf("want nil slice, got %v", got)
	}
}

// D07 · metadata round trip.
func TestAgentMetadata_RoundTrip(t *testing.T) {
	tmp := t.TempDir()
	m := AgentMetadata{
		AgentType:    "Explore",
		Description:  "scan repo",
		WorktreePath: filepath.Join(tmp, "worktree"),
		Prompt:       "look at main.go",
	}
	if err := WriteAgentMetadata(tmp, "agt-2", m); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := ReadAgentMetadata(tmp, "agt-2")
	if err != nil || got == nil {
		t.Fatalf("read: %v / %+v", err, got)
	}
	if got.AgentType != "Explore" || got.WorktreePath != m.WorktreePath {
		t.Fatalf("roundtrip lost fields: %+v", got)
	}
	if got.CreatedAt.IsZero() || got.UpdatedAt.IsZero() {
		t.Fatal("timestamps not populated")
	}
}

// D13 · filters drop orphan tool_use / thinking-only / blank assistants.
func TestFilterResumedTranscript(t *testing.T) {
	msgs := []Message{
		{Type: MessageTypeUser, UUID: "u1", Content: []ContentBlock{{Type: ContentBlockText, Text: "hi"}}},
		// Orphan tool_use (no matching result) → block dropped.
		{Type: MessageTypeAssistant, UUID: "a1", Content: []ContentBlock{
			{Type: ContentBlockText, Text: "try tool"},
			{Type: ContentBlockToolUse, ID: "orphan"},
		}},
		// Resolved tool_use (kept).
		{Type: MessageTypeAssistant, UUID: "a2", Content: []ContentBlock{
			{Type: ContentBlockToolUse, ID: "tu-ok"},
		}},
		{Type: MessageTypeUser, UUID: "u2", Content: []ContentBlock{
			{Type: ContentBlockToolResult, ToolUseID: "tu-ok", Content: "ok"},
		}},
		// Thinking-only assistant (dropped).
		{Type: MessageTypeAssistant, UUID: "a3", Content: []ContentBlock{
			{Type: ContentBlockThinking, Thinking: "hmm"},
		}},
		// Whitespace-only assistant (dropped).
		{Type: MessageTypeAssistant, UUID: "a4", Content: []ContentBlock{
			{Type: ContentBlockText, Text: "   "},
		}},
		// Final good assistant.
		{Type: MessageTypeAssistant, UUID: "a5", Content: []ContentBlock{
			{Type: ContentBlockText, Text: "done"},
		}},
	}
	got := FilterResumedTranscript(msgs)
	var kept []string
	for _, m := range got {
		kept = append(kept, m.UUID)
	}
	wantOrder := []string{"u1", "a1", "a2", "u2", "a5"}
	if strings.Join(kept, ",") != strings.Join(wantOrder, ",") {
		t.Fatalf("kept = %v; want %v", kept, wantOrder)
	}
	// The orphan tool_use block must have been stripped from a1.
	for _, m := range got {
		if m.UUID == "a1" {
			for _, blk := range m.Content {
				if blk.Type == ContentBlockToolUse {
					t.Fatalf("orphan tool_use survived: %+v", m)
				}
			}
		}
	}
}

// D08 · resume returns filtered + prompt.
func TestResumeAgent_RehydratesTranscript(t *testing.T) {
	tmp := t.TempDir()
	_, _ = RecordSidechainTranscript(tmp, "agt", []Message{
		{Type: MessageTypeUser, UUID: "u", Content: []ContentBlock{{Type: ContentBlockText, Text: "go"}}},
		{Type: MessageTypeAssistant, UUID: "a", Content: []ContentBlock{{Type: ContentBlockText, Text: "ok"}}},
	}, "")
	_ = WriteAgentMetadata(tmp, "agt", AgentMetadata{AgentType: "Explore"})

	res, err := ResumeAgent(ResumeAgentOptions{Cwd: tmp, AgentID: "agt", Prompt: "continue"})
	if err != nil {
		t.Fatalf("ResumeAgent: %v", err)
	}
	if res.Metadata == nil || res.Metadata.AgentType != "Explore" {
		t.Fatalf("metadata lost: %+v", res.Metadata)
	}
	if len(res.InitialMessages) != 2 {
		t.Fatalf("msgs = %d", len(res.InitialMessages))
	}
	if res.ContinuePrompt != "continue" {
		t.Fatalf("prompt = %q", res.ContinuePrompt)
	}
}

// D14 · reconstructForSubagentResume drops records without a matching
// tool_use in the transcript and preserves the rest.
func TestReconstructForSubagentResume(t *testing.T) {
	messages := []Message{
		{Type: MessageTypeAssistant, Content: []ContentBlock{
			{Type: ContentBlockToolUse, ID: "tu-1"},
		}},
		{Type: MessageTypeUser, Content: []ContentBlock{
			{Type: ContentBlockToolResult, ToolUseID: "tu-2", Content: "ok"},
		}},
	}
	records := []ContentReplacementRecord{
		{ToolUseID: "tu-1", Replaced: "short"},
		{ToolUseID: "tu-2", Replaced: "mid"},
		{ToolUseID: "tu-3", Replaced: "gone"},
	}
	got := ReconstructForSubagentResume(records, messages)
	kept, ok := got.([]ContentReplacementRecord)
	if !ok {
		t.Fatalf("unexpected type %T", got)
	}
	if len(kept) != 2 {
		t.Fatalf("kept = %d", len(kept))
	}
	for _, r := range kept {
		if r.ToolUseID == "tu-3" {
			t.Fatal("stale record survived")
		}
	}
	// No records → nil result.
	if ReconstructForSubagentResume(nil, messages) != nil {
		t.Fatal("nil records should yield nil")
	}
}
