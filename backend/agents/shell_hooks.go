package agents

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"
)

// ---------------------------------------------------------------------------
// Shell Hooks Framework
// Maps to TypeScript utils/hooks.ts
//
// Shell hooks are user-defined commands executed at lifecycle events
// (PreToolUse, PostToolUse, Stop, SessionStart, etc.). They run as
// subprocesses with JSON on stdin and produce JSON or plain text on stdout.
// ---------------------------------------------------------------------------

// HookEvent enumerates the lifecycle events that can trigger hooks.
type HookEvent string

const (
	HookEventPreToolUse        HookEvent = "PreToolUse"
	HookEventPostToolUse       HookEvent = "PostToolUse"
	HookEventPostToolUseFailure HookEvent = "PostToolUseFailure"
	HookEventStop              HookEvent = "Stop"
	HookEventStopFailure       HookEvent = "StopFailure"
	HookEventNotification      HookEvent = "Notification"
	HookEventSessionStart      HookEvent = "SessionStart"
	HookEventSessionEnd        HookEvent = "SessionEnd"
	HookEventSubagentStart     HookEvent = "SubagentStart"
	HookEventSubagentStop      HookEvent = "SubagentStop"
	HookEventUserPromptSubmit  HookEvent = "UserPromptSubmit"
	HookEventPreCompact        HookEvent = "PreCompact"
	HookEventPostCompact       HookEvent = "PostCompact"
	HookEventSetup             HookEvent = "Setup"
)

// HookCommand represents a user-defined shell hook command.
type HookCommand struct {
	// Command is the shell command to execute.
	Command string `json:"command"`

	// Matcher is a tool name pattern (glob) to match against.
	// Only applies to PreToolUse/PostToolUse events.
	Matcher string `json:"matcher,omitempty"`

	// Timeout in seconds. 0 means default (600s).
	Timeout int `json:"timeout,omitempty"`

	// Shell specifies the shell to use ("bash" or "powershell").
	// Default is "bash".
	Shell string `json:"shell,omitempty"`

	// Async indicates the hook should run in the background.
	Async bool `json:"async,omitempty"`
}

// HookInput is the JSON payload written to the hook's stdin.
type HookInput struct {
	// Common fields
	SessionID      string `json:"session_id"`
	TranscriptPath string `json:"transcript_path,omitempty"`
	Cwd            string `json:"cwd"`

	// PreToolUse / PostToolUse fields
	ToolName  string          `json:"tool_name,omitempty"`
	ToolInput json.RawMessage `json:"tool_input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`

	// PostToolUse specific
	ToolOutput string `json:"tool_output,omitempty"`
	IsError    bool   `json:"is_error,omitempty"`

	// Stop specific
	StopReason string `json:"stop_reason,omitempty"`

	// Extra context
	AgentID   string `json:"agent_id,omitempty"`
	AgentType string `json:"agent_type,omitempty"`
}

// HookOutput is the parsed JSON output from a hook's stdout.
type HookOutput struct {
	// Continue indicates whether processing should continue. Default true.
	Continue *bool `json:"continue,omitempty"`

	// SuppressOutput hides stdout from transcript.
	SuppressOutput bool `json:"suppressOutput,omitempty"`

	// StopReason message when Continue is false.
	StopReason string `json:"stopReason,omitempty"`

	// Decision is "approve" or "block".
	Decision string `json:"decision,omitempty"`

	// Reason explains the decision.
	Reason string `json:"reason,omitempty"`

	// SystemMessage is a warning shown to the user.
	SystemMessage string `json:"systemMessage,omitempty"`

	// Async indicates the hook wants to run asynchronously.
	Async bool `json:"async,omitempty"`

	// HookSpecificOutput contains event-specific fields.
	HookSpecificOutput *HookSpecificOutput `json:"hookSpecificOutput,omitempty"`
}

// HookSpecificOutput contains event-type-specific fields.
type HookSpecificOutput struct {
	HookEventName           string                 `json:"hookEventName,omitempty"`
	PermissionDecision      string                 `json:"permissionDecision,omitempty"`
	PermissionDecisionReason string                `json:"permissionDecisionReason,omitempty"`
	AdditionalContext       string                 `json:"additionalContext,omitempty"`
	UpdatedInput            map[string]interface{} `json:"updatedInput,omitempty"`
	InitialUserMessage      string                 `json:"initialUserMessage,omitempty"`
	WatchPaths              []string               `json:"watchPaths,omitempty"`
	Retry                   *bool                  `json:"retry,omitempty"`
}

// HookResult is the processed result of executing a hook.
type HookResult struct {
	Outcome string // "success", "blocking", "non_blocking_error", "cancelled"

	// BlockingError is set when the hook blocks the operation.
	BlockingError *HookBlockingError

	// PreventContinuation stops the query loop.
	PreventContinuation bool
	StopReason          string

	// PermissionBehavior overrides permission for PreToolUse hooks.
	PermissionBehavior string // "allow", "deny", "ask", "passthrough"
	PermissionReason   string

	// AdditionalContext to inject as a system reminder.
	AdditionalContext string

	// SystemMessage warning to show the user.
	SystemMessage string

	// UpdatedInput replaces tool input for PreToolUse hooks.
	UpdatedInput map[string]interface{}

	// Stdout/Stderr from the hook process.
	Stdout string
	Stderr string

	// ExitCode from the hook process.
	ExitCode int

	// DurationMs is the execution time.
	DurationMs int64

	// Command that was executed.
	CommandStr string
}

// HookBlockingError represents a hook that blocked the operation.
type HookBlockingError struct {
	BlockingError string `json:"blocking_error"`
	Command       string `json:"command"`
}

// AggregatedHookResult combines results from multiple hooks for the same event.
type AggregatedHookResult struct {
	BlockingErrors      []*HookBlockingError
	PreventContinuation bool
	StopReason          string
	PermissionBehavior  string
	PermissionReason    string
	AdditionalContexts  []string
	UpdatedInput        map[string]interface{}
	SystemMessage       string
}

// ---------------------------------------------------------------------------
// Shell Hook Registry
// ---------------------------------------------------------------------------

// ShellHookRegistry manages user-configured shell hooks.
type ShellHookRegistry struct {
	mu    sync.RWMutex
	hooks map[HookEvent][]HookCommand
}

// NewShellHookRegistry creates an empty shell hook registry.
func NewShellHookRegistry() *ShellHookRegistry {
	return &ShellHookRegistry{
		hooks: make(map[HookEvent][]HookCommand),
	}
}

// Register adds a hook command for an event.
func (r *ShellHookRegistry) Register(event HookEvent, cmd HookCommand) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.hooks[event] = append(r.hooks[event], cmd)
}

// GetHooks returns all hooks for an event.
func (r *ShellHookRegistry) GetHooks(event HookEvent) []HookCommand {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.hooks[event]
}

// GetMatchingHooks returns hooks for an event that match the given tool name.
// If a hook has no matcher, it matches all tools.
func (r *ShellHookRegistry) GetMatchingHooks(event HookEvent, toolName string) []HookCommand {
	r.mu.RLock()
	defer r.mu.RUnlock()

	all := r.hooks[event]
	var matched []HookCommand
	for _, h := range all {
		if h.Matcher == "" || matchToolName(h.Matcher, toolName) {
			matched = append(matched, h)
		}
	}
	return matched
}

// Clear removes all hooks.
func (r *ShellHookRegistry) Clear() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.hooks = make(map[HookEvent][]HookCommand)
}

// matchToolName checks if toolName matches a glob-like pattern.
// Supports simple prefix/suffix wildcards.
func matchToolName(pattern, toolName string) bool {
	if pattern == "*" || pattern == "" {
		return true
	}
	if strings.HasPrefix(pattern, "*") && strings.HasSuffix(pattern, "*") {
		return strings.Contains(toolName, pattern[1:len(pattern)-1])
	}
	if strings.HasPrefix(pattern, "*") {
		return strings.HasSuffix(toolName, pattern[1:])
	}
	if strings.HasSuffix(pattern, "*") {
		return strings.HasPrefix(toolName, pattern[:len(pattern)-1])
	}
	return pattern == toolName
}

// ---------------------------------------------------------------------------
// Hook execution
// ---------------------------------------------------------------------------

const defaultHookTimeout = 10 * time.Minute

// ExecShellHook executes a single shell hook command.
// It writes hookInput as JSON to stdin and reads stdout/stderr.
func ExecShellHook(
	ctx context.Context,
	hook HookCommand,
	input HookInput,
) (*HookResult, error) {
	startTime := time.Now()
	result := &HookResult{
		Outcome:    "success",
		CommandStr: hook.Command,
	}

	// Serialize input to JSON
	jsonInput, err := json.Marshal(input)
	if err != nil {
		return nil, fmt.Errorf("marshal hook input: %w", err)
	}

	// Determine timeout
	timeout := defaultHookTimeout
	if hook.Timeout > 0 {
		timeout = time.Duration(hook.Timeout) * time.Second
	}
	hookCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Build the command
	cmd := buildShellCommand(hookCtx, hook)
	cmd.Stdin = bytes.NewReader(append(jsonInput, '\n'))

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	// Execute
	runErr := cmd.Run()
	result.DurationMs = time.Since(startTime).Milliseconds()
	result.Stdout = stdout.String()
	result.Stderr = stderr.String()

	if runErr != nil {
		if exitErr, ok := runErr.(*exec.ExitError); ok {
			result.ExitCode = exitErr.ExitCode()
		} else {
			return nil, fmt.Errorf("exec hook %q: %w", hook.Command, runErr)
		}
	}

	// Parse JSON output if stdout starts with '{'
	trimmedOut := strings.TrimSpace(result.Stdout)
	if strings.HasPrefix(trimmedOut, "{") {
		var hookOutput HookOutput
		if jsonErr := json.Unmarshal([]byte(trimmedOut), &hookOutput); jsonErr == nil {
			processHookOutput(&hookOutput, result, hook.Command)
		}
	}

	// Exit code 2 means blocking error (TS convention)
	if result.ExitCode == 2 && result.BlockingError == nil {
		result.Outcome = "blocking"
		errMsg := result.Stderr
		if errMsg == "" {
			errMsg = result.Stdout
		}
		result.BlockingError = &HookBlockingError{
			BlockingError: errMsg,
			Command:       hook.Command,
		}
	} else if result.ExitCode != 0 && result.BlockingError == nil {
		result.Outcome = "non_blocking_error"
	}

	return result, nil
}

// buildShellCommand creates an exec.Cmd for the hook.
func buildShellCommand(ctx context.Context, hook HookCommand) *exec.Cmd {
	shell := hook.Shell
	if shell == "" {
		shell = "bash"
	}

	if shell == "powershell" {
		// Try pwsh first, fall back to powershell
		pwsh := "pwsh"
		if runtime.GOOS == "windows" {
			if _, err := exec.LookPath("pwsh"); err != nil {
				pwsh = "powershell"
			}
		}
		return exec.CommandContext(ctx, pwsh, "-NoProfile", "-NonInteractive", "-Command", hook.Command)
	}

	// Bash path
	if runtime.GOOS == "windows" {
		// On Windows, try Git Bash
		gitBash := findGitBash()
		if gitBash != "" {
			return exec.CommandContext(ctx, gitBash, "-c", hook.Command)
		}
	}

	return exec.CommandContext(ctx, "bash", "-c", hook.Command)
}

// findGitBash attempts to locate Git Bash on Windows.
func findGitBash() string {
	paths := []string{
		`C:\Program Files\Git\bin\bash.exe`,
		`C:\Program Files (x86)\Git\bin\bash.exe`,
	}
	for _, p := range paths {
		if _, err := exec.LookPath(p); err == nil {
			return p
		}
	}
	return ""
}

// processHookOutput maps parsed JSON output onto HookResult.
func processHookOutput(output *HookOutput, result *HookResult, command string) {
	if output.Continue != nil && !*output.Continue {
		result.PreventContinuation = true
		result.StopReason = output.StopReason
	}

	if output.SystemMessage != "" {
		result.SystemMessage = output.SystemMessage
	}

	switch output.Decision {
	case "approve":
		result.PermissionBehavior = "allow"
	case "block":
		result.PermissionBehavior = "deny"
		result.BlockingError = &HookBlockingError{
			BlockingError: output.Reason,
			Command:       command,
		}
		result.Outcome = "blocking"
	}

	if output.HookSpecificOutput != nil {
		hso := output.HookSpecificOutput

		if hso.AdditionalContext != "" {
			result.AdditionalContext = hso.AdditionalContext
		}

		if hso.UpdatedInput != nil {
			result.UpdatedInput = hso.UpdatedInput
		}

		// PreToolUse-specific permission decision
		if hso.PermissionDecision != "" {
			switch hso.PermissionDecision {
			case "allow":
				result.PermissionBehavior = "allow"
			case "deny":
				result.PermissionBehavior = "deny"
				reason := hso.PermissionDecisionReason
				if reason == "" {
					reason = output.Reason
				}
				if reason == "" {
					reason = "Blocked by hook"
				}
				result.BlockingError = &HookBlockingError{
					BlockingError: reason,
					Command:       command,
				}
				result.Outcome = "blocking"
			case "ask":
				result.PermissionBehavior = "ask"
			}
			result.PermissionReason = hso.PermissionDecisionReason
		}
	}
}

// ---------------------------------------------------------------------------
// Batch hook execution
// ---------------------------------------------------------------------------

// RunHooksForEvent executes all matching hooks for an event and aggregates results.
func RunHooksForEvent(
	ctx context.Context,
	registry *ShellHookRegistry,
	event HookEvent,
	input HookInput,
	toolName string,
) (*AggregatedHookResult, error) {
	var hooks []HookCommand
	if toolName != "" {
		hooks = registry.GetMatchingHooks(event, toolName)
	} else {
		hooks = registry.GetHooks(event)
	}

	if len(hooks) == 0 {
		return &AggregatedHookResult{}, nil
	}

	agg := &AggregatedHookResult{}

	for _, hook := range hooks {
		if ctx.Err() != nil {
			break
		}

		result, err := ExecShellHook(ctx, hook, input)
		if err != nil {
			// Non-fatal: log and continue
			continue
		}

		// Aggregate
		if result.BlockingError != nil {
			agg.BlockingErrors = append(agg.BlockingErrors, result.BlockingError)
		}
		if result.PreventContinuation {
			agg.PreventContinuation = true
			if agg.StopReason == "" {
				agg.StopReason = result.StopReason
			}
		}
		if result.PermissionBehavior != "" {
			agg.PermissionBehavior = result.PermissionBehavior
			agg.PermissionReason = result.PermissionReason
		}
		if result.AdditionalContext != "" {
			agg.AdditionalContexts = append(agg.AdditionalContexts, result.AdditionalContext)
		}
		if result.UpdatedInput != nil {
			agg.UpdatedInput = result.UpdatedInput
		}
		if result.SystemMessage != "" {
			agg.SystemMessage = result.SystemMessage
		}

		// If a hook blocks, stop running further hooks
		if result.Outcome == "blocking" {
			break
		}
	}

	return agg, nil
}
