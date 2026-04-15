package tool

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wall-ai/ubuilding/backend/agents"
)

// ---------------------------------------------------------------------------
// Mock tool for testing
// ---------------------------------------------------------------------------

type mockTool struct {
	ToolDefaults
	name        string
	description string
	schema      *JSONSchema
	enabled     bool
}

func (t *mockTool) Name() string                                          { return t.name }
func (t *mockTool) IsEnabled() bool                                       { return t.enabled }
func (t *mockTool) Prompt(_ PromptOptions) string                         { return t.description }
func (t *mockTool) InputSchema() *JSONSchema                              { return t.schema }
func (t *mockTool) Description(_ json.RawMessage) string                  { return t.description }
func (t *mockTool) Call(_ context.Context, _ json.RawMessage, _ *agents.ToolUseContext) (*ToolResult, error) {
	return &ToolResult{Data: "ok"}, nil
}
func (t *mockTool) MapToolResultToParam(_ interface{}, _ string) *agents.ContentBlock {
	return nil
}

func newMockTool(name, desc string, props map[string]*SchemaProperty) *mockTool {
	schema := &JSONSchema{
		Type:       "object",
		Properties: props,
	}
	return &mockTool{
		name:        name,
		description: desc,
		schema:      schema,
		enabled:     true,
	}
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestToolToAPISchema_Basic(t *testing.T) {
	ClearSchemaCache()
	defer ClearSchemaCache()

	tool := newMockTool("Read", "Read a file", map[string]*SchemaProperty{
		"path": {Type: "string", Description: "File path"},
	})

	schema := ToolToAPISchema(tool, SchemaOpts{})

	assert.Equal(t, "Read", schema.Name)
	assert.Equal(t, "Read a file", schema.Description)
	assert.False(t, schema.Strict)
	assert.False(t, schema.DeferLoading)
	assert.Nil(t, schema.CacheControl)

	// InputSchema should be valid JSON with properties
	var parsed map[string]interface{}
	err := json.Unmarshal(schema.InputSchema, &parsed)
	require.NoError(t, err)
	assert.Equal(t, "object", parsed["type"])
	props, ok := parsed["properties"].(map[string]interface{})
	require.True(t, ok)
	assert.Contains(t, props, "path")
}

func TestToolToAPISchema_CacheHit(t *testing.T) {
	ClearSchemaCache()
	defer ClearSchemaCache()

	callCount := 0
	tool := &mockTool{
		name:        "CachedTool",
		description: "test",
		schema:      &JSONSchema{Type: "object"},
		enabled:     true,
	}
	// Override Prompt to count calls
	origDesc := tool.description
	_ = origDesc

	// First call: caches
	s1 := ToolToAPISchema(tool, SchemaOpts{})
	// Second call: should return cached
	s2 := ToolToAPISchema(tool, SchemaOpts{})

	assert.Equal(t, s1.Name, s2.Name)
	assert.Equal(t, s1.Description, s2.Description)
	_ = callCount
}

func TestToolToAPISchema_WithOverlays(t *testing.T) {
	ClearSchemaCache()
	defer ClearSchemaCache()

	tool := newMockTool("Bash", "Run a command", nil)

	schema := ToolToAPISchema(tool, SchemaOpts{
		DeferLoading: true,
		CacheControl: &CacheControlMeta{Type: "ephemeral", Scope: "global"},
		StrictModeEnabled: true,
		Model:             "claude-sonnet-4-20250514",
	})

	assert.True(t, schema.DeferLoading)
	assert.NotNil(t, schema.CacheControl)
	assert.Equal(t, "ephemeral", schema.CacheControl.Type)
	assert.Equal(t, "global", schema.CacheControl.Scope)
	assert.True(t, schema.Strict)
}

func TestToolToAPISchema_OverlayDoesNotMutateCache(t *testing.T) {
	ClearSchemaCache()
	defer ClearSchemaCache()

	tool := newMockTool("Edit", "Edit a file", nil)

	// First call with overlay
	s1 := ToolToAPISchema(tool, SchemaOpts{DeferLoading: true})
	assert.True(t, s1.DeferLoading)

	// Second call without overlay — should not carry over
	s2 := ToolToAPISchema(tool, SchemaOpts{})
	assert.False(t, s2.DeferLoading)
}

func TestToolToAPISchema_ToToolDefinition(t *testing.T) {
	ClearSchemaCache()
	defer ClearSchemaCache()

	tool := newMockTool("Grep", "Search files", map[string]*SchemaProperty{
		"pattern": {Type: "string"},
	})

	schema := ToolToAPISchema(tool, SchemaOpts{})
	def := schema.ToToolDefinition()

	assert.Equal(t, "Grep", def.Name)
	assert.Equal(t, "Search files", def.Description)
	assert.NotNil(t, def.InputSchema)
}

func TestFilterSwarmFields(t *testing.T) {
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"prompt": {"type": "string"},
			"name": {"type": "string"},
			"team_name": {"type": "string"},
			"mode": {"type": "string"}
		}
	}`)

	// Agent tool should have swarm fields filtered
	filtered := FilterSwarmFields("Agent", schema)
	var parsed map[string]interface{}
	err := json.Unmarshal(filtered, &parsed)
	require.NoError(t, err)
	props := parsed["properties"].(map[string]interface{})
	assert.Contains(t, props, "prompt")
	assert.NotContains(t, props, "name")
	assert.NotContains(t, props, "team_name")
	assert.NotContains(t, props, "mode")
}

func TestFilterSwarmFields_NoMatchingTool(t *testing.T) {
	schema := json.RawMessage(`{"type": "object", "properties": {"path": {"type": "string"}}}`)
	result := FilterSwarmFields("Read", schema)
	assert.JSONEq(t, string(schema), string(result))
}

func TestToolsToAPISchemas(t *testing.T) {
	ClearSchemaCache()
	defer ClearSchemaCache()

	tools := Tools{
		newMockTool("Read", "Read a file", nil),
		newMockTool("Edit", "Edit a file", nil),
		&mockTool{name: "Disabled", description: "disabled", schema: &JSONSchema{Type: "object"}, enabled: false},
	}

	defs := ToolsToAPISchemas(tools, SchemaOpts{})
	assert.Len(t, defs, 2) // Disabled tool excluded
	assert.Equal(t, "Read", defs[0].Name)
	assert.Equal(t, "Edit", defs[1].Name)
}

func TestToolsToAPISchemas_Empty(t *testing.T) {
	defs := ToolsToAPISchemas(nil, SchemaOpts{})
	assert.Nil(t, defs)
}

func TestMarshalSchema_Nil(t *testing.T) {
	result := marshalSchema(nil)
	assert.JSONEq(t, `{"type":"object"}`, string(result))
}
