package memory

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wall-ai/ubuilding/backend/agents"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func setupExtractEnv(t *testing.T) (string, string) {
	t.Helper()
	base := filepath.Join(t.TempDir(), "membase")
	cwd := t.TempDir()
	t.Setenv(EnvRemoteMemoryDir, base)
	t.Setenv(EnvEnableAutoMemory, "1")
	t.Setenv(EnvEnableExtractMemories, "1")
	t.Setenv(EnvEnableTeamMemory, "")
	t.Setenv(EnvDisableAutoMemory, "")
	ResetMemoryBaseDirCache()
	return base, cwd
}

func fakeMessages(texts ...string) []agents.Message {
	var msgs []agents.Message
	for i, txt := range texts {
		role := agents.MessageTypeUser
		if i%2 == 1 {
			role = agents.MessageTypeAssistant
		}
		msgs = append(msgs, agents.Message{
			UUID:    txt[:min(8, len(txt))],
			Type:    role,
			Content: []agents.ContentBlock{{Type: agents.ContentBlockText, Text: txt}},
		})
	}
	return msgs
}

// ---------------------------------------------------------------------------
// T1 · IsEnabled
// ---------------------------------------------------------------------------

func TestExtractMemoriesService_IsEnabled(t *testing.T) {
	_, cwd := setupExtractEnv(t)

	cfg := agents.EngineConfig{AutoMemoryEnabled: true}
	svc := NewExtractMemoriesService(cwd, NopSettingsProvider, cfg)
	assert.True(t, svc.IsEnabled())

	// Disable env → disabled.
	t.Setenv(EnvEnableExtractMemories, "")
	assert.False(t, svc.IsEnabled())
}

func TestExtractMemoriesService_DisabledWithoutAutoMemory(t *testing.T) {
	_, cwd := setupExtractEnv(t)
	t.Setenv(EnvEnableAutoMemory, "")
	t.Setenv(EnvDisableAutoMemory, "1")
	ResetMemoryBaseDirCache()

	cfg := agents.EngineConfig{AutoMemoryEnabled: false}
	svc := NewExtractMemoriesService(cwd, NopSettingsProvider, cfg)
	assert.False(t, svc.IsEnabled())
}

// ---------------------------------------------------------------------------
// T2 · OnTurnEnd overlap guard
// ---------------------------------------------------------------------------

func TestExtractMemoriesService_OverlapGuard(t *testing.T) {
	_, cwd := setupExtractEnv(t)
	cfg := agents.EngineConfig{AutoMemoryEnabled: true}
	svc := NewExtractMemoriesService(cwd, NopSettingsProvider, cfg)

	// Inject a SideQueryFn that blocks until released.
	release := make(chan struct{})
	var callCount int32
	old := DefaultSideQueryFn
	DefaultSideQueryFn = func(_ context.Context, _, _ string) (string, error) {
		atomic.AddInt32(&callCount, 1)
		<-release
		return `{"memories":[],"index_entries":[]}`, nil
	}
	defer func() { DefaultSideQueryFn = old }()

	msgs := fakeMessages("hello user", "hello assistant", "second user", "second assistant")

	// First call — starts background goroutine.
	svc.OnTurnEnd(msgs)
	time.Sleep(50 * time.Millisecond) // let goroutine start

	// Second call — should be skipped (overlap).
	svc.OnTurnEnd(msgs)
	time.Sleep(50 * time.Millisecond)

	close(release) // unblock
	time.Sleep(100 * time.Millisecond)

	assert.Equal(t, int32(1), atomic.LoadInt32(&callCount), "overlap guard should prevent concurrent extractions")
}

// ---------------------------------------------------------------------------
// T3 · Full extraction writes memory files
// ---------------------------------------------------------------------------

func TestExtractMemoriesService_WritesMemoryFiles(t *testing.T) {
	_, cwd := setupExtractEnv(t)
	cfg := agents.EngineConfig{AutoMemoryEnabled: true}
	svc := NewExtractMemoriesService(cwd, NopSettingsProvider, cfg)

	old := DefaultSideQueryFn
	DefaultSideQueryFn = func(_ context.Context, system, user string) (string, error) {
		assert.Contains(t, system, "memory extraction agent")
		return `{
			"memories": [
				{
					"filename": "user_prefs.md",
					"action": "create",
					"frontmatter": {"name": "User Preferences", "description": "User likes Go", "type": "user"},
					"content": "The user prefers Go over Python for backend work."
				}
			],
			"index_entries": ["- [User Preferences](user_prefs.md) — prefers Go"]
		}`, nil
	}
	defer func() { DefaultSideQueryFn = old }()

	msgs := fakeMessages("I prefer Go", "Got it, you prefer Go for backend work", "yes exactly", "noted")

	svc.OnTurnEnd(msgs)

	// Wait for async extraction.
	require.Eventually(t, func() bool {
		svc.mu.Lock()
		defer svc.mu.Unlock()
		return !svc.inProgress
	}, 5*time.Second, 50*time.Millisecond, "extraction should complete")

	// Check memory file written.
	memDir := GetAutoMemPath(cwd, NopSettingsProvider)
	require.NotEmpty(t, memDir)

	data, err := os.ReadFile(filepath.Join(memDir, "user_prefs.md"))
	require.NoError(t, err)
	assert.Contains(t, string(data), "name: User Preferences")
	assert.Contains(t, string(data), "type: user")
	assert.Contains(t, string(data), "prefers Go")

	// Check MEMORY.md index updated.
	idx, err := os.ReadFile(filepath.Join(memDir, autoMemEntrypoint))
	require.NoError(t, err)
	assert.Contains(t, string(idx), "User Preferences")
}

// ---------------------------------------------------------------------------
// T4 · Extraction skipped when conversation already wrote to memory
// ---------------------------------------------------------------------------

func TestExtractMemoriesService_SkipsWhenConversationWroteMemory(t *testing.T) {
	_, cwd := setupExtractEnv(t)
	cfg := agents.EngineConfig{AutoMemoryEnabled: true}
	svc := NewExtractMemoriesService(cwd, NopSettingsProvider, cfg)

	memDir := GetAutoMemPath(cwd, NopSettingsProvider)
	require.NotEmpty(t, memDir)

	var called int32
	old := DefaultSideQueryFn
	DefaultSideQueryFn = func(_ context.Context, _, _ string) (string, error) {
		atomic.AddInt32(&called, 1)
		return `{"memories":[],"index_entries":[]}`, nil
	}
	defer func() { DefaultSideQueryFn = old }()

	// Simulate assistant writing to memory dir via tool_use.
	inputJSON := []byte(`{"file_path":"` + strings.ReplaceAll(filepath.Join(memDir, "note.md"), `\`, `\\`) + `"}`)
	msgs := []agents.Message{
		{UUID: "u1", Type: agents.MessageTypeUser, Content: []agents.ContentBlock{
			{Type: agents.ContentBlockText, Text: "remember this"},
		}},
		{UUID: "a1", Type: agents.MessageTypeAssistant, Content: []agents.ContentBlock{
			{Type: agents.ContentBlockToolUse, Name: "Write", Input: inputJSON},
		}},
		{UUID: "u2", Type: agents.MessageTypeUser, Content: []agents.ContentBlock{
			{Type: agents.ContentBlockText, Text: "thanks"},
		}},
		{UUID: "a2", Type: agents.MessageTypeAssistant, Content: []agents.ContentBlock{
			{Type: agents.ContentBlockText, Text: "done"},
		}},
	}

	svc.OnTurnEnd(msgs)
	time.Sleep(200 * time.Millisecond)

	// SideQueryFn should NOT have been called because the conversation
	// itself wrote to the memory directory.
	assert.Equal(t, int32(0), atomic.LoadInt32(&called),
		"extraction should be skipped when conversation already wrote to memory dir")
}

// ---------------------------------------------------------------------------
// T5 · Empty response → no files
// ---------------------------------------------------------------------------

func TestExtractMemoriesService_EmptyResponseNoFiles(t *testing.T) {
	_, cwd := setupExtractEnv(t)
	cfg := agents.EngineConfig{AutoMemoryEnabled: true}
	svc := NewExtractMemoriesService(cwd, NopSettingsProvider, cfg)

	old := DefaultSideQueryFn
	DefaultSideQueryFn = func(_ context.Context, _, _ string) (string, error) {
		return `{"memories":[],"index_entries":[]}`, nil
	}
	defer func() { DefaultSideQueryFn = old }()

	msgs := fakeMessages("hi", "hello")
	svc.OnTurnEnd(msgs)

	require.Eventually(t, func() bool {
		svc.mu.Lock()
		defer svc.mu.Unlock()
		return !svc.inProgress
	}, 3*time.Second, 50*time.Millisecond)

	memDir := GetAutoMemPath(cwd, NopSettingsProvider)
	entries, _ := os.ReadDir(memDir)
	// Only MEMORY.md (if pre-created) or nothing — no topic files.
	for _, e := range entries {
		if e.Name() != autoMemEntrypoint {
			t.Errorf("unexpected file: %s", e.Name())
		}
	}
}

// ---------------------------------------------------------------------------
// T6 · extractJSON helper
// ---------------------------------------------------------------------------

func TestExtractJSON_CodeFence(t *testing.T) {
	input := "Here is the result:\n```json\n{\"a\": 1}\n```\nDone."
	assert.Equal(t, `{"a": 1}`, extractJSON(input))
}

func TestExtractJSON_Raw(t *testing.T) {
	input := `{"memories": []}`
	assert.Equal(t, `{"memories": []}`, extractJSON(input))
}

func TestExtractJSON_Empty(t *testing.T) {
	assert.Equal(t, "", extractJSON("no json here"))
}

// ---------------------------------------------------------------------------
// T7 · countModelVisibleMessagesSince
// ---------------------------------------------------------------------------

func TestCountModelVisibleMessagesSince(t *testing.T) {
	msgs := fakeMessages("u1", "a1", "u2", "a2")

	// From start (empty cursor).
	assert.Equal(t, 4, countModelVisibleMessagesSince(msgs, ""))

	// After first message.
	assert.Equal(t, 3, countModelVisibleMessagesSince(msgs, msgs[0].UUID))

	// After last message.
	assert.Equal(t, 0, countModelVisibleMessagesSince(msgs, msgs[3].UUID))

	// Unknown UUID → fallback to all.
	assert.Equal(t, 4, countModelVisibleMessagesSince(msgs, "unknown"))
}

// ---------------------------------------------------------------------------
// T8 · BuildExtractAutoOnlyPrompt sanity
// ---------------------------------------------------------------------------

func TestBuildExtractAutoOnlyPrompt_ContainsSections(t *testing.T) {
	prompt := BuildExtractAutoOnlyPrompt(5, "manifest here", false)
	must := []string{
		"~5 messages",
		"manifest here",
		"Types of memory",
		"What NOT to save",
	}
	for _, s := range must {
		if !strings.Contains(prompt, s) {
			t.Errorf("prompt missing %q", s)
		}
	}
}

// ---------------------------------------------------------------------------
// T9 · BuildAssistantDailyLogPrompt
// ---------------------------------------------------------------------------

func TestBuildAssistantDailyLogPrompt_ContainsSections(t *testing.T) {
	prompt := BuildAssistantDailyLogPrompt("/mem/dir", false)
	must := []string{
		"# auto memory",
		"`/mem/dir`",
		"YYYY-MM-DD.md",
		"append-only",
		autoMemEntrypoint,
		"What to log",
	}
	for _, s := range must {
		if !strings.Contains(prompt, s) {
			t.Errorf("daily log prompt missing %q", s)
		}
	}
}

func TestBuildAssistantDailyLogPrompt_SkipIndex(t *testing.T) {
	prompt := BuildAssistantDailyLogPrompt("/mem/dir", true)
	// skipIndex omits the dedicated MEMORY.md orientation section.
	if strings.Contains(prompt, "## "+autoMemEntrypoint) {
		t.Error("skipIndex=true should omit MEMORY.md heading section")
	}
}

// ---------------------------------------------------------------------------
// T10 · LoadMemoryPrompt daily-log branch
// ---------------------------------------------------------------------------

func TestLoadMemoryPrompt_DailyLogMode(t *testing.T) {
	ResetMemoryBaseDirCache()
	base := filepath.Join(t.TempDir(), "base")
	t.Setenv(EnvRemoteMemoryDir, base)
	t.Setenv(EnvEnableAutoMemory, "1")
	t.Setenv(EnvEnableDailyLog, "1")
	t.Setenv(EnvEnableTeamMemory, "")
	t.Setenv(EnvDisableAutoMemory, "")

	cwd := t.TempDir()
	prompt, err := LoadMemoryPrompt(context.Background(), cwd,
		agents.EngineConfig{AutoMemoryEnabled: true}, NopSettingsProvider)
	require.NoError(t, err)
	assert.NotEmpty(t, prompt)
	assert.Contains(t, prompt, "# auto memory")
	assert.Contains(t, prompt, "YYYY-MM-DD.md")
	// Should NOT contain combined/team headings.
	assert.NotContains(t, prompt, "## Memory scope")
}
