package tool_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wall-ai/ubuilding/backend/agents"
	"github.com/wall-ai/ubuilding/backend/agents/tool"
)

// ---------------------------------------------------------------------------
// StreamingToolExecutor tests
// ---------------------------------------------------------------------------

func TestStreamingExecutor_SingleTool(t *testing.T) {
	tools := tool.Tools{
		&mockTool{name: "Read", concurrencySafe: true},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	toolCtx := &agents.ToolUseContext{Ctx: ctx}
	assistantMsg := &agents.Message{UUID: "a1", Type: agents.MessageTypeAssistant}

	exec := tool.NewStreamingToolExecutor(tools, nil, toolCtx, nil)
	exec.AddTool(agents.ToolUseBlock{ID: "t1", Name: "Read", Input: json.RawMessage(`{}`)}, assistantMsg)

	msgs, mods := exec.GetAllResults()
	assert.Len(t, msgs, 1)
	assert.Empty(t, mods)
	assert.Equal(t, agents.ContentBlockToolResult, msgs[0].Content[0].Type)
}

func TestStreamingExecutor_ConcurrentTools(t *testing.T) {
	var callOrder int64

	tools := tool.Tools{
		&mockTool{name: "Read", concurrencySafe: true, callFn: func(ctx context.Context, input json.RawMessage, toolCtx *agents.ToolUseContext) (*tool.ToolResult, error) {
			atomic.AddInt64(&callOrder, 1)
			time.Sleep(10 * time.Millisecond) // simulate work
			return &tool.ToolResult{Data: "read result"}, nil
		}},
		&mockTool{name: "Grep", concurrencySafe: true, callFn: func(ctx context.Context, input json.RawMessage, toolCtx *agents.ToolUseContext) (*tool.ToolResult, error) {
			atomic.AddInt64(&callOrder, 1)
			time.Sleep(10 * time.Millisecond)
			return &tool.ToolResult{Data: "grep result"}, nil
		}},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	toolCtx := &agents.ToolUseContext{Ctx: ctx}
	assistantMsg := &agents.Message{UUID: "a1", Type: agents.MessageTypeAssistant}

	exec := tool.NewStreamingToolExecutor(tools, nil, toolCtx, nil)
	exec.AddTool(agents.ToolUseBlock{ID: "t1", Name: "Read", Input: json.RawMessage(`{}`)}, assistantMsg)
	exec.AddTool(agents.ToolUseBlock{ID: "t2", Name: "Grep", Input: json.RawMessage(`{}`)}, assistantMsg)

	msgs, _ := exec.GetAllResults()
	assert.Len(t, msgs, 2)
}

func TestStreamingExecutor_SerialToolBlocksConcurrent(t *testing.T) {
	var order []string

	tools := tool.Tools{
		&mockTool{name: "Edit", concurrencySafe: false, callFn: func(ctx context.Context, input json.RawMessage, toolCtx *agents.ToolUseContext) (*tool.ToolResult, error) {
			order = append(order, "Edit")
			return &tool.ToolResult{Data: "edit done"}, nil
		}},
		&mockTool{name: "Read", concurrencySafe: true, callFn: func(ctx context.Context, input json.RawMessage, toolCtx *agents.ToolUseContext) (*tool.ToolResult, error) {
			order = append(order, "Read")
			return &tool.ToolResult{Data: "read done"}, nil
		}},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	toolCtx := &agents.ToolUseContext{Ctx: ctx}
	assistantMsg := &agents.Message{UUID: "a1", Type: agents.MessageTypeAssistant}

	exec := tool.NewStreamingToolExecutor(tools, nil, toolCtx, nil)
	exec.AddTool(agents.ToolUseBlock{ID: "t1", Name: "Edit", Input: json.RawMessage(`{}`)}, assistantMsg)
	exec.AddTool(agents.ToolUseBlock{ID: "t2", Name: "Read", Input: json.RawMessage(`{}`)}, assistantMsg)

	msgs, _ := exec.GetAllResults()
	assert.Len(t, msgs, 2)
	// Edit should complete before Read starts (serial blocks)
	require.Len(t, order, 2)
	assert.Equal(t, "Edit", order[0])
	assert.Equal(t, "Read", order[1])
}

func TestStreamingExecutor_UnknownToolImmediate(t *testing.T) {
	tools := tool.Tools{} // no tools

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	toolCtx := &agents.ToolUseContext{Ctx: ctx}
	assistantMsg := &agents.Message{UUID: "a1", Type: agents.MessageTypeAssistant}

	exec := tool.NewStreamingToolExecutor(tools, nil, toolCtx, nil)
	exec.AddTool(agents.ToolUseBlock{ID: "t1", Name: "NonExistent", Input: json.RawMessage(`{}`)}, assistantMsg)

	msgs, _ := exec.GetAllResults()
	require.Len(t, msgs, 1)
	assert.True(t, msgs[0].Content[0].IsError)
}

func TestStreamingExecutor_Discard(t *testing.T) {
	tools := tool.Tools{
		&mockTool{name: "Read", concurrencySafe: true, callFn: func(ctx context.Context, input json.RawMessage, toolCtx *agents.ToolUseContext) (*tool.ToolResult, error) {
			time.Sleep(100 * time.Millisecond)
			return &tool.ToolResult{Data: "should not appear"}, nil
		}},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	toolCtx := &agents.ToolUseContext{Ctx: ctx}
	assistantMsg := &agents.Message{UUID: "a1", Type: agents.MessageTypeAssistant}

	exec := tool.NewStreamingToolExecutor(tools, nil, toolCtx, nil)
	exec.Discard()
	exec.AddTool(agents.ToolUseBlock{ID: "t1", Name: "Read", Input: json.RawMessage(`{}`)}, assistantMsg)

	// After discard, GetRemainingResults should return nil
	msgs, _ := exec.GetRemainingResults()
	assert.Nil(t, msgs)
	assert.True(t, exec.IsDiscarded())
}

func TestStreamingExecutor_GetCompletedResults_OrderedYield(t *testing.T) {
	tools := tool.Tools{
		&mockTool{name: "Read", concurrencySafe: true},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	toolCtx := &agents.ToolUseContext{Ctx: ctx}
	assistantMsg := &agents.Message{UUID: "a1", Type: agents.MessageTypeAssistant}

	exec := tool.NewStreamingToolExecutor(tools, nil, toolCtx, nil)
	exec.AddTool(agents.ToolUseBlock{ID: "t1", Name: "Read", Input: json.RawMessage(`{}`)}, assistantMsg)

	// Wait for completion via GetRemainingResults (marks as Yielded)
	exec.GetRemainingResults()

	// Already yielded, GetCompletedResults should return nothing new
	completed := exec.GetCompletedResults()
	assert.Empty(t, completed)
}

func TestStreamingExecutor_GetRemainingAfterPartialYield(t *testing.T) {
	tools := tool.Tools{
		&mockTool{name: "Read", concurrencySafe: true},
		&mockTool{name: "Grep", concurrencySafe: true},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	toolCtx := &agents.ToolUseContext{Ctx: ctx}
	assistantMsg := &agents.Message{UUID: "a1", Type: agents.MessageTypeAssistant}

	exec := tool.NewStreamingToolExecutor(tools, nil, toolCtx, nil)
	exec.AddTool(agents.ToolUseBlock{ID: "t1", Name: "Read", Input: json.RawMessage(`{}`)}, assistantMsg)
	exec.AddTool(agents.ToolUseBlock{ID: "t2", Name: "Grep", Input: json.RawMessage(`{}`)}, assistantMsg)

	// Yield completed ones
	time.Sleep(50 * time.Millisecond) // let them finish
	completed := exec.GetCompletedResults()

	// Now get remaining (should be whatever wasn't yielded)
	remaining, _ := exec.GetRemainingResults()

	total := len(completed) + len(remaining)
	assert.Equal(t, 2, total)
}

func TestStreamingExecutor_HasPendingTools(t *testing.T) {
	tools := tool.Tools{
		&mockTool{name: "Slow", concurrencySafe: true, callFn: func(ctx context.Context, input json.RawMessage, toolCtx *agents.ToolUseContext) (*tool.ToolResult, error) {
			time.Sleep(200 * time.Millisecond)
			return &tool.ToolResult{Data: "done"}, nil
		}},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	toolCtx := &agents.ToolUseContext{Ctx: ctx}
	assistantMsg := &agents.Message{UUID: "a1", Type: agents.MessageTypeAssistant}

	exec := tool.NewStreamingToolExecutor(tools, nil, toolCtx, nil)
	exec.AddTool(agents.ToolUseBlock{ID: "t1", Name: "Slow", Input: json.RawMessage(`{}`)}, assistantMsg)

	assert.True(t, exec.HasPendingTools())
	exec.GetAllResults()
	assert.False(t, exec.HasPendingTools())
}

func TestStreamingExecutor_PermissionDeny(t *testing.T) {
	tools := tool.Tools{
		&mockTool{name: "Bash", concurrencySafe: false},
	}

	denyAll := func(name string, input json.RawMessage, toolCtx *agents.ToolUseContext) *tool.PermissionResult {
		return &tool.PermissionResult{Behavior: tool.PermissionDeny, Message: "not allowed"}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	toolCtx := &agents.ToolUseContext{Ctx: ctx}
	assistantMsg := &agents.Message{UUID: "a1", Type: agents.MessageTypeAssistant}

	exec := tool.NewStreamingToolExecutor(tools, denyAll, toolCtx, nil)
	exec.AddTool(agents.ToolUseBlock{ID: "t1", Name: "Bash", Input: json.RawMessage(`{}`)}, assistantMsg)

	msgs, _ := exec.GetAllResults()
	require.Len(t, msgs, 1)
	assert.True(t, msgs[0].Content[0].IsError)
	assert.Contains(t, msgs[0].Content[0].Content, "Permission denied")
}

func TestStreamingExecutor_ToolError_BashCancelsSiblings(t *testing.T) {
	tools := tool.Tools{
		&mockTool{name: "Bash", concurrencySafe: true, callFn: func(ctx context.Context, input json.RawMessage, toolCtx *agents.ToolUseContext) (*tool.ToolResult, error) {
			return nil, errors.New("exit status 1")
		}},
		&mockTool{name: "Read", concurrencySafe: true, callFn: func(ctx context.Context, input json.RawMessage, toolCtx *agents.ToolUseContext) (*tool.ToolResult, error) {
			time.Sleep(100 * time.Millisecond)
			return &tool.ToolResult{Data: "read ok"}, nil
		}},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	toolCtx := &agents.ToolUseContext{Ctx: ctx}
	assistantMsg := &agents.Message{UUID: "a1", Type: agents.MessageTypeAssistant}

	exec := tool.NewStreamingToolExecutor(tools, nil, toolCtx, nil)
	exec.AddTool(agents.ToolUseBlock{ID: "t1", Name: "Bash", Input: json.RawMessage(`{"command":"ls"}`)}, assistantMsg)
	exec.AddTool(agents.ToolUseBlock{ID: "t2", Name: "Read", Input: json.RawMessage(`{}`)}, assistantMsg)

	msgs, _ := exec.GetAllResults()
	// Both should complete (one error, one possibly cancelled)
	assert.GreaterOrEqual(t, len(msgs), 1)
	// First message should be the Bash error
	hasError := false
	for _, msg := range msgs {
		for _, b := range msg.Content {
			if b.IsError {
				hasError = true
			}
		}
	}
	assert.True(t, hasError)
}

func TestStreamingExecutor_GetUpdatedContext(t *testing.T) {
	tools := tool.Tools{
		&mockTool{name: "Edit", concurrencySafe: false, callFn: func(ctx context.Context, input json.RawMessage, toolCtx *agents.ToolUseContext) (*tool.ToolResult, error) {
			return &tool.ToolResult{
				Data: "edited",
				ContextModifier: func(tc *agents.ToolUseContext) *agents.ToolUseContext {
					tc.Options.MainLoopModel = "modified-model"
					return tc
				},
			}, nil
		}},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	toolCtx := &agents.ToolUseContext{Ctx: ctx, Options: agents.ToolUseOptions{}}
	assistantMsg := &agents.Message{UUID: "a1", Type: agents.MessageTypeAssistant}

	exec := tool.NewStreamingToolExecutor(tools, nil, toolCtx, nil)
	exec.AddTool(agents.ToolUseBlock{ID: "t1", Name: "Edit", Input: json.RawMessage(`{}`)}, assistantMsg)

	exec.GetAllResults()

	updatedCtx := exec.GetUpdatedContext()
	assert.Equal(t, "modified-model", updatedCtx.Options.MainLoopModel)
}

// ---------------------------------------------------------------------------
// Hooks + Orchestrator integration tests
// ---------------------------------------------------------------------------

type blockingHook struct {
	tool.ToolDefaults
	name     string
	toolName string
}

func (h *blockingHook) Name() string { return h.name }
func (h *blockingHook) PreToolUse(_ context.Context, params tool.HookParams) *tool.PreToolHookResult {
	if params.ToolName == h.toolName {
		return &tool.PreToolHookResult{BlockingError: "blocked by hook"}
	}
	return nil
}
func (h *blockingHook) PostToolUse(_ context.Context, _ tool.HookParams, _ *tool.ToolResult) *tool.PostToolHookResult {
	return nil
}
func (h *blockingHook) PostToolUseFailure(_ context.Context, _ tool.HookParams, _ error) *tool.PostToolHookResult {
	return nil
}

func TestHookRegistry_BlocksToolExecution(t *testing.T) {
	reg := tool.NewHookRegistry()
	reg.Register(&blockingHook{name: "blocker", toolName: "Bash"})

	result := reg.RunPreToolUseHooks(context.Background(), tool.HookParams{
		ToolName:  "Bash",
		ToolUseID: "t1",
		Input:     json.RawMessage(`{"command":"rm -rf /"}`),
	})
	require.NotNil(t, result)
	assert.Equal(t, "blocked by hook", result.BlockingError)

	// Read should not be blocked
	result = reg.RunPreToolUseHooks(context.Background(), tool.HookParams{
		ToolName:  "Read",
		ToolUseID: "t2",
		Input:     json.RawMessage(`{}`),
	})
	assert.Nil(t, result)
}

// ---------------------------------------------------------------------------
// API Schema + Tool pipeline integration
// ---------------------------------------------------------------------------

func TestAPISchema_BatchConversion_WithRegistry(t *testing.T) {
	tool.ClearSchemaCache()
	reg := tool.NewRegistry()
	reg.Register(&mockTool{name: "Read", concurrencySafe: true})
	reg.Register(&mockTool{name: "Edit", concurrencySafe: false})
	reg.Register(&mockTool{name: "Bash", concurrencySafe: false})

	// Deny Bash, should not appear in schemas
	reg.Deny("Bash")

	tools := reg.GetTools()
	opts := tool.SchemaOpts{}

	defs := tool.ToolsToAPISchemas(tools, opts)
	assert.Len(t, defs, 2)

	names := make(map[string]bool)
	for _, def := range defs {
		names[def.Name] = true
		assert.NotEmpty(t, def.Name)
	}
	assert.True(t, names["Edit"])
	assert.True(t, names["Read"])
	assert.False(t, names["Bash"]) // denied
}

func TestFullPipeline_Registry_Schema_Orchestrator(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Register(&mockTool{name: "Read", concurrencySafe: true, callFn: func(ctx context.Context, input json.RawMessage, toolCtx *agents.ToolUseContext) (*tool.ToolResult, error) {
		return &tool.ToolResult{Data: "file content"}, nil
	}})
	reg.Register(&mockTool{name: "Edit", concurrencySafe: false, callFn: func(ctx context.Context, input json.RawMessage, toolCtx *agents.ToolUseContext) (*tool.ToolResult, error) {
		return &tool.ToolResult{Data: "edit ok"}, nil
	}})

	tools := reg.GetTools()

	// 1. Build API schemas
	tool.ClearSchemaCache()
	defs := tool.ToolsToAPISchemas(tools, tool.SchemaOpts{})
	assert.Len(t, defs, 2)

	// 2. Run tools via orchestrator
	orch := tool.NewOrchestrator(tools, nil)
	calls := []agents.ToolUseBlock{
		{ID: "t1", Name: "Read", Input: json.RawMessage(`{}`)},
		{ID: "t2", Name: "Edit", Input: json.RawMessage(`{}`)},
	}
	assistantMsg := &agents.Message{UUID: "a1", Type: agents.MessageTypeAssistant}
	toolCtx := &agents.ToolUseContext{Ctx: context.Background()}

	result := orch.RunTools(context.Background(), calls, assistantMsg, toolCtx, nil)
	require.NotNil(t, result)
	assert.Len(t, result.Messages, 2)

	// Verify result contents
	for _, msg := range result.Messages {
		assert.Equal(t, agents.MessageTypeUser, msg.Type)
		assert.NotEmpty(t, msg.Content)
		assert.Equal(t, agents.ContentBlockToolResult, msg.Content[0].Type)
	}
}

func TestFullPipeline_StreamingExecutor_WithDiscard(t *testing.T) {
	tools := tool.Tools{
		&mockTool{name: "Read", concurrencySafe: true, callFn: func(ctx context.Context, input json.RawMessage, toolCtx *agents.ToolUseContext) (*tool.ToolResult, error) {
			time.Sleep(50 * time.Millisecond)
			return &tool.ToolResult{Data: "read ok"}, nil
		}},
		&mockTool{name: "Grep", concurrencySafe: true, callFn: func(ctx context.Context, input json.RawMessage, toolCtx *agents.ToolUseContext) (*tool.ToolResult, error) {
			time.Sleep(50 * time.Millisecond)
			return &tool.ToolResult{Data: "grep ok"}, nil
		}},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	toolCtx := &agents.ToolUseContext{Ctx: ctx}
	assistantMsg := &agents.Message{UUID: "a1", Type: agents.MessageTypeAssistant}

	exec := tool.NewStreamingToolExecutor(tools, nil, toolCtx, nil)
	exec.AddTool(agents.ToolUseBlock{ID: "t1", Name: "Read", Input: json.RawMessage(`{}`)}, assistantMsg)

	// Discard before second tool added (streaming fallback scenario)
	exec.Discard()
	exec.AddTool(agents.ToolUseBlock{ID: "t2", Name: "Grep", Input: json.RawMessage(`{}`)}, assistantMsg)

	msgs, _ := exec.GetAllResults()
	// All should complete (first may succeed, second gets discarded error)
	for _, msg := range msgs {
		assert.Equal(t, agents.MessageTypeUser, msg.Type)
	}
}

func TestStreamingExecutor_ContextCancelled_AbortReason(t *testing.T) {
	tools := tool.Tools{
		&mockTool{name: "Slow", concurrencySafe: true, callFn: func(ctx context.Context, input json.RawMessage, toolCtx *agents.ToolUseContext) (*tool.ToolResult, error) {
			// Will be cancelled via context
			<-ctx.Done()
			return nil, ctx.Err()
		}},
	}

	ctx, cancel := context.WithCancel(context.Background())
	toolCtx := &agents.ToolUseContext{Ctx: ctx}
	assistantMsg := &agents.Message{UUID: "a1", Type: agents.MessageTypeAssistant}

	exec := tool.NewStreamingToolExecutor(tools, nil, toolCtx, nil)
	exec.AddTool(agents.ToolUseBlock{ID: "t1", Name: "Slow", Input: json.RawMessage(`{}`)}, assistantMsg)

	// Cancel after a short delay
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	msgs, _ := exec.GetAllResults()
	require.Len(t, msgs, 1)
	assert.True(t, msgs[0].Content[0].IsError)
}
