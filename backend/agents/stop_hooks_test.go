package agents_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/wall-ai/ubuilding/backend/agents"
)

// ---------------------------------------------------------------------------
// Stop Hooks Tests
// ---------------------------------------------------------------------------

func TestStopHookRegistry_NoHooks(t *testing.T) {
	reg := agents.NewStopHookRegistry()
	state := &agents.LoopState{TurnCount: 5, Messages: []agents.Message{}}
	result := reg.HandleStopHooks(context.Background(), state, "end_turn", nil, false, nil)
	assert.NotNil(t, result)
	assert.False(t, result.PreventContinuation)
	assert.Empty(t, result.BlockingErrors)
	assert.Equal(t, 0, result.HookCount)
}

func TestMaxTurnsHook_Triggers(t *testing.T) {
	reg := agents.NewStopHookRegistry()
	reg.Register(&agents.MaxTurnsHook{MaxTurns: 3})

	state := &agents.LoopState{TurnCount: 3, Messages: []agents.Message{}}
	ch := make(chan agents.StreamEvent, 16)
	result := reg.HandleStopHooks(context.Background(), state, "end_turn", nil, false, ch)

	assert.True(t, result.PreventContinuation)
	assert.Contains(t, result.StopReason, "3")
	assert.Equal(t, 1, result.HookCount)
}

func TestMaxTurnsHook_DoesNotTrigger(t *testing.T) {
	reg := agents.NewStopHookRegistry()
	reg.Register(&agents.MaxTurnsHook{MaxTurns: 10})

	state := &agents.LoopState{TurnCount: 2, Messages: []agents.Message{}}
	result := reg.HandleStopHooks(context.Background(), state, "end_turn", nil, false, nil)

	assert.False(t, result.PreventContinuation)
	assert.Equal(t, 0, result.HookCount)
}

func TestBudgetExhaustedHook(t *testing.T) {
	bt := agents.NewBudgetTracker(&agents.TaskBudget{Total: 100})
	bt.RecordIteration(agents.Usage{InputTokens: 80, OutputTokens: 80})

	reg := agents.NewStopHookRegistry()
	reg.Register(&agents.BudgetExhaustedHook{Tracker: bt})

	state := &agents.LoopState{TurnCount: 1, Messages: []agents.Message{}}
	ch := make(chan agents.StreamEvent, 16)
	result := reg.HandleStopHooks(context.Background(), state, "end_turn", nil, false, ch)

	assert.True(t, result.PreventContinuation)
	assert.Equal(t, "Token budget exhausted", result.StopReason)
}

func TestApiErrorSkipHook(t *testing.T) {
	reg := agents.NewStopHookRegistry()
	reg.Register(&agents.ApiErrorSkipHook{})

	state := &agents.LoopState{
		TurnCount: 1,
		Messages: []agents.Message{
			{Type: agents.MessageTypeAssistant, IsApiError: true, Content: []agents.ContentBlock{{Type: agents.ContentBlockText, Text: "error"}}},
		},
	}
	result := reg.HandleStopHooks(context.Background(), state, "end_turn", nil, false, nil)

	assert.True(t, result.PreventContinuation)
	assert.Contains(t, result.StopReason, "API error")
}

func TestHandleStopHooks_GuardsRecursion(t *testing.T) {
	reg := agents.NewStopHookRegistry()
	reg.Register(&agents.MaxTurnsHook{MaxTurns: 1})

	// Even though MaxTurns would trigger, stopHookActive=true prevents it
	state := &agents.LoopState{TurnCount: 5, Messages: []agents.Message{}}
	result := reg.HandleStopHooks(context.Background(), state, "end_turn", nil, true, nil)

	assert.False(t, result.PreventContinuation)
	assert.Equal(t, 0, result.HookCount)
}

func TestHandleStopHooks_CancellationDuringExec(t *testing.T) {
	reg := agents.NewStopHookRegistry()
	reg.Register(&agents.MaxTurnsHook{MaxTurns: 1})

	state := &agents.LoopState{TurnCount: 5, Messages: []agents.Message{}}
	ch := make(chan agents.StreamEvent, 16)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Pre-cancel

	result := reg.HandleStopHooks(ctx, state, "end_turn", nil, false, ch)
	assert.True(t, result.PreventContinuation)
}
