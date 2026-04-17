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

func (t *Tool) Prompt(_ tool.PromptOptions) string {
	return `Send a message to the user. This is your primary visible output channel in
chat / assistant mode.

Rules:
- message is the text shown to the user (markdown-capable).
- status = "normal" when replying to the user's last turn; "proactive" when
  you are surfacing something unsolicited (task completion, blocker, status).
- attachments is an optional list of ABSOLUTE file paths for the UI to render
  alongside your message (images, diffs, logs, screenshots).
- This tool is read-only and concurrency-safe.`
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
