package bash

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

// Name is the primary tool name advertised to the model.
const Name = "Bash"

// DefaultTimeoutMs is applied when the caller does not specify a timeout.
const DefaultTimeoutMs = 120_000

// MaxTimeoutMs caps the timeout parameter (10 minutes).
const MaxTimeoutMs = 600_000

// MaxOutputBytes caps combined stdout/stderr returned to the model.
const MaxOutputBytes = 30_000

// Input matches the TS BashTool input shape (subset).
type Input struct {
	Command         string `json:"command"`
	Timeout         int    `json:"timeout,omitempty"`
	RunInBackground bool   `json:"run_in_background,omitempty"`
	SandboxMd       string `json:"sandbox,omitempty"`
}

// BgStartOutput is returned when the command is started in the background. The
// caller then polls via TaskOutput and cancels via TaskStop.
type BgStartOutput struct {
	BashID  string `json:"bash_id"`
	Command string `json:"command"`
	Status  string `json:"status"`
}

// Output captures the structured tool result.
type Output struct {
	Command    string `json:"command"`
	ExitCode   int    `json:"exitCode"`
	Stdout     string `json:"stdout"`
	Truncated  bool   `json:"truncated,omitempty"`
	TimedOut   bool   `json:"timedOut,omitempty"`
	Canceled   bool   `json:"canceled,omitempty"`
	DurationMs int64  `json:"durationMs"`
}

// Option configures a BashTool.
type Option func(*BashTool)

// WithAllowlist extends the read-only allowlist with additional command
// prefixes (lower-case, word-terminated matching).
func WithAllowlist(prefixes ...string) Option {
	return func(b *BashTool) { b.extraAllow = append(b.extraAllow, prefixes...) }
}

// WithDenylistReason registers an additional custom deny pattern with a
// human-readable reason. Patterns are plain substrings, case-insensitive.
func WithDenylistReason(substr, reason string) Option {
	return func(b *BashTool) {
		b.extraDeny = append(b.extraDeny, denyEntry{substr: strings.ToLower(substr), reason: reason})
	}
}

// BashTool implements tool.Tool.
type BashTool struct {
	tool.ToolDefaults
	shellPath  string
	extraAllow []string
	extraDeny  []denyEntry
}

type denyEntry struct {
	substr string
	reason string
}

// New returns a BashTool that invokes /bin/bash by default, falling back to
// /bin/sh when bash is not on PATH.
func New(opts ...Option) *BashTool {
	b := &BashTool{shellPath: defaultShellPath()}
	for _, opt := range opts {
		opt(b)
	}
	return b
}

func defaultShellPath() string {
	if runtime.GOOS == "windows" {
		// Still usable on Windows for tests that inject a POSIX shell.
		return "bash"
	}
	return "/bin/bash"
}

func (b *BashTool) Name() string      { return Name }
func (b *BashTool) Aliases() []string { return []string{"bash", "shell"} }
func (b *BashTool) IsEnabled() bool   { return runtime.GOOS != "windows" }
func (b *BashTool) IsReadOnly(input json.RawMessage) bool {
	return b.classify(input).Class == ClassReadOnly
}
func (b *BashTool) IsConcurrencySafe(_ json.RawMessage) bool { return false }
func (b *BashTool) IsDestructive(input json.RawMessage) bool {
	return b.classify(input).Class == ClassDeny
}
func (b *BashTool) MaxResultSizeChars() int { return MaxOutputBytes }

func (b *BashTool) InputSchema() *tool.JSONSchema {
	return &tool.JSONSchema{
		Type: "object",
		Properties: map[string]*tool.SchemaProperty{
			"command":           {Type: "string", Description: "The bash command to execute."},
			"timeout":           {Type: "integer", Description: "Timeout in milliseconds (max 600000)."},
			"run_in_background": {Type: "boolean", Description: "When true, spawn the command as a background task."},
		},
		Required: []string{"command"},
	}
}

func (b *BashTool) Description(input json.RawMessage) string {
	var in Input
	if err := json.Unmarshal(input, &in); err != nil || in.Command == "" {
		return "Run a shell command"
	}
	cmd := in.Command
	if len(cmd) > 80 {
		cmd = cmd[:80] + "…"
	}
	return "Bash: " + cmd
}

func (b *BashTool) Prompt(opts tool.PromptOptions) string {
	return buildPrompt(opts)
}

func (b *BashTool) ValidateInput(input json.RawMessage, _ *agents.ToolUseContext) *tool.ValidationResult {
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

func (b *BashTool) CheckPermissions(input json.RawMessage, _ *agents.ToolUseContext) (*tool.PermissionResult, error) {
	cls := b.classify(input)
	switch cls.Class {
	case ClassDeny:
		return &tool.PermissionResult{
			Behavior:       tool.PermissionDeny,
			UpdatedInput:   input,
			Message:        "Bash: " + cls.Reason,
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

func (b *BashTool) Call(ctx context.Context, input json.RawMessage, toolCtx *agents.ToolUseContext) (*tool.ToolResult, error) {
	var in Input
	if err := json.Unmarshal(input, &in); err != nil {
		return nil, fmt.Errorf("invalid input: %w", err)
	}
	timeout := in.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeoutMs
	}
	if in.RunInBackground {
		return b.callBackground(in, timeout, toolCtx)
	}
	res, err := shell.Run(ctx, shell.Spec{
		Cmd:            b.shellPath,
		Args:           []string{"-c", in.Command},
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
func (b *BashTool) callBackground(in Input, timeout int, toolCtx *agents.ToolUseContext) (*tool.ToolResult, error) {
	mgr := bgManagerFromCtx(toolCtx)
	if mgr == nil {
		return nil, errors.New("Bash: run_in_background requested but no bg.Manager attached to context")
	}
	shellPath := b.shellPath
	args := []string{"-c", in.Command}
	runner := func(ctx context.Context, write func(chunk string)) (int, error) {
		res, err := shell.Run(ctx, shell.Spec{
			Cmd: shellPath, Args: args,
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

// MapToolResultToParam renders the structured Output as a user-visible text
// block (claude-code-style: "$ <command>\n<stdout>\n(exit N)" suffixed).
func (b *BashTool) MapToolResultToParam(content interface{}, toolUseID string) *agents.ContentBlock {
	text := renderOutput(content)
	return &agents.ContentBlock{
		Type:      agents.ContentBlockToolResult,
		ToolUseID: toolUseID,
		Content:   text,
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
		fmt.Fprintf(&sb, "$ %s\n", out.Command)
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

func (b *BashTool) classify(input json.RawMessage) Classification {
	var in Input
	if err := json.Unmarshal(input, &in); err != nil {
		return Classification{Class: ClassNormal}
	}
	// Extra deny patterns first (substring, case-insensitive).
	low := strings.ToLower(in.Command)
	for _, d := range b.extraDeny {
		if strings.Contains(low, d.substr) {
			return Classification{Class: ClassDeny, Reason: d.reason}
		}
	}
	cls := Classify(in.Command)
	if cls.Class == ClassReadOnly {
		return cls
	}
	// Extra allowlist: any segment starting with a user-added prefix counts.
	if len(b.extraAllow) > 0 && cls.Class == ClassNormal {
		segments := splitPipelines(in.Command)
		all := true
		for _, seg := range segments {
			seg = strings.TrimSpace(strings.ToLower(seg))
			matched := false
			for _, p := range b.extraAllow {
				p = strings.ToLower(p)
				if strings.HasPrefix(seg, p) && (len(seg) == len(p) || seg[len(p)] == ' ') {
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

// Compile-time assertion.
var _ tool.Tool = (*BashTool)(nil)
