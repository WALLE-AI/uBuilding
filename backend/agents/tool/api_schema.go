package tool

import (
	"encoding/json"
	"sync"

	"github.com/wall-ai/ubuilding/backend/agents"
)

// ---------------------------------------------------------------------------
// Tool → API Schema conversion
// Maps to TypeScript toolToAPISchema() in utils/api.ts
// ---------------------------------------------------------------------------

// SchemaOpts controls per-request overlays on top of the cached base schema.
type SchemaOpts struct {
	// Model is the target model ID (used for strict mode eligibility).
	Model string

	// DeferLoading marks this tool for deferred loading (tool search feature).
	DeferLoading bool

	// CacheControl attaches cache control metadata to the schema.
	CacheControl *CacheControlMeta

	// StrictModeEnabled is the feature gate for strict tool schemas.
	StrictModeEnabled bool

	// EagerInputStreaming enables fine-grained per-tool input streaming.
	EagerInputStreaming bool

	// SwarmFieldsEnabled controls whether swarm-related fields are kept.
	SwarmFieldsEnabled bool
}

// CacheControlMeta describes prompt cache control for a tool schema.
// Maps to TS cache_control: { type: 'ephemeral', scope?: 'global' | 'org', ttl?: ... }
type CacheControlMeta struct {
	Type  string `json:"type"` // "ephemeral"
	Scope string `json:"scope,omitempty"`
	TTL   string `json:"ttl,omitempty"`
}

// APIToolSchema is the API-ready tool definition sent to the LLM.
// Maps to TS BetaToolWithExtras.
type APIToolSchema struct {
	Name                string          `json:"name"`
	Description         string          `json:"description,omitempty"`
	InputSchema         json.RawMessage `json:"input_schema"`
	Strict              bool            `json:"strict,omitempty"`
	DeferLoading        bool            `json:"defer_loading,omitempty"`
	CacheControl        *CacheControlMeta `json:"cache_control,omitempty"`
	EagerInputStreaming bool            `json:"eager_input_streaming,omitempty"`
}

// ToToolDefinition converts APIToolSchema to the agents.ToolDefinition used by CallModelParams.
func (s *APIToolSchema) ToToolDefinition() agents.ToolDefinition {
	var schema interface{}
	if len(s.InputSchema) > 0 {
		_ = json.Unmarshal(s.InputSchema, &schema)
	}
	return agents.ToolDefinition{
		Name:        s.Name,
		Description: s.Description,
		InputSchema: schema,
	}
}

// ---------------------------------------------------------------------------
// Schema cache — session-stable base schemas (replaces TS WeakMap)
// ---------------------------------------------------------------------------

var schemaCache sync.Map // map[string]*cachedBase

type cachedBase struct {
	Name                string          `json:"name"`
	Description         string          `json:"description"`
	InputSchema         json.RawMessage `json:"input_schema"`
	Strict              bool            `json:"strict,omitempty"`
	EagerInputStreaming bool            `json:"eager_input_streaming,omitempty"`
}

// ClearSchemaCache resets the schema cache. Called on /clear or session reset.
func ClearSchemaCache() {
	schemaCache.Range(func(key, _ interface{}) bool {
		schemaCache.Delete(key)
		return true
	})
}

// ---------------------------------------------------------------------------
// ToolToAPISchema — main conversion function
// ---------------------------------------------------------------------------

// ToolToAPISchema converts a Tool to an API-ready schema.
// The base schema (name, description, input_schema, strict, eager_input_streaming)
// is computed once per session and cached. Per-request overlays (defer_loading,
// cache_control) are applied on top.
//
// Maps to toolToAPISchema() in utils/api.ts L119-266.
func ToolToAPISchema(t Tool, opts SchemaOpts) APIToolSchema {
	cacheKey := t.Name()

	// Check cache for the base schema
	if cached, ok := schemaCache.Load(cacheKey); ok {
		base := cached.(*cachedBase)
		return applyOverlays(base, opts)
	}

	// Compute base schema
	inputSchema := t.InputSchema()
	schemaJSON := marshalSchema(inputSchema)

	// Filter swarm fields when swarms are not enabled
	if !opts.SwarmFieldsEnabled {
		schemaJSON = FilterSwarmFields(t.Name(), schemaJSON)
	}

	// Generate the description via the tool's Prompt method
	description := t.Prompt(PromptOptions{})

	base := &cachedBase{
		Name:        t.Name(),
		Description: description,
		InputSchema: schemaJSON,
	}

	// Strict mode: only if gate is enabled and tool is not read-only by default
	// (In TS, this checks tool.strict === true; here we use a simpler heuristic)
	if opts.StrictModeEnabled && opts.Model != "" {
		base.Strict = true
	}

	if opts.EagerInputStreaming {
		base.EagerInputStreaming = true
	}

	schemaCache.Store(cacheKey, base)
	return applyOverlays(base, opts)
}

// applyOverlays creates the final APIToolSchema by merging the cached base
// with per-request overlays. This avoids mutating the cache.
func applyOverlays(base *cachedBase, opts SchemaOpts) APIToolSchema {
	schema := APIToolSchema{
		Name:        base.Name,
		Description: base.Description,
		InputSchema: base.InputSchema,
	}

	if base.Strict {
		schema.Strict = true
	}
	if base.EagerInputStreaming {
		schema.EagerInputStreaming = true
	}

	// Per-request overlays
	if opts.DeferLoading {
		schema.DeferLoading = true
	}
	if opts.CacheControl != nil {
		schema.CacheControl = opts.CacheControl
	}

	return schema
}

// ---------------------------------------------------------------------------
// ToolsToAPISchemas — batch conversion
// ---------------------------------------------------------------------------

// ToolsToAPISchemas converts a set of tools to API schemas and returns
// the corresponding ToolDefinition slice for CallModelParams.
func ToolsToAPISchemas(tools Tools, opts SchemaOpts) []agents.ToolDefinition {
	if len(tools) == 0 {
		return nil
	}

	defs := make([]agents.ToolDefinition, 0, len(tools))
	for _, t := range tools {
		if !t.IsEnabled() {
			continue
		}
		schema := ToolToAPISchema(t, opts)
		defs = append(defs, schema.ToToolDefinition())
	}
	return defs
}

// ---------------------------------------------------------------------------
// Swarm field filtering
// Maps to filterSwarmFieldsFromSchema() in utils/api.ts L96-117
// ---------------------------------------------------------------------------

// swarmFieldsByTool lists fields to filter from tool schemas when swarms are disabled.
var swarmFieldsByTool = map[string][]string{
	"ExitPlanMode": {"launchSwarm", "teammateCount"},
	"Agent":        {"name", "team_name", "mode"},
}

// FilterSwarmFields removes swarm-related fields from a tool's JSON schema.
// Returns the original bytes if no filtering is needed.
func FilterSwarmFields(toolName string, schemaJSON json.RawMessage) json.RawMessage {
	fieldsToRemove, ok := swarmFieldsByTool[toolName]
	if !ok || len(fieldsToRemove) == 0 {
		return schemaJSON
	}

	// Decode the schema
	var schema map[string]interface{}
	if err := json.Unmarshal(schemaJSON, &schema); err != nil {
		return schemaJSON
	}

	props, ok := schema["properties"].(map[string]interface{})
	if !ok {
		return schemaJSON
	}

	// Remove swarm fields
	modified := false
	for _, field := range fieldsToRemove {
		if _, exists := props[field]; exists {
			delete(props, field)
			modified = true
		}
	}

	if !modified {
		return schemaJSON
	}

	schema["properties"] = props
	result, err := json.Marshal(schema)
	if err != nil {
		return schemaJSON
	}
	return result
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// marshalSchema converts a JSONSchema to json.RawMessage.
func marshalSchema(schema *JSONSchema) json.RawMessage {
	if schema == nil {
		return json.RawMessage(`{"type":"object"}`)
	}
	data, err := json.Marshal(schema)
	if err != nil {
		return json.RawMessage(`{"type":"object"}`)
	}
	return data
}
