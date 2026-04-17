package compact

// ---------------------------------------------------------------------------
// API Microcompact — Anthropic native context_management builder
// Maps to TypeScript src/services/compact/apiMicrocompact.ts (134 lines).
//
// Produces a ContextManagementConfig that providers supporting Anthropic's
// server-side context-management API can attach to their requests. Go-side
// context reduction is still handled by MicroCompactor (compact/micro.go);
// this module only covers the API-config path.
//
// Providers that do NOT support native context_management (e.g. OpenAI-compat)
// should simply ignore the returned ContextManagementConfig.
// ---------------------------------------------------------------------------

// Default values matching TS apiMicrocompact.ts L16-17.
const (
	DefaultMaxInputTokens    = 180_000 // typical warning threshold
	DefaultTargetInputTokens = 40_000  // target window to keep after clearing
)

// ToolsClearableResults are tools whose results are safe to clear (output only).
// Maps to TOOLS_CLEARABLE_RESULTS in apiMicrocompact.ts.
var ToolsClearableResults = []string{
	"Bash",
	"Glob",
	"Grep",
	"Read",
	"WebFetch",
	"WebSearch",
}

// ToolsClearableUses are tools whose inputs (not outputs) are safe to clear.
// Maps to TOOLS_CLEARABLE_USES in apiMicrocompact.ts.
var ToolsClearableUses = []string{
	"Edit",
	"Write",
	"NotebookEdit",
}

// ThinkingKeepAll is the sentinel used for the thinking-keep "all" variant.
const ThinkingKeepAll = "all"

// ContextEditStrategy represents one strategy in the context_management config.
// Union type — fields correspond to either clear_tool_uses_20250919 or
// clear_thinking_20251015.
type ContextEditStrategy struct {
	Type string `json:"type"`

	// clear_tool_uses_20250919 fields
	Trigger          *TokenThreshold `json:"trigger,omitempty"`
	Keep             *ToolUsesKeep   `json:"keep,omitempty"`
	ClearToolInputs  []string        `json:"clear_tool_inputs,omitempty"`
	ExcludeTools     []string        `json:"exclude_tools,omitempty"`
	ClearAtLeast     *TokenThreshold `json:"clear_at_least,omitempty"`

	// clear_thinking_20251015 fields
	KeepThinking *ThinkingKeep `json:"keep_thinking,omitempty"`
}

// TokenThreshold represents a threshold expressed in input tokens.
type TokenThreshold struct {
	Type  string `json:"type"` // always "input_tokens"
	Value int    `json:"value"`
}

// ToolUsesKeep represents how many tool-uses to keep.
type ToolUsesKeep struct {
	Type  string `json:"type"` // always "tool_uses"
	Value int    `json:"value"`
}

// ThinkingKeep represents the thinking-turn keep policy. When Mode == "all",
// Value is ignored; otherwise Value is the number of most-recent thinking turns.
type ThinkingKeep struct {
	Mode  string `json:"mode,omitempty"` // "" or "all"
	Type  string `json:"type,omitempty"` // "thinking_turns"
	Value int    `json:"value,omitempty"`
}

// ContextManagementConfig is the top-level wrapper.
type ContextManagementConfig struct {
	Edits []ContextEditStrategy `json:"edits"`
}

// APIContextOptions control which strategies are emitted.
type APIContextOptions struct {
	HasThinking             bool
	IsRedactThinkingActive  bool
	ClearAllThinking        bool
	UseClearToolResults     bool
	UseClearToolUses        bool
	MaxInputTokens          int // 0 falls back to DefaultMaxInputTokens
	TargetInputTokens       int // 0 falls back to DefaultTargetInputTokens
}

// GetAPIContextManagement builds a ContextManagementConfig from the given
// options. Returns nil when no strategy applies (caller should omit the
// context_management field from the API request).
//
// Maps to getAPIContextManagement() in apiMicrocompact.ts.
func GetAPIContextManagement(opts APIContextOptions) *ContextManagementConfig {
	var edits []ContextEditStrategy

	// 1) Thinking preservation: only when thinking is active and not redacted.
	if opts.HasThinking && !opts.IsRedactThinkingActive {
		var keep *ThinkingKeep
		if opts.ClearAllThinking {
			keep = &ThinkingKeep{Type: "thinking_turns", Value: 1}
		} else {
			keep = &ThinkingKeep{Mode: ThinkingKeepAll}
		}
		edits = append(edits, ContextEditStrategy{
			Type:         "clear_thinking_20251015",
			KeepThinking: keep,
		})
	}

	// 2) Tool clearing strategies (tool-results and/or tool-uses).
	maxInput := opts.MaxInputTokens
	if maxInput == 0 {
		maxInput = DefaultMaxInputTokens
	}
	target := opts.TargetInputTokens
	if target == 0 {
		target = DefaultTargetInputTokens
	}
	clearAtLeast := maxInput - target
	if clearAtLeast < 0 {
		clearAtLeast = 0
	}

	if opts.UseClearToolResults {
		edits = append(edits, ContextEditStrategy{
			Type:            "clear_tool_uses_20250919",
			Trigger:         &TokenThreshold{Type: "input_tokens", Value: maxInput},
			ClearAtLeast:    &TokenThreshold{Type: "input_tokens", Value: clearAtLeast},
			ClearToolInputs: append([]string(nil), ToolsClearableResults...),
		})
	}

	if opts.UseClearToolUses {
		edits = append(edits, ContextEditStrategy{
			Type:         "clear_tool_uses_20250919",
			Trigger:      &TokenThreshold{Type: "input_tokens", Value: maxInput},
			ClearAtLeast: &TokenThreshold{Type: "input_tokens", Value: clearAtLeast},
			ExcludeTools: append([]string(nil), ToolsClearableUses...),
		})
	}

	if len(edits) == 0 {
		return nil
	}
	return &ContextManagementConfig{Edits: edits}
}
