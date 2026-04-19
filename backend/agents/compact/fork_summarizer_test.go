package compact

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/wall-ai/ubuilding/backend/agents"
)

// installForkRunner registers a fake ForkRunner for the duration of the
// test. Returns the cleanup closure.
func installForkRunner(t *testing.T, fn func(ctx context.Context, params agents.ForkedAgentParams) (*agents.ForkedAgentResult, error)) func() {
	t.Helper()
	agents.RegisterForkRunner(fn)
	return func() { agents.RegisterForkRunner(nil) }
}

func TestNewForkCompactCallModel_HappyPath(t *testing.T) {
	// Seed a CacheSafeParams + runner that echoes the summary prompt.
	agents.SaveCacheSafeParams(&agents.CacheSafeParams{
		SystemPrompt:   "SYS",
		ToolUseContext: &agents.ToolUseContext{},
	})
	t.Cleanup(func() { agents.SaveCacheSafeParams(nil) })

	var capturedPrompt string
	var runnerCalls int32
	t.Cleanup(installForkRunner(t, func(_ context.Context, params agents.ForkedAgentParams) (*agents.ForkedAgentResult, error) {
		atomic.AddInt32(&runnerCalls, 1)
		for _, msg := range params.PromptMessages {
			for _, blk := range msg.Content {
				capturedPrompt += blk.Text
			}
		}
		return &agents.ForkedAgentResult{FinalText: "SUMMARY: " + capturedPrompt[:8]}, nil
	}))

	call := NewForkCompactCallModel(ForkSummarizerOptions{})
	ch, err := call(context.Background(), agents.CallModelParams{
		Messages: []agents.Message{{
			Type:    agents.MessageTypeUser,
			Content: []agents.ContentBlock{{Type: agents.ContentBlockText, Text: "Please summarize"}},
		}},
	})
	if err != nil {
		t.Fatalf("call: %v", err)
	}

	var events []agents.StreamEvent
	for ev := range ch {
		events = append(events, ev)
	}
	if len(events) != 1 || events[0].Type != agents.EventAssistant {
		t.Fatalf("expected 1 assistant event, got %+v", events)
	}
	body := events[0].Message.Content[0].Text
	if !strings.HasPrefix(body, "SUMMARY:") {
		t.Fatalf("summary body = %q", body)
	}
	if atomic.LoadInt32(&runnerCalls) != 1 {
		t.Fatalf("runner calls = %d", runnerCalls)
	}
}

func TestNewForkCompactCallModel_FallbackWhenNoRunner(t *testing.T) {
	agents.SaveCacheSafeParams(nil) // no cache-safe params
	agents.RegisterForkRunner(nil)  // no runner

	var fallbackCalled int32
	fallback := ForkSummarizerCallModel(func(_ context.Context, _ agents.CallModelParams) (<-chan agents.StreamEvent, error) {
		atomic.AddInt32(&fallbackCalled, 1)
		ch := make(chan agents.StreamEvent, 1)
		close(ch)
		return ch, nil
	})

	call := NewForkCompactCallModel(ForkSummarizerOptions{Fallback: fallback})
	_, err := call(context.Background(), agents.CallModelParams{
		Messages: []agents.Message{{Type: agents.MessageTypeUser, Content: []agents.ContentBlock{{Type: agents.ContentBlockText, Text: "x"}}}},
	})
	if err != nil {
		t.Fatalf("fallback path should not error: %v", err)
	}
	if atomic.LoadInt32(&fallbackCalled) != 1 {
		t.Fatalf("fallback calls = %d", fallbackCalled)
	}
}

func TestNewForkCompactCallModel_ErrorWhenUnavailable(t *testing.T) {
	agents.SaveCacheSafeParams(nil)
	agents.RegisterForkRunner(nil)

	call := NewForkCompactCallModel(ForkSummarizerOptions{})
	_, err := call(context.Background(), agents.CallModelParams{
		Messages: []agents.Message{{Type: agents.MessageTypeUser, Content: []agents.ContentBlock{{Type: agents.ContentBlockText, Text: "x"}}}},
	})
	if err == nil {
		t.Fatal("expected error when no cache-safe params + no fallback")
	}
}

func TestNewForkCompactCallModel_EmptyMessagesRejected(t *testing.T) {
	agents.SaveCacheSafeParams(&agents.CacheSafeParams{
		SystemPrompt:   "SYS",
		ToolUseContext: &agents.ToolUseContext{},
	})
	t.Cleanup(func() { agents.SaveCacheSafeParams(nil) })
	t.Cleanup(installForkRunner(t, func(context.Context, agents.ForkedAgentParams) (*agents.ForkedAgentResult, error) {
		return &agents.ForkedAgentResult{FinalText: "x"}, nil
	}))

	call := NewForkCompactCallModel(ForkSummarizerOptions{})
	_, err := call(context.Background(), agents.CallModelParams{})
	if err == nil {
		t.Fatal("empty Messages should error")
	}
}

func TestNewForkCompactCallModel_PropagatesRunnerError(t *testing.T) {
	agents.SaveCacheSafeParams(&agents.CacheSafeParams{
		SystemPrompt:   "SYS",
		ToolUseContext: &agents.ToolUseContext{},
	})
	t.Cleanup(func() { agents.SaveCacheSafeParams(nil) })
	t.Cleanup(installForkRunner(t, func(context.Context, agents.ForkedAgentParams) (*agents.ForkedAgentResult, error) {
		return nil, errors.New("model down")
	}))

	call := NewForkCompactCallModel(ForkSummarizerOptions{})
	_, err := call(context.Background(), agents.CallModelParams{
		Messages: []agents.Message{{Type: agents.MessageTypeUser, Content: []agents.ContentBlock{{Type: agents.ContentBlockText, Text: "x"}}}},
	})
	if err == nil || !strings.Contains(err.Error(), "model down") {
		t.Fatalf("expected runner error, got %v", err)
	}
}

func TestEnableForkSummarizerForCompactor_WrapsExistingCallModel(t *testing.T) {
	var legacyCalled int32
	legacy := func(context.Context, agents.CallModelParams) (<-chan agents.StreamEvent, error) {
		atomic.AddInt32(&legacyCalled, 1)
		ch := make(chan agents.StreamEvent, 1)
		close(ch)
		return ch, nil
	}
	ac := NewAutoCompactor(legacy)
	EnableForkSummarizerForCompactor(ac, ForkSummarizerOptions{})

	// With no fork runner or cache-safe params, the new CallModel should
	// fall back to the previous legacy.
	agents.SaveCacheSafeParams(nil)
	agents.RegisterForkRunner(nil)

	_, err := ac.CallModel(context.Background(), agents.CallModelParams{
		Messages: []agents.Message{{Type: agents.MessageTypeUser, Content: []agents.ContentBlock{{Type: agents.ContentBlockText, Text: "x"}}}},
	})
	if err != nil {
		t.Fatalf("wrapped call: %v", err)
	}
	if atomic.LoadInt32(&legacyCalled) != 1 {
		t.Fatalf("legacy CallModel fallback not invoked")
	}
}

func TestEnableForkSummarizerForCompactor_NilSafe(t *testing.T) {
	EnableForkSummarizerForCompactor(nil, ForkSummarizerOptions{}) // must not panic
}
