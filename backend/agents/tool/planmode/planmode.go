// Package planmode implements the ExitPlanMode tool (and a symmetrical
// EnterPlanMode helper, though claude-code's public tool surface only ships
// ExitPlanMode). The tool flips ToolUseContext.PlanMode and emits a
// EventPlanModeChange so hosts can react.
package planmode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/wall-ai/ubuilding/backend/agents"
	"github.com/wall-ai/ubuilding/backend/agents/tool"
)

// Name is the tool name exposed to the model.
const Name = "ExitPlanMode"

// Mode values.
const (
	ModeNormal = "normal"
	ModePlan   = "plan"
)

// Input matches claude-code's ExitPlanMode input.
type Input struct {
	Plan string `json:"plan"`
}

// Output is the structured result.
type Output struct {
	From    string `json:"from"`
	To      string `json:"to"`
	Summary string `json:"summary,omitempty"`
}

// Tool implements tool.Tool for ExitPlanMode.
type Tool struct {
	tool.ToolDefaults
}

// New returns an ExitPlanMode tool.
func New() *Tool { return &Tool{} }

func (t *Tool) Name() string                            { return Name }
func (t *Tool) IsReadOnly(_ json.RawMessage) bool       { return false }
func (t *Tool) IsConcurrencySafe(_ json.RawMessage) bool { return false }

func (t *Tool) InputSchema() *tool.JSONSchema {
	return &tool.JSONSchema{
		Type: "object",
		Properties: map[string]*tool.SchemaProperty{
			"plan": {Type: "string", Description: "Short markdown summary of the plan to execute."},
		},
		Required: []string{"plan"},
	}
}

func (t *Tool) Description(_ json.RawMessage) string { return "Exit plan mode and begin execution" }

func (t *Tool) Prompt(_ tool.PromptOptions) string {
	return `Signals the end of plan mode and the start of execution.

Rules:
- Only call this from plan mode (the engine refuses the call otherwise).
- plan: a concise markdown summary of what will be done next.
- After this call, the engine switches to normal (execution) mode and hosts are notified via a plan_mode_change event.`
}

func (t *Tool) ValidateInput(input json.RawMessage, _ *agents.ToolUseContext) *tool.ValidationResult {
	var in Input
	if err := json.Unmarshal(input, &in); err != nil {
		return &tool.ValidationResult{Valid: false, Message: fmt.Sprintf("invalid input: %v", err)}
	}
	if strings.TrimSpace(in.Plan) == "" {
		return &tool.ValidationResult{Valid: false, Message: "plan must not be empty"}
	}
	return &tool.ValidationResult{Valid: true}
}

func (t *Tool) CheckPermissions(input json.RawMessage, _ *agents.ToolUseContext) (*tool.PermissionResult, error) {
	return &tool.PermissionResult{Behavior: tool.PermissionAllow, UpdatedInput: input, DecisionReason: "planmode-default-allow"}, nil
}

func (t *Tool) Call(ctx context.Context, input json.RawMessage, toolCtx *agents.ToolUseContext) (*tool.ToolResult, error) {
	var in Input
	if err := json.Unmarshal(input, &in); err != nil {
		return nil, err
	}
	if toolCtx == nil {
		return nil, errors.New("ExitPlanMode: no tool context")
	}
	from := toolCtx.PlanMode
	if from == "" {
		from = ModeNormal
	}
	if from != ModePlan {
		return nil, fmt.Errorf("ExitPlanMode: not in plan mode (current=%s)", from)
	}
	toolCtx.PlanMode = ModeNormal
	if toolCtx.EmitEvent != nil {
		data, _ := json.Marshal(agents.PlanModeChange{From: ModePlan, To: ModeNormal, Summary: in.Plan})
		toolCtx.EmitEvent(agents.StreamEvent{Type: agents.EventPlanModeChange, Data: data})
	}
	return &tool.ToolResult{Data: Output{From: ModePlan, To: ModeNormal, Summary: in.Plan}}, nil
}

func (t *Tool) MapToolResultToParam(content interface{}, toolUseID string) *agents.ContentBlock {
	return &agents.ContentBlock{
		Type:      agents.ContentBlockToolResult,
		ToolUseID: toolUseID,
		Content:   renderOutput(content),
	}
}

func renderOutput(content interface{}) string {
	var out Output
	switch v := content.(type) {
	case Output:
		out = v
	case *Output:
		if v != nil {
			out = *v
		}
	case string:
		return v
	default:
		b, _ := json.Marshal(content)
		return string(b)
	}
	if out.Summary == "" {
		return fmt.Sprintf("Exited plan mode (%s → %s).", out.From, out.To)
	}
	return fmt.Sprintf("Exited plan mode (%s → %s):\n%s", out.From, out.To, out.Summary)
}

var _ tool.Tool = (*Tool)(nil)
