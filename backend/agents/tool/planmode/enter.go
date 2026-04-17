package planmode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/wall-ai/ubuilding/backend/agents"
	"github.com/wall-ai/ubuilding/backend/agents/tool"
)

// EnterName matches claude-code-main's ENTER_PLAN_MODE_TOOL_NAME.
const EnterName = "EnterPlanMode"

// EnterInput is the EnterPlanMode input — claude-code-main takes no parameters,
// but we tolerate an optional free-form note for audit logs.
type EnterInput struct {
	Note string `json:"note,omitempty"`
}

// EnterOutput is the structured result.
type EnterOutput struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// EnterTool requests a transition into plan mode. The host must honour
// the follow-up prompt injected by mapToolResultToParam (which instructs the
// model to explore and then call ExitPlanMode to execute).
type EnterTool struct {
	tool.ToolDefaults
}

// NewEnter returns an EnterPlanMode tool.
func NewEnter() *EnterTool { return &EnterTool{} }

func (t *EnterTool) Name() string                             { return EnterName }
func (t *EnterTool) IsReadOnly(_ json.RawMessage) bool        { return true }
func (t *EnterTool) IsConcurrencySafe(_ json.RawMessage) bool { return true }

func (t *EnterTool) InputSchema() *tool.JSONSchema {
	return &tool.JSONSchema{
		Type: "object",
		Properties: map[string]*tool.SchemaProperty{
			"note": {Type: "string", Description: "Optional free-form note, surfaced in telemetry only."},
		},
	}
}

func (t *EnterTool) Description(_ json.RawMessage) string {
	return "Requests permission to enter plan mode for complex tasks requiring exploration and design"
}

func (t *EnterTool) Prompt(_ tool.PromptOptions) string {
	return `Switches the agent into plan mode. Use this BEFORE touching files on
non-trivial tasks where you need to:
  - read code to understand existing patterns,
  - weigh trade-offs across multiple approaches,
  - or surface a concrete design for the user to approve.

Rules:
- Plan mode is read-only. Do not edit files until you have called ExitPlanMode.
- Subagents (ToolUseContext.AgentID != "") cannot enter plan mode — call this
  from the main thread only.
- The engine refuses the call if you are already in plan mode.`
}

func (t *EnterTool) ValidateInput(input json.RawMessage, _ *agents.ToolUseContext) *tool.ValidationResult {
	if len(input) == 0 {
		return &tool.ValidationResult{Valid: true}
	}
	var in EnterInput
	if err := json.Unmarshal(input, &in); err != nil {
		return &tool.ValidationResult{Valid: false, Message: fmt.Sprintf("invalid input: %v", err)}
	}
	return &tool.ValidationResult{Valid: true}
}

func (t *EnterTool) CheckPermissions(input json.RawMessage, _ *agents.ToolUseContext) (*tool.PermissionResult, error) {
	return &tool.PermissionResult{Behavior: tool.PermissionAllow, UpdatedInput: input, DecisionReason: "enterplanmode-default-allow"}, nil
}

func (t *EnterTool) Call(_ context.Context, _ json.RawMessage, toolCtx *agents.ToolUseContext) (*tool.ToolResult, error) {
	if toolCtx == nil {
		return nil, errors.New("EnterPlanMode: no tool context")
	}
	if toolCtx.AgentID != "" {
		return nil, errors.New("EnterPlanMode tool cannot be used in agent contexts")
	}
	from := toolCtx.PlanMode
	if from == "" {
		from = ModeNormal
	}
	if from == ModePlan {
		return nil, fmt.Errorf("EnterPlanMode: already in plan mode")
	}
	toolCtx.PlanMode = ModePlan
	if toolCtx.EmitEvent != nil {
		data, _ := json.Marshal(agents.PlanModeChange{From: from, To: ModePlan})
		toolCtx.EmitEvent(agents.StreamEvent{Type: agents.EventPlanModeChange, Data: data})
	}
	return &tool.ToolResult{Data: EnterOutput{From: from, To: ModePlan}}, nil
}

// MapToolResultToParam re-injects the model-facing instructions that claude-code
// attaches on transition (exploration rules, read-only reminder).
func (t *EnterTool) MapToolResultToParam(_ interface{}, toolUseID string) *agents.ContentBlock {
	return &agents.ContentBlock{
		Type:      agents.ContentBlockToolResult,
		ToolUseID: toolUseID,
		Content: `Entered plan mode. You should now focus on exploring the codebase and designing an implementation approach.

In plan mode, you should:
1. Thoroughly explore the codebase to understand existing patterns
2. Identify similar features and architectural approaches
3. Consider multiple approaches and their trade-offs
4. Use AskUserQuestion if you need to clarify the approach
5. Design a concrete implementation strategy
6. When ready, use ExitPlanMode to present your plan for approval

Remember: DO NOT write or edit any files yet. This is a read-only exploration and planning phase.`,
	}
}

var _ tool.Tool = (*EnterTool)(nil)
