package agents_test

// Plumbing-level integration tests for the ported toolset (Read/Edit/Write,
// Glob/Grep, Todo, AskUser, PlanMode, Task*). No LLM is involved — the test
// drives the same ExecuteTools bridge that the engine uses at runtime, so it
// exercises the real Tool implementations end-to-end.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wall-ai/ubuilding/backend/agents"
	"github.com/wall-ai/ubuilding/backend/agents/tool"
	"github.com/wall-ai/ubuilding/backend/agents/tool/askuser"
	"github.com/wall-ai/ubuilding/backend/agents/tool/bash"
	"github.com/wall-ai/ubuilding/backend/agents/tool/bg"
	"github.com/wall-ai/ubuilding/backend/agents/tool/builtin"
	"github.com/wall-ai/ubuilding/backend/agents/tool/fileio"
	"github.com/wall-ai/ubuilding/backend/agents/tool/glob"
	"github.com/wall-ai/ubuilding/backend/agents/tool/grep"
	"github.com/wall-ai/ubuilding/backend/agents/tool/notebook"
	"github.com/wall-ai/ubuilding/backend/agents/tool/planmode"
	"github.com/wall-ai/ubuilding/backend/agents/tool/powershell"
	"github.com/wall-ai/ubuilding/backend/agents/tool/taskgraph"
	"github.com/wall-ai/ubuilding/backend/agents/tool/todo"
)

// newPortedDeps builds a toolsDeps with the full ported toolset registered.
func newPortedDeps(t *testing.T, roots ...string) *toolsDeps {
	t.Helper()
	reg := tool.NewRegistry()
	builtin.RegisterAll(reg, builtin.Options{WorkspaceRoots: roots})
	return &toolsDeps{registry: reg}
}

func runOne(t *testing.T, deps *toolsDeps, toolCtx *agents.ToolUseContext, name string, input interface{}) *agents.Message {
	t.Helper()
	raw, err := json.Marshal(input)
	require.NoError(t, err)
	if toolCtx == nil {
		toolCtx = &agents.ToolUseContext{Ctx: context.Background(), ReadFileState: agents.NewFileStateCache()}
	}
	assistantMsg := &agents.Message{UUID: "asst_" + name, Type: agents.MessageTypeAssistant}
	calls := []agents.ToolUseBlock{{ID: "tu_" + name, Name: name, Input: raw}}
	res := deps.ExecuteTools(context.Background(), calls, assistantMsg, toolCtx, false)
	require.NotNil(t, res)
	require.Len(t, res.Messages, 1)
	return &res.Messages[0]
}

func TestIntegration_Ported_BuildsAllTools(t *testing.T) {
	deps := newPortedDeps(t)
	defs := deps.BuildToolDefinitions(nil)
	names := make(map[string]bool, len(defs))
	for _, d := range defs {
		names[d.Name] = true
	}

	want := []string{"WebSearch", "WebFetch", "Read", "Edit", "Write", "NotebookEdit", "Glob", "Grep",
		"TodoWrite", "AskUserQuestion", "ExitPlanMode", "EnterPlanMode",
		"SendUserMessage",
		"ListMcpResourcesTool", "ReadMcpResourceTool",
		"TaskOutput", "TaskStop",
		"TaskCreate", "TaskGet", "TaskUpdate", "TaskList",
		"Task", "Bash"}
	_ = runtime.GOOS
	for _, n := range want {
		assert.True(t, names[n], "missing tool %q in builtin.AllTools (got: %v)", n, keys(names))
	}
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestIntegration_Ported_ReadEditWriteRoundTrip(t *testing.T) {
	dir := t.TempDir()
	deps := newPortedDeps(t, dir)
	tc := &agents.ToolUseContext{Ctx: context.Background(), ReadFileState: agents.NewFileStateCache()}

	// Write a new file.
	target := filepath.Join(dir, "a.txt")
	runOne(t, deps, tc, "Write", fileio.WriteInput{FilePath: target, Content: "hello world\n"})
	data, err := os.ReadFile(target)
	require.NoError(t, err)
	require.Equal(t, "hello world\n", string(data))

	// Read it (populates ReadFileState).
	msg := runOne(t, deps, tc, "Read", fileio.ReadInput{FilePath: target})
	require.Len(t, msg.Content, 1)
	content, _ := msg.Content[0].Content.(string)
	assert.Contains(t, content, "hello world")

	// Edit it (requires fresh read via tc).
	runOne(t, deps, tc, "Edit", fileio.EditInput{FilePath: target, OldString: "hello", NewString: "hi"})
	data, _ = os.ReadFile(target)
	assert.Equal(t, "hi world\n", string(data))
}

func TestIntegration_Ported_GlobAndGrep(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a\nfunc Foo() {}\n"), 0o644)
	os.MkdirAll(filepath.Join(dir, "sub"), 0o755)
	os.WriteFile(filepath.Join(dir, "sub", "b.go"), []byte("package b\nfunc Bar() {}\n"), 0o644)

	deps := newPortedDeps(t, dir)

	msg := runOne(t, deps, nil, "Glob", glob.Input{Pattern: "**/*.go", Path: dir})
	text, _ := msg.Content[0].Content.(string)
	assert.Contains(t, text, "a.go")
	assert.Contains(t, text, "b.go")

	msg = runOne(t, deps, nil, "Grep", grep.Input{Pattern: "^func ", Path: dir, Glob: "**/*.go"})
	text, _ = msg.Content[0].Content.(string)
	assert.Contains(t, text, "Foo")
	assert.Contains(t, text, "Bar")
}

func TestIntegration_Ported_NotebookEdit(t *testing.T) {
	dir := t.TempDir()
	nbPath := filepath.Join(dir, "nb.ipynb")
	os.WriteFile(nbPath, []byte(`{"cells":[{"cell_type":"code","id":"c1","metadata":{},"source":["1+1\n"],"outputs":[],"execution_count":null}],"metadata":{},"nbformat":4,"nbformat_minor":5}`), 0o644)

	deps := newPortedDeps(t, dir)
	runOne(t, deps, nil, "NotebookEdit", notebook.Input{NotebookPath: nbPath, CellID: "c1", NewSource: "2+2"})
	body, _ := os.ReadFile(nbPath)
	assert.Contains(t, string(body), "2+2")
}

func TestIntegration_Ported_Todo(t *testing.T) {
	deps := newPortedDeps(t)
	store := todo.NewStore()
	tc := &agents.ToolUseContext{Ctx: context.Background(), TodoStore: store}
	runOne(t, deps, tc, "TodoWrite", todo.Input{Todos: []todo.Item{
		{ID: "1", Content: "do", ActiveForm: "doing", Status: "in_progress", Priority: "high"},
	}})
	snap := store.Snapshot()
	require.Len(t, snap, 1)
	assert.Equal(t, "in_progress", snap[0].Status)
}

func TestIntegration_Ported_AskUser(t *testing.T) {
	deps := newPortedDeps(t)
	var seen agents.AskUserPayload
	tc := &agents.ToolUseContext{
		Ctx: context.Background(),
		AskUser: func(_ context.Context, p agents.AskUserPayload) (agents.AskUserResponse, error) {
			seen = p
			return agents.AskUserResponse{Selected: []string{"A"}}, nil
		},
	}
	msg := runOne(t, deps, tc, "AskUserQuestion", askuser.Input{
		Question: "pick?",
		Options:  []agents.AskUserOption{{Label: "A"}, {Label: "B"}},
	})
	assert.Equal(t, "pick?", seen.Question)
	text, _ := msg.Content[0].Content.(string)
	assert.Contains(t, text, "Selected: A")
}

func TestIntegration_Ported_PlanModeExit(t *testing.T) {
	deps := newPortedDeps(t)
	tc := &agents.ToolUseContext{Ctx: context.Background(), PlanMode: planmode.ModePlan}
	runOne(t, deps, tc, "ExitPlanMode", planmode.Input{Plan: "do the thing"})
	assert.Equal(t, planmode.ModeNormal, tc.PlanMode)
}

func TestIntegration_Ported_BgTaskLifecycle(t *testing.T) {
	deps := newPortedDeps(t)
	mgr := bg.NewManager()
	tc := &agents.ToolUseContext{Ctx: context.Background(), TaskManager: mgr}

	// 1) Kick off via Bash (or PowerShell on Windows) with run_in_background.
	var startMsg *agents.Message
	if runtime.GOOS == "windows" {
		startMsg = runOne(t, deps, tc, "Bash", powershell.Input{
			Command: "Write-Output bg-tasked", RunInBackground: true,
		})
	} else {
		startMsg = runOne(t, deps, tc, "Bash", bash.Input{
			Command: "echo bg-tasked", RunInBackground: true,
		})
	}

	// Tool renderer is synchronous bash's format by default; the bg branch
	// returns BgStartOutput via JSON. Extract the bash_id directly from the
	// bg manager instead of parsing the rendered text.
	var id string
	for _, j := range mgr.List() {
		id = j.ID
	}
	require.NotEmpty(t, id, "bg manager should have recorded the job")

	_, err := mgr.WaitForTerminal(context.Background(), id)
	require.NoError(t, err)
	_ = startMsg

	// 2) Poll via TaskOutput.
	outMsg := runOne(t, deps, tc, "TaskOutput", bg.OutputInput{BashID: id})
	outText, _ := outMsg.Content[0].Content.(string)
	assert.Contains(t, outText, "succeeded")
	assert.Contains(t, outText, "bg-tasked")
}

func TestIntegration_Ported_TaskGraphCRUD(t *testing.T) {
	deps := newPortedDeps(t)
	store := taskgraph.NewStore()
	tc := &agents.ToolUseContext{Ctx: context.Background(), TaskGraph: store}

	// Create.
	msg := runOne(t, deps, tc, "TaskCreate", taskgraph.CreateInput{Title: "plan"})
	text, _ := msg.Content[0].Content.(string)
	assert.Contains(t, text, "plan")
	nodes := store.List("")
	require.Len(t, nodes, 1)
	id := nodes[0].ID

	// Update status to completed.
	status := taskgraph.StatusCompleted
	runOne(t, deps, tc, "TaskUpdate", taskgraph.UpdateInput{ID: id, Status: &status})

	// Get back.
	getMsg := runOne(t, deps, tc, "TaskGet", taskgraph.GetInput{ID: id})
	getText, _ := getMsg.Content[0].Content.(string)
	assert.Contains(t, getText, taskgraph.StatusCompleted)

	// List filter.
	listMsg := runOne(t, deps, tc, "TaskList", taskgraph.ListInput{Status: taskgraph.StatusCompleted})
	listText, _ := listMsg.Content[0].Content.(string)
	assert.Contains(t, listText, id)
}

func TestIntegration_Ported_TaskStopDualDispatch(t *testing.T) {
	deps := newPortedDeps(t)
	mgr := bg.NewManager()
	store := taskgraph.NewStore()
	tc := &agents.ToolUseContext{Ctx: context.Background(), TaskManager: mgr, TaskGraph: store}

	// Stop a bg job.
	id, _ := mgr.Start(context.Background(), "loop", func(ctx context.Context, _ func(string)) (int, error) {
		<-ctx.Done()
		return -1, ctx.Err()
	})
	msg := runOne(t, deps, tc, "TaskStop", bg.StopInput{ID: id})
	text, _ := msg.Content[0].Content.(string)
	assert.Contains(t, text, "bg")

	// Stop a task-graph node.
	n, _ := store.Add(taskgraph.Node{Title: "wip", Status: taskgraph.StatusInProgress})
	msg = runOne(t, deps, tc, "TaskStop", bg.StopInput{ID: n.ID})
	text, _ = msg.Content[0].Content.(string)
	assert.Contains(t, text, "graph")
}

func TestIntegration_Ported_ShellPlatformRoute(t *testing.T) {
	deps := newPortedDeps(t)
	tc := &agents.ToolUseContext{Ctx: context.Background()}
	if runtime.GOOS == "windows" {
		msg := runOne(t, deps, tc, "Bash", powershell.Input{Command: "Write-Output routed"})
		text, _ := msg.Content[0].Content.(string)
		assert.Contains(t, text, "routed")
	} else {
		msg := runOne(t, deps, tc, "Bash", bash.Input{Command: "echo routed"})
		text, _ := msg.Content[0].Content.(string)
		assert.Contains(t, text, "routed")
	}
}

// TestIntegration_Ported_ExecutorTimeout guards against hangs in the shell
// executor when the child command exceeds its timeout.
func TestIntegration_Ported_ExecutorTimeout(t *testing.T) {
	deps := newPortedDeps(t)
	tc := &agents.ToolUseContext{Ctx: context.Background()}
	start := time.Now()
	if runtime.GOOS == "windows" {
		runOne(t, deps, tc, "Bash", powershell.Input{Command: "Start-Sleep -Seconds 5", Timeout: 300})
	} else {
		runOne(t, deps, tc, "Bash", bash.Input{Command: "sleep 5", Timeout: 300})
	}
	assert.Less(t, time.Since(start), 3*time.Second, "timeout enforcement too slow")
}
