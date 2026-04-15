package compact

import (
	"testing"

	"github.com/wall-ai/ubuilding/backend/agents"
)

func TestSnipCompactor_BelowThreshold(t *testing.T) {
	sc := NewSnipCompactor()
	msgs := []agents.Message{
		{Type: agents.MessageTypeUser, Content: []agents.ContentBlock{{Type: agents.ContentBlockText, Text: "hello"}}},
		{Type: agents.MessageTypeAssistant, Content: []agents.ContentBlock{{Type: agents.ContentBlockText, Text: "world"}}},
	}
	result := sc.SnipIfNeeded(msgs)
	if result.TokensFreed != 0 {
		t.Errorf("expected no tokens freed, got %d", result.TokensFreed)
	}
	if len(result.Messages) != 2 {
		t.Errorf("expected 2 messages, got %d", len(result.Messages))
	}
}

func TestSnipCompactor_ExceedsThreshold(t *testing.T) {
	sc := &SnipCompactor{
		PreserveCount:    2,
		MaxHistoryTokens: 10, // very low threshold to force snip
	}

	// Build messages that exceed the threshold
	bigContent := make([]byte, 200) // 200 chars ≈ 50 tokens > 10
	for i := range bigContent {
		bigContent[i] = 'a'
	}

	msgs := []agents.Message{
		{Type: agents.MessageTypeUser, Content: []agents.ContentBlock{{Type: agents.ContentBlockText, Text: string(bigContent)}}},
		{Type: agents.MessageTypeAssistant, Content: []agents.ContentBlock{{Type: agents.ContentBlockText, Text: "response1"}}},
		{Type: agents.MessageTypeUser, Content: []agents.ContentBlock{{Type: agents.ContentBlockText, Text: "followup"}}},
		{Type: agents.MessageTypeAssistant, Content: []agents.ContentBlock{{Type: agents.ContentBlockText, Text: "response2"}}},
	}

	result := sc.SnipIfNeeded(msgs)
	if result.TokensFreed == 0 {
		t.Error("expected tokens freed > 0")
	}
	if result.BoundaryMessage == nil {
		t.Error("expected boundary message")
	}
	if result.BoundaryMessage.Subtype != "snip_boundary" {
		t.Errorf("expected snip_boundary subtype, got %q", result.BoundaryMessage.Subtype)
	}
	// Should have fewer messages than original
	if len(result.Messages) >= len(msgs) {
		t.Errorf("expected fewer messages after snip, got %d vs original %d", len(result.Messages), len(msgs))
	}
}

func TestSnipCompactor_TooFewMessages(t *testing.T) {
	sc := &SnipCompactor{
		PreserveCount:    10,
		MaxHistoryTokens: 1, // force threshold exceeded
	}
	msgs := []agents.Message{
		{Type: agents.MessageTypeUser, Content: []agents.ContentBlock{{Type: agents.ContentBlockText, Text: "hello"}}},
	}
	result := sc.SnipIfNeeded(msgs)
	if result.TokensFreed != 0 {
		t.Errorf("expected no tokens freed for too few messages, got %d", result.TokensFreed)
	}
}
