// Package askuser implements the AskUserQuestion tool. The tool emits an
// EventAskUser and calls ToolUseContext.AskUser to collect the user's answer.
package askuser

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
const Name = "AskUserQuestion"

// MaxOptions caps the option count.
const MaxOptions = 4

// Input matches claude-code's ask_user tool shape.
type Input struct {
	Question      string                  `json:"question"`
	Options       []agents.AskUserOption  `json:"options,omitempty"`
	AllowMultiple bool                    `json:"allowMultiple,omitempty"`
}

// Output is the structured result.
type Output struct {
	Question string   `json:"question"`
	Selected []string `json:"selected,omitempty"`
	Text     string   `json:"text,omitempty"`
}

// Tool implements tool.Tool.
type Tool struct {
	tool.ToolDefaults
}

// New returns an AskUserQuestion tool.
func New() *Tool { return &Tool{} }

func (t *Tool) Name() string                            { return Name }
func (t *Tool) IsReadOnly(_ json.RawMessage) bool       { return true }
func (t *Tool) IsConcurrencySafe(_ json.RawMessage) bool { return false }

func (t *Tool) InputSchema() *tool.JSONSchema {
	return &tool.JSONSchema{
		Type: "object",
		Properties: map[string]*tool.SchemaProperty{
			"question":      {Type: "string", Description: "The question to ask the user."},
			"options":       {Type: "array", Description: "Up to 4 predefined options."},
			"allowMultiple": {Type: "boolean", Description: "Allow selecting multiple options."},
		},
		Required: []string{"question", "options"},
	}
}

func (t *Tool) Description(input json.RawMessage) string {
	var in Input
	_ = json.Unmarshal(input, &in)
	q := in.Question
	if q == "" {
		return "Ask the user"
	}
	if len(q) > 80 {
		q = q[:80] + "…"
	}
	return "Ask: " + q
}

func (t *Tool) Prompt(_ tool.PromptOptions) string {
	return `Asks the user a clarifying question with up to 4 predefined options.

Use this when the user's intent is ambiguous and you need a choice among a small set of alternatives.

Rules:
- Provide a concise question.
- Provide 2–4 options (label + short description).
- Never include "other" as an option — the user can always type a custom answer.
- Set allowMultiple=true only when multiple selections make sense.`
}

func (t *Tool) ValidateInput(input json.RawMessage, _ *agents.ToolUseContext) *tool.ValidationResult {
	var in Input
	if err := json.Unmarshal(input, &in); err != nil {
		return &tool.ValidationResult{Valid: false, Message: fmt.Sprintf("invalid input: %v", err)}
	}
	if strings.TrimSpace(in.Question) == "" {
		return &tool.ValidationResult{Valid: false, Message: "question must not be empty"}
	}
	if len(in.Options) > MaxOptions {
		return &tool.ValidationResult{Valid: false, Message: fmt.Sprintf("at most %d options allowed", MaxOptions)}
	}
	for i, o := range in.Options {
		if strings.TrimSpace(o.Label) == "" {
			return &tool.ValidationResult{Valid: false, Message: fmt.Sprintf("options[%d]: label required", i)}
		}
		if strings.EqualFold(strings.TrimSpace(o.Label), "other") {
			return &tool.ValidationResult{Valid: false, Message: "\"other\" is not a valid option label"}
		}
	}
	return &tool.ValidationResult{Valid: true}
}

func (t *Tool) CheckPermissions(input json.RawMessage, _ *agents.ToolUseContext) (*tool.PermissionResult, error) {
	return &tool.PermissionResult{Behavior: tool.PermissionAllow, UpdatedInput: input, DecisionReason: "askuser-default-allow"}, nil
}

func (t *Tool) Call(ctx context.Context, input json.RawMessage, toolCtx *agents.ToolUseContext) (*tool.ToolResult, error) {
	var in Input
	if err := json.Unmarshal(input, &in); err != nil {
		return nil, err
	}
	if toolCtx == nil || toolCtx.AskUser == nil {
		return nil, errors.New("AskUserQuestion: no AskUser handler attached to context")
	}
	payload := agents.AskUserPayload{
		Question:      in.Question,
		Options:       in.Options,
		AllowMultiple: in.AllowMultiple,
	}
	if toolCtx.EmitEvent != nil {
		if data, err := json.Marshal(payload); err == nil {
			toolCtx.EmitEvent(agents.StreamEvent{Type: agents.EventAskUser, Data: data})
		}
	}
	resp, err := toolCtx.AskUser(ctx, payload)
	if err != nil {
		return nil, err
	}
	return &tool.ToolResult{Data: Output{
		Question: in.Question,
		Selected: resp.Selected,
		Text:     resp.Text,
	}}, nil
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
	var sb strings.Builder
	fmt.Fprintf(&sb, "Q: %s\n", out.Question)
	if len(out.Selected) > 0 {
		fmt.Fprintf(&sb, "Selected: %s\n", strings.Join(out.Selected, ", "))
	}
	if out.Text != "" {
		fmt.Fprintf(&sb, "Text: %s\n", out.Text)
	}
	return sb.String()
}

var _ tool.Tool = (*Tool)(nil)
