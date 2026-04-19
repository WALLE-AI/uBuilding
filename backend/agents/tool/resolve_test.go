package tool

import (
	"context"
	"encoding/json"
	"reflect"
	"sort"
	"testing"

	"github.com/wall-ai/ubuilding/backend/agents"
	"github.com/wall-ai/ubuilding/backend/agents/permission"
)

// --- Fake tool ---------------------------------------------------------------

type fakeTool struct {
	ToolDefaults
	name string
}

func (f *fakeTool) Name() string                                { return f.name }
func (f *fakeTool) IsReadOnly(_ json.RawMessage) bool           { return true }
func (f *fakeTool) IsConcurrencySafe(_ json.RawMessage) bool    { return true }
func (f *fakeTool) InputSchema() *JSONSchema                    { return &JSONSchema{Type: "object"} }
func (f *fakeTool) Description(_ json.RawMessage) string        { return f.name }
func (f *fakeTool) Prompt(_ PromptOptions) string               { return f.name }
func (f *fakeTool) ValidateInput(_ json.RawMessage, _ *agents.ToolUseContext) *ValidationResult {
	return &ValidationResult{Valid: true}
}
func (f *fakeTool) CheckPermissions(input json.RawMessage, _ *agents.ToolUseContext) (*PermissionResult, error) {
	return &PermissionResult{Behavior: PermissionAllow, UpdatedInput: input}, nil
}
func (f *fakeTool) Call(_ context.Context, _ json.RawMessage, _ *agents.ToolUseContext) (*ToolResult, error) {
	return &ToolResult{}, nil
}
func (f *fakeTool) MapToolResultToParam(content interface{}, toolUseID string) *agents.ContentBlock {
	return &agents.ContentBlock{}
}

var _ Tool = (*fakeTool)(nil)

func fakeTools(names ...string) Tools {
	out := make(Tools, len(names))
	for i, n := range names {
		out[i] = &fakeTool{name: n}
	}
	return out
}

func toolNames(ts Tools) []string {
	out := make([]string, 0, len(ts))
	for _, t := range ts {
		out = append(out, t.Name())
	}
	sort.Strings(out)
	return out
}

// --- FilterToolsForAgent ------------------------------------------------------

func TestFilterToolsForAgent_BaselineDisallow(t *testing.T) {
	tools := fakeTools("Read", "Task", "ExitPlanMode", "AskUserQuestion", "Edit")
	got := FilterToolsForAgent(tools, FilterToolsForAgentOpts{IsBuiltIn: true})
	want := []string{"Edit", "Read"}
	if !reflect.DeepEqual(toolNames(got), want) {
		t.Errorf("baseline filter = %v; want %v", toolNames(got), want)
	}
}

func TestFilterToolsForAgent_PlanModeRestoresExitPlanMode(t *testing.T) {
	tools := fakeTools("Read", "ExitPlanMode")
	got := FilterToolsForAgent(tools, FilterToolsForAgentOpts{
		IsBuiltIn:      true,
		PermissionMode: permission.ModePlan,
	})
	want := []string{"ExitPlanMode", "Read"}
	if !reflect.DeepEqual(toolNames(got), want) {
		t.Errorf("plan mode filter = %v; want %v", toolNames(got), want)
	}
}

func TestFilterToolsForAgent_MCPPassesThrough(t *testing.T) {
	tools := fakeTools("Task", "mcp__slack__list", "mcp__slack__post")
	got := FilterToolsForAgent(tools, FilterToolsForAgentOpts{IsBuiltIn: true})
	want := []string{"mcp__slack__list", "mcp__slack__post"}
	if !reflect.DeepEqual(toolNames(got), want) {
		t.Errorf("mcp filter = %v; want %v", toolNames(got), want)
	}
}

func TestFilterToolsForAgent_AsyncRestricted(t *testing.T) {
	tools := fakeTools("Read", "Edit", "Bash", "Grep", "ExitPlanMode", "ExitWorktree")
	got := FilterToolsForAgent(tools, FilterToolsForAgentOpts{IsBuiltIn: true, IsAsync: true})
	// ExitPlanMode is disallowed baseline; ExitWorktree is in async-allowed;
	// Read/Edit/Bash/Grep are allowed.
	want := []string{"Bash", "Edit", "ExitWorktree", "Grep", "Read"}
	if !reflect.DeepEqual(toolNames(got), want) {
		t.Errorf("async filter = %v; want %v", toolNames(got), want)
	}
}

func TestFilterToolsForAgent_CustomExtraDeny(t *testing.T) {
	tools := fakeTools("Read", "Task", "Edit")
	// Custom agent — baseline still removes Task. (CustomAgentDisallowedTools
	// currently == baseline, but assert the call path uses the custom switch.)
	got := FilterToolsForAgent(tools, FilterToolsForAgentOpts{IsBuiltIn: false})
	want := []string{"Edit", "Read"}
	if !reflect.DeepEqual(toolNames(got), want) {
		t.Errorf("custom filter = %v; want %v", toolNames(got), want)
	}
}

// --- ResolveAgentTools --------------------------------------------------------

func TestResolveAgentTools_Wildcard(t *testing.T) {
	tools := fakeTools("Read", "Edit", "Grep", "Task")
	agent := &agents.AgentDefinition{
		Source: agents.AgentSourceBuiltIn,
		Tools:  []string{"*"},
	}
	res := ResolveAgentTools(tools, agent, false, false)
	if !res.HasWildcard {
		t.Fatal("expected wildcard")
	}
	// Task is stripped by FilterToolsForAgent.
	want := []string{"Edit", "Grep", "Read"}
	if !reflect.DeepEqual(toolNames(res.ResolvedTools), want) {
		t.Errorf("wildcard resolved = %v; want %v", toolNames(res.ResolvedTools), want)
	}
}

func TestResolveAgentTools_Allowlist(t *testing.T) {
	tools := fakeTools("Read", "Edit", "Grep", "Bash")
	agent := &agents.AgentDefinition{
		Source: agents.AgentSourceBuiltIn,
		Tools:  []string{"Read", "Grep"},
	}
	res := ResolveAgentTools(tools, agent, false, false)
	if len(res.InvalidTools) != 0 {
		t.Errorf("InvalidTools = %v", res.InvalidTools)
	}
	want := []string{"Grep", "Read"}
	if !reflect.DeepEqual(toolNames(res.ResolvedTools), want) {
		t.Errorf("allowlist resolved = %v; want %v", toolNames(res.ResolvedTools), want)
	}
}

func TestResolveAgentTools_DenyOverridesAllow(t *testing.T) {
	tools := fakeTools("Read", "Edit")
	agent := &agents.AgentDefinition{
		Source:          agents.AgentSourceUser,
		Tools:           []string{"Read", "Edit"},
		DisallowedTools: []string{"Edit"},
	}
	res := ResolveAgentTools(tools, agent, false, false)
	want := []string{"Read"}
	if !reflect.DeepEqual(toolNames(res.ResolvedTools), want) {
		t.Errorf("deny > allow resolved = %v; want %v", toolNames(res.ResolvedTools), want)
	}
}

func TestResolveAgentTools_AgentSpecCollectsAllowedTypes(t *testing.T) {
	tools := fakeTools("Read")
	agent := &agents.AgentDefinition{
		Source: agents.AgentSourceUser,
		Tools:  []string{"Read", "Agent(worker, researcher)"},
	}
	res := ResolveAgentTools(tools, agent, false, false)
	want := []string{"Read"}
	if !reflect.DeepEqual(toolNames(res.ResolvedTools), want) {
		t.Errorf("resolved = %v; want %v", toolNames(res.ResolvedTools), want)
	}
	if !reflect.DeepEqual(res.AllowedAgentTypes, []string{"worker", "researcher"}) {
		t.Errorf("AllowedAgentTypes = %v", res.AllowedAgentTypes)
	}
}

func TestResolveAgentTools_MCPPrefixMatch(t *testing.T) {
	tools := fakeTools("Read", "mcp__slack__post", "mcp__slack__list")
	agent := &agents.AgentDefinition{
		Source: agents.AgentSourceUser,
		Tools:  []string{"Read", "mcp__slack__post"},
	}
	res := ResolveAgentTools(tools, agent, false, false)
	want := []string{"Read", "mcp__slack__post"}
	if !reflect.DeepEqual(toolNames(res.ResolvedTools), want) {
		t.Errorf("mcp allowlist resolved = %v; want %v", toolNames(res.ResolvedTools), want)
	}
}

func TestResolveAgentTools_NilAgentReturnsInput(t *testing.T) {
	tools := fakeTools("Read")
	res := ResolveAgentTools(tools, nil, false, false)
	if len(res.ResolvedTools) != 1 {
		t.Fatalf("expected passthrough, got %v", res.ResolvedTools)
	}
}
