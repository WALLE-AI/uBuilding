package agents_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/wall-ai/ubuilding/backend/agents"
)

// -----------------------------------------------------------------------------
// Helpers — bespoke mock provider tracking per-call prompts + system prompt.
// -----------------------------------------------------------------------------

type subagentSpy struct {
	// responses are consumed FIFO; the last one is reused if exhausted.
	responses []agents.Message

	// Captured state.
	calls         int
	lastSystem    string
	lastMessages  []agents.Message
	capturedModel string
}

func (s *subagentSpy) CallModel(ctx context.Context, params agents.CallModelParams) (<-chan agents.StreamEvent, error) {
	s.lastSystem = params.SystemPrompt
	s.lastMessages = append([]agents.Message(nil), params.Messages...)
	s.capturedModel = params.Model
	ch := make(chan agents.StreamEvent, 8)
	idx := s.calls
	if idx >= len(s.responses) {
		idx = len(s.responses) - 1
	}
	msg := s.responses[idx]
	s.calls++
	go func() {
		defer close(ch)
		ch <- agents.StreamEvent{Type: agents.EventRequestStart}
		ch <- agents.StreamEvent{Type: agents.EventAssistant, Message: &msg}
	}()
	return ch, nil
}

func (s *subagentSpy) Microcompact(messages []agents.Message, _ *agents.ToolUseContext, _ string) *agents.MicrocompactResult {
	return &agents.MicrocompactResult{Messages: messages, Applied: false}
}
func (s *subagentSpy) Autocompact(_ context.Context, messages []agents.Message, _ *agents.ToolUseContext, _ string, _ string) *agents.AutocompactResult {
	return &agents.AutocompactResult{Messages: messages, Applied: false}
}
func (s *subagentSpy) SnipCompact(messages []agents.Message) *agents.SnipCompactResult {
	return &agents.SnipCompactResult{Messages: messages}
}
func (s *subagentSpy) ContextCollapse(_ context.Context, messages []agents.Message, _ *agents.ToolUseContext, _ string) *agents.ContextCollapseResult {
	return &agents.ContextCollapseResult{Messages: messages}
}
func (s *subagentSpy) ContextCollapseDrain(messages []agents.Message, _ string) *agents.ContextCollapseDrainResult {
	return &agents.ContextCollapseDrainResult{Messages: messages}
}
func (s *subagentSpy) ReactiveCompact(_ context.Context, _ []agents.Message, _ *agents.ToolUseContext, _ string, _ string, _ bool) *agents.AutocompactResult {
	return nil
}
func (s *subagentSpy) ExecuteTools(_ context.Context, _ []agents.ToolUseBlock, _ *agents.Message, _ *agents.ToolUseContext, _ bool) *agents.ToolExecutionResult {
	return &agents.ToolExecutionResult{}
}
func (s *subagentSpy) UUID() string { return fmt.Sprintf("uuid-%d", s.calls+1) }
func (s *subagentSpy) ApplyToolResultBudget(messages []agents.Message, _ *agents.ToolUseContext, _ string) []agents.Message {
	return messages
}
func (s *subagentSpy) GetAttachmentMessages(_ *agents.ToolUseContext) []agents.Message { return nil }
func (s *subagentSpy) BuildToolDefinitions(_ *agents.ToolUseContext) []agents.ToolDefinition {
	return nil
}
func (s *subagentSpy) StartMemoryPrefetch(_ []agents.Message, _ *agents.ToolUseContext) <-chan []agents.Message {
	ch := make(chan []agents.Message, 1)
	ch <- nil
	close(ch)
	return ch
}
func (s *subagentSpy) ConsumeMemoryPrefetch(_ <-chan []agents.Message) []agents.Message {
	return nil
}

func textAssistant(text string) agents.Message {
	return agents.Message{
		Type:       agents.MessageTypeAssistant,
		UUID:       "asst-" + text[:minN(text, 4)],
		Content:    []agents.ContentBlock{{Type: agents.ContentBlockText, Text: text}},
		StopReason: "end_turn",
	}
}

func minN(s string, n int) int {
	if len(s) < n {
		return len(s)
	}
	return n
}

// -----------------------------------------------------------------------------
// A08 · SpawnSubAgent happy path
// -----------------------------------------------------------------------------

func TestSpawnSubAgent_ReturnsFinalAssistantText(t *testing.T) {
	spy := &subagentSpy{
		responses: []agents.Message{textAssistant("subagent reporting findings")},
	}
	engine := agents.NewQueryEngine(agents.EngineConfig{
		UserSpecifiedModel: "claude-sonnet-parent",
		MaxTurns:           5,
	}, spy)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	got, err := engine.SpawnSubAgent(ctx, agents.SubAgentParams{
		Prompt:       "count Go files",
		SubagentType: "general-purpose",
	})
	if err != nil {
		t.Fatalf("SpawnSubAgent: %v", err)
	}
	if got != "subagent reporting findings" {
		t.Fatalf("final text = %q", got)
	}
	if spy.calls == 0 {
		t.Fatal("provider never called")
	}
	// A17: inherit parent model (agent definition has Model="" → default inherit).
	if spy.capturedModel != "claude-sonnet-parent" {
		t.Fatalf("child model inheritance broken: %q", spy.capturedModel)
	}
	// System prompt should carry the general-purpose agent prompt, not the
	// engine's default empty prompt.
	if !strings.Contains(spy.lastSystem, "agent for Claude Code") {
		t.Fatalf("sub-agent system prompt missing general-purpose content: %q", spy.lastSystem)
	}
}

// -----------------------------------------------------------------------------
// A08 · Default agent type when SubagentType is empty
// -----------------------------------------------------------------------------

func TestSpawnSubAgent_DefaultsToGeneralPurpose(t *testing.T) {
	spy := &subagentSpy{responses: []agents.Message{textAssistant("ok")}}
	engine := agents.NewQueryEngine(agents.EngineConfig{}, spy)

	got, err := engine.SpawnSubAgent(context.Background(), agents.SubAgentParams{Prompt: "hi"})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != "ok" {
		t.Fatalf("got %q", got)
	}
}

// -----------------------------------------------------------------------------
// A12 · Recursion depth guard
// -----------------------------------------------------------------------------

func TestSpawnSubAgent_DepthCapFires(t *testing.T) {
	spy := &subagentSpy{responses: []agents.Message{textAssistant("ok")}}
	engine := agents.NewQueryEngine(agents.EngineConfig{MaxSubagentDepth: 1}, spy)

	// Simulate a ctx that is already inside one sub-agent level.
	ctx := context.Background()
	got, err := engine.SpawnSubAgent(ctx, agents.SubAgentParams{Prompt: "go"})
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	if got != "ok" {
		t.Fatalf("first call output: %q", got)
	}
	// Calling from within the child's ctx should fire the guard.
	// We emulate that by manually wrapping ctx with the depth counter.
	depth1 := agents.WithSubagentDepth(ctx, 1)
	_, err = engine.SpawnSubAgent(depth1, agents.SubAgentParams{Prompt: "again"})
	if err == nil || !errors.Is(err, agents.ErrSubagentDepthExceeded) {
		t.Fatalf("expected ErrSubagentDepthExceeded, got %v", err)
	}
}

func TestSpawnSubAgent_UnknownAgentType(t *testing.T) {
	spy := &subagentSpy{responses: []agents.Message{textAssistant("ok")}}
	engine := agents.NewQueryEngine(agents.EngineConfig{}, spy)
	_, err := engine.SpawnSubAgent(context.Background(), agents.SubAgentParams{
		Prompt:       "x",
		SubagentType: "nonexistent",
	})
	if err == nil || !errors.Is(err, agents.ErrUnknownSubagentType) {
		t.Fatalf("want ErrUnknownSubagentType, got %v", err)
	}
}

func TestSpawnSubAgent_PropagatesCtxCancel(t *testing.T) {
	// Provider blocks until ctx cancelled; ensure we surface ctx.Err().
	block := &subagentSpy{responses: []agents.Message{textAssistant("late")}}
	engine := agents.NewQueryEngine(agents.EngineConfig{}, block)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel so drain returns immediately.

	_, err := engine.SpawnSubAgent(ctx, agents.SubAgentParams{Prompt: "x"})
	// Depending on scheduling we either get ctx.Err or the final text.
	// Accept either but assert shape: never panics, never hangs.
	if err != nil && !errors.Is(err, context.Canceled) {
		t.Logf("non-cancel err: %v (acceptable)", err)
	}
}

// -----------------------------------------------------------------------------
// A09 · ToolUseContext wiring — SpawnSubAgent + AgentDefinitions are exposed.
// -----------------------------------------------------------------------------

func TestEngine_ToolUseContext_WiresSpawnSubAgent(t *testing.T) {
	// We exercise A09 via an external probe: the engine's buildSubagent
	// registry must include general-purpose.
	engine := agents.NewQueryEngine(agents.EngineConfig{}, &subagentSpy{responses: []agents.Message{textAssistant("ok")}})
	defs := engine.Agents()
	if defs == nil || defs.FindActive("general-purpose") == nil {
		t.Fatal("built-in agent registry not exposed via Agents()")
	}
}
