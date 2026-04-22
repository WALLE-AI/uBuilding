package session_memory

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wall-ai/ubuilding/backend/agents"
	"github.com/wall-ai/ubuilding/backend/agents/compact"
)

func TestCompactWithSessionMemory_MissingFile(t *testing.T) {
	idx, content := CompactWithSessionMemory(
		nil, filepath.Join(t.TempDir(), "nope.md"), "", nil,
	)
	assert.Equal(t, 0, idx)
	assert.Empty(t, content)
}

func TestCompactWithSessionMemory_EmptyTemplate(t *testing.T) {
	dir := t.TempDir()
	notesPath := filepath.Join(dir, "notes.md")
	require.NoError(t, os.WriteFile(notesPath, []byte(DefaultSessionMemoryTemplate), 0o644))

	idx, content := CompactWithSessionMemory(nil, notesPath, "", nil)
	assert.Equal(t, 0, idx)
	assert.Empty(t, content, "empty template should fall back")
}

func TestCompactWithSessionMemory_WithContent(t *testing.T) {
	compact.ResetSessionMemoryCompactConfig()

	dir := t.TempDir()
	notesPath := filepath.Join(dir, "notes.md")
	notes := DefaultSessionMemoryTemplate + "\nActual extracted content here"
	require.NoError(t, os.WriteFile(notesPath, []byte(notes), 0o644))

	// Create enough messages for TruncateForCompact to work with.
	var msgs []agents.Message
	for i := 0; i < 10; i++ {
		msgs = append(msgs, agents.Message{
			Type:    agents.MessageTypeUser,
			UUID:    "u" + string(rune('0'+i)),
			Content: []agents.ContentBlock{{Type: agents.ContentBlockText, Text: "msg"}},
		})
	}

	sm := NewSessionStateManager()
	sm.MarkInitialized()

	idx, content := CompactWithSessionMemory(msgs, notesPath, "", sm)
	assert.NotEmpty(t, content, "should return session content")
	assert.GreaterOrEqual(t, idx, 0)
	assert.Contains(t, content, "Actual extracted content here")
}

func TestShouldUseSessionMemoryCompact_Disabled(t *testing.T) {
	cfg := agents.EngineConfig{SessionMemoryEnabled: false}
	assert.False(t, ShouldUseSessionMemoryCompact(cfg, "", "", nil))
}

func TestShouldUseSessionMemoryCompact_NotInitialized(t *testing.T) {
	cfg := agents.EngineConfig{SessionMemoryEnabled: true}
	t.Setenv(EnvEnableSessionMemory, "1")

	sm := NewSessionStateManager() // not initialized
	assert.False(t, ShouldUseSessionMemoryCompact(cfg, "", "", sm))
}

func TestShouldUseSessionMemoryCompact_EmptyNotes(t *testing.T) {
	cfg := agents.EngineConfig{SessionMemoryEnabled: true}
	t.Setenv(EnvEnableSessionMemory, "1")

	dir := t.TempDir()
	notesPath := filepath.Join(dir, "notes.md")
	require.NoError(t, os.WriteFile(notesPath, []byte(DefaultSessionMemoryTemplate), 0o644))

	sm := NewSessionStateManager()
	sm.MarkInitialized()

	assert.False(t, ShouldUseSessionMemoryCompact(cfg, notesPath, "", sm))
}

func TestShouldUseSessionMemoryCompact_WithContent(t *testing.T) {
	cfg := agents.EngineConfig{SessionMemoryEnabled: true}
	t.Setenv(EnvEnableSessionMemory, "1")

	dir := t.TempDir()
	notesPath := filepath.Join(dir, "notes.md")
	notes := DefaultSessionMemoryTemplate + "\nReal content"
	require.NoError(t, os.WriteFile(notesPath, []byte(notes), 0o644))

	sm := NewSessionStateManager()
	sm.MarkInitialized()

	assert.True(t, ShouldUseSessionMemoryCompact(cfg, notesPath, "", sm))
}
