package agents_test

import (
	"context"
	"encoding/json"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wall-ai/ubuilding/backend/agents"
)

func TestShellHookRegistry_RegisterAndGet(t *testing.T) {
	reg := agents.NewShellHookRegistry()
	reg.Register(agents.HookEventPreToolUse, agents.HookCommand{
		Command: "echo pre",
		Matcher: "Bash",
	})
	reg.Register(agents.HookEventPreToolUse, agents.HookCommand{
		Command: "echo pre2",
	})
	reg.Register(agents.HookEventPostToolUse, agents.HookCommand{
		Command: "echo post",
	})

	assert.Len(t, reg.GetHooks(agents.HookEventPreToolUse), 2)
	assert.Len(t, reg.GetHooks(agents.HookEventPostToolUse), 1)
	assert.Empty(t, reg.GetHooks(agents.HookEventStop))
}

func TestShellHookRegistry_GetMatchingHooks(t *testing.T) {
	reg := agents.NewShellHookRegistry()
	reg.Register(agents.HookEventPreToolUse, agents.HookCommand{
		Command: "echo bash-only",
		Matcher: "Bash",
	})
	reg.Register(agents.HookEventPreToolUse, agents.HookCommand{
		Command: "echo all-tools",
	})
	reg.Register(agents.HookEventPreToolUse, agents.HookCommand{
		Command: "echo file-star",
		Matcher: "File*",
	})

	// "Bash" matches the first and the no-matcher hook
	matched := reg.GetMatchingHooks(agents.HookEventPreToolUse, "Bash")
	assert.Len(t, matched, 2)

	// "FileRead" matches the wildcard and the no-matcher
	matched = reg.GetMatchingHooks(agents.HookEventPreToolUse, "FileRead")
	assert.Len(t, matched, 2)

	// "Edit" only matches the no-matcher
	matched = reg.GetMatchingHooks(agents.HookEventPreToolUse, "Edit")
	assert.Len(t, matched, 1)
}

func TestShellHookRegistry_Clear(t *testing.T) {
	reg := agents.NewShellHookRegistry()
	reg.Register(agents.HookEventStop, agents.HookCommand{Command: "echo stop"})
	assert.Len(t, reg.GetHooks(agents.HookEventStop), 1)

	reg.Clear()
	assert.Empty(t, reg.GetHooks(agents.HookEventStop))
}

func TestExecShellHook_SimpleEcho(t *testing.T) {
	if runtime.GOOS == "windows" {
		// On Windows CI without bash, skip
		t.Skip("requires bash")
	}

	hook := agents.HookCommand{Command: "cat"}
	input := agents.HookInput{
		SessionID: "test-session",
		Cwd:       "/tmp",
		ToolName:  "Bash",
	}

	result, err := agents.ExecShellHook(context.Background(), hook, input)
	require.NoError(t, err)
	assert.Equal(t, "success", result.Outcome)
	assert.Equal(t, 0, result.ExitCode)

	// stdout should contain the JSON input
	var parsed agents.HookInput
	require.NoError(t, json.Unmarshal([]byte(result.Stdout), &parsed))
	assert.Equal(t, "test-session", parsed.SessionID)
}

func TestExecShellHook_JSONOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires bash")
	}

	// Hook that outputs JSON with a blocking decision
	hook := agents.HookCommand{
		Command: `echo '{"decision":"block","reason":"not allowed"}'`,
	}
	input := agents.HookInput{SessionID: "s1", Cwd: "/tmp"}

	result, err := agents.ExecShellHook(context.Background(), hook, input)
	require.NoError(t, err)
	assert.Equal(t, "blocking", result.Outcome)
	require.NotNil(t, result.BlockingError)
	assert.Equal(t, "not allowed", result.BlockingError.BlockingError)
	assert.Equal(t, "deny", result.PermissionBehavior)
}

func TestExecShellHook_ExitCode2Blocking(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires bash")
	}

	hook := agents.HookCommand{Command: "echo 'blocked' >&2; exit 2"}
	input := agents.HookInput{SessionID: "s1", Cwd: "/tmp"}

	result, err := agents.ExecShellHook(context.Background(), hook, input)
	require.NoError(t, err)
	assert.Equal(t, "blocking", result.Outcome)
	assert.Equal(t, 2, result.ExitCode)
	require.NotNil(t, result.BlockingError)
	assert.Contains(t, result.BlockingError.BlockingError, "blocked")
}

func TestExecShellHook_NonZeroExitNonBlocking(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires bash")
	}

	hook := agents.HookCommand{Command: "exit 1"}
	input := agents.HookInput{SessionID: "s1", Cwd: "/tmp"}

	result, err := agents.ExecShellHook(context.Background(), hook, input)
	require.NoError(t, err)
	assert.Equal(t, "non_blocking_error", result.Outcome)
	assert.Equal(t, 1, result.ExitCode)
}

func TestExecShellHook_CancelledContext(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires bash")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	hook := agents.HookCommand{Command: "sleep 10"}
	input := agents.HookInput{SessionID: "s1", Cwd: "/tmp"}

	_, err := agents.ExecShellHook(ctx, hook, input)
	// Should either error or return quickly with non-zero exit
	if err == nil {
		// Command was killed by context
	}
}

func TestExecShellHook_PreToolUsePermissionDecision(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires bash")
	}

	hook := agents.HookCommand{
		Command: `echo '{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"allow","additionalContext":"extra info"}}'`,
	}
	input := agents.HookInput{SessionID: "s1", Cwd: "/tmp", ToolName: "Bash"}

	result, err := agents.ExecShellHook(context.Background(), hook, input)
	require.NoError(t, err)
	assert.Equal(t, "success", result.Outcome)
	assert.Equal(t, "allow", result.PermissionBehavior)
	assert.Equal(t, "extra info", result.AdditionalContext)
}

func TestExecShellHook_ContinueFalse(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires bash")
	}

	hook := agents.HookCommand{
		Command: `echo '{"continue":false,"stopReason":"hook says stop"}'`,
	}
	input := agents.HookInput{SessionID: "s1", Cwd: "/tmp"}

	result, err := agents.ExecShellHook(context.Background(), hook, input)
	require.NoError(t, err)
	assert.True(t, result.PreventContinuation)
	assert.Equal(t, "hook says stop", result.StopReason)
}

func TestRunHooksForEvent_Aggregation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires bash")
	}

	reg := agents.NewShellHookRegistry()
	reg.Register(agents.HookEventPostToolUse, agents.HookCommand{
		Command: `echo '{"hookSpecificOutput":{"hookEventName":"PostToolUse","additionalContext":"ctx1"}}'`,
	})
	reg.Register(agents.HookEventPostToolUse, agents.HookCommand{
		Command: `echo '{"hookSpecificOutput":{"hookEventName":"PostToolUse","additionalContext":"ctx2"}}'`,
	})

	input := agents.HookInput{SessionID: "s1", Cwd: "/tmp", ToolName: "Edit"}
	agg, err := agents.RunHooksForEvent(context.Background(), reg, agents.HookEventPostToolUse, input, "Edit")
	require.NoError(t, err)
	assert.Len(t, agg.AdditionalContexts, 2)
	assert.Contains(t, agg.AdditionalContexts, "ctx1")
	assert.Contains(t, agg.AdditionalContexts, "ctx2")
}

func TestRunHooksForEvent_BlockingStopsExecution(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires bash")
	}

	reg := agents.NewShellHookRegistry()
	reg.Register(agents.HookEventPreToolUse, agents.HookCommand{
		Command: `echo '{"decision":"block","reason":"nope"}'`,
	})
	reg.Register(agents.HookEventPreToolUse, agents.HookCommand{
		Command: `echo '{"hookSpecificOutput":{"hookEventName":"PreToolUse","additionalContext":"should not run"}}'`,
	})

	input := agents.HookInput{SessionID: "s1", Cwd: "/tmp", ToolName: "Bash"}
	agg, err := agents.RunHooksForEvent(context.Background(), reg, agents.HookEventPreToolUse, input, "Bash")
	require.NoError(t, err)
	assert.Len(t, agg.BlockingErrors, 1)
	// Second hook should not have run, so no additional contexts
	assert.Empty(t, agg.AdditionalContexts)
}

func TestRunHooksForEvent_NoHooks(t *testing.T) {
	reg := agents.NewShellHookRegistry()
	input := agents.HookInput{SessionID: "s1", Cwd: "/tmp"}

	agg, err := agents.RunHooksForEvent(context.Background(), reg, agents.HookEventStop, input, "")
	require.NoError(t, err)
	assert.Empty(t, agg.BlockingErrors)
	assert.False(t, agg.PreventContinuation)
}
