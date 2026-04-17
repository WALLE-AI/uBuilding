package tool

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/wall-ai/ubuilding/backend/agents"
)

// newFakeTool quickly produces a Tool with the given name.
func newFakeTool(name string) Tool {
	return BuildTool(ToolDef{
		Name:        name,
		InputSchema: func() *JSONSchema { return &JSONSchema{Type: "object"} },
		Description: func(_ json.RawMessage) string { return name },
		Call: func(_ context.Context, _ json.RawMessage, _ *agents.ToolUseContext) (*ToolResult, error) {
			return &ToolResult{Data: name}, nil
		},
		MapToolResultToParam: func(_ interface{}, toolUseID string) *agents.ContentBlock {
			return &agents.ContentBlock{Type: agents.ContentBlockToolResult, ToolUseID: toolUseID}
		},
	})
}

func TestAssembleToolPool_BuiltinPrefix(t *testing.T) {
	r := NewRegistry()
	r.Register(newFakeTool("WebSearch"), WithBuiltin())
	r.Register(newFakeTool("Bash"), WithBuiltin())

	mcp := Tools{
		newFakeTool("mcp__github__list_repos"),
		newFakeTool("mcp__acme__query"),
	}
	pool := AssembleToolPool(r, agents.NewEmptyToolPermissionContext(), mcp)

	wantOrder := []string{
		"Bash", "WebSearch", // built-ins, alphabetical
		"mcp__acme__query", "mcp__github__list_repos", // MCP, alphabetical after built-ins
	}
	if got := pool.Names(); !equalStrings(got, wantOrder) {
		t.Errorf("AssembleToolPool order = %v, want %v", got, wantOrder)
	}
}

func TestAssembleToolPool_DedupBuiltinWins(t *testing.T) {
	r := NewRegistry()
	r.Register(newFakeTool("Shared"), WithBuiltin())
	mcp := Tools{newFakeTool("Shared")}
	pool := AssembleToolPool(r, agents.NewEmptyToolPermissionContext(), mcp)
	names := pool.Names()
	if len(names) != 1 || names[0] != "Shared" {
		t.Errorf("expected [Shared], got %v", names)
	}
}

func TestFilterByDenyRules_Blanket(t *testing.T) {
	tools := Tools{
		newFakeTool("Read"),
		newFakeTool("Bash"),
	}
	permCtx := agents.NewEmptyToolPermissionContext()
	permCtx.AlwaysDenyRules["Bash"] = []agents.PermissionRule{{Tool: "Bash"}}
	out := FilterByDenyRules(tools, permCtx)
	if len(out) != 1 || out[0].Name() != "Read" {
		t.Errorf("Bash should be denied, got %v", out.Names())
	}
}

func TestFilterByDenyRules_MCPServerPrefix(t *testing.T) {
	tools := Tools{
		newFakeTool("mcp__github__a"),
		newFakeTool("mcp__github__b"),
		newFakeTool("mcp__acme__x"),
	}
	permCtx := agents.NewEmptyToolPermissionContext()
	permCtx.AlwaysDenyRules["mcp__github"] = []agents.PermissionRule{{Tool: "mcp__github"}}
	out := FilterByDenyRules(tools, permCtx)
	names := out.Names()
	if len(names) != 1 || names[0] != "mcp__acme__x" {
		t.Errorf("mcp__github__* should be denied, got %v", names)
	}
}

func TestFilterByDenyRules_PatternDoesNotBlanket(t *testing.T) {
	// Rule with a non-empty pattern is NOT a blanket deny — filtering leaves
	// the tool intact (runtime permission check handles per-input rules).
	tools := Tools{newFakeTool("Bash")}
	permCtx := agents.NewEmptyToolPermissionContext()
	permCtx.AlwaysDenyRules["Bash"] = []agents.PermissionRule{{Tool: "Bash", Pattern: "rm -rf*"}}
	out := FilterByDenyRules(tools, permCtx)
	if len(out) != 1 {
		t.Errorf("pattern rule should not blanket-deny, got %v", out.Names())
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
