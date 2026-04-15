package agents_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wall-ai/ubuilding/backend/agents"
)

// ---------------------------------------------------------------------------
// Mock Provider / Deps
// ---------------------------------------------------------------------------

// mockDeps implements agents.QueryDeps for testing.
type mockDeps struct {
	responses []agents.Message // pre-canned assistant responses
	callCount int
	uuidCount int
}

func (m *mockDeps) CallModel(ctx context.Context, params agents.CallModelParams) (<-chan agents.StreamEvent, error) {
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

		// Stream text deltas
		for _, block := range msg.Content {
			if block.Type == agents.ContentBlockText {
				ch <- agents.StreamEvent{Type: agents.EventTextDelta, Text: block.Text}
			}
		}

		ch <- agents.StreamEvent{Type: agents.EventAssistant, Message: &msg}
	}()
	return ch, nil
}

func (m *mockDeps) Microcompact(messages []agents.Message, _ *agents.ToolUseContext, _ string) *agents.MicrocompactResult {
	return &agents.MicrocompactResult{Messages: messages, Applied: false}
}

func (m *mockDeps) Autocompact(_ context.Context, messages []agents.Message, _ *agents.ToolUseContext, _ string, _ string) *agents.AutocompactResult {
	return &agents.AutocompactResult{Messages: messages, Applied: false}
}

func (m *mockDeps) SnipCompact(messages []agents.Message) *agents.SnipCompactResult {
	return &agents.SnipCompactResult{Messages: messages, TokensFreed: 0}
}

func (m *mockDeps) ContextCollapse(_ context.Context, messages []agents.Message, _ *agents.ToolUseContext, _ string) *agents.ContextCollapseResult {
	return &agents.ContextCollapseResult{Messages: messages, Applied: false}
}

func (m *mockDeps) ContextCollapseDrain(messages []agents.Message, _ string) *agents.ContextCollapseDrainResult {
	return &agents.ContextCollapseDrainResult{Messages: messages, Committed: 0}
}

func (m *mockDeps) ReactiveCompact(_ context.Context, _ []agents.Message, _ *agents.ToolUseContext, _ string, _ string, _ bool) *agents.AutocompactResult {
	return nil
}

func (m *mockDeps) ExecuteTools(_ context.Context, calls []agents.ToolUseBlock, assistantMsg *agents.Message, _ *agents.ToolUseContext, _ bool) *agents.ToolExecutionResult {
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

func (m *mockDeps) UUID() string {
	m.uuidCount++
	return "test-uuid-" + string(rune('0'+m.uuidCount))
}

func (m *mockDeps) ApplyToolResultBudget(messages []agents.Message, _ *agents.ToolUseContext, _ string) []agents.Message {
	return messages
}

func (m *mockDeps) GetAttachmentMessages(_ *agents.ToolUseContext) []agents.Message {
	return nil
}

func (m *mockDeps) BuildToolDefinitions(_ *agents.ToolUseContext) []agents.ToolDefinition {
	return nil
}

func (m *mockDeps) StartMemoryPrefetch(_ []agents.Message, _ *agents.ToolUseContext) <-chan []agents.Message {
	ch := make(chan []agents.Message, 1)
	ch <- nil
	close(ch)
	return ch
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestNewQueryEngine(t *testing.T) {
	deps := &mockDeps{}
	config := agents.EngineConfig{
		Cwd:                "/tmp/test",
		UserSpecifiedModel: "claude-sonnet-4-20250514",
		MaxTurns:           10,
	}

	engine := agents.NewQueryEngine(config, deps)
	assert.NotNil(t, engine)
	assert.NotEmpty(t, engine.GetSessionID())
	assert.Empty(t, engine.GetMessages())
}

func TestSubmitMessage_SimpleResponse(t *testing.T) {
	deps := &mockDeps{
		responses: []agents.Message{
			{
				Type:       agents.MessageTypeAssistant,
				StopReason: "end_turn",
				Content: []agents.ContentBlock{
					{Type: agents.ContentBlockText, Text: "Hello! How can I help?"},
				},
				Usage: &agents.Usage{InputTokens: 10, OutputTokens: 5},
			},
		},
	}

	config := agents.EngineConfig{MaxTurns: 10}
	engine := agents.NewQueryEngine(config, deps)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ch := engine.SubmitMessage(ctx, "Hello")

	var events []agents.StreamEvent
	for event := range ch {
		events = append(events, event)
	}

	require.NotEmpty(t, events)

	// Should have: SystemInit, RequestStart, TextDelta, Assistant, Done
	hasSystemInit := false
	hasTextDelta := false
	hasDone := false
	for _, e := range events {
		switch e.Type {
		case agents.EventSystemInit:
			hasSystemInit = true
		case agents.EventTextDelta:
			hasTextDelta = true
			assert.Equal(t, "Hello! How can I help?", e.Text)
		case agents.EventDone:
			hasDone = true
		}
	}
	assert.True(t, hasSystemInit, "should have system init event")
	assert.True(t, hasTextDelta, "should have text delta event")
	assert.True(t, hasDone, "should have done event")
}

func TestSubmitMessage_Cancellation(t *testing.T) {
	deps := &mockDeps{
		responses: []agents.Message{
			{
				Type:       agents.MessageTypeAssistant,
				StopReason: "end_turn",
				Content: []agents.ContentBlock{
					{Type: agents.ContentBlockText, Text: "response"},
				},
			},
		},
	}

	config := agents.EngineConfig{MaxTurns: 10}
	engine := agents.NewQueryEngine(config, deps)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	ch := engine.SubmitMessage(ctx, "Hello")

	var events []agents.StreamEvent
	for event := range ch {
		events = append(events, event)
	}

	// Channel should close quickly after cancellation
	// May have partial events, but should not hang
	assert.NotNil(t, events) // just verify it didn't panic
}

func TestSubmitMessage_WithBuildSystemPromptFn(t *testing.T) {
	// Verify that BuildSystemPromptFn is invoked and the resulting system prompt
	// is passed through to the query loop.
	promptCalled := false
	deps := &mockDeps{
		responses: []agents.Message{{
			Type:       agents.MessageTypeAssistant,
			StopReason: "end_turn",
			Content:    []agents.ContentBlock{{Type: agents.ContentBlockText, Text: "ok"}},
			Usage:      &agents.Usage{InputTokens: 5, OutputTokens: 2},
		}},
	}

	config := agents.EngineConfig{
		MaxTurns: 10,
		BuildSystemPromptFn: func() (string, map[string]string, map[string]string) {
			promptCalled = true
			return "full system prompt", map[string]string{"key": "val"}, nil
		},
	}
	engine := agents.NewQueryEngine(config, deps)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ch := engine.SubmitMessage(ctx, "test")
	for range ch {
	}

	assert.True(t, promptCalled, "BuildSystemPromptFn should have been called")
}

func TestSubmitMessage_LegacyBaseSystemPrompt(t *testing.T) {
	// When BaseSystemPrompt is set, BuildSystemPromptFn should NOT be called.
	fnCalled := false
	deps := &mockDeps{
		responses: []agents.Message{{
			Type:       agents.MessageTypeAssistant,
			StopReason: "end_turn",
			Content:    []agents.ContentBlock{{Type: agents.ContentBlockText, Text: "ok"}},
			Usage:      &agents.Usage{InputTokens: 5, OutputTokens: 2},
		}},
	}

	config := agents.EngineConfig{
		MaxTurns:         10,
		BaseSystemPrompt: "legacy prompt",
		BuildSystemPromptFn: func() (string, map[string]string, map[string]string) {
			fnCalled = true
			return "should not be used", nil, nil
		},
	}
	engine := agents.NewQueryEngine(config, deps)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ch := engine.SubmitMessage(ctx, "test")
	for range ch {
	}

	assert.False(t, fnCalled, "BuildSystemPromptFn should NOT be called when BaseSystemPrompt is set")
}

func TestSubmitMessage_Interrupt(t *testing.T) {
	deps := &mockDeps{
		responses: []agents.Message{
			{
				Type:       agents.MessageTypeAssistant,
				StopReason: "end_turn",
				Content: []agents.ContentBlock{
					{Type: agents.ContentBlockText, Text: "response"},
				},
			},
		},
	}

	config := agents.EngineConfig{MaxTurns: 10}
	engine := agents.NewQueryEngine(config, deps)

	ctx := context.Background()
	ch := engine.SubmitMessage(ctx, "Hello")

	// Interrupt after first event
	for range ch {
		engine.Interrupt()
		break
	}

	// Drain remaining events
	for range ch {
	}
}
