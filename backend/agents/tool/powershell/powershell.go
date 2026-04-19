package powershell

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"runtime"
	"strings"

	"github.com/wall-ai/ubuilding/backend/agents"
	"github.com/wall-ai/ubuilding/backend/agents/tool"
	"github.com/wall-ai/ubuilding/backend/agents/tool/bg"
	"github.com/wall-ai/ubuilding/backend/agents/tool/shell"
)

// Name is the primary tool name. On Windows the builtin registry aliases this
// to "Bash" so model-side naming stays platform-agnostic.
const Name = "PowerShell"

const (
	DefaultTimeoutMs = 120_000
	MaxTimeoutMs     = 600_000
	MaxOutputBytes   = 30_000
)

// Input matches the Bash input shape for cross-tool consistency.
type Input struct {
	Command         string `json:"command"`
	Timeout         int    `json:"timeout,omitempty"`
	RunInBackground bool   `json:"run_in_background,omitempty"`
}

// BgStartOutput is returned when the command is launched in the background.
type BgStartOutput struct {
	BashID  string `json:"bash_id"`
	Command string `json:"command"`
	Status  string `json:"status"`
}

type Output struct {
	Command    string `json:"command"`
	ExitCode   int    `json:"exitCode"`
	Stdout     string `json:"stdout"`
	Truncated  bool   `json:"truncated,omitempty"`
	TimedOut   bool   `json:"timedOut,omitempty"`
	Canceled   bool   `json:"canceled,omitempty"`
	DurationMs int64  `json:"durationMs"`
}

type Option func(*PowerShellTool)

func WithAllowlist(prefixes ...string) Option {
	return func(p *PowerShellTool) { p.extraAllow = append(p.extraAllow, prefixes...) }
}

func WithDenylistReason(substr, reason string) Option {
	return func(p *PowerShellTool) {
		p.extraDeny = append(p.extraDeny, denyEntry{substr: strings.ToLower(substr), reason: reason})
	}
}

// WithAlias overrides the primary name advertised to the model. Set to
// "Bash" by the builtin registry on Windows so prompts stay platform-agnostic.
func WithAlias(name string) Option {
	return func(p *PowerShellTool) { p.name = name }
}

type PowerShellTool struct {
	tool.ToolDefaults
	name       string
	extraAllow []string
	extraDeny  []denyEntry
}

type denyEntry struct {
	substr string
	reason string
}

func New(opts ...Option) *PowerShellTool {
	p := &PowerShellTool{name: Name}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

func (p *PowerShellTool) Name() string      { return p.name }
func (p *PowerShellTool) Aliases() []string { return []string{"powershell", "pwsh", "shell"} }
func (p *PowerShellTool) IsEnabled() bool   { return runtime.GOOS == "windows" }
func (p *PowerShellTool) IsReadOnly(input json.RawMessage) bool {
	return p.classify(input).Class == ClassReadOnly
}
func (p *PowerShellTool) IsConcurrencySafe(_ json.RawMessage) bool { return false }
func (p *PowerShellTool) IsDestructive(input json.RawMessage) bool {
	return p.classify(input).Class == ClassDeny
}
func (p *PowerShellTool) MaxResultSizeChars() int { return MaxOutputBytes }

func (p *PowerShellTool) InputSchema() *tool.JSONSchema {
	return &tool.JSONSchema{
		Type: "object",
		Properties: map[string]*tool.SchemaProperty{
			"command":           {Type: "string", Description: "The PowerShell command to execute."},
			"timeout":           {Type: "integer", Description: "Timeout in milliseconds (max 600000)."},
			"run_in_background": {Type: "boolean", Description: "When true, spawn the command as a background task."},
		},
		Required: []string{"command"},
	}
}

func (p *PowerShellTool) Description(input json.RawMessage) string {
	var in Input
	if err := json.Unmarshal(input, &in); err != nil || in.Command == "" {
		return "Run a PowerShell command"
	}
	cmd := in.Command
	if len(cmd) > 80 {
		cmd = cmd[:80] + "…"
	}
	return "PowerShell: " + cmd
}

func (p *PowerShellTool) Prompt(opts tool.PromptOptions) string {
	return buildPrompt(opts)
}

func (p *PowerShellTool) ValidateInput(input json.RawMessage, _ *agents.ToolUseContext) *tool.ValidationResult {
	var in Input
	if err := json.Unmarshal(input, &in); err != nil {
		return &tool.ValidationResult{Valid: false, Message: fmt.Sprintf("invalid input: %v", err)}
	}
	if strings.TrimSpace(in.Command) == "" {
		return &tool.ValidationResult{Valid: false, Message: "command must not be empty"}
	}
	if in.Timeout < 0 {
		return &tool.ValidationResult{Valid: false, Message: "timeout must be non-negative"}
	}
	if in.Timeout > MaxTimeoutMs {
		return &tool.ValidationResult{Valid: false, Message: fmt.Sprintf("timeout exceeds maximum of %d ms", MaxTimeoutMs)}
	}
	return &tool.ValidationResult{Valid: true}
}

func (p *PowerShellTool) CheckPermissions(input json.RawMessage, _ *agents.ToolUseContext) (*tool.PermissionResult, error) {
	cls := p.classify(input)
	switch cls.Class {
	case ClassDeny:
		return &tool.PermissionResult{
			Behavior:       tool.PermissionDeny,
			UpdatedInput:   input,
			Message:        "PowerShell: " + cls.Reason,
			RiskLevel:      "high",
			DecisionReason: "blocklist",
		}, nil
	case ClassReadOnly:
		return &tool.PermissionResult{
			Behavior:       tool.PermissionAllow,
			UpdatedInput:   input,
			RiskLevel:      "low",
			DecisionReason: "readonly-allowlist",
		}, nil
	default:
		return &tool.PermissionResult{
			Behavior:       tool.PermissionAsk,
			UpdatedInput:   input,
			RiskLevel:      "medium",
			DecisionReason: "default-ask",
		}, nil
	}
}

func (p *PowerShellTool) Call(ctx context.Context, input json.RawMessage, toolCtx *agents.ToolUseContext) (*tool.ToolResult, error) {
	var in Input
	if err := json.Unmarshal(input, &in); err != nil {
		return nil, fmt.Errorf("invalid input: %w", err)
	}
	timeout := in.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeoutMs
	}
	if in.RunInBackground {
		return p.callBackground(in, timeout, toolCtx)
	}
	res, err := shell.Run(ctx, shell.Spec{
		Cmd:            "powershell",
		Args:           []string{"-NoProfile", "-NonInteractive", "-Command", in.Command},
		TimeoutMs:      timeout,
		MaxOutputBytes: MaxOutputBytes,
	})
	if err != nil {
		return nil, err
	}
	out := Output{
		Command:    in.Command,
		ExitCode:   res.ExitCode,
		Stdout:     res.Output,
		Truncated:  res.Truncated,
		TimedOut:   res.TimedOut,
		Canceled:   res.Canceled,
		DurationMs: res.Duration.Milliseconds(),
	}
	return &tool.ToolResult{Data: out}, nil
}

// callBackground spawns the command via bg.Manager and returns immediately.
func (p *PowerShellTool) callBackground(in Input, timeout int, toolCtx *agents.ToolUseContext) (*tool.ToolResult, error) {
	mgr := bgManagerFromCtx(toolCtx)
	if mgr == nil {
		return nil, errors.New("PowerShell: run_in_background requested but no bg.Manager attached to context")
	}
	runner := func(ctx context.Context, write func(chunk string)) (int, error) {
		res, err := shell.Run(ctx, shell.Spec{
			Cmd:            "powershell",
			Args:           []string{"-NoProfile", "-NonInteractive", "-Command", in.Command},
			TimeoutMs:      timeout,
			MaxOutputBytes: -1,
		})
		if err != nil {
			return -1, err
		}
		if res.Output != "" {
			write(res.Output)
		}
		if res.TimedOut {
			return res.ExitCode, fmt.Errorf("timed out after %dms", timeout)
		}
		if res.Canceled {
			return res.ExitCode, context.Canceled
		}
		return res.ExitCode, nil
	}
	id, err := mgr.Start(context.Background(), in.Command, runner)
	if err != nil {
		return nil, err
	}
	return &tool.ToolResult{Data: BgStartOutput{
		BashID:  id,
		Command: in.Command,
		Status:  bg.StatusRunning,
	}}, nil
}

func bgManagerFromCtx(toolCtx *agents.ToolUseContext) *bg.Manager {
	if toolCtx == nil {
		return nil
	}
	if m, ok := toolCtx.TaskManager.(*bg.Manager); ok {
		return m
	}
	return nil
}

func (p *PowerShellTool) MapToolResultToParam(content interface{}, toolUseID string) *agents.ContentBlock {
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
	if out.Command != "" {
		fmt.Fprintf(&sb, "PS> %s\n", out.Command)
	}
	sb.WriteString(out.Stdout)
	if !strings.HasSuffix(out.Stdout, "\n") {
		sb.WriteString("\n")
	}
	switch {
	case out.TimedOut:
		fmt.Fprintf(&sb, "(timed out after %dms, exit=%d)", out.DurationMs, out.ExitCode)
	case out.Canceled:
		sb.WriteString("(cancelled)")
	case out.Truncated:
		fmt.Fprintf(&sb, "(output truncated, exit=%d)", out.ExitCode)
	default:
		fmt.Fprintf(&sb, "(exit=%d)", out.ExitCode)
	}
	return sb.String()
}

func (p *PowerShellTool) classify(input json.RawMessage) Classification {
	var in Input
	if err := json.Unmarshal(input, &in); err != nil {
		return Classification{Class: ClassNormal}
	}
	low := strings.ToLower(in.Command)
	for _, d := range p.extraDeny {
		if strings.Contains(low, d.substr) {
			return Classification{Class: ClassDeny, Reason: d.reason}
		}
	}
	cls := Classify(in.Command)
	if cls.Class == ClassReadOnly {
		return cls
	}
	if len(p.extraAllow) > 0 && cls.Class == ClassNormal {
		segments := splitPipelines(in.Command)
		all := true
		for _, seg := range segments {
			seg = strings.TrimSpace(strings.ToLower(seg))
			matched := false
			for _, prefix := range p.extraAllow {
				prefix = strings.ToLower(prefix)
				if strings.HasPrefix(seg, prefix) && (len(seg) == len(prefix) || seg[len(prefix)] == ' ') {
					matched = true
					break
				}
			}
			if !matched {
				all = false
				break
			}
		}
		if all && len(segments) > 0 {
			return Classification{Class: ClassReadOnly, Reason: "extra-allowlist"}
		}
	}
	return cls
}

var _ tool.Tool = (*PowerShellTool)(nil)
