package compact

import (
	"context"
	"strings"
	"testing"

	"github.com/wall-ai/ubuilding/backend/agents"
)

func TestContextCollapser_NothingToCollapse(t *testing.T) {
	cc := NewContextCollapser(nil)
	msgs := []agents.Message{
		{Type: agents.MessageTypeUser, Content: []agents.ContentBlock{{Type: agents.ContentBlockText, Text: "hello"}}},
	}
	result := cc.ApplyCollapsesIfNeeded(context.Background(), msgs, nil, "")
	if result.Applied {
		t.Error("expected no collapse applied")
	}
	if len(result.Messages) != 1 {
		t.Errorf("expected 1 message, got %d", len(result.Messages))
	}
}

func TestContextCollapser_StagesLargeToolResult(t *testing.T) {
	cc := NewContextCollapser(nil)
	cc.CollapseThresholdChars = 50

	bigContent := strings.Repeat("x", 200)
	msgs := []agents.Message{
		{Type: agents.MessageTypeUser, Content: []agents.ContentBlock{
			{Type: agents.ContentBlockToolResult, ToolUseID: "t1", Content: bigContent},
		}},
		{Type: agents.MessageTypeAssistant, Content: []agents.ContentBlock{
			{Type: agents.ContentBlockText, Text: "ok"},
		}},
		// Current turn user message — should not be collapsed
		{Type: agents.MessageTypeUser, Content: []agents.ContentBlock{
			{Type: agents.ContentBlockText, Text: "next"},
		}},
	}

	// First call: stages but doesn't apply (only stages, first message not the last)
	result := cc.ApplyCollapsesIfNeeded(context.Background(), msgs, nil, "")
	if len(cc.StagedCollapses) != 1 {
		t.Fatalf("expected 1 staged collapse, got %d", len(cc.StagedCollapses))
	}

	// Second call with the same messages — now should apply the staged collapse
	result = cc.ApplyCollapsesIfNeeded(context.Background(), msgs, nil, "")
	if !result.Applied {
		t.Error("expected collapse applied on second call")
	}

	// First message's tool result should be collapsed
	contentStr, ok := result.Messages[0].Content[0].Content.(string)
	if !ok {
		t.Fatal("expected string content")
	}
	if !strings.HasPrefix(contentStr, "[Collapsed]") {
		t.Errorf("expected collapsed prefix, got %q", contentStr[:30])
	}
}

func TestContextCollapser_RecoverFromOverflow(t *testing.T) {
	cc := NewContextCollapser(nil)
	cc.CollapseThresholdChars = 10

	bigContent := strings.Repeat("y", 100)
	msgs := []agents.Message{
		{Type: agents.MessageTypeUser, Content: []agents.ContentBlock{
			{Type: agents.ContentBlockToolResult, ToolUseID: "t1", Content: bigContent},
		}},
	}

	// Stage first
	cc.ApplyCollapsesIfNeeded(context.Background(), msgs, nil, "")
	if len(cc.StagedCollapses) == 0 {
		t.Fatal("expected staged collapses")
	}

	// Drain
	drainResult := cc.RecoverFromOverflow(msgs, "")
	if drainResult.Committed == 0 {
		t.Error("expected at least 1 committed collapse")
	}
	if len(cc.StagedCollapses) != 0 {
		t.Error("expected staged collapses to be cleared after drain")
	}
}

func TestContextCollapser_IsWithheldPromptTooLong(t *testing.T) {
	cc := NewContextCollapser(nil)
	if cc.IsWithheldPromptTooLong() {
		t.Error("expected false with no staged collapses")
	}
	cc.StagedCollapses = append(cc.StagedCollapses, StagedCollapse{})
	if !cc.IsWithheldPromptTooLong() {
		t.Error("expected true with staged collapses")
	}
}

func TestContextCollapser_Disabled(t *testing.T) {
	cc := NewContextCollapser(nil)
	cc.Enabled = false
	msgs := []agents.Message{
		{Type: agents.MessageTypeUser, Content: []agents.ContentBlock{
			{Type: agents.ContentBlockToolResult, ToolUseID: "t1", Content: strings.Repeat("z", 5000)},
		}},
	}
	result := cc.ApplyCollapsesIfNeeded(context.Background(), msgs, nil, "")
	if result.Applied {
		t.Error("expected no collapse when disabled")
	}
}
