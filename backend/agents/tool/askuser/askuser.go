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
	Question      string                 `json:"question"`
	Options       []agents.AskUserOption `json:"options,omitempty"`
	AllowMultiple bool                   `json:"allowMultiple,omitempty"`
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

func (t *Tool) Name() string                             { return Name }
func (t *Tool) IsReadOnly(_ json.RawMessage) bool        { return true }
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

func (t *Tool) Prompt(opts tool.PromptOptions) string {
	exitPlanRef := resolvePeer(opts, "ExitPlanMode")

	// Upstream `AskUserQuestionTool/prompt.ts` ends with a PREVIEW_FEATURE_PROMPT
	// paragraph whose wording switches between markdown (default) and html
	// when the host renders the preview panel as HTML. PromptOptions.PreviewFormat
	// toggles which variant we emit; the zero value ("") defaults to markdown
	// to match the upstream default.
	previewBlock := previewSectionMarkdown
	if strings.EqualFold(opts.PreviewFormat, "html") {
		previewBlock = previewSectionHTML
	}

	return `Use this tool when you need to ask the user questions during execution. This allows you to:
1. Gather user preferences or requirements
2. Clarify ambiguous instructions
3. Get decisions on implementation choices as you work
4. Offer choices to the user about what direction to take.

Usage notes:
- Users will always be able to select "Other" to provide custom text input
- Use multiSelect: true to allow multiple answers to be selected for a question
- If you recommend a specific option, make that the first option in the list and add "(Recommended)" at the end of the label

Plan mode note: In plan mode, use this tool to clarify requirements or choose between approaches BEFORE finalizing your plan. Do NOT use this tool to ask "Is my plan ready?" or "Should I proceed?" - use ` + exitPlanRef + ` for plan approval. IMPORTANT: Do not reference "the plan" in your questions (e.g., "Do you have feedback about the plan?", "Does the plan look good?") because the user cannot see the plan in the UI until you call ` + exitPlanRef + `. If you need plan approval, use ` + exitPlanRef + ` instead.
` + previewBlock
}

const previewSectionMarkdown = `
Preview feature:
Use the optional ` + "`preview`" + ` field on options when presenting concrete artifacts that users need to visually compare:
- ASCII mockups of UI layouts or components
- Code snippets showing different implementations
- Diagram variations
- Configuration examples

Preview content is rendered as markdown in a monospace box. Multi-line text with newlines is supported. When any option has a preview, the UI switches to a side-by-side layout with a vertical option list on the left and preview on the right. Do not use previews for simple preference questions where labels and descriptions suffice. Note: previews are only supported for single-select questions (not multiSelect).
`

const previewSectionHTML = `
Preview feature:
Use the optional ` + "`preview`" + ` field on options when presenting concrete artifacts that users need to visually compare:
- HTML mockups of UI layouts or components
- Formatted code snippets showing different implementations
- Visual comparisons or diagrams

Preview content must be a self-contained HTML fragment (no <html>/<body> wrapper, no <script> or <style> tags — use inline style attributes instead). Do not use previews for simple preference questions where labels and descriptions suffice. Note: previews are only supported for single-select questions (not multiSelect).
`

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
