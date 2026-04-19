package agents

import (
	"context"
	"errors"
	"testing"
)

// E01 · SaveCacheSafeParams / GetLastCacheSafeParams round-trip.
func TestCacheSafeParams_RoundTrip(t *testing.T) {
	SaveCacheSafeParams(nil)
	if got := GetLastCacheSafeParams(); got != nil {
		t.Fatalf("initial slot should be nil, got %+v", got)
	}

	p := &CacheSafeParams{
		SystemPrompt:   "hello",
		UserContext:    map[string]string{"k": "v"},
		SystemContext:  map[string]string{"os": "darwin"},
		ToolUseContext: &ToolUseContext{Ctx: context.Background()},
	}
	SaveCacheSafeParams(p)

	got := GetLastCacheSafeParams()
	if got == nil {
		t.Fatal("GetLastCacheSafeParams returned nil after save")
	}
	if got == p {
		t.Fatal("GetLastCacheSafeParams should return a shallow copy, not the original pointer")
	}
	if got.SystemPrompt != "hello" {
		t.Errorf("SystemPrompt lost: %q", got.SystemPrompt)
	}
	if got.UserContext["k"] != "v" {
		t.Errorf("UserContext lost")
	}
	// Mutating the copy's top-level pointer must not affect later readers.
	got.SystemPrompt = "changed"
	if GetLastCacheSafeParams().SystemPrompt != "hello" {
		t.Fatal("mutating copy leaked back into the slot")
	}
	SaveCacheSafeParams(nil)
	if GetLastCacheSafeParams() != nil {
		t.Fatal("nil save should clear the slot")
	}
}

// E02 · RunForkedAgent without a runner errors with the registered sentinel.
func TestRunForkedAgent_NoRunner(t *testing.T) {
	RegisterForkRunner(nil)
	_, err := RunForkedAgent(context.Background(), ForkedAgentParams{
		PromptMessages: []Message{{Type: MessageTypeUser}},
		CacheSafeParams: CacheSafeParams{
			SystemPrompt:   "x",
			ToolUseContext: &ToolUseContext{Ctx: context.Background()},
		},
	})
	if err == nil || !errors.Is(err, errForkRunnerUnavailable) {
		t.Fatalf("want errForkRunnerUnavailable, got %v", err)
	}
}

// E02 · Installed runner gets invoked with the requested params.
func TestRunForkedAgent_InvokesRegisteredRunner(t *testing.T) {
	var captured ForkedAgentParams
	RegisterForkRunner(func(_ context.Context, p ForkedAgentParams) (*ForkedAgentResult, error) {
		captured = p
		return &ForkedAgentResult{
			Messages: []Message{{Type: MessageTypeAssistant, Content: []ContentBlock{{Type: ContentBlockText, Text: "done"}}}},
			FinalText: "done",
		}, nil
	})
	t.Cleanup(func() { RegisterForkRunner(nil) })

	params := ForkedAgentParams{
		PromptMessages: []Message{{Type: MessageTypeUser}},
		CacheSafeParams: CacheSafeParams{
			SystemPrompt:   "sys",
			ToolUseContext: &ToolUseContext{Ctx: context.Background()},
		},
		QuerySource: "session_memory",
		ForkLabel:   "test",
	}
	res, err := RunForkedAgent(context.Background(), params)
	if err != nil {
		t.Fatalf("RunForkedAgent: %v", err)
	}
	if res == nil || res.FinalText != "done" {
		t.Fatalf("unexpected result: %+v", res)
	}
	if captured.ForkLabel != "test" || captured.QuerySource != "session_memory" {
		t.Fatalf("captured params lost metadata: %+v", captured)
	}
	if !HasForkRunner() {
		t.Fatal("HasForkRunner should be true after RegisterForkRunner")
	}
}

// E02 · validateForkedAgentParams rejects missing required fields.
func TestRunForkedAgent_Validation(t *testing.T) {
	RegisterForkRunner(func(context.Context, ForkedAgentParams) (*ForkedAgentResult, error) {
		t.Fatal("runner should not fire when validation fails")
		return nil, nil
	})
	t.Cleanup(func() { RegisterForkRunner(nil) })

	cases := []struct {
		name   string
		params ForkedAgentParams
	}{
		{
			name: "missing prompt",
			params: ForkedAgentParams{
				CacheSafeParams: CacheSafeParams{SystemPrompt: "x", ToolUseContext: &ToolUseContext{Ctx: context.Background()}},
			},
		},
		{
			name: "missing system prompt",
			params: ForkedAgentParams{
				PromptMessages:  []Message{{Type: MessageTypeUser}},
				CacheSafeParams: CacheSafeParams{ToolUseContext: &ToolUseContext{Ctx: context.Background()}},
			},
		},
		{
			name: "missing tool use context",
			params: ForkedAgentParams{
				PromptMessages:  []Message{{Type: MessageTypeUser}},
				CacheSafeParams: CacheSafeParams{SystemPrompt: "x"},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := RunForkedAgent(context.Background(), tc.params); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

// Helper · ExtractForkFinalText returns the last assistant's text or default.
func TestExtractForkFinalText(t *testing.T) {
	if got := ExtractForkFinalText(nil, "fallback"); got != "fallback" {
		t.Fatalf("empty slice = %q", got)
	}
	msgs := []Message{
		{Type: MessageTypeUser, Content: []ContentBlock{{Type: ContentBlockText, Text: "user"}}},
		{Type: MessageTypeAssistant, Content: []ContentBlock{{Type: ContentBlockText, Text: "first"}}},
		{Type: MessageTypeAssistant, Content: []ContentBlock{{Type: ContentBlockText, Text: "second"}, {Type: ContentBlockText, Text: "trailing"}}},
	}
	if got := ExtractForkFinalText(msgs, "fallback"); got != "second\ntrailing" {
		t.Fatalf("got %q", got)
	}
}
