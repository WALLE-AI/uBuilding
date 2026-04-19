package permission

import (
	"encoding/json"
	"testing"

	"github.com/wall-ai/ubuilding/backend/agents"
)

func newCtx(mode Mode) *agents.ToolPermissionContext {
	return &agents.ToolPermissionContext{
		Mode:             string(mode),
		AlwaysAllowRules: map[string][]agents.PermissionRule{},
		AlwaysDenyRules:  map[string][]agents.PermissionRule{},
		AlwaysAskRules:   map[string][]agents.PermissionRule{},
	}
}

func TestCheck_ModeAliases(t *testing.T) {
	// legacy auto_accept → acceptEdits → allow-all
	c := NewChecker(newCtx(ModeAutoAccept), "/")
	if r := c.Check("Edit", json.RawMessage(`{}`), nil); !r.Allowed() {
		t.Errorf("auto_accept should allow Edit: %+v", r)
	}
	// bypass_all → bypassPermissions → allow-all
	c = NewChecker(newCtx(ModeBypassAll), "/")
	if r := c.Check("Bash", json.RawMessage(`{}`), nil); !r.Allowed() {
		t.Errorf("bypass_all should allow Bash: %+v", r)
	}
}

func TestCheck_PlanModeBlocksWrites(t *testing.T) {
	c := NewChecker(newCtx(ModePlan), "/")
	r := c.Check("Write", json.RawMessage(`{"file_path":"/tmp/x"}`), nil)
	if !r.Denied() {
		t.Fatalf("plan mode should deny Write: %+v", r)
	}
	// Plan mode still allows read-only tools through default-allow.
	r = c.Check("Read", json.RawMessage(`{"file_path":"/tmp/x"}`), nil)
	if r.Denied() {
		t.Fatalf("plan mode must not block Read: %+v", r)
	}
}

func TestCheck_PlanModeHonoursExplicitAllow(t *testing.T) {
	ctx := newCtx(ModePlan)
	ctx.AlwaysAllowRules["Bash"] = []agents.PermissionRule{{Tool: "Bash"}}
	c := NewChecker(ctx, "/")
	r := c.Check("Bash", json.RawMessage(`{"command":"ls"}`), nil)
	if !r.Allowed() {
		t.Fatalf("explicit allow should override plan deny: %+v", r)
	}
}

func TestCheck_DefaultModeFallbackAllow(t *testing.T) {
	c := NewChecker(newCtx(ModeDefault), "/")
	r := c.Check("UnknownTool", json.RawMessage(`{}`), nil)
	if !r.Allowed() {
		t.Fatalf("default mode must fall back to allow: %+v", r)
	}
}

// B04 · per-invocation mode override narrows the parent's mode.
func TestCheck_AgentPermissionModeOverlayBlocksWrites(t *testing.T) {
	c := NewChecker(newCtx(ModeDefault), "/")
	toolCtx := &agents.ToolUseContext{
		Options: agents.ToolUseOptions{AgentPermissionMode: string(ModePlan)},
	}
	r := c.Check("Write", json.RawMessage(`{}`), toolCtx)
	if !r.Denied() {
		t.Fatalf("overlay should deny Write under plan mode: %+v", r)
	}
}

// B04 · per-invocation override can widen too (e.g. plan parent, bypass
// child for a trusted agent).
func TestCheck_AgentPermissionModeOverlayAllowsBypass(t *testing.T) {
	c := NewChecker(newCtx(ModePlan), "/")
	toolCtx := &agents.ToolUseContext{
		Options: agents.ToolUseOptions{AgentPermissionMode: string(ModeBypassPermissions)},
	}
	r := c.Check("Write", json.RawMessage(`{}`), toolCtx)
	if !r.Allowed() {
		t.Fatalf("overlay should widen to bypass: %+v", r)
	}
}

func TestNormalizeMode(t *testing.T) {
	cases := []struct {
		in, want Mode
	}{
		{ModeAutoAccept, ModeAcceptEdits},
		{ModeBypassAll, ModeBypassPermissions},
		{ModePlan, ModePlan},
		{ModeDefault, ModeDefault},
		{Mode("unknown"), Mode("unknown")},
	}
	for _, tc := range cases {
		if got := NormalizeMode(tc.in); got != tc.want {
			t.Errorf("NormalizeMode(%q) = %q; want %q", tc.in, got, tc.want)
		}
	}
	if !ModeAcceptEdits.IsLaxerThanDefault() || !ModeBypassPermissions.IsLaxerThanDefault() {
		t.Error("acceptEdits/bypassPermissions must be laxer than default")
	}
	if ModeDefault.IsLaxerThanDefault() || ModePlan.IsLaxerThanDefault() {
		t.Error("default/plan must not be laxer than default")
	}
}
