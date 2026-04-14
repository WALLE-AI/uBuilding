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

func (m *mockDeps) UUID() string {
	m.uuidCount++
	return "test-uuid-" + string(rune('0'+m.uuidCount))
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
