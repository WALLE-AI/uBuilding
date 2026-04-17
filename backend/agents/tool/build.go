package tool

import (
	"context"
	"encoding/json"

	"github.com/wall-ai/ubuilding/backend/agents"
)

// ---------------------------------------------------------------------------
// BuildTool — factory that produces a complete Tool from a partial ToolDef,
// filling in fail-closed defaults for every optional method. Maps to TypeScript
// buildTool() + TOOL_DEFAULTS in src/Tool.ts.
// ---------------------------------------------------------------------------

// ToolDef is the partial definition accepted by BuildTool. Only the five
// required fields (Name, Call, InputSchema, Description, MapToolResultToParam)
// must be provided; everything else has a safe default.
type ToolDef struct {
	// Required.
	Name                 string
	Call                 func(ctx context.Context, input json.RawMessage, toolCtx *agents.ToolUseContext) (*ToolResult, error)
	InputSchema          func() *JSONSchema
	Description          func(input json.RawMessage) string
	MapToolResultToParam func(result interface{}, toolUseID string) *agents.ContentBlock

	// Optional — overridden via function fields so the owner keeps lexical
	// closure over any state without subclassing.
	Aliases            []string
	Prompt             func(opts PromptOptions) string
	IsEnabled          func() bool
	IsReadOnly         func(input json.RawMessage) bool
	IsConcurrencySafe  func(input json.RawMessage) bool
	IsDestructive      func(input json.RawMessage) bool
	MaxResultSizeChars int
	ValidateInput      func(input json.RawMessage, toolCtx *agents.ToolUseContext) *ValidationResult
	CheckPermissions   func(input json.RawMessage, toolCtx *agents.ToolUseContext) (*PermissionResult, error)
}

// BuildTool assembles a fully-populated Tool from def. Panics if any required
// field is missing — tool construction is a startup-time concern.
func BuildTool(def ToolDef) Tool {
	if def.Name == "" {
		panic("tool.BuildTool: Name is required")
	}
	if def.Call == nil {
		panic("tool.BuildTool: Call is required")
	}
	if def.InputSchema == nil {
		panic("tool.BuildTool: InputSchema is required")
	}
	if def.Description == nil {
		panic("tool.BuildTool: Description is required")
	}
	if def.MapToolResultToParam == nil {
		panic("tool.BuildTool: MapToolResultToParam is required")
	}

	if def.MaxResultSizeChars == 0 {
		def.MaxResultSizeChars = 100_000
	}
	if def.IsEnabled == nil {
		def.IsEnabled = func() bool { return true }
	}
	if def.IsReadOnly == nil {
		// Fail-closed: assume writes unless the tool declares otherwise.
		def.IsReadOnly = func(_ json.RawMessage) bool { return false }
	}
	if def.IsConcurrencySafe == nil {
		// Fail-closed: assume not safe to run in parallel.
		def.IsConcurrencySafe = func(_ json.RawMessage) bool { return false }
	}
	if def.IsDestructive == nil {
		def.IsDestructive = func(_ json.RawMessage) bool { return false }
	}
	if def.ValidateInput == nil {
		def.ValidateInput = func(_ json.RawMessage, _ *agents.ToolUseContext) *ValidationResult {
			return &ValidationResult{Valid: true}
		}
	}
	if def.CheckPermissions == nil {
		def.CheckPermissions = func(input json.RawMessage, _ *agents.ToolUseContext) (*PermissionResult, error) {
			return &PermissionResult{Behavior: PermissionAllow, UpdatedInput: input}, nil
		}
	}
	if def.Prompt == nil {
		name := def.Name
		def.Prompt = func(_ PromptOptions) string { return name }
	}

	return &builtTool{def: def}
}

// builtTool implements Tool by dispatching to the closures stored in ToolDef.
type builtTool struct {
	def ToolDef
}

func (b *builtTool) Name() string          { return b.def.Name }
func (b *builtTool) Aliases() []string     { return b.def.Aliases }
func (b *builtTool) InputSchema() *JSONSchema { return b.def.InputSchema() }
func (b *builtTool) Description(input json.RawMessage) string {
	return b.def.Description(input)
}
func (b *builtTool) Prompt(opts PromptOptions) string { return b.def.Prompt(opts) }
func (b *builtTool) IsEnabled() bool                    { return b.def.IsEnabled() }
func (b *builtTool) IsReadOnly(input json.RawMessage) bool {
	return b.def.IsReadOnly(input)
}
func (b *builtTool) IsConcurrencySafe(input json.RawMessage) bool {
	return b.def.IsConcurrencySafe(input)
}
func (b *builtTool) IsDestructive(input json.RawMessage) bool {
	return b.def.IsDestructive(input)
}
func (b *builtTool) MaxResultSizeChars() int { return b.def.MaxResultSizeChars }
func (b *builtTool) ValidateInput(input json.RawMessage, toolCtx *agents.ToolUseContext) *ValidationResult {
	return b.def.ValidateInput(input, toolCtx)
}
func (b *builtTool) CheckPermissions(input json.RawMessage, toolCtx *agents.ToolUseContext) (*PermissionResult, error) {
	return b.def.CheckPermissions(input, toolCtx)
}
func (b *builtTool) Call(ctx context.Context, input json.RawMessage, toolCtx *agents.ToolUseContext) (*ToolResult, error) {
	return b.def.Call(ctx, input, toolCtx)
}
func (b *builtTool) MapToolResultToParam(result interface{}, toolUseID string) *agents.ContentBlock {
	return b.def.MapToolResultToParam(result, toolUseID)
}

// Compile-time assertion.
var _ Tool = (*builtTool)(nil)
