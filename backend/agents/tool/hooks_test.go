package tool

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wall-ai/ubuilding/backend/agents"
)

// ---------------------------------------------------------------------------
// Mock hook for testing
// ---------------------------------------------------------------------------

type testHook struct {
	name           string
	preResult      *PreToolHookResult
	postResult     *PostToolHookResult
	failureResult  *PostToolHookResult
	preCalled      bool
	postCalled     bool
	failureCalled  bool
}

func (h *testHook) Name() string { return h.name }
func (h *testHook) PreToolUse(_ context.Context, _ HookParams) *PreToolHookResult {
	h.preCalled = true
	return h.preResult
}
func (h *testHook) PostToolUse(_ context.Context, _ HookParams, _ *ToolResult) *PostToolHookResult {
	h.postCalled = true
	return h.postResult
}
func (h *testHook) PostToolUseFailure(_ context.Context, _ HookParams, _ error) *PostToolHookResult {
	h.failureCalled = true
	return h.failureResult
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestHookRegistry_Register(t *testing.T) {
	reg := NewHookRegistry()
	h1 := &testHook{name: "hook1"}
	h2 := &testHook{name: "hook2"}
	h1dup := &testHook{name: "hook1"}

	reg.Register(h1)
	reg.Register(h2)
	reg.Register(h1dup) // duplicate, should be ignored

	reg.mu.RLock()
	assert.Len(t, reg.hooks, 2)
	reg.mu.RUnlock()
}

func TestHookRegistry_Unregister(t *testing.T) {
	reg := NewHookRegistry()
	h1 := &testHook{name: "hook1"}
	h2 := &testHook{name: "hook2"}
	reg.Register(h1)
	reg.Register(h2)

	reg.Unregister("hook1")

	reg.mu.RLock()
	assert.Len(t, reg.hooks, 1)
	assert.Equal(t, "hook2", reg.hooks[0].Name())
	reg.mu.RUnlock()
}

func TestRunPreToolUseHooks_AllAllow(t *testing.T) {
	reg := NewHookRegistry()
	reg.Register(&testHook{name: "h1", preResult: nil})
	reg.Register(&testHook{name: "h2", preResult: nil})

	result := reg.RunPreToolUseHooks(context.Background(), HookParams{
		ToolName: "Read", ToolUseID: "tu1", Input: json.RawMessage(`{}`),
	})
	assert.Nil(t, result) // no hooks returned anything
}

func TestRunPreToolUseHooks_BlockingError(t *testing.T) {
	reg := NewHookRegistry()
	h1 := &testHook{name: "h1", preResult: nil}
	h2 := &testHook{name: "h2", preResult: &PreToolHookResult{BlockingError: "blocked by h2"}}
	h3 := &testHook{name: "h3", preResult: nil}
	reg.Register(h1)
	reg.Register(h2)
	reg.Register(h3)

	result := reg.RunPreToolUseHooks(context.Background(), HookParams{
		ToolName: "Bash", ToolUseID: "tu2", Input: json.RawMessage(`{}`),
	})
	require.NotNil(t, result)
	assert.Equal(t, "blocked by h2", result.BlockingError)
	assert.True(t, h1.preCalled)
	assert.True(t, h2.preCalled)
	assert.False(t, h3.preCalled) // should not be called after blocking
}

func TestRunPreToolUseHooks_ModifiedInput(t *testing.T) {
	reg := NewHookRegistry()
	newInput := json.RawMessage(`{"modified": true}`)
	reg.Register(&testHook{name: "h1", preResult: &PreToolHookResult{ModifiedInput: newInput}})

	result := reg.RunPreToolUseHooks(context.Background(), HookParams{
		ToolName: "Edit", ToolUseID: "tu3", Input: json.RawMessage(`{}`),
	})
	require.NotNil(t, result)
	assert.JSONEq(t, `{"modified": true}`, string(result.ModifiedInput))
}

func TestRunPreToolUseHooks_CancelledContext(t *testing.T) {
	reg := NewHookRegistry()
	reg.Register(&testHook{name: "h1", preResult: nil})

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	result := reg.RunPreToolUseHooks(ctx, HookParams{ToolName: "Read"})
	require.NotNil(t, result)
	assert.Contains(t, result.BlockingError, "aborted")
}

func TestRunPostToolUseHooks_NoHooks(t *testing.T) {
	reg := NewHookRegistry()
	result := reg.RunPostToolUseHooks(context.Background(), HookParams{
		ToolName: "Read",
	}, &ToolResult{Data: "ok"})
	assert.Nil(t, result)
}

func TestRunPostToolUseHooks_AdditionalContext(t *testing.T) {
	reg := NewHookRegistry()
	reg.Register(&testHook{name: "h1", postResult: &PostToolHookResult{
		AdditionalContext: "context from h1",
	}})
	reg.Register(&testHook{name: "h2", postResult: &PostToolHookResult{
		AdditionalContext: "context from h2",
	}})

	result := reg.RunPostToolUseHooks(context.Background(), HookParams{
		ToolName: "Read",
	}, &ToolResult{Data: "ok"})
	require.NotNil(t, result)
	assert.Contains(t, result.AdditionalContext, "context from h1")
	assert.Contains(t, result.AdditionalContext, "context from h2")
}

func TestRunPostToolUseHooks_PreventContinuation(t *testing.T) {
	reg := NewHookRegistry()
	reg.Register(&testHook{name: "h1", postResult: &PostToolHookResult{
		PreventContinuation: true,
		StopReason:          "task done",
	}})

	result := reg.RunPostToolUseHooks(context.Background(), HookParams{
		ToolName: "Bash",
	}, &ToolResult{Data: "ok"})
	require.NotNil(t, result)
	assert.True(t, result.PreventContinuation)
	assert.Equal(t, "task done", result.StopReason)
}

func TestRunPostToolUseHooks_AttachmentMessages(t *testing.T) {
	reg := NewHookRegistry()
	reg.Register(&testHook{name: "h1", postResult: &PostToolHookResult{
		AttachmentMessages: []agents.Message{
			{Type: agents.MessageTypeUser, Content: []agents.ContentBlock{{Type: agents.ContentBlockText, Text: "attachment"}}},
		},
	}})

	result := reg.RunPostToolUseHooks(context.Background(), HookParams{
		ToolName: "Edit",
	}, &ToolResult{Data: "ok"})
	require.NotNil(t, result)
	assert.Len(t, result.AttachmentMessages, 1)
}

func TestRunPostToolUseFailureHooks(t *testing.T) {
	reg := NewHookRegistry()
	h1 := &testHook{name: "h1", failureResult: &PostToolHookResult{
		BlockingError: "fatal error logged",
	}}
	reg.Register(h1)

	result := reg.RunPostToolUseFailureHooks(context.Background(), HookParams{
		ToolName: "Bash",
	}, errors.New("bash exited 1"))
	require.NotNil(t, result)
	assert.Equal(t, "fatal error logged", result.BlockingError)
	assert.True(t, h1.failureCalled)
}

func TestRunPostToolUseFailureHooks_NoHooks(t *testing.T) {
	reg := NewHookRegistry()
	result := reg.RunPostToolUseFailureHooks(context.Background(), HookParams{
		ToolName: "Edit",
	}, errors.New("error"))
	assert.Nil(t, result)
}
