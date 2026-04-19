package agents

import (
	"context"
	"strings"
	"testing"
)

// B02 · CreateSubagentContext clones mutable state.
func TestCreateSubagentContext_ClonesReadFileState(t *testing.T) {
	parentCache := NewFileStateCache()
	parentCache.Set("/a.go", &FileState{Path: "/a.go", Size: 10})
	parent := &ToolUseContext{
		Ctx:           context.Background(),
		ReadFileState: parentCache,
		Options: ToolUseOptions{
			AgentDefinitions: &AgentDefinitions{},
		},
	}
	child := CreateSubagentContext(parent, SubagentContextOverrides{})
	defer child.CancelFunc()

	if child.ReadFileState == parent.ReadFileState {
		t.Fatal("child should own a distinct cache instance")
	}
	if _, ok := child.ReadFileState.Get("/a.go"); !ok {
		t.Fatal("child cache must inherit parent entries")
	}
	// Mutating child must not touch parent.
	child.ReadFileState.Set("/b.go", &FileState{Path: "/b.go"})
	if parent.ReadFileState.Has("/b.go") {
		t.Fatal("child mutation leaked into parent cache")
	}
}

// B04 · Permission mode overlay flows to ToolUseOptions.AgentPermissionMode.
func TestCreateSubagentContext_PermissionModeOverlay(t *testing.T) {
	parent := &ToolUseContext{
		Ctx:           context.Background(),
		ReadFileState: NewFileStateCache(),
		Options:       ToolUseOptions{AgentDefinitions: &AgentDefinitions{}},
	}
	child := CreateSubagentContext(parent, SubagentContextOverrides{
		AgentPermissionMode:          "plan",
		ShouldAvoidPermissionPrompts: true,
	})
	defer child.CancelFunc()

	if child.Options.AgentPermissionMode != "plan" {
		t.Errorf("AgentPermissionMode = %q; want plan", child.Options.AgentPermissionMode)
	}
	if !child.Options.ShouldAvoidPermissionPromptsOverride {
		t.Error("ShouldAvoidPermissionPromptsOverride lost")
	}
}

// B02 · Abort propagates from parent ctx → child ctx.
func TestCreateSubagentContext_AbortPropagates(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	parent := &ToolUseContext{
		Ctx:           ctx,
		CancelFunc:    cancel,
		ReadFileState: NewFileStateCache(),
	}
	child := CreateSubagentContext(parent, SubagentContextOverrides{})
	defer child.CancelFunc()

	cancel()
	// Child ctx must now be done.
	select {
	case <-child.Ctx.Done():
	default:
		t.Fatal("child ctx did not cancel after parent cancel")
	}
}

// B02 · SetAppState isolation — child's setter is a no-op unless shared.
func TestCreateSubagentContext_SetAppStateIsolated(t *testing.T) {
	written := 0
	parent := &ToolUseContext{
		Ctx:           context.Background(),
		ReadFileState: NewFileStateCache(),
		SetAppState: func(f func(prev *AppState) *AppState) {
			written++
		},
	}
	child := CreateSubagentContext(parent, SubagentContextOverrides{})
	defer child.CancelFunc()
	child.SetAppState(func(prev *AppState) *AppState { return prev })
	if written != 0 {
		t.Fatalf("child SetAppState leaked to parent (%d writes)", written)
	}

	// Share opt-in reaches parent.
	childShared := CreateSubagentContext(parent, SubagentContextOverrides{ShareSetAppState: true})
	defer childShared.CancelFunc()
	childShared.SetAppState(func(prev *AppState) *AppState { return prev })
	if written != 1 {
		t.Fatalf("shared SetAppState should forward; got %d", written)
	}
}

// B02 · Metadata overrides flow through.
func TestCreateSubagentContext_Metadata(t *testing.T) {
	parent := &ToolUseContext{
		Ctx:           context.Background(),
		ReadFileState: NewFileStateCache(),
	}
	child := CreateSubagentContext(parent, SubagentContextOverrides{
		AgentID:              "agt-1",
		AgentType:            "Explore",
		ToolUseID:            "tool-123",
		RenderedSystemPrompt: "base",
	})
	defer child.CancelFunc()
	if child.AgentID != "agt-1" || child.AgentType != "Explore" {
		t.Errorf("metadata lost: %+v", child)
	}
	if child.ToolUseID != "tool-123" || child.RenderedSystemPrompt != "base" {
		t.Errorf("override fields lost: %+v", child)
	}
}

// B03 · SpawnSubAgent consults ResolveSubagentTools to filter the pool.
func TestSpawnSubAgent_InvokesResolver(t *testing.T) {
	var gotAgentType string
	var parentPool []interface{}

	spy := &subagentSpy{
		responses: []Message{textAssistantInPkg("ok")},
	}
	engine := NewQueryEngine(EngineConfig{
		UserSpecifiedModel: "claude-sonnet-parent",
		Tools:              []interface{}{stubNamedTool{"Read"}, stubNamedTool{"Edit"}},
		ResolveSubagentTools: func(parent []interface{}, def *AgentDefinition, isAsync bool) []interface{} {
			parentPool = parent
			gotAgentType = def.AgentType
			return []interface{}{stubNamedTool{"Read"}} // simulate allow-list
		},
	}, spy)
	_, err := engine.SpawnSubAgent(context.Background(), SubAgentParams{Prompt: "p"})
	if err != nil {
		t.Fatalf("SpawnSubAgent: %v", err)
	}
	if gotAgentType != "general-purpose" {
		t.Fatalf("resolver saw agent %q", gotAgentType)
	}
	if len(parentPool) != 2 {
		t.Fatalf("resolver saw parent pool size %d; want 2", len(parentPool))
	}
}

// B03 · Agent PermissionMode flows into child engine config → ToolUseOptions.
func TestSpawnSubAgent_PropagatesAgentPermissionMode(t *testing.T) {
	plan := PlanAgent // Plan has PermissionMode="plan" and OmitClaudeMd=true.
	spy := &subagentSpy{responses: []Message{textAssistantInPkg("planned")}}
	engine := NewQueryEngine(EngineConfig{
		UserSpecifiedModel: "claude-sonnet-parent",
	}, spy)

	// Exercise the builder directly so we can inspect the child cfg without
	// running the full child engine (which would need a mock provider hook).
	childCfg := engine.buildChildEngineConfig(&plan, SubAgentParams{})
	if childCfg.SubagentPermissionMode != "plan" {
		t.Fatalf("child cfg SubagentPermissionMode = %q", childCfg.SubagentPermissionMode)
	}
	// Build the default ToolUseContext through an interim engine.
	childEngine := NewQueryEngine(childCfg, spy)
	tuc := childEngine.defaultToolUseContext(context.Background())
	defer tuc.CancelFunc()
	if tuc.Options.AgentPermissionMode != "plan" {
		t.Fatalf("child ToolUseContext permission mode = %q", tuc.Options.AgentPermissionMode)
	}
	// System prompt built by the agent definition retains plan guard text.
	if !strings.Contains(childCfg.BaseSystemPrompt, "READ-ONLY") {
		t.Fatalf("child system prompt missing Plan's read-only guard: %q", childCfg.BaseSystemPrompt)
	}
}

// --- helpers ---------------------------------------------------------------

type stubNamedTool struct{ n string }

func (s stubNamedTool) Name() string { return s.n }

func textAssistantInPkg(text string) Message {
	return Message{
		Type: MessageTypeAssistant,
		UUID: "asst-" + text[:minN(text, 4)],
		Content: []ContentBlock{
			{Type: ContentBlockText, Text: text},
		},
		StopReason: "end_turn",
	}
}

// subagentSpy mirrors the external test double but lives in-package so this
// test can construct child engines without exposing internal fields to the
// tests package. It only implements the QueryDeps methods exercised here.
type subagentSpy struct {
	responses []Message
	calls     int
}

func (s *subagentSpy) CallModel(_ context.Context, params CallModelParams) (<-chan StreamEvent, error) {
	ch := make(chan StreamEvent, 4)
	idx := s.calls
	if idx >= len(s.responses) {
		idx = len(s.responses) - 1
	}
	msg := s.responses[idx]
	s.calls++
	go func() {
		defer close(ch)
		ch <- StreamEvent{Type: EventRequestStart}
		ch <- StreamEvent{Type: EventAssistant, Message: &msg}
	}()
	return ch, nil
}

func (s *subagentSpy) Microcompact(messages []Message, _ *ToolUseContext, _ string) *MicrocompactResult {
	return &MicrocompactResult{Messages: messages, Applied: false}
}
func (s *subagentSpy) Autocompact(_ context.Context, messages []Message, _ *ToolUseContext, _ string, _ string) *AutocompactResult {
	return &AutocompactResult{Messages: messages, Applied: false}
}
func (s *subagentSpy) SnipCompact(messages []Message) *SnipCompactResult {
	return &SnipCompactResult{Messages: messages}
}
func (s *subagentSpy) ContextCollapse(_ context.Context, messages []Message, _ *ToolUseContext, _ string) *ContextCollapseResult {
	return &ContextCollapseResult{Messages: messages}
}
func (s *subagentSpy) ContextCollapseDrain(messages []Message, _ string) *ContextCollapseDrainResult {
	return &ContextCollapseDrainResult{Messages: messages}
}
func (s *subagentSpy) ReactiveCompact(_ context.Context, _ []Message, _ *ToolUseContext, _ string, _ string, _ bool) *AutocompactResult {
	return nil
}
func (s *subagentSpy) ExecuteTools(_ context.Context, _ []ToolUseBlock, _ *Message, _ *ToolUseContext, _ bool) *ToolExecutionResult {
	return &ToolExecutionResult{}
}
func (s *subagentSpy) UUID() string { return "uuid-stub" }
func (s *subagentSpy) ApplyToolResultBudget(messages []Message, _ *ToolUseContext, _ string) []Message {
	return messages
}
func (s *subagentSpy) GetAttachmentMessages(_ *ToolUseContext) []Message { return nil }
func (s *subagentSpy) BuildToolDefinitions(_ *ToolUseContext) []ToolDefinition {
	return nil
}
func (s *subagentSpy) StartMemoryPrefetch(_ []Message, _ *ToolUseContext) <-chan []Message {
	ch := make(chan []Message, 1)
	ch <- nil
	close(ch)
	return ch
}
func (s *subagentSpy) ConsumeMemoryPrefetch(_ <-chan []Message) []Message { return nil }

func minN(s string, n int) int {
	if len(s) < n {
		return len(s)
	}
	return n
}
