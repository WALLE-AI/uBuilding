package tool

import (
	"context"
	"encoding/json"

	"github.com/wall-ai/ubuilding/backend/agents"
)

// ---------------------------------------------------------------------------
// Tool interface — maps to TypeScript Tool<Input, Output, P> from Tool.ts
// ---------------------------------------------------------------------------

// Tool defines the contract for all tools in the agent engine.
// Each tool must implement this interface to be registered and executed.
type Tool interface {
	// Name returns the primary tool name (e.g., "Bash", "Read", "Edit").
	Name() string

	// Aliases returns alternative names for backward compatibility.
	Aliases() []string

	// Call executes the tool with the given input and returns a result.
	Call(ctx context.Context, input json.RawMessage, toolCtx *agents.ToolUseContext) (*ToolResult, error)

	// Description returns a human-readable description for the given input.
	Description(input json.RawMessage) string

	// InputSchema returns the JSON Schema for this tool's input.
	InputSchema() *JSONSchema

	// CheckPermissions determines if the tool is allowed to run with this input.
	CheckPermissions(input json.RawMessage, toolCtx *agents.ToolUseContext) (*PermissionResult, error)

	// IsConcurrencySafe returns true if this tool can run in parallel with other
	// concurrent-safe tools for the given input.
	IsConcurrencySafe(input json.RawMessage) bool

	// IsReadOnly returns true if this tool only reads state (no side effects).
	IsReadOnly(input json.RawMessage) bool

	// IsEnabled returns true if this tool is currently available.
	IsEnabled() bool

	// IsDestructive returns true if this tool performs irreversible operations.
	IsDestructive(input json.RawMessage) bool

	// MaxResultSizeChars returns the maximum tool result size before disk persistence.
	MaxResultSizeChars() int

	// Prompt returns the tool's description for the system prompt.
	Prompt(opts PromptOptions) string

	// ValidateInput checks if the input is valid before execution.
	ValidateInput(input json.RawMessage, toolCtx *agents.ToolUseContext) *ValidationResult

	// MapToolResultToParam converts a tool result to API-compatible format.
	MapToolResultToParam(result interface{}, toolUseID string) *agents.ContentBlock
}

// ---------------------------------------------------------------------------
// Supporting types
// ---------------------------------------------------------------------------

// ToolResult is the output of a tool execution.
type ToolResult struct {
	Data            interface{}        `json:"data"`
	NewMessages     []agents.Message   `json:"new_messages,omitempty"`
	ContextModifier func(ctx *agents.ToolUseContext) *agents.ToolUseContext `json:"-"`
}

// JSONSchema represents a JSON Schema definition for tool inputs.
type JSONSchema struct {
	Type       string                 `json:"type"`
	Properties map[string]*SchemaProperty `json:"properties,omitempty"`
	Required   []string               `json:"required,omitempty"`
}

// SchemaProperty defines a single property in a JSON Schema.
type SchemaProperty struct {
	Type        string      `json:"type"`
	Description string      `json:"description,omitempty"`
	Enum        []string    `json:"enum,omitempty"`
	Default     interface{} `json:"default,omitempty"`
}

// PermissionResult represents the outcome of a permission check.
type PermissionResult struct {
	Behavior     string          `json:"behavior"` // "allow" | "deny" | "ask"
	UpdatedInput json.RawMessage `json:"updated_input,omitempty"`
	Message      string          `json:"message,omitempty"`
	// RiskLevel indicates assessed risk: "low" | "medium" | "high"
	RiskLevel    string          `json:"risk_level,omitempty"`
}

// Permission behaviors
const (
	PermissionAllow = "allow"
	PermissionDeny  = "deny"
	PermissionAsk   = "ask"
)

// ValidationResult represents the outcome of input validation.
type ValidationResult struct {
	Valid   bool   `json:"result"`
	Message string `json:"message,omitempty"`
	Code    int    `json:"error_code,omitempty"`
}

// PromptOptions are passed to Tool.Prompt() for context-aware descriptions.
type PromptOptions struct {
	Tools               []Tool
	ToolPermissionContext *agents.ToolPermissionContext
}

// ---------------------------------------------------------------------------
// ToolDef — simplified builder for creating tools with defaults
// ---------------------------------------------------------------------------

// ToolDefaults provides safe default implementations for optional Tool methods.
type ToolDefaults struct{}

func (d ToolDefaults) Aliases() []string                                         { return nil }
func (d ToolDefaults) IsConcurrencySafe(_ json.RawMessage) bool                  { return false }
func (d ToolDefaults) IsReadOnly(_ json.RawMessage) bool                         { return false }
func (d ToolDefaults) IsDestructive(_ json.RawMessage) bool                      { return false }
func (d ToolDefaults) IsEnabled() bool                                            { return true }
func (d ToolDefaults) MaxResultSizeChars() int                                    { return 100000 }
func (d ToolDefaults) ValidateInput(_ json.RawMessage, _ *agents.ToolUseContext) *ValidationResult {
	return &ValidationResult{Valid: true}
}
func (d ToolDefaults) CheckPermissions(input json.RawMessage, _ *agents.ToolUseContext) (*PermissionResult, error) {
	return &PermissionResult{Behavior: PermissionAllow, UpdatedInput: input}, nil
}

// ---------------------------------------------------------------------------
// Tools — collection type (matches TypeScript `type Tools = readonly Tool[]`)
// ---------------------------------------------------------------------------

// Tools is a slice of Tool. Using a named type makes it easy to track where
// tool sets are assembled, passed, and filtered across the codebase.
type Tools []Tool

// FindByName finds a tool by primary name or alias.
func (ts Tools) FindByName(name string) Tool {
	for _, t := range ts {
		if t.Name() == name {
			return t
		}
		for _, alias := range t.Aliases() {
			if alias == name {
				return t
			}
		}
	}
	return nil
}

// Names returns the primary names of all tools.
func (ts Tools) Names() []string {
	names := make([]string, len(ts))
	for i, t := range ts {
		names[i] = t.Name()
	}
	return names
}

// Enabled returns only the tools that are currently enabled.
func (ts Tools) Enabled() Tools {
	var result Tools
	for _, t := range ts {
		if t.IsEnabled() {
			result = append(result, t)
		}
	}
	return result
}
