package agents

import (
	"context"
	"strings"
	"testing"
)

// D01 · feature flag responds to env.
func TestForkSubagentEnabled_EnvGates(t *testing.T) {
	t.Setenv("UBUILDING_FORK_SUBAGENT", "")
	t.Setenv("UBUILDING_COORDINATOR_MODE", "")
	t.Setenv("UBUILDING_NON_INTERACTIVE", "")
	if ForkSubagentEnabled() {
		t.Fatal("should default disabled")
	}
	t.Setenv("UBUILDING_FORK_SUBAGENT", "1")
	if !ForkSubagentEnabled() {
		t.Fatal("env on should enable")
	}
	t.Setenv("UBUILDING_COORDINATOR_MODE", "1")
	if ForkSubagentEnabled() {
		t.Fatal("coordinator mode must disable fork")
	}
	t.Setenv("UBUILDING_COORDINATOR_MODE", "")
	t.Setenv("UBUILDING_NON_INTERACTIVE", "1")
	if ForkSubagentEnabled() {
		t.Fatal("non-interactive must disable fork")
	}
}

// D02 · BuildForkedMessages preserves the assistant history and builds the
// parallel tool_result / directive message.
func TestBuildForkedMessages_WithToolUses(t *testing.T) {
	assistant := &Message{
		Type:    MessageTypeAssistant,
		UUID:    "asst-1",
		Content: []ContentBlock{
			{Type: ContentBlockText, Text: "thinking"},
			{Type: ContentBlockToolUse, ID: "tu-1", Name: "Read"},
			{Type: ContentBlockToolUse, ID: "tu-2", Name: "Grep"},
		},
	}
	got := BuildForkedMessages("audit the branch", assistant)
	if len(got) != 2 {
		t.Fatalf("msgs = %d", len(got))
	}
	if got[0].Type != MessageTypeAssistant || len(got[0].Content) != 3 {
		t.Fatalf("assistant copy malformed: %+v", got[0])
	}
	user := got[1]
	if user.Type != MessageTypeUser {
		t.Fatalf("second msg type = %s", user.Type)
	}
	// Should have 2 tool_result blocks + 1 text directive.
	var results, texts int
	for _, blk := range user.Content {
		switch blk.Type {
		case ContentBlockToolResult:
			results++
			if blk.Content != ForkPlaceholderResult {
				t.Fatalf("placeholder content = %v", blk.Content)
			}
		case ContentBlockText:
			texts++
		}
	}
	if results != 2 || texts != 1 {
		t.Fatalf("tool_result=%d text=%d", results, texts)
	}
	if !strings.Contains(user.Content[len(user.Content)-1].Text, ForkDirectivePrefix) {
		t.Fatal("directive tag missing")
	}
}

func TestBuildForkedMessages_NoToolUses(t *testing.T) {
	assistant := &Message{
		Type:    MessageTypeAssistant,
		UUID:    "asst-1",
		Content: []ContentBlock{{Type: ContentBlockText, Text: "idle"}},
	}
	got := BuildForkedMessages("go", assistant)
	if len(got) != 2 {
		t.Fatalf("want 2, got %d", len(got))
	}
	if got[1].Type != MessageTypeUser || got[1].Content[0].Type != ContentBlockText {
		t.Fatalf("fallback user msg malformed: %+v", got[1])
	}
}

func TestBuildForkedMessages_NilAssistant(t *testing.T) {
	got := BuildForkedMessages("do it", nil)
	if len(got) != 1 {
		t.Fatalf("want 1, got %d", len(got))
	}
}

// D03 · IsInForkChild detects the boilerplate tag.
func TestIsInForkChild_Detects(t *testing.T) {
	msgs := []Message{
		{Type: MessageTypeUser, Content: []ContentBlock{{Type: ContentBlockText, Text: "regular message"}}},
	}
	if IsInForkChild(msgs) {
		t.Fatal("no fork tag, must be false")
	}
	msgs = BuildForkedMessages("do", &Message{Type: MessageTypeAssistant, UUID: "x", Content: nil})
	if !IsInForkChild(msgs) {
		t.Fatal("should detect fork boilerplate")
	}
}

// D11 · two fork dispatches with identical cache-safe params produce
// matching fingerprints; changing the directive alone does too (because
// the directive lives in PromptMessages, not the cache-safe prefix).
func TestForkPrefixFingerprint_Stable(t *testing.T) {
	history := []Message{
		{Type: MessageTypeAssistant, UUID: "asst-1", Content: []ContentBlock{{Type: ContentBlockText, Text: "foo"}}},
	}
	params := CacheSafeParams{
		SystemPrompt:        "SYS",
		UserContext:         map[string]string{"claudemd": "hello"},
		SystemContext:       map[string]string{"cwd": "/tmp"},
		ForkContextMessages: history,
	}
	a := ForkPrefixFingerprint(params)
	b := ForkPrefixFingerprint(params)
	if a != b {
		t.Fatalf("same inputs should hash equal: %s vs %s", a, b)
	}
	params.SystemPrompt = "DIFFERENT"
	if ForkPrefixFingerprint(params) == a {
		t.Fatal("different prompt should yield different fingerprint")
	}
}

// D05 · DefaultForkRunner requires a QueryDeps to be registered; without
// one, RunForkedAgent errors out sensibly.
func TestDefaultForkRunner_RequiresRegisteredDeps(t *testing.T) {
	// Start clean.
	deps4Fork.Store(nil)
	RegisterForkRunner(nil)
	_, err := RunForkedAgent(context.Background(), ForkedAgentParams{
		PromptMessages: []Message{{Type: MessageTypeUser, Content: []ContentBlock{{Type: ContentBlockText, Text: "hi"}}}},
		CacheSafeParams: CacheSafeParams{
			SystemPrompt:   "sys",
			ToolUseContext: &ToolUseContext{Ctx: context.Background()},
		},
	})
	if err == nil {
		t.Fatal("no runner registered → expected error")
	}
}
