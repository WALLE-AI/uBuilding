package permission

import (
	"reflect"
	"testing"

	"github.com/wall-ai/ubuilding/backend/agents"
)

func TestTieredAllowRules_AllAndLookup(t *testing.T) {
	r := TieredAllowRules{
		CliArg:  []string{"Bash(git *)"},
		Session: []string{"Read", "Bash(npm test)"},
		Command: []string{"Edit"},
	}
	want := []string{"Bash(git *)", "Read", "Bash(npm test)", "Edit"}
	if !reflect.DeepEqual(r.All(), want) {
		t.Fatalf("All() = %v", r.All())
	}
	bash := r.LookupTool("Bash")
	if len(bash) != 2 {
		t.Fatalf("Bash lookup = %v", bash)
	}
	if bash[0].Pattern != "git *" || bash[1].Pattern != "npm test" {
		t.Errorf("lookup patterns: %+v", bash)
	}
	if r.IsEmpty() {
		t.Error("IsEmpty should be false")
	}
	empty := TieredAllowRules{}
	if !empty.IsEmpty() || len(empty.LookupTool("anything")) != 0 {
		t.Error("empty rules should match nothing")
	}
}

func TestTieredAllowRules_ReplaceSession(t *testing.T) {
	orig := TieredAllowRules{
		CliArg:  []string{"Bash"},
		Session: []string{"oldA", "oldB"},
		Command: []string{"Edit"},
	}
	next := orig.ReplaceSession([]string{"newA"})
	if !reflect.DeepEqual(orig.Session, []string{"oldA", "oldB"}) {
		t.Fatal("original Session mutated")
	}
	if !reflect.DeepEqual(next.Session, []string{"newA"}) {
		t.Fatal("ReplaceSession did not set new session rules")
	}
	if !reflect.DeepEqual(next.CliArg, orig.CliArg) || !reflect.DeepEqual(next.Command, orig.Command) {
		t.Fatal("CliArg/Command tiers must survive replacement")
	}
}

func TestFilterDeniedAgents(t *testing.T) {
	ctx := &agents.ToolPermissionContext{
		AlwaysDenyRules: map[string][]agents.PermissionRule{
			"Task": {{Tool: "Task", Pattern: "hacker, reviewer"}},
		},
	}
	got := FilterDeniedAgents(ctx, "Task", []string{"general-purpose", "hacker", "reviewer", "explore"})
	want := []string{"general-purpose", "explore"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("FilterDeniedAgents = %v; want %v", got, want)
	}

	match := GetDenyRuleForAgent(ctx, "Task", "reviewer")
	if match == nil || match.AgentType != "reviewer" {
		t.Fatalf("GetDenyRuleForAgent = %+v", match)
	}

	match = GetDenyRuleForAgent(ctx, "Task", "allowed")
	if match != nil {
		t.Fatalf("allowed agent should not match: %+v", match)
	}
}

func TestFilterDeniedAgentsByType(t *testing.T) {
	ctx := &agents.ToolPermissionContext{
		AlwaysDenyRules: map[string][]agents.PermissionRule{
			"Task": {{Tool: "Task", Pattern: "blocked"}},
		},
	}
	defs := []*agents.AgentDefinition{
		{AgentType: "allowed"},
		{AgentType: "blocked"},
		nil,
	}
	got := FilterDeniedAgentsByType(ctx, "Task", defs)
	if len(got) != 1 || got[0].AgentType != "allowed" {
		t.Fatalf("FilterDeniedAgentsByType = %+v", got)
	}
}

func TestGetDenyRuleForAgent_BlanketBlocksAll(t *testing.T) {
	ctx := &agents.ToolPermissionContext{
		AlwaysDenyRules: map[string][]agents.PermissionRule{
			"Task": {{Tool: "Task", Pattern: ""}},
		},
	}
	if GetDenyRuleForAgent(ctx, "Task", "anything") == nil {
		t.Fatal("blanket deny should match everything")
	}
}

func TestApplySessionAllowToContext_AppendsRules(t *testing.T) {
	ctx := &agents.ToolPermissionContext{
		AlwaysAllowRules: map[string][]agents.PermissionRule{
			"Bash": {{Tool: "Bash", Pattern: "git *"}},
		},
	}
	after := ApplySessionAllowToContext(ctx, []string{"Read", "Bash(npm test)"})
	if len(after["Bash"]) != 2 {
		t.Fatalf("Bash = %+v", after["Bash"])
	}
	if len(after["Read"]) != 1 {
		t.Fatalf("Read = %+v", after["Read"])
	}
	// Original untouched.
	if len(ctx.AlwaysAllowRules["Bash"]) != 1 {
		t.Fatal("input context mutated")
	}
}

func TestAdditionalWorkingDirectoriesFromContext(t *testing.T) {
	ctx := &agents.ToolPermissionContext{
		AdditionalWorkingDirectories: map[string]string{"/b": "", "/a": "", "/c": ""},
	}
	got := AdditionalWorkingDirectoriesFromContext(ctx)
	if !reflect.DeepEqual(got, []string{"/a", "/b", "/c"}) {
		t.Fatalf("dirs = %v", got)
	}
	if AdditionalWorkingDirectoriesFromContext(nil) != nil {
		t.Fatal("nil ctx → nil slice")
	}
}

func TestFormatDenyMatch(t *testing.T) {
	m := &AgentDenyMatch{
		AgentType: "reviewer",
		Rule:      RuleValue{Tool: "Task", Pattern: "reviewer", HasArgs: true},
		Source:    "projectSettings",
	}
	got := FormatDenyMatch(m)
	want := "Agent 'reviewer' has been denied by permission rule 'Task(reviewer)' from projectSettings."
	if got != want {
		t.Fatalf("FormatDenyMatch = %q", got)
	}
	if FormatDenyMatch(nil) != "" {
		t.Fatal("nil match should render empty string")
	}
}
