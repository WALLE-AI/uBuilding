package tool_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/wall-ai/ubuilding/backend/agents"
	"github.com/wall-ai/ubuilding/backend/agents/tool"
)

// ---------------------------------------------------------------------------
// Mock tool for testing
// ---------------------------------------------------------------------------

type mockTool struct {
	tool.ToolDefaults
	name            string
	concurrencySafe bool
	callFn          func(ctx context.Context, input json.RawMessage, toolCtx *agents.ToolUseContext) (*tool.ToolResult, error)
}

func (t *mockTool) Name() string                          { return t.name }
func (t *mockTool) Description(_ json.RawMessage) string  { return "mock tool" }
func (t *mockTool) InputSchema() *tool.JSONSchema          { return &tool.JSONSchema{Type: "object"} }
func (t *mockTool) IsConcurrencySafe(_ json.RawMessage) bool { return t.concurrencySafe }
func (t *mockTool) Prompt(_ tool.PromptOptions) string      { return "" }
func (t *mockTool) MapToolResultToParam(result interface{}, toolUseID string) *agents.ContentBlock {
	return &agents.ContentBlock{
		Type:      agents.ContentBlockToolResult,
		ToolUseID: toolUseID,
		Content:   result,
	}
}
func (t *mockTool) Call(ctx context.Context, input json.RawMessage, toolCtx *agents.ToolUseContext) (*tool.ToolResult, error) {
	if t.callFn != nil {
		return t.callFn(ctx, input, toolCtx)
	}
	return &tool.ToolResult{Data: "mock result for " + t.name}, nil
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestPartitionToolCalls_AllConcurrent(t *testing.T) {
	tools := tool.Tools{
		&mockTool{name: "Read", concurrencySafe: true},
		&mockTool{name: "Grep", concurrencySafe: true},
	}

	orch := tool.NewOrchestrator(tools, nil)

	calls := []agents.ToolUseBlock{
		{ID: "1", Name: "Read", Input: json.RawMessage(`{}`)},
		{ID: "2", Name: "Grep", Input: json.RawMessage(`{}`)},
	}

	assistantMsg := &agents.Message{UUID: "assistant-1", Type: agents.MessageTypeAssistant}
	toolCtx := &agents.ToolUseContext{Ctx: context.Background()}

	result := orch.RunTools(context.Background(), calls, assistantMsg, toolCtx, nil)
	assert.NotNil(t, result)
	assert.Len(t, result.Messages, 2)
}

func TestPartitionToolCalls_SerialTool(t *testing.T) {
	tools := tool.Tools{
		&mockTool{name: "Edit", concurrencySafe: false},
	}

	orch := tool.NewOrchestrator(tools, nil)

	calls := []agents.ToolUseBlock{
		{ID: "1", Name: "Edit", Input: json.RawMessage(`{}`)},
	}

	assistantMsg := &agents.Message{UUID: "assistant-1", Type: agents.MessageTypeAssistant}
	toolCtx := &agents.ToolUseContext{Ctx: context.Background()}

	result := orch.RunTools(context.Background(), calls, assistantMsg, toolCtx, nil)
	assert.NotNil(t, result)
	assert.Len(t, result.Messages, 1)
}

func TestPartitionToolCalls_MixedConcurrency(t *testing.T) {
	tools := tool.Tools{
		&mockTool{name: "Read", concurrencySafe: true},
		&mockTool{name: "Edit", concurrencySafe: false},
		&mockTool{name: "Grep", concurrencySafe: true},
	}

	orch := tool.NewOrchestrator(tools, nil)

	// Read(concurrent) → Edit(serial) → Grep(concurrent)
	// Should produce 3 groups: [Read], [Edit], [Grep]
	calls := []agents.ToolUseBlock{
		{ID: "1", Name: "Read", Input: json.RawMessage(`{}`)},
		{ID: "2", Name: "Edit", Input: json.RawMessage(`{}`)},
		{ID: "3", Name: "Grep", Input: json.RawMessage(`{}`)},
	}

	assistantMsg := &agents.Message{UUID: "assistant-1", Type: agents.MessageTypeAssistant}
	toolCtx := &agents.ToolUseContext{Ctx: context.Background()}

	result := orch.RunTools(context.Background(), calls, assistantMsg, toolCtx, nil)
	assert.NotNil(t, result)
	assert.Len(t, result.Messages, 3)
}

func TestPartitionToolCalls_UnknownTool(t *testing.T) {
	tools := tool.Tools{} // no tools registered

	orch := tool.NewOrchestrator(tools, nil)

	calls := []agents.ToolUseBlock{
		{ID: "1", Name: "NonExistent", Input: json.RawMessage(`{}`)},
	}

	assistantMsg := &agents.Message{UUID: "assistant-1", Type: agents.MessageTypeAssistant}
	toolCtx := &agents.ToolUseContext{Ctx: context.Background()}

	result := orch.RunTools(context.Background(), calls, assistantMsg, toolCtx, nil)
	assert.NotNil(t, result)
	assert.Len(t, result.Messages, 1)
	// Should be an error message
	assert.True(t, result.Messages[0].Content[0].IsError)
}

func TestRegistry_BasicOperations(t *testing.T) {
	r := tool.NewRegistry()
	assert.Empty(t, r.GetTools())

	r.Register(&mockTool{name: "Read", concurrencySafe: true})
	r.Register(&mockTool{name: "Edit", concurrencySafe: false})

	tools := r.GetTools()
	assert.Len(t, tools, 2)

	// Should be sorted alphabetically
	assert.Equal(t, "Edit", tools[0].Name())
	assert.Equal(t, "Read", tools[1].Name())

	// FindByName
	assert.NotNil(t, r.FindByName("Read"))
	assert.Nil(t, r.FindByName("NonExistent"))

	// Deny
	r.Deny("Edit")
	tools = r.GetTools()
	assert.Len(t, tools, 1)
	assert.Equal(t, "Read", tools[0].Name())

	// Undeny
	r.Undeny("Edit")
	tools = r.GetTools()
	assert.Len(t, tools, 2)
}
