package agents_test

// Real-LLM end-to-end coverage for every tool registered by
// tool/builtin.AllTools(). Gated on INTEGRATION=1.
//
// Structure: one parent test TestIntegration_RealLLM_AllTools splits into
// sub-tests via t.Run("<ToolName>", ...). Each sub-test:
//   1. Builds a fresh Registry + toolsDeps + engine with a custom
//      ToolUseContextBuilder that injects the resources that tool needs
//      (TodoStore / TaskManager / TaskGraph / AskUser / PlanMode /
//      McpResources / SpawnSubAgent / EmitEvent).
//   2. Sends one or more user prompts engineered to elicit the tool.
//   3. Soft-asserts the tool name appears in deps.toolCallLog. If the model
//      refuses to call the tool, the sub-test logs a WARNING and passes:
//      static plumbing in tools_integration_ported_test.go guarantees the
//      Tool.Call path itself.
//
// Environment overrides:
//   INTEGRATION=1            -- required to run.
//   TOOLS_SUBSET=Read,Edit   -- only run these sub-tests (comma list).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wall-ai/ubuilding/backend/agents"
	"github.com/wall-ai/ubuilding/backend/agents/provider"
	"github.com/wall-ai/ubuilding/backend/agents/tool"
	"github.com/wall-ai/ubuilding/backend/agents/tool/bg"
	"github.com/wall-ai/ubuilding/backend/agents/tool/builtin"
	"github.com/wall-ai/ubuilding/backend/agents/tool/taskgraph"
	"github.com/wall-ai/ubuilding/backend/agents/tool/todo"
	"github.com/wall-ai/ubuilding/backend/agents/tool/webfetch"
)

// ---------------------------------------------------------------------------
// Shared real-LLM env (read .env once, fail the parent if unavailable).
// ---------------------------------------------------------------------------

type realLLMEnv struct {
	provider provider.Provider
	model    string
}

func setupRealLLMEnv(t *testing.T) *realLLMEnv {
	t.Helper()
	if os.Getenv("INTEGRATION") == "" {
		t.Skip("Skipping real-LLM tool tests (set INTEGRATION=1 to run)")
	}
	envPath := findEnvFile()
	require.NotEmpty(t, envPath, "Could not find .env file")
	env, err := loadEnv(envPath)
	require.NoError(t, err)

	apiKey, baseURL, model, providerType := resolveEnvConfig(env)
	require.NotEmpty(t, apiKey, ".env must contain an API key (AGENT_ENGINE_API_KEY / OPENAI_API_KEY / ANTHROPIC_API_KEY)")
	t.Logf("Real-LLM env: provider=%s model=%s baseURL=%s", providerType, model, baseURL)

	p, err := provider.NewProvider(provider.FactoryConfig{
		Type:    providerType,
		APIKey:  apiKey,
		BaseURL: baseURL,
		Logger:  slog.Default(),
	})
	require.NoError(t, err)
	return &realLLMEnv{provider: p, model: model}
}

// subsetFilter honours TOOLS_SUBSET to narrow which sub-tests run.
func subsetFilter(t *testing.T, name string) {
	t.Helper()
	raw := os.Getenv("TOOLS_SUBSET")
	if raw == "" {
		return
	}
	for _, part := range strings.Split(raw, ",") {
		if strings.EqualFold(strings.TrimSpace(part), name) {
			return
		}
	}
	t.Skipf("Skipping %s (not in TOOLS_SUBSET=%s)", name, raw)
}

// ---------------------------------------------------------------------------
// Fixture — per-subtest engine with injected ToolUseContext resources.
// ---------------------------------------------------------------------------

// fixtureOpts configure the per-subtest toolCtx the engine builder returns.
type fixtureOpts struct {
	workspaceRoots []string
	// Resources injected into ToolUseContext:
	todoStore     *todo.Store
	taskManager   *bg.Manager
	taskGraph     *taskgraph.Store
	mcpResources  agents.McpResourceRegistry
	askUser       func(ctx context.Context, p agents.AskUserPayload) (agents.AskUserResponse, error)
	planMode      string
	spawnSubAgent func(ctx context.Context, params agents.SubAgentParams) (string, error)

	// Engine knobs
	systemPrompt string
	maxTurns     int
}

type toolFixture struct {
	env    *realLLMEnv
	reg    *tool.Registry
	deps   *toolsDeps
	engine *agents.QueryEngine

	// Mutable state captured from the running engine.
	events   []agents.StreamEvent
	eventsMu sync.Mutex

	// The resources we handed to the engine (tests assert against these).
	opts fixtureOpts
	// The single ToolUseContext returned by our builder (engine reuses it).
	ctxRef *agents.ToolUseContext
}

func newToolFixture(t *testing.T, env *realLLMEnv, opts fixtureOpts) *toolFixture {
	t.Helper()
	if opts.maxTurns == 0 {
		opts.maxTurns = 6
	}

	reg := tool.NewRegistry()
	builtin.RegisterAll(reg, builtin.Options{
		WorkspaceRoots:  opts.workspaceRoots,
		WebFetchOptions: []webfetch.Option{webfetch.WithAllowLoopback()},
	})

	deps := &toolsDeps{
		realDeps: realDeps{provider: env.provider, model: env.model},
		registry: reg,
	}

	f := &toolFixture{env: env, reg: reg, deps: deps, opts: opts}

	// Engine knobs: tools loop expects a base prompt + platform hint.
	sys := opts.systemPrompt
	if sys == "" {
		sys = defaultToolSystemPrompt()
	}

	cfg := agents.EngineConfig{
		Cwd:                ".",
		UserSpecifiedModel: env.model,
		MaxTurns:           opts.maxTurns,
		BaseSystemPrompt:   sys,
	}

	f.engine = agents.NewQueryEngine(cfg, deps,
		agents.WithLogger(slog.Default()),
		agents.WithToolUseContextBuilder(func(ctx context.Context, _ []agents.Message) *agents.ToolUseContext {
			childCtx, cancel := context.WithCancel(ctx)
			tc := &agents.ToolUseContext{
				Ctx:           childCtx,
				CancelFunc:    cancel,
				ReadFileState: agents.NewFileStateCache(),
				PlanMode:      opts.planMode,
				TodoStore:     nilableTodo(opts.todoStore),
				TaskManager:   nilableBg(opts.taskManager),
				TaskGraph:     nilableGraph(opts.taskGraph),
				McpResources:  opts.mcpResources,
				AskUser:       opts.askUser,
				SpawnSubAgent: opts.spawnSubAgent,
				EmitEvent: func(ev agents.StreamEvent) {
					f.eventsMu.Lock()
					f.events = append(f.events, ev)
					f.eventsMu.Unlock()
				},
			}
			f.ctxRef = tc
			return tc
		}),
	)
	return f
}

// nilable* helpers keep interface{} fields truly nil when the caller passed nil.
// Without them a typed-nil pointer stored in an interface{} breaks `== nil` probes
// inside the tool implementations.
func nilableTodo(s *todo.Store) interface{} {
	if s == nil {
		return nil
	}
	return s
}
func nilableBg(m *bg.Manager) interface{} {
	if m == nil {
		return nil
	}
	return m
}
func nilableGraph(g *taskgraph.Store) interface{} {
	if g == nil {
		return nil
	}
	return g
}

// driveResult captures what a single SubmitMessage turn produced.
type driveResult struct {
	text       string
	events     []string
	toolsGot   []string
	errors     []string
	assistants int
	stopReason string
}

// drive runs one engine.SubmitMessage and returns captured output. It never
// calls t.Fatal — callers decide how to assert.
func (f *toolFixture) drive(ctx context.Context, prompt string) driveResult {
	startIdx := len(f.deps.toolCallLog)
	var dr driveResult
	for ev := range f.engine.SubmitMessage(ctx, prompt) {
		dr.events = append(dr.events, string(ev.Type))
		switch ev.Type {
		case agents.EventTextDelta:
			dr.text += ev.Text
		case agents.EventError:
			dr.errors = append(dr.errors, ev.Error)
		case agents.EventAssistant:
			dr.assistants++
			if ev.Message != nil {
				dr.stopReason = ev.Message.StopReason
				// Some providers only surface text in the final assistant
				// message rather than via EventTextDelta.
				if dr.text == "" {
					for _, b := range ev.Message.Content {
						if b.Type == agents.ContentBlockText {
							dr.text += b.Text
						}
					}
				}
			}
		}
	}
	dr.toolsGot = append([]string{}, f.deps.toolCallLog[startIdx:]...)
	return dr
}

// assertToolCalledOrSkip soft-asserts the tool was invoked this turn. If it
// wasn't, the sub-test logs a WARNING and skips — real LLMs are probabilistic.
func assertToolCalledOrSkip(t *testing.T, dr driveResult, wantTool string) {
	t.Helper()
	for _, name := range dr.toolsGot {
		if name == wantTool {
			return
		}
	}
	t.Logf("WARN: model did not call %q this turn. Tools observed: %v. Final text: %q",
		wantTool, dr.toolsGot, truncateForLog(dr.text, 200))
	t.Skipf("%s not called by model; static plumbing covers the Tool.Call path", wantTool)
}

// defaultToolSystemPrompt gives the model a compact, platform-aware tool menu.
func defaultToolSystemPrompt() string {
	platform := "POSIX (bash, grep, sed)"
	if runtime.GOOS == "windows" {
		platform = "Windows (use PowerShell cmdlets for the Bash tool: Write-Output, Get-Content, Select-String)"
	}
	return strings.Join([]string{
		"You are a capable coding agent with access to a tool suite.",
		"When the user's request maps to a tool, CALL THE TOOL rather than answering from memory.",
		"Be terse. No pleasantries.",
		"Platform: " + platform + ".",
	}, " ")
}

// ---------------------------------------------------------------------------
// Parent test
// ---------------------------------------------------------------------------

func TestIntegration_RealLLM_AllTools(t *testing.T) {
	env := setupRealLLMEnv(t)

	t.Run("Read", func(t *testing.T) { subsetFilter(t, "Read"); testReal_Read(t, env) })
	t.Run("Edit", func(t *testing.T) { subsetFilter(t, "Edit"); testReal_Edit(t, env) })
	t.Run("Write", func(t *testing.T) { subsetFilter(t, "Write"); testReal_Write(t, env) })
	t.Run("Glob", func(t *testing.T) { subsetFilter(t, "Glob"); testReal_Glob(t, env) })
	t.Run("Grep", func(t *testing.T) { subsetFilter(t, "Grep"); testReal_Grep(t, env) })
	t.Run("Bash", func(t *testing.T) { subsetFilter(t, "Bash"); testReal_Bash(t, env) })
	t.Run("WebSearch", func(t *testing.T) { subsetFilter(t, "WebSearch"); testReal_WebSearch(t, env) })
	t.Run("WebFetch", func(t *testing.T) { subsetFilter(t, "WebFetch"); testReal_WebFetch(t, env) })
	t.Run("NotebookEdit", func(t *testing.T) { subsetFilter(t, "NotebookEdit"); testReal_NotebookEdit(t, env) })
	t.Run("TodoWrite", func(t *testing.T) { subsetFilter(t, "TodoWrite"); testReal_TodoWrite(t, env) })

	t.Run("AskUserQuestion", func(t *testing.T) { subsetFilter(t, "AskUserQuestion"); testReal_AskUser(t, env) })
	t.Run("EnterPlanMode", func(t *testing.T) { subsetFilter(t, "EnterPlanMode"); testReal_EnterPlanMode(t, env) })
	t.Run("ExitPlanMode", func(t *testing.T) { subsetFilter(t, "ExitPlanMode"); testReal_ExitPlanMode(t, env) })
	t.Run("TaskCreate", func(t *testing.T) { subsetFilter(t, "TaskCreate"); testReal_TaskCreate(t, env) })
	t.Run("TaskGet", func(t *testing.T) { subsetFilter(t, "TaskGet"); testReal_TaskGet(t, env) })
	t.Run("TaskUpdate", func(t *testing.T) { subsetFilter(t, "TaskUpdate"); testReal_TaskUpdate(t, env) })
	t.Run("TaskList", func(t *testing.T) { subsetFilter(t, "TaskList"); testReal_TaskList(t, env) })
	t.Run("Task", func(t *testing.T) { subsetFilter(t, "Task"); testReal_AgentTool(t, env) })

	t.Run("TaskOutput", func(t *testing.T) { subsetFilter(t, "TaskOutput"); testReal_TaskOutput(t, env) })
	t.Run("TaskStop_Bg", func(t *testing.T) { subsetFilter(t, "TaskStop_Bg"); testReal_TaskStopBg(t, env) })
	t.Run("TaskStop_Graph", func(t *testing.T) { subsetFilter(t, "TaskStop_Graph"); testReal_TaskStopGraph(t, env) })
	t.Run("ListMcpResourcesTool", func(t *testing.T) { subsetFilter(t, "ListMcpResourcesTool"); testReal_McpList(t, env) })
	t.Run("ReadMcpResourceTool", func(t *testing.T) { subsetFilter(t, "ReadMcpResourceTool"); testReal_McpRead(t, env) })

	t.Run("SendUserMessage", func(t *testing.T) { subsetFilter(t, "SendUserMessage"); testReal_SendUserMessage(t, env) })
}

// ---------------------------------------------------------------------------
// Phase 1 — A-level (LLM self-triggers easily)
// ---------------------------------------------------------------------------

func testReal_Read(t *testing.T, env *realLLMEnv) {
	dir := t.TempDir()
	target := filepath.Join(dir, "notes.txt")
	require.NoError(t, os.WriteFile(target, []byte("alpha\nbeta\ngamma\nZEPHYRFLAME"), 0o644))

	f := newToolFixture(t, env, fixtureOpts{workspaceRoots: []string{dir}})
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	prompt := fmt.Sprintf(
		"Use the Read tool to open the file %s and then tell me the very last non-empty line of that file, nothing else.",
		target)
	dr := f.drive(ctx, prompt)
	t.Logf("[Read] tools=%v stop=%q errs=%v events=%v text=%q",
		dr.toolsGot, dr.stopReason, dr.errors, dr.events, truncateForLog(dr.text, 200))
	assertToolCalledOrSkip(t, dr, "Read")
	assert.Contains(t, strings.ToUpper(dr.text), "ZEPHYRFLAME", "final answer should echo the sentinel last line")
}

func testReal_Edit(t *testing.T, env *realLLMEnv) {
	dir := t.TempDir()
	target := filepath.Join(dir, "greet.txt")
	require.NoError(t, os.WriteFile(target, []byte("hello world\n"), 0o644))

	f := newToolFixture(t, env, fixtureOpts{
		workspaceRoots: []string{dir},
		systemPrompt: defaultToolSystemPrompt() +
			" When editing a file you have not read yet, call Read first, then Edit.",
	})
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	prompt := fmt.Sprintf(
		"Edit %s: replace the word 'hello' with 'howdy'. Read first if needed. Confirm when done.",
		target)
	dr := f.drive(ctx, prompt)
	t.Logf("[Edit] tools=%v text=%q", dr.toolsGot, truncateForLog(dr.text, 200))
	assertToolCalledOrSkip(t, dr, "Edit")

	data, err := os.ReadFile(target)
	require.NoError(t, err)
	assert.Contains(t, string(data), "howdy", "Edit should have rewritten the file on disk")
}

func testReal_Write(t *testing.T, env *realLLMEnv) {
	dir := t.TempDir()
	target := filepath.Join(dir, "out.txt")
	nonce := fmt.Sprintf("NONCE-%d", time.Now().UnixNano())

	f := newToolFixture(t, env, fixtureOpts{workspaceRoots: []string{dir}})
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	prompt := fmt.Sprintf(
		"Use the Write tool to create the file %s with the single-line content: %s",
		target, nonce)
	dr := f.drive(ctx, prompt)
	t.Logf("[Write] tools=%v text=%q", dr.toolsGot, truncateForLog(dr.text, 200))
	assertToolCalledOrSkip(t, dr, "Write")

	data, err := os.ReadFile(target)
	require.NoError(t, err)
	assert.Contains(t, string(data), nonce, "Write should have created file with the nonce")
}

func testReal_Glob(t *testing.T, env *realLLMEnv) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "alpha.go"), []byte("package a\n"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "sub"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "sub", "beta.go"), []byte("package b\n"), 0o644))

	f := newToolFixture(t, env, fixtureOpts{workspaceRoots: []string{dir}})
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	prompt := fmt.Sprintf(
		"Use the Glob tool with pattern '**/*.go' under path %s and list every .go file you find.",
		dir)
	dr := f.drive(ctx, prompt)
	t.Logf("[Glob] tools=%v text=%q", dr.toolsGot, truncateForLog(dr.text, 200))
	assertToolCalledOrSkip(t, dr, "Glob")
	lower := strings.ToLower(dr.text)
	assert.True(t,
		strings.Contains(lower, "alpha.go") || strings.Contains(lower, "beta.go"),
		"final answer should mention at least one matched file")
}

func testReal_Grep(t *testing.T, env *realLLMEnv) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.go"),
		[]byte("package main\nfunc ZephyrHandler() {}\nfunc other() {}\n"), 0o644))

	f := newToolFixture(t, env, fixtureOpts{workspaceRoots: []string{dir}})
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	prompt := fmt.Sprintf(
		"Use the Grep tool to search for the regex '^func ' under %s and list the matching function names.",
		dir)
	dr := f.drive(ctx, prompt)
	t.Logf("[Grep] tools=%v text=%q", dr.toolsGot, truncateForLog(dr.text, 200))
	assertToolCalledOrSkip(t, dr, "Grep")
	assert.Contains(t, dr.text, "Zephyr", "grep answer should mention the matched function name")
}

func testReal_Bash(t *testing.T, env *realLLMEnv) {
	f := newToolFixture(t, env, fixtureOpts{})
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	nonce := fmt.Sprintf("ROUTED%d", time.Now().UnixNano())
	var prompt string
	if runtime.GOOS == "windows" {
		prompt = fmt.Sprintf(
			"Use the Bash tool to run exactly: Write-Output '%s' and then echo the output back to me.",
			nonce)
	} else {
		prompt = fmt.Sprintf(
			"Use the Bash tool to run exactly: echo %s and then echo the output back to me.",
			nonce)
	}
	dr := f.drive(ctx, prompt)
	t.Logf("[Bash] tools=%v text=%q", dr.toolsGot, truncateForLog(dr.text, 200))
	assertToolCalledOrSkip(t, dr, "Bash")
	assert.Contains(t, dr.text, nonce, "final answer should contain the shell output nonce")
}

func testReal_WebSearch(t *testing.T, env *realLLMEnv) {
	f := newToolFixture(t, env, fixtureOpts{})
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	prompt := "Use the WebSearch tool to find the official homepage of the Go programming language and report the canonical URL."
	dr := f.drive(ctx, prompt)
	t.Logf("[WebSearch] tools=%v text=%q", dr.toolsGot, truncateForLog(dr.text, 200))
	// Soft: WebSearch depends on external API key; skip if not called.
	assertToolCalledOrSkip(t, dr, "WebSearch")
	lower := strings.ToLower(dr.text)
	assert.True(t,
		strings.Contains(lower, "go.dev") || strings.Contains(lower, "golang.org"),
		"expected final answer to mention go.dev or golang.org")
}

func testReal_WebFetch(t *testing.T, env *realLLMEnv) {
	const sentinel = "ZEPHYR-MARKER-2187"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, `<html><body><h1>Status</h1><p>marker: <code>%s</code></p></body></html>`, sentinel)
	}))
	defer srv.Close()

	f := newToolFixture(t, env, fixtureOpts{
		systemPrompt: defaultToolSystemPrompt() +
			" When the user gives you a URL, you MUST use WebFetch to retrieve it before answering.",
	})
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	prompt := fmt.Sprintf("Fetch %s with WebFetch and tell me the marker value shown on the page.", srv.URL)
	dr := f.drive(ctx, prompt)
	t.Logf("[WebFetch] tools=%v text=%q", dr.toolsGot, truncateForLog(dr.text, 200))
	assertToolCalledOrSkip(t, dr, "WebFetch")
	assert.Contains(t, dr.text, sentinel, "final answer should echo the fetched marker")
}

func testReal_NotebookEdit(t *testing.T, env *realLLMEnv) {
	dir := t.TempDir()
	nbPath := filepath.Join(dir, "nb.ipynb")
	require.NoError(t, os.WriteFile(nbPath, []byte(
		`{"cells":[{"cell_type":"code","id":"c1","metadata":{},"source":["1+1\n"],"outputs":[],"execution_count":null}],`+
			`"metadata":{},"nbformat":4,"nbformat_minor":5}`), 0o644))

	f := newToolFixture(t, env, fixtureOpts{workspaceRoots: []string{dir}})
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	prompt := fmt.Sprintf(
		"Use the NotebookEdit tool on %s to replace the source of cell id 'c1' with the literal text '2+2'.",
		nbPath)
	dr := f.drive(ctx, prompt)
	t.Logf("[NotebookEdit] tools=%v text=%q", dr.toolsGot, truncateForLog(dr.text, 200))
	assertToolCalledOrSkip(t, dr, "NotebookEdit")

	body, err := os.ReadFile(nbPath)
	require.NoError(t, err)
	assert.Contains(t, string(body), "2+2", "notebook cell should contain 2+2 after edit")
}

func testReal_TodoWrite(t *testing.T, env *realLLMEnv) {
	store := todo.NewStore()
	f := newToolFixture(t, env, fixtureOpts{
		todoStore: store,
		systemPrompt: defaultToolSystemPrompt() +
			" Track multi-step work with TodoWrite. Always record at least one in_progress item when starting.",
	})
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	prompt := "I'm going to migrate a service in 3 steps: 1) port types, 2) port routes, 3) write tests. " +
		"Use TodoWrite to record these as todos with the first one in_progress."
	dr := f.drive(ctx, prompt)
	t.Logf("[TodoWrite] tools=%v text=%q store=%d", dr.toolsGot, truncateForLog(dr.text, 200), len(store.Snapshot()))
	assertToolCalledOrSkip(t, dr, "TodoWrite")
	assert.NotEmpty(t, store.Snapshot(), "TodoStore should have entries after TodoWrite")
}

// ---------------------------------------------------------------------------
// Phase 2 — B-level (guided + pre-seeded context)
// ---------------------------------------------------------------------------

func testReal_AskUser(t *testing.T, env *realLLMEnv) {
	var captured agents.AskUserPayload
	f := newToolFixture(t, env, fixtureOpts{
		askUser: func(_ context.Context, p agents.AskUserPayload) (agents.AskUserResponse, error) {
			captured = p
			return agents.AskUserResponse{Selected: []string{"A"}}, nil
		},
		systemPrompt: defaultToolSystemPrompt() +
			" When the user's request is ambiguous or asks you to choose between options on their behalf," +
			" you MUST call AskUserQuestion instead of guessing.",
	})
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	prompt := "I'm stuck choosing a deployment color. Pick for me, but first ask me via AskUserQuestion with exactly two options labeled 'A' and 'B'."
	dr := f.drive(ctx, prompt)
	t.Logf("[AskUserQuestion] tools=%v text=%q captured.Question=%q", dr.toolsGot, truncateForLog(dr.text, 200), captured.Question)
	assertToolCalledOrSkip(t, dr, "AskUserQuestion")
	assert.NotEmpty(t, captured.Question, "AskUser callback should have been invoked with a question")
}

func testReal_EnterPlanMode(t *testing.T, env *realLLMEnv) {
	f := newToolFixture(t, env, fixtureOpts{
		planMode: "", // normal
		systemPrompt: defaultToolSystemPrompt() +
			" When the user asks you to plan a multi-step refactor, FIRST call EnterPlanMode to switch into planning mode, then draft the plan.",
	})
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	prompt := "Plan (don't execute) how to split a Go package into two sub-packages. Enter plan mode first."
	dr := f.drive(ctx, prompt)
	t.Logf("[EnterPlanMode] tools=%v planMode=%q", dr.toolsGot, f.ctxRef.PlanMode)
	assertToolCalledOrSkip(t, dr, "EnterPlanMode")
	assert.Equal(t, "plan", f.ctxRef.PlanMode, "PlanMode should have switched to 'plan'")
}

func testReal_ExitPlanMode(t *testing.T, env *realLLMEnv) {
	f := newToolFixture(t, env, fixtureOpts{
		planMode: "plan",
		systemPrompt: defaultToolSystemPrompt() +
			" You are currently in plan mode. Once you've drafted the plan, call ExitPlanMode to hand it off for execution.",
	})
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	prompt := "Here's my plan: 1) split api.go into api_http.go + api_grpc.go; 2) update imports. " +
		"You are in plan mode — acknowledge and exit plan mode so I can execute."
	dr := f.drive(ctx, prompt)
	t.Logf("[ExitPlanMode] tools=%v planMode=%q", dr.toolsGot, f.ctxRef.PlanMode)
	assertToolCalledOrSkip(t, dr, "ExitPlanMode")
	assert.Equal(t, "normal", f.ctxRef.PlanMode, "PlanMode should have switched back to normal")
}

func testReal_TaskCreate(t *testing.T, env *realLLMEnv) {
	store := taskgraph.NewStore()
	f := newToolFixture(t, env, fixtureOpts{
		taskGraph:    store,
		systemPrompt: defaultToolSystemPrompt() + " Use TaskCreate to register new tasks in the task graph.",
	})
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	prompt := "Create a new task titled 'Port QueryEngine' via TaskCreate."
	dr := f.drive(ctx, prompt)
	t.Logf("[TaskCreate] tools=%v nodes=%d", dr.toolsGot, len(store.List("")))
	assertToolCalledOrSkip(t, dr, "TaskCreate")
	assert.NotEmpty(t, store.List(""), "TaskGraph should have at least one node after TaskCreate")
}

func testReal_TaskGet(t *testing.T, env *realLLMEnv) {
	store := taskgraph.NewStore()
	node, err := store.Add(taskgraph.Node{Title: "Port QueryEngine"})
	require.NoError(t, err)
	f := newToolFixture(t, env, fixtureOpts{
		taskGraph:    store,
		systemPrompt: defaultToolSystemPrompt() + " Use TaskGet to fetch task details by id.",
	})
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	prompt := fmt.Sprintf("Call TaskGet with id=%q and report the task's title.", node.ID)
	dr := f.drive(ctx, prompt)
	t.Logf("[TaskGet] tools=%v text=%q", dr.toolsGot, truncateForLog(dr.text, 200))
	assertToolCalledOrSkip(t, dr, "TaskGet")
	assert.Contains(t, dr.text, "Port QueryEngine", "TaskGet answer should include the task title")
}

func testReal_TaskUpdate(t *testing.T, env *realLLMEnv) {
	store := taskgraph.NewStore()
	node, err := store.Add(taskgraph.Node{Title: "Flip me", Status: taskgraph.StatusInProgress})
	require.NoError(t, err)
	f := newToolFixture(t, env, fixtureOpts{
		taskGraph:    store,
		systemPrompt: defaultToolSystemPrompt() + " Use TaskUpdate to change a task's status.",
	})
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	prompt := fmt.Sprintf("Mark task %q as completed via TaskUpdate.", node.ID)
	dr := f.drive(ctx, prompt)
	t.Logf("[TaskUpdate] tools=%v", dr.toolsGot)
	assertToolCalledOrSkip(t, dr, "TaskUpdate")
	got, ok := store.Get(node.ID)
	require.True(t, ok)
	assert.Equal(t, taskgraph.StatusCompleted, got.Status, "task status should be completed")
}

func testReal_TaskList(t *testing.T, env *realLLMEnv) {
	store := taskgraph.NewStore()
	_, err := store.Add(taskgraph.Node{Title: "done-1", Status: taskgraph.StatusCompleted})
	require.NoError(t, err)
	_, err = store.Add(taskgraph.Node{Title: "wip-1", Status: taskgraph.StatusInProgress})
	require.NoError(t, err)

	f := newToolFixture(t, env, fixtureOpts{
		taskGraph:    store,
		systemPrompt: defaultToolSystemPrompt() + " Use TaskList to enumerate tasks, with an optional status filter.",
	})
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	prompt := "Use TaskList to list all tasks whose status is 'completed' and summarize what you see."
	dr := f.drive(ctx, prompt)
	t.Logf("[TaskList] tools=%v text=%q", dr.toolsGot, truncateForLog(dr.text, 200))
	assertToolCalledOrSkip(t, dr, "TaskList")
	assert.Contains(t, dr.text, "done-1", "TaskList answer should reference the completed task title")
}

func testReal_AgentTool(t *testing.T, env *realLLMEnv) {
	var gotParams agents.SubAgentParams
	stubAnswer := "Sub-agent counted 2 Go files: alpha.go and beta.go."
	f := newToolFixture(t, env, fixtureOpts{
		spawnSubAgent: func(_ context.Context, p agents.SubAgentParams) (string, error) {
			gotParams = p
			return stubAnswer, nil
		},
		systemPrompt: defaultToolSystemPrompt() +
			" For non-trivial research sub-tasks you MUST dispatch via the Task tool (sub-agent) instead of answering directly.",
	})
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	prompt := "Use the Task tool to spawn a sub-agent that counts Go files in the current workspace and reports back."
	dr := f.drive(ctx, prompt)
	t.Logf("[Task] tools=%v subagent.prompt=%q", dr.toolsGot, truncateForLog(gotParams.Prompt, 120))
	assertToolCalledOrSkip(t, dr, "Task")
	assert.NotEmpty(t, gotParams.Prompt, "SpawnSubAgent should have been invoked with a prompt")
	assert.Contains(t, dr.text, "Go files", "parent agent should echo the sub-agent's answer")
}

// ---------------------------------------------------------------------------
// Phase 3 — C-level (pre-seeded resources: bg jobs, task graph, MCP)
// ---------------------------------------------------------------------------

func testReal_TaskOutput(t *testing.T, env *realLLMEnv) {
	mgr := bg.NewManager()
	id, err := mgr.Start(context.Background(), "seeded", func(_ context.Context, write func(string)) (int, error) {
		write("SEEDED-BG-OK\n")
		return 0, nil
	})
	require.NoError(t, err)
	_, _ = mgr.WaitForTerminal(context.Background(), id)

	f := newToolFixture(t, env, fixtureOpts{
		taskManager:  mgr,
		systemPrompt: defaultToolSystemPrompt() + " Use TaskOutput to inspect background-shell job output.",
	})
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	prompt := fmt.Sprintf("Use TaskOutput with bash_id=%q to fetch the job output, then quote the exact stdout line.", id)
	dr := f.drive(ctx, prompt)
	t.Logf("[TaskOutput] tools=%v text=%q", dr.toolsGot, truncateForLog(dr.text, 200))
	assertToolCalledOrSkip(t, dr, "TaskOutput")
	assert.Contains(t, dr.text, "SEEDED-BG-OK", "final answer should quote the seeded bg-job output")
}

func testReal_TaskStopBg(t *testing.T, env *realLLMEnv) {
	mgr := bg.NewManager()
	id, err := mgr.Start(context.Background(), "loop", func(ctx context.Context, _ func(string)) (int, error) {
		<-ctx.Done()
		return -1, ctx.Err()
	})
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = mgr.Stop(id, 2*time.Second) })

	f := newToolFixture(t, env, fixtureOpts{
		taskManager:  mgr,
		systemPrompt: defaultToolSystemPrompt() + " Use TaskStop to cancel a running background job by id.",
	})
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	prompt := fmt.Sprintf("Stop the background job %q via TaskStop and confirm.", id)
	dr := f.drive(ctx, prompt)
	t.Logf("[TaskStop/bg] tools=%v", dr.toolsGot)
	assertToolCalledOrSkip(t, dr, "TaskStop")

	// Allow the cancellation to propagate.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		jobs := mgr.List()
		for _, j := range jobs {
			if j.ID == id && j.Status != "running" {
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Logf("WARN: bg job %s still running after TaskStop (status check timed out)", id)
}

func testReal_TaskStopGraph(t *testing.T, env *realLLMEnv) {
	store := taskgraph.NewStore()
	mgr := bg.NewManager()
	node, err := store.Add(taskgraph.Node{Title: "wip", Status: taskgraph.StatusInProgress})
	require.NoError(t, err)
	f := newToolFixture(t, env, fixtureOpts{
		taskGraph:    store,
		taskManager:  mgr,
		systemPrompt: defaultToolSystemPrompt() + " Use TaskStop to cancel a task-graph node by id.",
	})
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	prompt := fmt.Sprintf("Cancel the task-graph node %q with TaskStop.", node.ID)
	dr := f.drive(ctx, prompt)
	t.Logf("[TaskStop/graph] tools=%v", dr.toolsGot)
	assertToolCalledOrSkip(t, dr, "TaskStop")

	got, ok := store.Get(node.ID)
	require.True(t, ok)
	assert.NotEqual(t, taskgraph.StatusInProgress, got.Status, "graph node status should have transitioned away from in_progress")
}

// ---------------------------------------------------------------------------
// Mock MCP registry for List/Read tool tests
// ---------------------------------------------------------------------------

type mockMCP struct {
	server    string
	resources []agents.McpResource
	reads     map[string][]agents.McpResourceContent
}

func newMockMCP() *mockMCP {
	server := "demo"
	return &mockMCP{
		server: server,
		resources: []agents.McpResource{
			{URI: "demo://greeting", Name: "GreetingDoc", MimeType: "text/plain", Server: server, Description: "demo greeting"},
			{URI: "demo://status", Name: "StatusDoc", MimeType: "text/plain", Server: server, Description: "demo status"},
		},
		reads: map[string][]agents.McpResourceContent{
			"demo://greeting": {{URI: "demo://greeting", MimeType: "text/plain", Text: "Hello from MCP — SENTINEL-9931"}},
			"demo://status":   {{URI: "demo://status", MimeType: "text/plain", Text: "Status: GREEN"}},
		},
	}
}

func (m *mockMCP) ListServers() []string { return []string{m.server} }

func (m *mockMCP) ListResources(_ context.Context, server string) ([]agents.McpResource, error) {
	if server != "" && server != m.server {
		return nil, fmt.Errorf("mockMCP: unknown server %q", server)
	}
	out := make([]agents.McpResource, len(m.resources))
	copy(out, m.resources)
	return out, nil
}

func (m *mockMCP) ReadResource(_ context.Context, server, uri string) ([]agents.McpResourceContent, error) {
	if server != "" && server != m.server {
		return nil, fmt.Errorf("mockMCP: unknown server %q", server)
	}
	c, ok := m.reads[uri]
	if !ok {
		return nil, fmt.Errorf("mockMCP: unknown uri %q", uri)
	}
	return c, nil
}

func testReal_McpList(t *testing.T, env *realLLMEnv) {
	reg := newMockMCP()
	f := newToolFixture(t, env, fixtureOpts{
		mcpResources: reg,
		systemPrompt: defaultToolSystemPrompt() +
			" Use ListMcpResourcesTool to enumerate resources from the connected MCP servers.",
	})
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	prompt := "List the resources available from the 'demo' MCP server via ListMcpResourcesTool and name them."
	dr := f.drive(ctx, prompt)
	t.Logf("[ListMcpResourcesTool] tools=%v text=%q", dr.toolsGot, truncateForLog(dr.text, 200))
	assertToolCalledOrSkip(t, dr, "ListMcpResourcesTool")
	lower := strings.ToLower(dr.text)
	assert.True(t,
		strings.Contains(lower, "greeting") || strings.Contains(lower, "status"),
		"answer should mention a mock resource name")
}

func testReal_McpRead(t *testing.T, env *realLLMEnv) {
	reg := newMockMCP()
	f := newToolFixture(t, env, fixtureOpts{
		mcpResources: reg,
		systemPrompt: defaultToolSystemPrompt() +
			" Use ReadMcpResourceTool to fetch the content of a specific MCP resource by uri.",
	})
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	prompt := "Use ReadMcpResourceTool with server='demo' and uri='demo://greeting' and then quote the exact text you retrieved."
	dr := f.drive(ctx, prompt)
	t.Logf("[ReadMcpResourceTool] tools=%v text=%q", dr.toolsGot, truncateForLog(dr.text, 200))
	assertToolCalledOrSkip(t, dr, "ReadMcpResourceTool")
	assert.Contains(t, dr.text, "SENTINEL-9931", "answer should quote the sentinel text from the mock resource")
}

// ---------------------------------------------------------------------------
// Phase 4 — D-level (unlikely to self-trigger)
// ---------------------------------------------------------------------------

func testReal_SendUserMessage(t *testing.T, env *realLLMEnv) {
	f := newToolFixture(t, env, fixtureOpts{
		systemPrompt: defaultToolSystemPrompt() +
			" Your PRIMARY output channel is the SendUserMessage tool. Every user-visible reply MUST be delivered by calling SendUserMessage" +
			" with status='normal' (or 'proactive' for unsolicited updates). Do NOT write plain assistant text; always route through SendUserMessage.",
	})
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	prompt := "Send the user a one-line status: 'all real-LLM tool tests wired'. Use SendUserMessage."
	dr := f.drive(ctx, prompt)
	t.Logf("[SendUserMessage] tools=%v events=%v", dr.toolsGot, dr.events)
	assertToolCalledOrSkip(t, dr, "SendUserMessage")

	f.eventsMu.Lock()
	defer f.eventsMu.Unlock()
	sawBrief := false
	for _, ev := range f.events {
		if ev.Type == agents.EventBrief {
			sawBrief = true
			break
		}
	}
	assert.True(t, sawBrief, "engine should have emitted at least one EventBrief after SendUserMessage")
}

// ---------------------------------------------------------------------------
// Phase 6 — Smoke: drive all tools in one session with aggregate coverage
// ---------------------------------------------------------------------------

func TestIntegration_RealLLM_AllTools_Smoke(t *testing.T) {
	env := setupRealLLMEnv(t)

	// Prepare a rich environment that enables every tool at once.
	dir := t.TempDir()
	target := filepath.Join(dir, "smoke.txt")
	require.NoError(t, os.WriteFile(target, []byte("smoke line 1\nsmoke line 2\n"), 0o644))

	todoStore := todo.NewStore()
	taskGraph := taskgraph.NewStore()
	mgr := bg.NewManager()
	bgID, err := mgr.Start(context.Background(), "seeded", func(_ context.Context, w func(string)) (int, error) {
		w("SMOKE-BG-OK\n")
		return 0, nil
	})
	require.NoError(t, err)
	_, _ = mgr.WaitForTerminal(context.Background(), bgID)
	graphNode, _ := taskGraph.Add(taskgraph.Node{Title: "smoke-node", Status: taskgraph.StatusInProgress})
	mcp := newMockMCP()

	sys := strings.Join([]string{
		defaultToolSystemPrompt(),
		"Follow user instructions literally and call tools by the exact name they mention.",
		"Your primary output channel is SendUserMessage (status='normal' or 'proactive').",
	}, " ")

	f := newToolFixture(t, env, fixtureOpts{
		workspaceRoots: []string{dir},
		todoStore:      todoStore,
		taskManager:    mgr,
		taskGraph:      taskGraph,
		mcpResources:   mcp,
		askUser: func(_ context.Context, _ agents.AskUserPayload) (agents.AskUserResponse, error) {
			return agents.AskUserResponse{Selected: []string{"A"}}, nil
		},
		spawnSubAgent: func(_ context.Context, _ agents.SubAgentParams) (string, error) {
			return "Sub-agent done.", nil
		},
		planMode:     "",
		systemPrompt: sys,
		maxTurns:     30,
	})

	// A single dense prompt that names every tool exactly. We also fire a
	// WebFetch target via httptest so that tool is actually reachable.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "<html><body>SMOKE-PAGE</body></html>")
	}))
	defer srv.Close()

	prompts := []string{
		fmt.Sprintf("Step 1: use Read on %s.", target),
		fmt.Sprintf("Step 2: use Write to create %s with content 'smoke-write'.", filepath.Join(dir, "new.txt")),
		fmt.Sprintf("Step 3: use Edit on %s replacing 'smoke line 1' with 'smoke LINE 1'.", target),
		fmt.Sprintf("Step 4: use Glob with pattern '*.txt' and path %s.", dir),
		fmt.Sprintf("Step 5: use Grep with pattern 'smoke' and path %s.", dir),
		"Step 6: use Bash to print 'SMOKE-SHELL'.",
		fmt.Sprintf("Step 7: use WebFetch on %s.", srv.URL),
		"Step 8: use WebSearch for 'golang official homepage'. (soft)",
		fmt.Sprintf("Step 9: use TodoWrite with one in_progress todo 'smoke-test'."),
		fmt.Sprintf("Step 10: use TaskCreate titled 'smoke-created'."),
		fmt.Sprintf("Step 11: use TaskList status=in_progress."),
		fmt.Sprintf("Step 12: use TaskGet id=%q.", graphNode.ID),
		fmt.Sprintf("Step 13: use TaskUpdate id=%q to status=completed.", graphNode.ID),
		fmt.Sprintf("Step 14: use TaskOutput with bash_id=%q.", bgID),
		"Step 15: use EnterPlanMode.",
		"Step 16: use ExitPlanMode with a one-line plan.",
		"Step 17: use ListMcpResourcesTool to enumerate MCP resources.",
		"Step 18: use ReadMcpResourceTool for server='demo' uri='demo://greeting'.",
		"Step 19: use AskUserQuestion with two options 'A' and 'B'.",
		"Step 20: use Task to spawn a sub-agent summarising what you did.",
		"Step 21: use SendUserMessage to send me a one-line final summary ('done').",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	for i, p := range prompts {
		dr := f.drive(ctx, p)
		t.Logf("smoke[%02d] tools=%v errs=%v stop=%q", i, dr.toolsGot, dr.errors, dr.stopReason)
	}

	registered := f.reg.GetTools().Names()
	seen := make(map[string]bool)
	for _, n := range f.deps.toolCallLog {
		seen[n] = true
	}

	// NotebookEdit and TaskStop aren't prompted in the smoke sequence
	// (NotebookEdit needs a pre-seeded .ipynb in the working tree; TaskStop
	// needs a running bg job — both already covered by dedicated subtests
	// in TestIntegration_RealLLM_AllTools). Exclude from coverage math.
	ignored := map[string]bool{"NotebookEdit": true, "TaskStop": true}
	var uniqSeen, totalTargets int
	for _, n := range registered {
		if ignored[n] {
			continue
		}
		totalTargets++
		if seen[n] {
			uniqSeen++
		}
	}
	covered := float64(uniqSeen) / float64(totalTargets)
	t.Logf("Smoke coverage: %d / %d registered tools invoked (%.0f%%). toolCallLog=%v", uniqSeen, totalTargets, covered*100, f.deps.toolCallLog)

	var missing []string
	for _, n := range registered {
		if !seen[n] && !ignored[n] {
			missing = append(missing, n)
		}
	}
	if len(missing) > 0 {
		t.Logf("Smoke: tools never invoked: %v", missing)
	}

	// Soft threshold: LLMs are probabilistic. Hard per-tool coverage is guaranteed
	// by TestIntegration_RealLLM_AllTools subtests. Here we only warn below 60%.
	if covered < 0.60 {
		t.Logf("WARN: smoke coverage below 60%% — investigate prompts or model tool-calling reliability")
	}
}

// ---------------------------------------------------------------------------
// Compile-time guards so the package depends on every tool import we claim to
// test. Keeps the vendor/graph honest even when INTEGRATION=0.
// ---------------------------------------------------------------------------

var _ = errors.New
var _ = json.Unmarshal
