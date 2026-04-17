package tool

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/wall-ai/ubuilding/backend/agents"
)

func newMinimalDef(name string) ToolDef {
	return ToolDef{
		Name:        name,
		InputSchema: func() *JSONSchema { return &JSONSchema{Type: "object"} },
		Description: func(_ json.RawMessage) string { return name },
		Call: func(_ context.Context, _ json.RawMessage, _ *agents.ToolUseContext) (*ToolResult, error) {
			return &ToolResult{Data: "ok"}, nil
		},
		MapToolResultToParam: func(_ interface{}, toolUseID string) *agents.ContentBlock {
			return &agents.ContentBlock{Type: agents.ContentBlockToolResult, ToolUseID: toolUseID}
		},
	}
}

func TestBuildTool_DefaultsAreFailClosed(t *testing.T) {
	tl := BuildTool(newMinimalDef("X"))
	if tl.IsReadOnly(nil) {
		t.Error("default IsReadOnly should be false (fail-closed)")
	}
	if tl.IsConcurrencySafe(nil) {
		t.Error("default IsConcurrencySafe should be false (fail-closed)")
	}
	if tl.IsDestructive(nil) {
		t.Error("default IsDestructive should be false")
	}
	if !tl.IsEnabled() {
		t.Error("default IsEnabled should be true")
	}
	if tl.MaxResultSizeChars() != 100_000 {
		t.Errorf("default MaxResultSizeChars = %d, want 100000", tl.MaxResultSizeChars())
	}
	v := tl.ValidateInput(nil, nil)
	if v == nil || !v.Valid {
		t.Error("default ValidateInput should return Valid=true")
	}
	p, err := tl.CheckPermissions(json.RawMessage(`{}`), nil)
	if err != nil {
		t.Fatalf("default CheckPermissions returned error: %v", err)
	}
	if p.Behavior != PermissionAllow {
		t.Errorf("default CheckPermissions behavior = %q, want allow", p.Behavior)
	}
}

func TestBuildTool_Overrides(t *testing.T) {
	def := newMinimalDef("Y")
	def.IsReadOnly = func(_ json.RawMessage) bool { return true }
	def.IsConcurrencySafe = func(_ json.RawMessage) bool { return true }
	def.Aliases = []string{"y-alias"}
	tl := BuildTool(def)
	if !tl.IsReadOnly(nil) {
		t.Error("override IsReadOnly should win")
	}
	if !tl.IsConcurrencySafe(nil) {
		t.Error("override IsConcurrencySafe should win")
	}
	if got := tl.Aliases(); len(got) != 1 || got[0] != "y-alias" {
		t.Errorf("aliases = %v", got)
	}
}

func TestBuildTool_RequiredFieldsPanic(t *testing.T) {
	cases := []struct {
		name  string
		mutate func(*ToolDef)
	}{
		{"no Name", func(d *ToolDef) { d.Name = "" }},
		{"no Call", func(d *ToolDef) { d.Call = nil }},
		{"no InputSchema", func(d *ToolDef) { d.InputSchema = nil }},
		{"no Description", func(d *ToolDef) { d.Description = nil }},
		{"no Map", func(d *ToolDef) { d.MapToolResultToParam = nil }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r == nil {
					t.Error("expected panic")
				}
			}()
			d := newMinimalDef("Z")
			c.mutate(&d)
			BuildTool(d)
		})
	}
}
