package agents_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wall-ai/ubuilding/backend/agents"
)

// ---------------------------------------------------------------------------
// queryloop-specific mock deps
// ---------------------------------------------------------------------------

// qloopDeps extends mockDeps with hooks for controlling test scenarios.
type qloopDeps struct {
	responses      []agents.Message // responses for each CallModel invocation
	callCount      int
	uuidCount      int
	autocompactFn  func(ctx context.Context, msgs []agents.Message) *agents.AutocompactResult
	microcompactFn func(msgs []agents.Message) *agents.MicrocompactResult
}

func (m *qloopDeps) CallModel(_ context.Context, params agents.CallModelParams) (<-chan agents.StreamEvent, error) {
	ch := make(chan agents.StreamEvent, 16)
	go func() {
		defer close(ch)
		ch <- agents.StreamEvent{Type: agents.EventRequestStart}

		idx := m.callCount
		if idx >= len(m.responses) {
			idx = len(m.responses) - 1
		}
		if idx < 0 {
			return
		}
		msg := m.responses[idx]
		m.callCount++

		// Invoke streaming fallback callback if set
		if params.OnStreamingFallback != nil && msg.StopReason == "fallback_trigger" {
			params.OnStreamingFallback()
		}

		for _, block := range msg.Content {
			if block.Type == agents.ContentBlockText {
				ch <- agents.StreamEvent{Type: agents.EventTextDelta, Text: block.Text}
			}
		}

		ch <- agents.StreamEvent{Type: agents.EventAssistant, Message: &msg}
	}()
	return ch, nil
}

func (m *qloopDeps) Microcompact(messages []agents.Message, _ *agents.ToolUseContext, _ string) *agents.MicrocompactResult {
	if m.microcompactFn != nil {
		return m.microcompactFn(messages)
	}
	return &agents.MicrocompactResult{Messages: messages, Applied: false}
}

func (m *qloopDeps) Autocompact(ctx context.Context, messages []agents.Message, _ *agents.ToolUseContext, _ string, _ string) *agents.AutocompactResult {
	if m.autocompactFn != nil {
		return m.autocompactFn(ctx, messages)
	}
	return &agents.AutocompactResult{Messages: messages, Applied: false}
}

func (m *qloopDeps) SnipCompact(messages []agents.Message) *agents.SnipCompactResult {
	return &agents.SnipCompactResult{Messages: messages, TokensFreed: 0}
}

func (m *qloopDeps) ContextCollapse(_ context.Context, messages []agents.Message, _ *agents.ToolUseContext, _ string) *agents.ContextCollapseResult {
	return &agents.ContextCollapseResult{Messages: messages, Applied: false}
}

func (m *qloopDeps) ContextCollapseDrain(messages []agents.Message, _ string) *agents.ContextCollapseDrainResult {
	return &agents.ContextCollapseDrainResult{Messages: messages, Committed: 0}
}

func (m *qloopDeps) ReactiveCompact(_ context.Context, _ []agents.Message, _ *agents.ToolUseContext, _ string, _ string, _ bool) *agents.AutocompactResult {
	return nil
}

func (m *qloopDeps) ExecuteTools(_ context.Context, calls []agents.ToolUseBlock, assistantMsg *agents.Message, _ *agents.ToolUseContext, _ bool) *agents.ToolExecutionResult {
	var msgs []agents.Message
	for _, call := range calls {
		msgs = append(msgs, agents.Message{
			Type: agents.MessageTypeUser,
			Content: []agents.ContentBlock{{
				Type:      agents.ContentBlockToolResult,
				ToolUseID: call.ID,
				Content:   "mock result for " + call.Name,
			}},
			SourceToolAssistantUUID: assistantMsg.UUID,
		})
	}
	return &agents.ToolExecutionResult{Messages: msgs}
}

func (m *qloopDeps) UUID() string {
	m.uuidCount++
	return fmt.Sprintf("uuid-%d", m.uuidCount)
}

func (m *qloopDeps) ApplyToolResultBudget(messages []agents.Message, _ *agents.ToolUseContext, _ string) []agents.Message {
	return messages
}

func (m *qloopDeps) GetAttachmentMessages(_ *agents.ToolUseContext) []agents.Message {
	return nil
}

func (m *qloopDeps) BuildToolDefinitions(_ *agents.ToolUseContext) []agents.ToolDefinition {
	return nil
}

func (m *qloopDeps) StartMemoryPrefetch(_ []agents.Message, _ *agents.ToolUseContext) <-chan []agents.Message {
	ch := make(chan []agents.Message, 1)
	ch <- nil
	close(ch)
	return ch
}

// collectEvents drains a StreamEvent channel into a slice.
func collectEvents(ch <-chan agents.StreamEvent) []agents.StreamEvent {
	var events []agents.StreamEvent
	for e := range ch {
		events = append(events, e)
	}
	return events
}

// hasEventType checks if an event type exists in the slice.
func hasEventType(events []agents.StreamEvent, t agents.EventType) bool {
	for _, e := range events {
		if e.Type == t {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestQueryLoop_SimpleEndTurn(t *testing.T) {
	deps := &qloopDeps{
		responses: []agents.Message{
			{
				Type:       agents.MessageTypeAssistant,
				StopReason: "end_turn",
				Content:    []agents.ContentBlock{{Type: agents.ContentBlockText, Text: "Done"}},
				Usage:      &agents.Usage{InputTokens: 10, OutputTokens: 5},
			},
		},
	}

	ch := make(chan agents.StreamEvent, 128)
	params := agents.QueryParams{
		Messages:     []agents.Message{{Type: agents.MessageTypeUser, Content: []agents.ContentBlock{{Type: agents.ContentBlockText, Text: "Hi"}}}},
		SystemPrompt: "test prompt",
		MaxTurns:     10,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go func() {
		defer close(ch)
		_ = agents.QueryLoop(ctx, params, deps, ch, nil)
	}()

	events := collectEvents(ch)
	assert.True(t, hasEventType(events, agents.EventTextDelta))
	assert.True(t, hasEventType(events, agents.EventAssistant))
}

func TestQueryLoop_CancellationBeforeCall(t *testing.T) {
	deps := &qloopDeps{
		responses: []agents.Message{
			{Type: agents.MessageTypeAssistant, StopReason: "end_turn", Content: []agents.ContentBlock{{Type: agents.ContentBlockText, Text: "response"}}},
		},
	}

	ch := make(chan agents.StreamEvent, 128)
	params := agents.QueryParams{
		Messages: []agents.Message{{Type: agents.MessageTypeUser, Content: []agents.ContentBlock{{Type: agents.ContentBlockText, Text: "Hi"}}}},
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	terminal := agents.QueryLoop(ctx, params, deps, ch, nil)
	assert.Equal(t, agents.TerminalAbortedStreaming, terminal.Reason)
}

func TestQueryLoop_MaxTurnsStopsLoop(t *testing.T) {
	// Two turns: first has tool_use, second has end_turn
	deps := &qloopDeps{
		responses: []agents.Message{
			{
				Type:       agents.MessageTypeAssistant,
				StopReason: "end_turn",
				Content: []agents.ContentBlock{
					{Type: agents.ContentBlockText, Text: "Let me check"},
					{Type: agents.ContentBlockToolUse, ID: "tool-1", Name: "Read", Input: []byte(`{"path":"f.txt"}`)},
				},
			},
			{
				Type:       agents.MessageTypeAssistant,
				StopReason: "end_turn",
				Content: []agents.ContentBlock{
					{Type: agents.ContentBlockText, Text: "result"},
					{Type: agents.ContentBlockToolUse, ID: "tool-2", Name: "Read", Input: []byte(`{"path":"g.txt"}`)},
				},
			},
		},
	}

	ch := make(chan agents.StreamEvent, 256)
	params := agents.QueryParams{
		Messages: []agents.Message{{Type: agents.MessageTypeUser, Content: []agents.ContentBlock{{Type: agents.ContentBlockText, Text: "go"}}}},
		MaxTurns: 1, // stop after 1 turn
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go func() {
		defer close(ch)
		terminal := agents.QueryLoop(ctx, params, deps, ch, nil)
		assert.Equal(t, agents.TerminalMaxTurns, terminal.Reason)
	}()

	events := collectEvents(ch)
	// Should have an attachment for max_turns_reached
	hasAttachment := false
	for _, e := range events {
		if e.Type == agents.EventAttachment && e.Message != nil && e.Message.Attachment != nil {
			if e.Message.Attachment.Type == "max_turns_reached" {
				hasAttachment = true
			}
		}
	}
	assert.True(t, hasAttachment, "should emit max_turns_reached attachment")
}

func TestQueryLoop_WithheldMaxOutputTokensRecovery(t *testing.T) {
	// First response: withheld max_tokens error (API error with max_tokens stop)
	// Second response: normal completion
	deps := &qloopDeps{
		responses: []agents.Message{
			{
				Type:       agents.MessageTypeAssistant,
				StopReason: "max_tokens",
				IsApiError: true,
				Content:    []agents.ContentBlock{{Type: agents.ContentBlockText, Text: "max_output_tokens exceeded"}},
			},
			{
				Type:       agents.MessageTypeAssistant,
				StopReason: "end_turn",
				Content:    []agents.ContentBlock{{Type: agents.ContentBlockText, Text: "Completed"}},
				Usage:      &agents.Usage{InputTokens: 10, OutputTokens: 5},
			},
		},
	}

	ch := make(chan agents.StreamEvent, 256)
	params := agents.QueryParams{
		Messages: []agents.Message{{Type: agents.MessageTypeUser, Content: []agents.ContentBlock{{Type: agents.ContentBlockText, Text: "Do work"}}}},
		MaxTurns: 10,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go func() {
		defer close(ch)
		_ = agents.QueryLoop(ctx, params, deps, ch, nil)
	}()

	events := collectEvents(ch)
	require.NotEmpty(t, events)

	// Should have retried (escalation) and then completed
	assert.GreaterOrEqual(t, deps.callCount, 2, "should have made at least 2 API calls (escalation + retry)")
}

func TestQueryLoop_ToolExecution(t *testing.T) {
	// Response with a tool_use block
	deps := &qloopDeps{
		responses: []agents.Message{
			{
				Type:       agents.MessageTypeAssistant,
				StopReason: "end_turn",
				Content: []agents.ContentBlock{
					{Type: agents.ContentBlockText, Text: "Let me read that"},
					{Type: agents.ContentBlockToolUse, ID: "tu-1", Name: "Read", Input: []byte(`{"path":"file.txt"}`)},
				},
			},
			{
				Type:       agents.MessageTypeAssistant,
				StopReason: "end_turn",
				Content:    []agents.ContentBlock{{Type: agents.ContentBlockText, Text: "Here's the content"}},
				Usage:      &agents.Usage{InputTokens: 20, OutputTokens: 10},
			},
		},
	}

	ch := make(chan agents.StreamEvent, 256)
	params := agents.QueryParams{
		Messages: []agents.Message{{Type: agents.MessageTypeUser, Content: []agents.ContentBlock{{Type: agents.ContentBlockText, Text: "Read file"}}}},
		MaxTurns: 10,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go func() {
		defer close(ch)
		terminal := agents.QueryLoop(ctx, params, deps, ch, nil)
		assert.Equal(t, agents.TerminalCompleted, terminal.Reason)
	}()

	events := collectEvents(ch)

	// Should have tool use and tool result events
	hasToolUse := hasEventType(events, agents.EventToolUse)
	hasToolResult := hasEventType(events, agents.EventToolResult)
	assert.True(t, hasToolUse, "should have tool_use event")
	assert.True(t, hasToolResult, "should have tool_result event")

	// Should have made 2 API calls
	assert.Equal(t, 2, deps.callCount)
}

func TestQueryLoop_BlockingLimitWithoutReactiveCompact(t *testing.T) {
	// Generate large messages to trigger blocking limit
	// The blocking check only runs when ReactiveCompact gate is off.
	// We use BuildQueryConfig which reads env — instead we test the
	// calculateTokenWarningState behavior directly via engine integration.
	// This test verifies that the loop exits properly on model error.

	deps := &qloopDeps{
		responses: nil, // no responses needed — should not reach API
	}

	ch := make(chan agents.StreamEvent, 128)

	// Large system prompt to push over 95% of 200k context
	bigPrompt := make([]byte, 850000) // ~212k tokens at 4 chars/token → >95%
	for i := range bigPrompt {
		bigPrompt[i] = 'a'
	}

	params := agents.QueryParams{
		Messages:     []agents.Message{{Type: agents.MessageTypeUser, Content: []agents.ContentBlock{{Type: agents.ContentBlockText, Text: "hi"}}}},
		SystemPrompt: string(bigPrompt),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Note: The default config has ReactiveCompact: true, so the blocking preempt
	// is skipped. This test just validates the API error path works.
	// We'll test the full blocking path differently via a direct function test.
	terminal := agents.QueryLoop(ctx, params, deps, ch, nil)
	// With ReactiveCompact: true (default), we reach the API call with nil responses
	assert.Equal(t, agents.TerminalModelError, terminal.Reason)
}
