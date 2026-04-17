// Package mcp implements the ListMcpResources and ReadMcpResource tools,
// ported from claude-code-main's ListMcpResourcesTool.ts / ReadMcpResourceTool.ts.
//
// Both tools are read-only and concurrency-safe. They defer to the
// agents.McpResourceRegistry attached to ToolUseContext.McpResources, so
// hosts can plug in any MCP transport (stdio, SSE, in-process fake for tests).
package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/wall-ai/ubuilding/backend/agents"
	"github.com/wall-ai/ubuilding/backend/agents/tool"
)

// Tool names (match claude-code-main).
const (
	ListName = "ListMcpResourcesTool"
	ReadName = "ReadMcpResourceTool"
)

// ──────────────────────────────────────────────────────────────────────────
// ListMcpResources
// ──────────────────────────────────────────────────────────────────────────

// ListInput is the ListMcpResources input.
type ListInput struct {
	// Server is an optional filter. Empty = aggregate across all servers.
	Server string `json:"server,omitempty"`
}

// ListTool implements tool.Tool for ListMcpResources.
type ListTool struct{ tool.ToolDefaults }

// NewListTool returns a ListMcpResources tool.
func NewListTool() *ListTool { return &ListTool{} }

func (t *ListTool) Name() string                             { return ListName }
func (t *ListTool) IsReadOnly(_ json.RawMessage) bool        { return true }
func (t *ListTool) IsConcurrencySafe(_ json.RawMessage) bool { return true }

func (t *ListTool) InputSchema() *tool.JSONSchema {
	return &tool.JSONSchema{
		Type: "object",
		Properties: map[string]*tool.SchemaProperty{
			"server": {Type: "string", Description: "Optional server name to filter resources by."},
		},
	}
}

func (t *ListTool) Description(_ json.RawMessage) string { return "List MCP resources" }

func (t *ListTool) Prompt(_ tool.PromptOptions) string {
	return `Lists resources advertised by connected MCP servers.

- Pass "server" to scope results to a single server; omit it to aggregate.
- Returns [] when no resources exist; MCP servers may still provide tools.
- This tool is read-only and concurrency-safe.`
}

func (t *ListTool) ValidateInput(input json.RawMessage, _ *agents.ToolUseContext) *tool.ValidationResult {
	var in ListInput
	if len(input) > 0 {
		if err := json.Unmarshal(input, &in); err != nil {
			return &tool.ValidationResult{Valid: false, Message: fmt.Sprintf("invalid input: %v", err)}
		}
	}
	return &tool.ValidationResult{Valid: true}
}

func (t *ListTool) CheckPermissions(input json.RawMessage, _ *agents.ToolUseContext) (*tool.PermissionResult, error) {
	return &tool.PermissionResult{Behavior: tool.PermissionAllow, UpdatedInput: input, DecisionReason: "listmcpresources-read-only"}, nil
}

func (t *ListTool) Call(ctx context.Context, input json.RawMessage, toolCtx *agents.ToolUseContext) (*tool.ToolResult, error) {
	reg, err := registry(toolCtx)
	if err != nil {
		return nil, err
	}
	var in ListInput
	if len(input) > 0 {
		_ = json.Unmarshal(input, &in)
	}
	if in.Server != "" {
		// Validate server exists up-front so we emit a helpful error.
		if !containsServer(reg, in.Server) {
			return nil, fmt.Errorf(
				"server %q not found. Available servers: %s",
				in.Server, strings.Join(reg.ListServers(), ", "),
			)
		}
	}
	res, err := reg.ListResources(ctx, in.Server)
	if err != nil {
		return nil, err
	}
	return &tool.ToolResult{Data: res}, nil
}

func (t *ListTool) MapToolResultToParam(content interface{}, toolUseID string) *agents.ContentBlock {
	list, ok := content.([]agents.McpResource)
	if !ok || len(list) == 0 {
		return &agents.ContentBlock{
			Type:      agents.ContentBlockToolResult,
			ToolUseID: toolUseID,
			Content:   "No resources found. MCP servers may still provide tools even if they have no resources.",
		}
	}
	b, _ := json.Marshal(list)
	return &agents.ContentBlock{Type: agents.ContentBlockToolResult, ToolUseID: toolUseID, Content: string(b)}
}

// ──────────────────────────────────────────────────────────────────────────
// ReadMcpResource
// ──────────────────────────────────────────────────────────────────────────

// ReadInput is the ReadMcpResource input.
type ReadInput struct {
	Server string `json:"server"`
	URI    string `json:"uri"`
}

// ReadTool implements tool.Tool for ReadMcpResource.
type ReadTool struct{ tool.ToolDefaults }

// NewReadTool returns a ReadMcpResource tool.
func NewReadTool() *ReadTool { return &ReadTool{} }

func (t *ReadTool) Name() string                             { return ReadName }
func (t *ReadTool) IsReadOnly(_ json.RawMessage) bool        { return true }
func (t *ReadTool) IsConcurrencySafe(_ json.RawMessage) bool { return true }

func (t *ReadTool) InputSchema() *tool.JSONSchema {
	return &tool.JSONSchema{
		Type: "object",
		Properties: map[string]*tool.SchemaProperty{
			"server": {Type: "string", Description: "The MCP server name."},
			"uri":    {Type: "string", Description: "The resource URI to read."},
		},
		Required: []string{"server", "uri"},
	}
}

func (t *ReadTool) Description(_ json.RawMessage) string { return "Read a specific MCP resource" }

func (t *ReadTool) Prompt(_ tool.PromptOptions) string {
	return `Reads a specific MCP resource by server name + URI.

- Returns text content inline; binary blobs are persisted to disk by the host
  and surfaced via blobSavedTo (with a short human-readable note in text).
- Use ListMcpResources first to discover valid URIs.`
}

func (t *ReadTool) ValidateInput(input json.RawMessage, _ *agents.ToolUseContext) *tool.ValidationResult {
	var in ReadInput
	if err := json.Unmarshal(input, &in); err != nil {
		return &tool.ValidationResult{Valid: false, Message: fmt.Sprintf("invalid input: %v", err)}
	}
	if strings.TrimSpace(in.Server) == "" {
		return &tool.ValidationResult{Valid: false, Message: "server required"}
	}
	if strings.TrimSpace(in.URI) == "" {
		return &tool.ValidationResult{Valid: false, Message: "uri required"}
	}
	return &tool.ValidationResult{Valid: true}
}

func (t *ReadTool) CheckPermissions(input json.RawMessage, _ *agents.ToolUseContext) (*tool.PermissionResult, error) {
	return &tool.PermissionResult{Behavior: tool.PermissionAllow, UpdatedInput: input, DecisionReason: "readmcpresource-read-only"}, nil
}

func (t *ReadTool) Call(ctx context.Context, input json.RawMessage, toolCtx *agents.ToolUseContext) (*tool.ToolResult, error) {
	reg, err := registry(toolCtx)
	if err != nil {
		return nil, err
	}
	var in ReadInput
	if err := json.Unmarshal(input, &in); err != nil {
		return nil, err
	}
	if !containsServer(reg, in.Server) {
		return nil, fmt.Errorf(
			"server %q not found. Available servers: %s",
			in.Server, strings.Join(reg.ListServers(), ", "),
		)
	}
	contents, err := reg.ReadResource(ctx, in.Server, in.URI)
	if err != nil {
		return nil, err
	}
	return &tool.ToolResult{Data: contents}, nil
}

func (t *ReadTool) MapToolResultToParam(content interface{}, toolUseID string) *agents.ContentBlock {
	b, _ := json.Marshal(content)
	return &agents.ContentBlock{Type: agents.ContentBlockToolResult, ToolUseID: toolUseID, Content: string(b)}
}

// ──────────────────────────────────────────────────────────────────────────
// helpers
// ──────────────────────────────────────────────────────────────────────────

func registry(toolCtx *agents.ToolUseContext) (agents.McpResourceRegistry, error) {
	if toolCtx == nil || toolCtx.McpResources == nil {
		return nil, errors.New("mcp: no McpResources registry attached to ToolUseContext")
	}
	return toolCtx.McpResources, nil
}

func containsServer(reg agents.McpResourceRegistry, name string) bool {
	for _, n := range reg.ListServers() {
		if n == name {
			return true
		}
	}
	return false
}

// Compile-time assertions.
var (
	_ tool.Tool = (*ListTool)(nil)
	_ tool.Tool = (*ReadTool)(nil)
)
