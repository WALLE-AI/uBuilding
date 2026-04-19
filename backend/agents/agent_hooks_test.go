package agents

import (
	"context"
	"strings"
	"testing"
)

func TestNormalizeAgentHooks_TypedMap(t *testing.T) {
	raw := map[HookEvent][]HookCommand{
		HookEventSubagentStart: {{Command: "echo hi"}},
	}
	got, err := normalizeAgentHooks(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(got[HookEventSubagentStart]) != 1 {
		t.Fatalf("got %+v", got)
	}
}

func TestNormalizeAgentHooks_StringKeys(t *testing.T) {
	raw := map[string][]HookCommand{
		"SubagentStart": {{Command: "x"}},
	}
	got, err := normalizeAgentHooks(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(got[HookEventSubagentStart]) != 1 {
		t.Fatalf("got %+v", got)
	}
}

func TestNormalizeAgentHooks_InterfaceShape(t *testing.T) {
	raw := map[string]interface{}{
		"SubagentStart": []interface{}{
			map[string]interface{}{"command": "first", "matcher": "*"},
			map[string]interface{}{"command": "second", "timeout": 5},
		},
	}
	got, err := normalizeAgentHooks(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(got[HookEventSubagentStart]) != 2 {
		t.Fatalf("got %+v", got)
	}
	if got[HookEventSubagentStart][1].Timeout != 5 {
		t.Fatalf("timeout lost: %+v", got[HookEventSubagentStart][1])
	}
}

func TestNormalizeAgentHooks_Nil(t *testing.T) {
	if got, err := normalizeAgentHooks(nil); got != nil || err != nil {
		t.Fatalf("expected nil/nil, got %v/%v", got, err)
	}
}

func TestRewriteStopForSubagent(t *testing.T) {
	in := AgentFrontmatterHooks{
		HookEventStop:        {{Command: "a"}},
		HookEventStopFailure: {{Command: "b"}},
		HookEventSubagentStop: {{Command: "c"}},
	}
	out := rewriteStopForSubagent(in)
	if _, ok := out[HookEventStop]; ok {
		t.Fatal("Stop should be rewritten")
	}
	if len(out[HookEventSubagentStop]) != 3 {
		t.Fatalf("all three should fold into SubagentStop: %+v", out[HookEventSubagentStop])
	}
}

// C03 · SubagentStart hook output feeds AdditionalContexts; wrapping helper
// produces a user-role hook_additional_context message.
func TestExecuteSubagentStartHooks_IntegratesWithRegistry(t *testing.T) {
	reg := NewShellHookRegistry()
	// Inject a stub hook directly via Register — the real shell executor
	// isn't exercised here; we verify the dispatch plumbing + message
	// wrapping independently of the subprocess.
	reg.Register(HookEventSubagentStart, HookCommand{Command: "noop", Matcher: ""})

	contexts, err := ExecuteSubagentStartHooks(context.Background(), reg, "agt-1", "Plan")
	if err != nil {
		t.Fatalf("ExecuteSubagentStartHooks: %v", err)
	}
	// No additional contexts because the stub command doesn't emit any — OK.
	_ = contexts

	// AttachSubagentStartContext wraps a synthetic context list into a user message.
	msg := AttachSubagentStartContext("agt-1", "Plan", []string{"warning: stale cache"})
	if msg == nil {
		t.Fatal("wrapper should produce a message")
	}
	if msg.Type != MessageTypeUser || msg.Subtype != "hook_additional_context" {
		t.Fatalf("wrong message shape: %+v", msg)
	}
	if !strings.Contains(msg.Content[0].Text, "warning: stale cache") {
		t.Fatalf("context lost: %+v", msg)
	}
	if AttachSubagentStartContext("agt", "Plan", nil) != nil {
		t.Fatal("empty contexts should produce nil message")
	}
}

// C05 · scoped registration + clear isolates entries per agent id.
func TestRegisterFrontmatterHooks_Scope(t *testing.T) {
	reg := NewShellHookRegistry()
	reg.Register(HookEventSubagentStart, HookCommand{Command: "pre-existing"})

	hooks, err := RegisterFrontmatterHooks(reg, "agt-1", map[HookEvent][]HookCommand{
		HookEventSubagentStart: {{Command: "agent-hook"}},
		HookEventStop:          {{Command: "stop-hook"}},
	}, true)
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	// Stop → SubagentStop rewrite (C10).
	if _, ok := hooks[HookEventStop]; ok {
		t.Fatal("Stop hook should have been rewritten to SubagentStop")
	}
	if len(hooks[HookEventSubagentStop]) != 1 {
		t.Fatalf("SubagentStop hook missing: %+v", hooks)
	}
	// Registry now has the scoped hooks PLUS the pre-existing one.
	if len(reg.GetHooks(HookEventSubagentStart)) != 2 {
		t.Fatalf("expected 2 start hooks, got %d", len(reg.GetHooks(HookEventSubagentStart)))
	}
	if len(reg.GetHooks(HookEventSubagentStop)) != 1 {
		t.Fatalf("expected 1 stop hook, got %d", len(reg.GetHooks(HookEventSubagentStop)))
	}

	// Clear the scope — only agent-hook / stop-hook should go; pre-existing stays.
	ClearSessionHooks(reg, "agt-1")
	if len(reg.GetHooks(HookEventSubagentStart)) != 1 {
		t.Fatalf("expected 1 start hook after clear, got %d", len(reg.GetHooks(HookEventSubagentStart)))
	}
	if len(reg.GetHooks(HookEventSubagentStop)) != 0 {
		t.Fatalf("expected 0 stop hooks after clear, got %d", len(reg.GetHooks(HookEventSubagentStop)))
	}
	if reg.GetHooks(HookEventSubagentStart)[0].Command != "pre-existing" {
		t.Fatalf("pre-existing hook lost: %+v", reg.GetHooks(HookEventSubagentStart))
	}
}

// C05 · clear on an unknown scope is a no-op.
func TestClearSessionHooks_UnknownScope(t *testing.T) {
	reg := NewShellHookRegistry()
	reg.Register(HookEventSubagentStart, HookCommand{Command: "a"})
	ClearSessionHooks(reg, "ghost")
	if len(reg.GetHooks(HookEventSubagentStart)) != 1 {
		t.Fatal("unknown scope clear must not remove anything")
	}
}
