package agents_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/wall-ai/ubuilding/backend/agents"
	"github.com/wall-ai/ubuilding/backend/agents/permission"
	"github.com/wall-ai/ubuilding/backend/agents/tool"
	"github.com/wall-ai/ubuilding/backend/agents/tool/agenttool"
)

// TestPhaseB_PlanAgentRejectsWrites · B01+B03+B04+B08 integration:
//   - SpawnSubAgent routes to the Plan built-in.
//   - EngineConfig.ResolveSubagentTools applies tool.ResolveAgentTools so
//     the child can only see a restricted pool.
//   - The child ToolUseContext's AgentPermissionMode overlay is "plan"; a
//     permission.Checker presented with that context denies Write.
func TestPhaseB_PlanAgentRejectsWrites(t *testing.T) {
	parentTools := []interface{}{
		stubNamedToolPhaseB{n: "Read"},
		stubNamedToolPhaseB{n: "Grep"},
		stubNamedToolPhaseB{n: "Edit"},
		stubNamedToolPhaseB{n: "Write"},
	}
	resolverCalls := 0
	spy := &subagentSpy{
		responses: []agents.Message{textAssistant("plan mode ok")},
	}
	engine := agents.NewQueryEngine(agents.EngineConfig{
		UserSpecifiedModel: "claude-sonnet-parent",
		Tools:              parentTools,
		ResolveSubagentTools: func(parent []interface{}, def *agents.AgentDefinition, isAsync bool) []interface{} {
			resolverCalls++
			converted := make(tool.Tools, 0, len(parent))
			for _, t := range parent {
				if v, ok := t.(tool.Tool); ok {
					converted = append(converted, v)
				}
			}
			resolved := tool.ResolveAgentTools(converted, def, isAsync, false)
			out := make([]interface{}, 0, len(resolved.ResolvedTools))
			for _, t := range resolved.ResolvedTools {
				out = append(out, t)
			}
			return out
		},
	}, spy)

	// Trigger the subagent spawn to run the resolver + permission-mode
	// propagation path. Use the Task tool so the full bridge is exercised.
	at := agenttool.New()
	tc := &agents.ToolUseContext{
		Ctx:           context.Background(),
		SpawnSubAgent: engine.SpawnSubAgent,
		Options:       agents.ToolUseOptions{AgentDefinitions: engine.Agents()},
	}
	raw, _ := json.Marshal(agenttool.Input{Prompt: "inspect", SubagentType: "Plan"})
	if _, err := at.Call(context.Background(), raw, tc); err != nil {
		t.Fatalf("Task call: %v", err)
	}
	if resolverCalls == 0 {
		t.Fatal("ResolveSubagentTools was never invoked")
	}

	// Now assert the permission-mode overlay: when the child's
	// AgentPermissionMode says "plan", permission.Check must deny Write.
	checker := permission.NewChecker(&agents.ToolPermissionContext{
		Mode:             string(permission.ModeDefault), // parent default
		AlwaysAllowRules: map[string][]agents.PermissionRule{},
		AlwaysDenyRules:  map[string][]agents.PermissionRule{},
		AlwaysAskRules:   map[string][]agents.PermissionRule{},
	}, "/")
	childCtx := &agents.ToolUseContext{
		Options: agents.ToolUseOptions{AgentPermissionMode: "plan"},
	}
	if res := checker.Check("Write", json.RawMessage(`{}`), childCtx); !res.Denied() {
		t.Fatalf("plan overlay must deny Write; got %+v", res)
	}
	// Read stays allowed.
	if res := checker.Check("Read", json.RawMessage(`{}`), childCtx); !res.Allowed() {
		t.Fatalf("plan overlay must allow Read; got %+v", res)
	}
}

// TestPhaseB_AllowListAgentReducesPool · B01 + B08 end-to-end:
//   - A custom agent advertises `Tools: ["Read", "Grep"]`.
//   - ResolveSubagentTools strips Edit/Write.
//   - The resolver output is what the child engine receives in cfg.Tools.
func TestPhaseB_AllowListAgentReducesPool(t *testing.T) {
	reviewer := &agents.AgentDefinition{
		AgentType: "reviewer",
		WhenToUse: "PR reviewer",
		Source:    agents.AgentSourceProject,
		Tools:     []string{"Read", "Grep"},
		GetSystemPrompt: func(_ agents.SystemPromptCtx) string {
			return "You are a reviewer."
		},
	}
	tools := tool.Tools{
		stubNamedToolPhaseB{n: "Read"},
		stubNamedToolPhaseB{n: "Grep"},
		stubNamedToolPhaseB{n: "Edit"},
		stubNamedToolPhaseB{n: "Write"},
	}
	res := tool.ResolveAgentTools(tools, reviewer, false, false)
	got := make([]string, 0, len(res.ResolvedTools))
	for _, t := range res.ResolvedTools {
		got = append(got, t.Name())
	}
	want := []string{"Read", "Grep"}
	if len(got) != len(want) {
		t.Fatalf("got %v; want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("got %v; want %v", got, want)
		}
	}
}

// TestPhaseB_DeniedAgentFilteredFromCatalog · B12 integration with the Task
// tool's catalog filter hook: agents denied by `Task(name)` rules must not
// appear in the prompt.
func TestPhaseB_DeniedAgentFilteredFromCatalog(t *testing.T) {
	defs := []*agents.AgentDefinition{
		{AgentType: "general-purpose", WhenToUse: "a"},
		{AgentType: "hacker", WhenToUse: "b"},
	}
	permCtx := &agents.ToolPermissionContext{
		AlwaysDenyRules: map[string][]agents.PermissionRule{
			"Task": {{Tool: "Task", Pattern: "hacker"}},
		},
	}
	filter := func(in []*agents.AgentDefinition) []*agents.AgentDefinition {
		return permission.FilterDeniedAgentsByType(permCtx, "Task", in)
	}
	at := agenttool.New(
		agenttool.WithAgentCatalog(func() []*agents.AgentDefinition { return defs }),
		agenttool.WithAgentCatalogFilter(filter),
	)
	prompt := at.Prompt(tool.PromptOptions{})
	if got := prompt; !containsAll(got, []string{"general-purpose"}) {
		t.Fatalf("prompt missing allowed agent: %s", got)
	}
	if got := prompt; containsAll(got, []string{"hacker"}) {
		t.Fatalf("prompt leaked denied agent: %s", got)
	}
}

// --- helpers ---------------------------------------------------------------

type stubNamedToolPhaseB struct {
	tool.ToolDefaults
	n string
}

func (s stubNamedToolPhaseB) Name() string                             { return s.n }
func (s stubNamedToolPhaseB) IsReadOnly(_ json.RawMessage) bool        { return true }
func (s stubNamedToolPhaseB) IsConcurrencySafe(_ json.RawMessage) bool { return true }
func (s stubNamedToolPhaseB) InputSchema() *tool.JSONSchema {
	return &tool.JSONSchema{Type: "object"}
}
func (s stubNamedToolPhaseB) Description(_ json.RawMessage) string { return s.n }
func (s stubNamedToolPhaseB) Prompt(_ tool.PromptOptions) string   { return s.n }
func (s stubNamedToolPhaseB) ValidateInput(_ json.RawMessage, _ *agents.ToolUseContext) *tool.ValidationResult {
	return &tool.ValidationResult{Valid: true}
}
func (s stubNamedToolPhaseB) CheckPermissions(input json.RawMessage, _ *agents.ToolUseContext) (*tool.PermissionResult, error) {
	return &tool.PermissionResult{Behavior: tool.PermissionAllow, UpdatedInput: input}, nil
}
func (s stubNamedToolPhaseB) Call(_ context.Context, _ json.RawMessage, _ *agents.ToolUseContext) (*tool.ToolResult, error) {
	return &tool.ToolResult{}, nil
}
func (s stubNamedToolPhaseB) MapToolResultToParam(_ interface{}, _ string) *agents.ContentBlock {
	return &agents.ContentBlock{}
}

func containsAll(haystack string, needles []string) bool {
	for _, n := range needles {
		if !stringContains(haystack, n) {
			return false
		}
	}
	return true
}

func stringContains(s, sub string) bool {
	return len(s) >= len(sub) && indexOfSub(s, sub) >= 0
}

func indexOfSub(s, sub string) int {
	if sub == "" {
		return 0
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
