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

	msgs1 := fakeMessages("hello user", "hello assistant", "second user", "second assistant")

	// First call — starts background goroutine.
	svc.OnTurnEnd(msgs1)
	time.Sleep(50 * time.Millisecond) // let goroutine start

	// Second call — queued as trailing run with NEW messages (different UUIDs).
	msgs2 := fakeMessages("third user!!", "third assistant", "fourth user!!", "fourth assistant")
	svc.OnTurnEnd(msgs2)
	time.Sleep(50 * time.Millisecond)

	// At this point only 1 call should be in progress.
	assert.Equal(t, int32(1), atomic.LoadInt32(&callCount), "only one concurrent extraction")

	close(release) // unblock — trailing run will execute next

	require.Eventually(t, func() bool {
		return atomic.LoadInt32(&callCount) >= 2
	}, 3*time.Second, 50*time.Millisecond, "trailing run should fire")
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

func TestExtractJSON_ThinkBlock(t *testing.T) {
	input := `<think>用户告诉我名字是张三</think>{"memories":[],"index_entries":[]}`
	assert.Equal(t, `{"memories":[],"index_entries":[]}`, extractJSON(input))
}

func TestExtractJSON_ThinkBlockOnly(t *testing.T) {
	input := `<think>这里面没有JSON</think>`
	assert.Equal(t, "", extractJSON(input))
}

func TestExtractJSON_UnclosedThink(t *testing.T) {
	input := `<think>LLM只返回了推理过程`
	assert.Equal(t, "", extractJSON(input))
}

func TestExtractJSON_TrailingTextAfterJSON(t *testing.T) {
	input := `{"memories": [], "index_entries": []}."` + "\n\n这条消息中没有什么需要记忆的"
	assert.Equal(t, `{"memories": [], "index_entries": []}`, extractJSON(input))
}

func TestExtractJSON_ThinkThenCodeFence(t *testing.T) {
	input := "<think>reasoning</think>\n```json\n{\"memories\":[{\"filename\":\"test.md\"}]}\n```"
	assert.Equal(t, `{"memories":[{"filename":"test.md"}]}`, extractJSON(input))
}

func TestExtractJSON_JSONInsideThinkBlock(t *testing.T) {
	// Model puts JSON inside the think block — fallback to original text.
	input := `<think>分析完毕，返回结果：{"memories":[{"filename":"user.md","action":"create","frontmatter":{},"content":"test"}],"index_entries":[]}</think>`
	result := extractJSON(input)
	assert.Contains(t, result, `"memories"`)
	assert.Contains(t, result, `"user.md"`)
}

func TestExtractJSON_ThinkWithNoCloseAndJSONInside(t *testing.T) {
	// Unclosed think with JSON inside the reasoning.
	input := `<think>分析：{"memories":[],"index_entries":[]}`
	result := extractJSON(input)
	assert.Equal(t, `{"memories":[],"index_entries":[]}`, result)
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

// ---------------------------------------------------------------------------
// T11 · TrailingRun: queue → run after in-flight
// ---------------------------------------------------------------------------

func TestExtractMemoriesService_TrailingRun(t *testing.T) {
	_, cwd := setupExtractEnv(t)
	cfg := agents.EngineConfig{AutoMemoryEnabled: true}
	svc := NewExtractMemoriesService(cwd, NopSettingsProvider, cfg)

	release := make(chan struct{})
	var callCount int32
	old := DefaultSideQueryFn
	DefaultSideQueryFn = func(_ context.Context, _, _ string) (string, error) {
		c := atomic.AddInt32(&callCount, 1)
		if c == 1 {
			<-release // block first call
		}
		return `{"memories":[],"index_entries":[]}`, nil
	}
	defer func() { DefaultSideQueryFn = old }()

	msgs := fakeMessages("u1", "a1", "u2", "a2")
	svc.OnTurnEnd(msgs)
	time.Sleep(50 * time.Millisecond)

	// Queue trailing run while first is blocked.
	msgs2 := fakeMessages("u3", "a3", "u4", "a4")
	svc.OnTurnEnd(msgs2)

	close(release)
	require.Eventually(t, func() bool {
		return atomic.LoadInt32(&callCount) >= 2
	}, 3*time.Second, 50*time.Millisecond, "trailing run should fire")
}

// ---------------------------------------------------------------------------
// T12 · Drain: graceful shutdown
// ---------------------------------------------------------------------------

func TestExtractMemoriesService_Drain(t *testing.T) {
	_, cwd := setupExtractEnv(t)
	cfg := agents.EngineConfig{AutoMemoryEnabled: true}
	svc := NewExtractMemoriesService(cwd, NopSettingsProvider, cfg)

	old := DefaultSideQueryFn
	DefaultSideQueryFn = func(_ context.Context, _, _ string) (string, error) {
		time.Sleep(100 * time.Millisecond)
		return `{"memories":[],"index_entries":[]}`, nil
	}
	defer func() { DefaultSideQueryFn = old }()

	msgs := fakeMessages("u1", "a1", "u2", "a2")
	svc.OnTurnEnd(msgs)
	time.Sleep(20 * time.Millisecond) // let goroutine start

	ok := svc.DrainPending(5 * time.Second)
	assert.True(t, ok, "drain should complete")

	svc.mu.Lock()
	ip := svc.inProgress
	svc.mu.Unlock()
	assert.False(t, ip, "should no longer be in progress after drain")
}

// ---------------------------------------------------------------------------
// T13 · CanUseTool: whitelist/blacklist assertions
// ---------------------------------------------------------------------------

func TestCreateAutoMemCanUseTool(t *testing.T) {
	memDir := filepath.Join(t.TempDir(), "mem")
	fn := CreateAutoMemCanUseTool(memDir)

	// Read tools — allowed anywhere.
	assert.True(t, fn("Read", "/some/other/path.go"))
	assert.True(t, fn("Grep", "/some/other/path.go"))
	assert.True(t, fn("Glob", "/some/other/path.go"))

	// Write tools — only inside memDir.
	assert.True(t, fn("Edit", filepath.Join(memDir, "notes.md")))
	assert.True(t, fn("Write", filepath.Join(memDir, "sub", "deep.md")))
	assert.False(t, fn("Edit", "/other/dir/file.md"))
	assert.False(t, fn("Write", "/other/dir/file.md"))

	// Bash — always disallowed.
	assert.False(t, fn("Bash", ""))

	// Unknown tool — disallowed.
	assert.False(t, fn("UnknownTool", ""))
}

// ---------------------------------------------------------------------------
// T14 · AgentID filter: sub-agent skipped
// ---------------------------------------------------------------------------

func TestExtractMemoriesService_AgentIDFilter(t *testing.T) {
	_, cwd := setupExtractEnv(t)
	cfg := agents.EngineConfig{AutoMemoryEnabled: true}
	svc := NewExtractMemoriesService(cwd, NopSettingsProvider, cfg)
	svc.SetAgentID("sub-agent-1") // non-main agent

	var called int32
	old := DefaultSideQueryFn
	DefaultSideQueryFn = func(_ context.Context, _, _ string) (string, error) {
		atomic.AddInt32(&called, 1)
		return `{"memories":[],"index_entries":[]}`, nil
	}
	defer func() { DefaultSideQueryFn = old }()

	msgs := fakeMessages("u1", "a1", "u2", "a2")
	svc.OnTurnEnd(msgs)
	time.Sleep(100 * time.Millisecond)

	assert.Equal(t, int32(0), atomic.LoadInt32(&called),
		"sub-agent should not trigger extraction")
}

// ---------------------------------------------------------------------------
// T15 · AppendSystemMessage callback
// ---------------------------------------------------------------------------

func TestExtractMemoriesService_AppendSystemMessage(t *testing.T) {
	_, cwd := setupExtractEnv(t)
	cfg := agents.EngineConfig{AutoMemoryEnabled: true}
	svc := NewExtractMemoriesService(cwd, NopSettingsProvider, cfg)

	old := DefaultSideQueryFn
	DefaultSideQueryFn = func(_ context.Context, _, _ string) (string, error) {
		return `{
			"memories": [{"filename":"test.md","action":"create","frontmatter":{},"content":"test"}],
			"index_entries": []
		}`, nil
	}
	defer func() { DefaultSideQueryFn = old }()

	var notified int32
	svc.SetAppendSystemMessage(func(text string) {
		atomic.AddInt32(&notified, 1)
		assert.Contains(t, text, "Saved")
	})

	msgs := fakeMessages("u1", "a1", "u2", "a2")
	svc.OnTurnEnd(msgs)

	require.Eventually(t, func() bool {
		return atomic.LoadInt32(&notified) > 0
	}, 5*time.Second, 50*time.Millisecond, "should get notification callback")
}
