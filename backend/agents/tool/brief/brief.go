// Package brief implements the BriefTool, ported from claude-code-main's
// src/tools/BriefTool/BriefTool.ts. It is the model's primary user-facing
// output channel in chat / assistant modes: the model emits a BriefTool
// call and the host renders it to the user (our pipe is EventBrief).
//
// Attachment resolution is delegated to a pluggable AttachmentResolver so
// tests can exercise the tool without touching the filesystem.
package brief

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/wall-ai/ubuilding/backend/agents"
	"github.com/wall-ai/ubuilding/backend/agents/tool"
)

// Name matches claude-code-main's BRIEF_TOOL_NAME.
const Name = "SendUserMessage"

// LegacyName is the older alias still accepted by claude-code clients.
const LegacyName = "BriefTool"

// Input is the BriefTool input.
type Input struct {
	Message     string   `json:"message"`
	Attachments []string `json:"attachments,omitempty"`
	Status      string   `json:"status"`
}

// AttachmentResolver validates + resolves attachment paths. The default
// resolver stats local files; hosts can inject their own for sandbox rules.
type AttachmentResolver interface {
	Resolve(ctx context.Context, paths []string) ([]agents.BriefAttachment, error)
}

// Tool implements tool.Tool for BriefTool.
type Tool struct {
	tool.ToolDefaults
	resolver AttachmentResolver
}

// Option configures a Tool.
type Option func(*Tool)

// WithAttachmentResolver overrides the default filesystem resolver.
func WithAttachmentResolver(r AttachmentResolver) Option {
	return func(t *Tool) { t.resolver = r }
}

// New returns a BriefTool with sensible defaults.
func New(opts ...Option) *Tool {
	t := &Tool{resolver: defaultResolver{}}
	for _, o := range opts {
		o(t)
	}
	return t
}

func (t *Tool) Name() string                             { return Name }
func (t *Tool) IsReadOnly(_ json.RawMessage) bool        { return true }
func (t *Tool) IsConcurrencySafe(_ json.RawMessage) bool { return true }

func (t *Tool) InputSchema() *tool.JSONSchema {
	return &tool.JSONSchema{
		Type: "object",
		Properties: map[string]*tool.SchemaProperty{
			"message":     {Type: "string", Description: "Message to the user. Supports markdown."},
			"attachments": {Type: "array", Description: "Optional absolute file paths to attach."},
			"status":      {Type: "string", Description: `"normal" for a reply, "proactive" for an unsolicited update.`},
		},
		Required: []string{"message", "status"},
	}
}

func (t *Tool) Description(_ json.RawMessage) string {
	return "Send a message to the user (primary visible output channel)"
}

func (t *Tool) Prompt(_ tool.PromptOptions) string { return ToolPrompt }

// ToolPrompt mirrors upstream BRIEF_TOOL_PROMPT
// (opensource/claude-code-main/src/tools/BriefTool/prompt.ts).
const ToolPrompt = "Send a message the user will read. Text outside this tool is visible in the detail view, but most won't open it — the answer lives here.\n\n" +
	"`message` supports markdown. `attachments` takes file paths (absolute or cwd-relative) for images, diffs, logs.\n\n" +
	"`status` labels intent: 'normal' when replying to what they just asked; 'proactive' when you're initiating — a scheduled task finished, a blocker surfaced during background work, you need input on something they haven't asked about. Set it honestly; downstream routing uses it."

// ProactiveSection returns the BRIEF_PROACTIVE_SECTION block callers can
// splice into the system prompt. It mirrors BRIEF_PROACTIVE_SECTION in
// prompt.ts and references whichever name the host registered the brief
// tool under (defaults to "SendUserMessage").
func ProactiveSection(opts tool.PromptOptions) string {
	name := Name
	if len(opts.Tools) > 0 {
		for _, tl := range opts.Tools {
			if tl == nil {
				continue
			}
			if tl.Name() == Name || tl.Name() == LegacyName {
				name = tl.Name()
				break
			}
			for _, alias := range tl.Aliases() {
				if alias == Name || alias == LegacyName {
					name = tl.Name()
					break
				}
			}
		}
	}
	return "## Talking to the user\n\n" +
		name + " is where your replies go. Text outside it is visible if the user expands the detail view, but most won't — assume unread. Anything you want them to actually see goes through " + name + ". The failure mode: the real answer lives in plain text while " + name + " just says \"done!\" — they see \"done!\" and miss everything.\n\n" +
		"So: every time the user says something, the reply they actually read comes through " + name + ". Even for \"hi\". Even for \"thanks\".\n\n" +
		"If you can answer right away, send the answer. If you need to go look — run a command, read files, check something — ack first in one line (\"On it — checking the test output\"), then work, then send the result. Without the ack they're staring at a spinner.\n\n" +
		"For longer work: ack → work → result. Between those, send a checkpoint when something useful happened — a decision you made, a surprise you hit, a phase boundary. Skip the filler (\"running tests...\") — a checkpoint earns its place by carrying information.\n\n" +
		"Keep messages tight — the decision, the file:line, the PR number. Second person always (\"your config\"), never third."
}

func (t *Tool) ValidateInput(input json.RawMessage, _ *agents.ToolUseContext) *tool.ValidationResult {
	var in Input
	if err := json.Unmarshal(input, &in); err != nil {
		return &tool.ValidationResult{Valid: false, Message: fmt.Sprintf("invalid input: %v", err)}
	}
	if strings.TrimSpace(in.Message) == "" {
		return &tool.ValidationResult{Valid: false, Message: "message required"}
	}
	switch in.Status {
	case "normal", "proactive":
	default:
		return &tool.ValidationResult{Valid: false, Message: `status must be "normal" or "proactive"`}
	}
	for _, p := range in.Attachments {
		if !filepath.IsAbs(p) {
			return &tool.ValidationResult{Valid: false, Message: fmt.Sprintf("attachment path must be absolute: %q", p)}
		}
	}
	return &tool.ValidationResult{Valid: true}
}

func (t *Tool) CheckPermissions(input json.RawMessage, _ *agents.ToolUseContext) (*tool.PermissionResult, error) {
	return &tool.PermissionResult{Behavior: tool.PermissionAllow, UpdatedInput: input, DecisionReason: "brief-read-only"}, nil
}

func (t *Tool) Call(ctx context.Context, input json.RawMessage, toolCtx *agents.ToolUseContext) (*tool.ToolResult, error) {
	var in Input
	if err := json.Unmarshal(input, &in); err != nil {
		return nil, err
	}
	payload := agents.BriefPayload{
		Message: in.Message,
		Status:  in.Status,
		SentAt:  time.Now().UTC().Format(time.RFC3339Nano),
	}
	if len(in.Attachments) > 0 {
		if t.resolver == nil {
			return nil, errors.New("brief: no attachment resolver")
		}
		resolved, err := t.resolver.Resolve(ctx, in.Attachments)
		if err != nil {
			return nil, err
		}
		payload.Attachments = resolved
	}
	if toolCtx != nil && toolCtx.EmitEvent != nil {
		if data, err := json.Marshal(payload); err == nil {
			toolCtx.EmitEvent(agents.StreamEvent{Type: agents.EventBrief, Data: data})
		}
	}
	return &tool.ToolResult{Data: payload}, nil
}

func (t *Tool) MapToolResultToParam(content interface{}, toolUseID string) *agents.ContentBlock {
	p, ok := content.(agents.BriefPayload)
	suffix := ""
	if ok {
		n := len(p.Attachments)
		if n > 0 {
			unit := "attachment"
			if n != 1 {
				unit = "attachments"
			}
			suffix = fmt.Sprintf(" (%d %s included)", n, unit)
		}
	}
	return &agents.ContentBlock{
		Type:      agents.ContentBlockToolResult,
		ToolUseID: toolUseID,
		Content:   fmt.Sprintf("Message delivered to user.%s", suffix),
	}
}

// ──────────────────────────────────────────────────────────────────────────
// default resolver
// ──────────────────────────────────────────────────────────────────────────

// defaultResolver stats each path on local disk and rejects missing files.
// Image detection is a light mime sniff on extension (images keep the
// UI renderer's "inline" path).
type defaultResolver struct{}

func (defaultResolver) Resolve(_ context.Context, paths []string) ([]agents.BriefAttachment, error) {
	out := make([]agents.BriefAttachment, 0, len(paths))
	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil {
			return nil, fmt.Errorf("attachment %q: %w", p, err)
		}
		if info.IsDir() {
			return nil, fmt.Errorf("attachment %q is a directory", p)
		}
		out = append(out, agents.BriefAttachment{
			Path:    p,
			Size:    info.Size(),
			IsImage: isImageExt(filepath.Ext(p)),
		})
	}
	return out, nil
}

func isImageExt(ext string) bool {
	switch strings.ToLower(ext) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".bmp", ".svg", ".tiff", ".heic", ".heif":
		return true
	}
	return false
}

var _ tool.Tool = (*Tool)(nil)
