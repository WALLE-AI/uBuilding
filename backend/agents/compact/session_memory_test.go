package compact_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/wall-ai/ubuilding/backend/agents"
	"github.com/wall-ai/ubuilding/backend/agents/compact"
)

func TestSessionMemoryCompactConfig_DefaultsAndSet(t *testing.T) {
	compact.ResetSessionMemoryCompactConfig()
	defer compact.ResetSessionMemoryCompactConfig()

	cfg := compact.GetSessionMemoryCompactConfig()
	assert.Equal(t, compact.DefaultSessionMemoryCompactConfig, cfg)

	// Negative / zero values should not overwrite defaults.
	compact.SetSessionMemoryCompactConfig(compact.SessionMemoryCompactConfig{MinTokens: 0, MaxTokens: -1})
	assert.Equal(t, compact.DefaultSessionMemoryCompactConfig, compact.GetSessionMemoryCompactConfig())

	// Positive values override.
	compact.SetSessionMemoryCompactConfig(compact.SessionMemoryCompactConfig{MinTokens: 123, MaxTokens: 456, MinTextBlockMessages: 3})
	got := compact.GetSessionMemoryCompactConfig()
	assert.Equal(t, 123, got.MinTokens)
	assert.Equal(t, 456, got.MaxTokens)
	assert.Equal(t, 3, got.MinTextBlockMessages)
}

func TestHasTextBlocks(t *testing.T) {
	assert.False(t, compact.HasTextBlocks(agents.Message{Type: agents.MessageTypeUser}))
	assert.True(t, compact.HasTextBlocks(agents.Message{
		Type:    agents.MessageTypeUser,
		Content: []agents.ContentBlock{{Type: agents.ContentBlockText, Text: "hi"}},
	}))
	// Empty text should count as no text.
	assert.False(t, compact.HasTextBlocks(agents.Message{
		Type:    agents.MessageTypeAssistant,
		Content: []agents.ContentBlock{{Type: agents.ContentBlockText, Text: ""}},
	}))
}

func TestGetToolResultIDs(t *testing.T) {
	// Only user messages carry tool_result blocks.
	user := agents.Message{
		Type: agents.MessageTypeUser,
		Content: []agents.ContentBlock{
			{Type: agents.ContentBlockToolResult, ToolUseID: "t1"},
			{Type: agents.ContentBlockToolResult, ToolUseID: "t2"},
			{Type: agents.ContentBlockText, Text: "hi"},
		},
	}
	assert.ElementsMatch(t, []string{"t1", "t2"}, compact.GetToolResultIDs(user))

	// Assistant messages return nothing even when they contain tool_result-shaped blocks.
	assistant := agents.Message{
		Type:    agents.MessageTypeAssistant,
		Content: []agents.ContentBlock{{Type: agents.ContentBlockToolResult, ToolUseID: "x"}},
	}
	assert.Empty(t, compact.GetToolResultIDs(assistant))
}

func TestHasToolUseWithIDs(t *testing.T) {
	msg := agents.Message{
		Type: agents.MessageTypeAssistant,
		Content: []agents.ContentBlock{
			{Type: agents.ContentBlockToolUse, ID: "a"},
			{Type: agents.ContentBlockToolUse, ID: "b"},
		},
	}
	assert.True(t, compact.HasToolUseWithIDs(msg, map[string]struct{}{"b": {}}))
	assert.False(t, compact.HasToolUseWithIDs(msg, map[string]struct{}{"z": {}}))

	// User messages return false.
	u := agents.Message{Type: agents.MessageTypeUser}
	assert.False(t, compact.HasToolUseWithIDs(u, map[string]struct{}{"a": {}}))
}

func TestAdjustIndex_BoundsPassthrough(t *testing.T) {
	msgs := []agents.Message{{Type: agents.MessageTypeUser}}
	assert.Equal(t, 0, compact.AdjustIndexToPreserveAPIInvariants(msgs, 0))
	assert.Equal(t, 5, compact.AdjustIndexToPreserveAPIInvariants(msgs, 5))
}

func TestAdjustIndex_WidensForOrphanToolResult(t *testing.T) {
	// index 0: assistant with tool_use id=T1
	// index 1: assistant with tool_use id=T2
	// index 2: user with tool_result id=T1
	msgs := []agents.Message{
		{Type: agents.MessageTypeAssistant, Content: []agents.ContentBlock{{Type: agents.ContentBlockToolUse, ID: "T1"}}},
		{Type: agents.MessageTypeAssistant, Content: []agents.ContentBlock{{Type: agents.ContentBlockToolUse, ID: "T2"}}},
		{Type: agents.MessageTypeUser, Content: []agents.ContentBlock{{Type: agents.ContentBlockToolResult, ToolUseID: "T1"}}},
	}
	// Starting at 2 would orphan the tool_result for T1. Starting at 1 still orphans
	// (T1 tool_use is at index 0). Adjustment must widen back to 0.
	assert.Equal(t, 0, compact.AdjustIndexToPreserveAPIInvariants(msgs, 2))
	assert.Equal(t, 0, compact.AdjustIndexToPreserveAPIInvariants(msgs, 1))
}

func TestAdjustIndex_CoalescesSameUUIDAssistant(t *testing.T) {
	// Two assistant messages share a UUID (streaming split thinking + tool_use).
	msgs := []agents.Message{
		{Type: agents.MessageTypeAssistant, UUID: "X", Content: []agents.ContentBlock{{Type: agents.ContentBlockThinking, Thinking: "..."}}},
		{Type: agents.MessageTypeAssistant, UUID: "X", Content: []agents.ContentBlock{{Type: agents.ContentBlockText, Text: "hi"}}},
		{Type: agents.MessageTypeUser, Content: []agents.ContentBlock{{Type: agents.ContentBlockText, Text: "go"}}},
	}
	// Starting at 1 must widen to 0 to keep the thinking split-message.
	assert.Equal(t, 0, compact.AdjustIndexToPreserveAPIInvariants(msgs, 1))
}

func TestTruncateForCompact_KeepsAllWhenFewTextMessages(t *testing.T) {
	msgs := []agents.Message{
		{Type: agents.MessageTypeUser, Content: []agents.ContentBlock{{Type: agents.ContentBlockText, Text: "a"}}},
		{Type: agents.MessageTypeAssistant, Content: []agents.ContentBlock{{Type: agents.ContentBlockText, Text: "b"}}},
	}
	start, tail := compact.TruncateForCompact(msgs, compact.SessionMemoryCompactConfig{MinTextBlockMessages: 5})
	assert.Equal(t, 0, start)
	assert.Len(t, tail, 2)
}

func TestTruncateForCompact_HonorsMinTextBlockMessages(t *testing.T) {
	// 4 text-bearing messages; min=2 should select the last two.
	text := func(s string) agents.Message {
		return agents.Message{Type: agents.MessageTypeAssistant, Content: []agents.ContentBlock{{Type: agents.ContentBlockText, Text: s}}}
	}
	msgs := []agents.Message{text("1"), text("2"), text("3"), text("4")}
	start, tail := compact.TruncateForCompact(msgs, compact.SessionMemoryCompactConfig{MinTextBlockMessages: 2})
	assert.Equal(t, 2, start)
	assert.Len(t, tail, 2)
}
