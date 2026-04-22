package session_memory

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wall-ai/ubuilding/backend/agents"
)

// ---------------------------------------------------------------------------
// M11.T1 · SessionMemory extractor tests.
// ---------------------------------------------------------------------------

func makeTextMsg(msgType agents.MessageType, uuid, text string) agents.Message {
	return agents.Message{
		Type: msgType,
		UUID: uuid,
		Content: []agents.ContentBlock{
			{Type: agents.ContentBlockText, Text: text},
		},
	}
}

func makeToolUseMsg(uuid string) agents.Message {
	return agents.Message{
		Type: agents.MessageTypeAssistant,
		UUID: uuid,
		Content: []agents.ContentBlock{
			{Type: agents.ContentBlockToolUse, Name: "read_file"},
		},
	}
}

func TestShouldExtractMemory_BelowInitThreshold(t *testing.T) {
	ResetSessionMemoryConfig()
	ext := NewSessionMemoryExtractor(agents.EngineConfig{}, nil, "", "", nil)

	msgs := []agents.Message{
		makeTextMsg(agents.MessageTypeUser, "u1", "hello"),
		makeTextMsg(agents.MessageTypeAssistant, "a1", "hi there"),
	}
	assert.False(t, ext.ShouldExtractMemory(msgs, 5000),
		"below init threshold should not extract")
}

func TestShouldExtractMemory_InitAndUpdate(t *testing.T) {
	ResetSessionMemoryConfig()
	SetSessionMemoryConfig(SessionMemoryConfig{
		MinimumMessageTokensToInit: 100,
		MinimumTokensBetweenUpdate: 50,
		ToolCallsBetweenUpdates:    1,
	})
	defer ResetSessionMemoryConfig()

	ext := NewSessionMemoryExtractor(agents.EngineConfig{}, nil, "", "", nil)

	msgs := []agents.Message{
		makeTextMsg(agents.MessageTypeUser, "u1", "hello"),
		makeToolUseMsg("a1"),
		makeTextMsg(agents.MessageTypeAssistant, "a2", "done"),
	}

	// First call — init threshold met.
	assert.True(t, ext.ShouldExtractMemory(msgs, 200),
		"should extract on first init")

	// Simulate recording extraction token count (as Extract() would do).
	ext.State().RecordExtractionTokenCount(200)

	// Second call — token threshold not met yet.
	assert.False(t, ext.ShouldExtractMemory(msgs, 210),
		"should not extract when token delta is below threshold")

	// Third call — token threshold met + tool calls present.
	msgs = append(msgs, makeToolUseMsg("a3"))
	assert.True(t, ext.ShouldExtractMemory(msgs, 300),
		"should extract when both thresholds met")
}

func TestShouldExtractMemory_ExtractAtNaturalBreak(t *testing.T) {
	ResetSessionMemoryConfig()
	SetSessionMemoryConfig(SessionMemoryConfig{
		MinimumMessageTokensToInit: 100,
		MinimumTokensBetweenUpdate: 50,
		ToolCallsBetweenUpdates:    10, // high so tool-call gate alone doesn't pass
	})
	defer ResetSessionMemoryConfig()

	ext := NewSessionMemoryExtractor(agents.EngineConfig{}, nil, "", "", nil)

	// First call to initialize.
	msgs := []agents.Message{
		makeTextMsg(agents.MessageTypeUser, "u1", "hello"),
		makeTextMsg(agents.MessageTypeAssistant, "a1", "text only — no tool calls"),
	}
	assert.True(t, ext.ShouldExtractMemory(msgs, 200),
		"should extract at natural break (no tool calls in last turn)")
}

func TestExtract_WritesNotes(t *testing.T) {
	dir := t.TempDir()
	notesPath := filepath.Join(dir, "notes.md")

	mockSideQuery := func(_ context.Context, system, user string) (string, error) {
		return "# Updated Notes\nSome extracted content", nil
	}

	ext := NewSessionMemoryExtractor(
		agents.EngineConfig{},
		mockSideQuery,
		notesPath,
		"",
		nil,
	)

	msgs := []agents.Message{
		makeTextMsg(agents.MessageTypeUser, "u1", "hello"),
		makeTextMsg(agents.MessageTypeAssistant, "a1", "hi"),
	}

	err := ext.Extract(context.Background(), msgs, 15000)
	require.NoError(t, err)

	content, readErr := os.ReadFile(notesPath)
	require.NoError(t, readErr)
	assert.Contains(t, string(content), "Updated Notes")
}

func TestExtract_CreatesNotesFileIfMissing(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "subdir")
	notesPath := filepath.Join(dir, "notes.md")

	mockSideQuery := func(_ context.Context, system, user string) (string, error) {
		return "extracted", nil
	}

	ext := NewSessionMemoryExtractor(agents.EngineConfig{}, mockSideQuery, notesPath, "", nil)
	err := ext.Extract(context.Background(), nil, 10000)
	require.NoError(t, err)

	_, statErr := os.Stat(notesPath)
	assert.NoError(t, statErr, "notes file should exist")
}

func TestExtract_SkipsIfAlreadyRunning(t *testing.T) {
	ext := NewSessionMemoryExtractor(agents.EngineConfig{}, nil, "", "", nil)
	ext.state.MarkExtractionStarted()

	err := ext.Extract(context.Background(), nil, 0)
	assert.NoError(t, err, "should silently skip")
}

func TestCountToolCallsSince(t *testing.T) {
	msgs := []agents.Message{
		makeTextMsg(agents.MessageTypeUser, "u1", "hello"),
		makeToolUseMsg("a1"),
		makeToolUseMsg("a2"),
	}

	assert.Equal(t, 2, countToolCallsSince(msgs, ""))
	assert.Equal(t, 1, countToolCallsSince(msgs, "a1"))
	assert.Equal(t, 0, countToolCallsSince(msgs, "a2"))
}

func TestHasToolCallsInLastAssistantTurn(t *testing.T) {
	msgsWithTool := []agents.Message{
		makeTextMsg(agents.MessageTypeUser, "u1", "do something"),
		makeToolUseMsg("a1"),
	}
	assert.True(t, hasToolCallsInLastAssistantTurn(msgsWithTool))

	msgsWithoutTool := []agents.Message{
		makeTextMsg(agents.MessageTypeUser, "u1", "hello"),
		makeTextMsg(agents.MessageTypeAssistant, "a1", "hi there"),
	}
	assert.False(t, hasToolCallsInLastAssistantTurn(msgsWithoutTool))

	assert.False(t, hasToolCallsInLastAssistantTurn(nil))
}

func TestLastMessageUUID(t *testing.T) {
	assert.Empty(t, lastMessageUUID(nil))
	msgs := []agents.Message{
		makeTextMsg(agents.MessageTypeUser, "u1", "a"),
		makeTextMsg(agents.MessageTypeAssistant, "a1", "b"),
	}
	assert.Equal(t, "a1", lastMessageUUID(msgs))
}

func TestBuildConversationContext(t *testing.T) {
	msgs := []agents.Message{
		makeTextMsg(agents.MessageTypeUser, "u1", "first message"),
		makeTextMsg(agents.MessageTypeAssistant, "a1", "first reply"),
		makeTextMsg(agents.MessageTypeUser, "u2", "second message"),
	}

	ctx := buildConversationContext(msgs, "")
	assert.Contains(t, ctx, "first message")
	assert.Contains(t, ctx, "second message")

	ctxSince := buildConversationContext(msgs, "a1")
	assert.NotContains(t, ctxSince, "first message")
	assert.Contains(t, ctxSince, "second message")
}

func TestBuildConversationContext_Empty(t *testing.T) {
	ctx := buildConversationContext(nil, "")
	assert.Equal(t, "(no recent messages)", ctx)
}
