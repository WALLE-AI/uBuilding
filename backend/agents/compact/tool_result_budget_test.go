package compact_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wall-ai/ubuilding/backend/agents"
	"github.com/wall-ai/ubuilding/backend/agents/compact"
)

func makeUserMsgWithToolResult(toolUseID, content string) agents.Message {
	return agents.Message{
		Type: agents.MessageTypeUser,
		Content: []agents.ContentBlock{{
			Type:      agents.ContentBlockToolResult,
			ToolUseID: toolUseID,
			Content:   content,
		}},
	}
}

func makeAssistantMsg(uuid string, toolUses ...string) agents.Message {
	var blocks []agents.ContentBlock
	for _, name := range toolUses {
		blocks = append(blocks, agents.ContentBlock{
			Type: agents.ContentBlockToolUse,
			ID:   "tu-" + name,
			Name: name,
		})
	}
	return agents.Message{
		Type:    agents.MessageTypeAssistant,
		UUID:    uuid,
		Content: blocks,
	}
}

func TestEnforceToolResultBudget_NilState(t *testing.T) {
	msgs := []agents.Message{makeUserMsgWithToolResult("t1", "hello")}
	result := compact.EnforceToolResultBudget(msgs, nil, 100, nil, nil)
	assert.Equal(t, msgs, result.Messages)
	assert.Empty(t, result.NewlyReplaced)
}

func TestEnforceToolResultBudget_UnderBudget(t *testing.T) {
	state := compact.NewContentReplacementState()
	msgs := []agents.Message{
		makeAssistantMsg("a1", "read"),
		makeUserMsgWithToolResult("tu-read", "small content"),
	}
	result := compact.EnforceToolResultBudget(msgs, state, 1000, nil, nil)
	assert.Equal(t, msgs, result.Messages)
	assert.Empty(t, result.NewlyReplaced)
}

func TestEnforceToolResultBudget_OverBudget_Truncates(t *testing.T) {
	state := compact.NewContentReplacementState()
	// Create a message with large tool result (10K chars, budget = 5K)
	largeContent := strings.Repeat("x", 10000)
	msgs := []agents.Message{
		makeAssistantMsg("a1", "bash"),
		makeUserMsgWithToolResult("tu-bash", largeContent),
	}
	result := compact.EnforceToolResultBudget(msgs, state, 5000, nil, nil)

	require.Len(t, result.NewlyReplaced, 1)
	assert.Equal(t, "tu-bash", result.NewlyReplaced[0].ToolUseID)
	assert.Equal(t, "tool-result", result.NewlyReplaced[0].Kind)

	// Verify the content was replaced with a preview
	replaced := result.Messages[1].Content[0].Content.(string)
	assert.True(t, strings.HasPrefix(replaced, compact.PersistedOutputTag))
	assert.True(t, strings.Contains(replaced, compact.PersistedOutputClosingTag))
}

func TestEnforceToolResultBudget_ReapplyCached(t *testing.T) {
	state := compact.NewContentReplacementState()
	largeContent := strings.Repeat("y", 10000)
	msgs := []agents.Message{
		makeAssistantMsg("a1", "bash"),
		makeUserMsgWithToolResult("tu-bash", largeContent),
	}

	// First enforcement — creates replacement
	result1 := compact.EnforceToolResultBudget(msgs, state, 5000, nil, nil)
	require.Len(t, result1.NewlyReplaced, 1)

	// Second enforcement — should re-apply cached (0 newly replaced)
	result2 := compact.EnforceToolResultBudget(msgs, state, 5000, nil, nil)
	assert.Empty(t, result2.NewlyReplaced)
	assert.Equal(t, 1, result2.ReappliedCount)

	// Content should be identical between runs
	content1 := result1.Messages[1].Content[0].Content.(string)
	content2 := result2.Messages[1].Content[0].Content.(string)
	assert.Equal(t, content1, content2)
}

func TestEnforceToolResultBudget_FrozenResults(t *testing.T) {
	state := compact.NewContentReplacementState()
	// First message: small result (under budget) — gets seen but not replaced
	msgs := []agents.Message{
		makeAssistantMsg("a1", "read"),
		makeUserMsgWithToolResult("tu-read", "small"),
	}
	compact.EnforceToolResultBudget(msgs, state, 1000, nil, nil)

	// Now add a large result in a new message group — the old one is frozen
	msgs = []agents.Message{
		makeAssistantMsg("a1", "read"),
		makeUserMsgWithToolResult("tu-read", "small"),
		makeAssistantMsg("a2", "bash"),
		makeUserMsgWithToolResult("tu-bash", strings.Repeat("z", 10000)),
	}
	result := compact.EnforceToolResultBudget(msgs, state, 5000, nil, nil)

	// Only bash should be truncated (in its own message group), read stays frozen
	require.Len(t, result.NewlyReplaced, 1)
	assert.Equal(t, "tu-bash", result.NewlyReplaced[0].ToolUseID)
}

func TestEnforceToolResultBudget_SkipToolNames(t *testing.T) {
	state := compact.NewContentReplacementState()
	largeContent := strings.Repeat("w", 10000)
	msgs := []agents.Message{
		makeAssistantMsg("a1", "Read"),
		makeUserMsgWithToolResult("tu-Read", largeContent),
	}

	skip := map[string]bool{"Read": true}
	result := compact.EnforceToolResultBudget(msgs, state, 5000, skip, nil)

	// Read tool should be skipped — no replacement
	assert.Empty(t, result.NewlyReplaced)
}

func TestApplyToolResultBudget_CallsTranscriptWriter(t *testing.T) {
	state := compact.NewContentReplacementState()
	largeContent := strings.Repeat("v", 10000)
	msgs := []agents.Message{
		makeAssistantMsg("a1", "bash"),
		makeUserMsgWithToolResult("tu-bash", largeContent),
	}

	var written []compact.ToolResultReplacementRecord
	result := compact.ApplyToolResultBudget(msgs, state, 5000, nil, func(records []compact.ToolResultReplacementRecord) {
		written = records
	}, nil)

	require.Len(t, written, 1)
	assert.Equal(t, "tu-bash", written[0].ToolUseID)

	// Verify messages were modified
	replaced := result[1].Content[0].Content.(string)
	assert.True(t, strings.HasPrefix(replaced, compact.PersistedOutputTag))
}

func TestCloneContentReplacementState(t *testing.T) {
	src := compact.NewContentReplacementState()
	// Force some state via enforcement
	largeContent := strings.Repeat("a", 10000)
	msgs := []agents.Message{
		makeAssistantMsg("a1", "bash"),
		makeUserMsgWithToolResult("tu-bash", largeContent),
	}
	compact.EnforceToolResultBudget(msgs, src, 5000, nil, nil)

	// Clone
	dst := compact.CloneContentReplacementState(src)
	assert.NotNil(t, dst)

	// Mutate dst — should not affect src
	compact.EnforceToolResultBudget([]agents.Message{
		makeAssistantMsg("a2", "write"),
		makeUserMsgWithToolResult("tu-write", strings.Repeat("b", 10000)),
	}, dst, 5000, nil, nil)

	// src should still have only 1 replacement from before
	result := compact.EnforceToolResultBudget(msgs, src, 5000, nil, nil)
	assert.Empty(t, result.NewlyReplaced)       // all seen
	assert.Equal(t, 1, result.ReappliedCount) // only the original bash
}
