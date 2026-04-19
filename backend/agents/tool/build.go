package tool

import (
	"context"
	"encoding/json"
	"runtime"
	"time"

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

func (b *builtTool) Name() string             { return b.def.Name }
func (b *builtTool) Aliases() []string        { return b.def.Aliases }
func (b *builtTool) InputSchema() *JSONSchema { return b.def.InputSchema() }
func (b *builtTool) Description(input json.RawMessage) string {
	return b.def.Description(input)
}
func (b *builtTool) Prompt(opts PromptOptions) string { return b.def.Prompt(opts) }
func (b *builtTool) IsEnabled() bool                  { return b.def.IsEnabled() }
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

// ---------------------------------------------------------------------------
// AssemblePromptOptions — one-shot builder for the dynamic PromptOptions
// fields that every Tool.Prompt() implementation shares. Hosts call this
// once per assembly (system-prompt rebuild) and pass the result down to
// Tool.Prompt(opts). Individual tools must tolerate any field being the
// zero value (keeps legacy callers who still pass `PromptOptions{}`
// working unchanged).
// ---------------------------------------------------------------------------

// AssembleOptions carries the ambient state AssemblePromptOptions consults.
// Every field has a sensible zero default; hosts only populate the bits
// they care about.
type AssembleOptions struct {
	// Tools is the final resolved Tools slice (after assembly + deny
	// filtering). Mirrors the legacy `PromptOptions.Tools` field.
	Tools Tools

	// PermissionContext mirrors legacy `PromptOptions.ToolPermissionContext`.
	PermissionContext *agents.ToolPermissionContext

	// UserType is "ant" for the Anthropic-internal variant, otherwise
	// empty. Hosts typically read os.Getenv("USER_TYPE").
	UserType string

	// EmbeddedSearchTools signals the host is running inside an IDE or
	// other embedded env — flips BashTool/PowerShell preferences.
	EmbeddedSearchTools bool

	// ForkEnabled toggles the AgentTool fork section.
	ForkEnabled bool

	// SandboxEnabled toggles the Bash sandbox block.
	SandboxEnabled bool

	// AgentSwarmsEnabled toggles TaskCreate/TaskList/TaskUpdate teammate
	// wording.
	AgentSwarmsEnabled bool

	// PowerShellEdition is one of "desktop" | "core" | "" (unknown).
	// Callers detect it via tool/shell.DetectPowerShellEdition() and pass
	// the value in — AssemblePromptOptions does not probe anything.
	PowerShellEdition string

	// PreviewFormat for AskUserQuestion: "markdown" (default) or "html".
	PreviewFormat string

	// PlanModeInterviewEnabled mirrors TS's
	// `isPlanModeInterviewPhaseEnabled()` and controls whether
	// EnterPlanMode's inline "What Happens" section is emitted.
	PlanModeInterviewEnabled bool

	// AgentTool wiring.
	AgentToolIsCoordinator bool
	AgentListViaAttachment bool
	IsTeammate             bool
	IsInProcessTeammate    bool
	DisableBackgroundTasks bool
	SubscriptionType       string

	// Now is an override clock for testing; when zero-valued
	// time.Now() is used to compute MonthYear.
	Now time.Time
}

// AssemblePromptOptions builds a PromptOptions from opts. It populates the
// derived fields (PlatformOS, MonthYear) from runtime state and leaves
// caller-supplied bits (UserType, ForkEnabled, …) untouched.
func AssemblePromptOptions(opts AssembleOptions) PromptOptions {
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}
	return PromptOptions{
		Tools:                    opts.Tools,
		ToolPermissionContext:    opts.PermissionContext,
		UserType:                 opts.UserType,
		PlatformOS:               runtime.GOOS,
		EmbeddedSearchTools:      opts.EmbeddedSearchTools,
		ForkEnabled:              opts.ForkEnabled,
		SandboxEnabled:           opts.SandboxEnabled,
		AgentSwarmsEnabled:       opts.AgentSwarmsEnabled,
		PowerShellEdition:        opts.PowerShellEdition,
		MonthYear:                now.Format("January 2006"),
		PreviewFormat:            opts.PreviewFormat,
		PlanModeInterviewEnabled: opts.PlanModeInterviewEnabled,
		AgentToolIsCoordinator:   opts.AgentToolIsCoordinator,
		AgentListViaAttachment:   opts.AgentListViaAttachment,
		IsTeammate:               opts.IsTeammate,
		IsInProcessTeammate:      opts.IsInProcessTeammate,
		DisableBackgroundTasks:   opts.DisableBackgroundTasks,
		SubscriptionType:         opts.SubscriptionType,
	}
}
