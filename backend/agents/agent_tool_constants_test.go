package agents

import "testing"

func TestAgentToolConstants_KeyMembers(t *testing.T) {
	mustContain := func(name string, set map[string]struct{}, label string) {
		t.Helper()
		if _, ok := set[name]; !ok {
			t.Errorf("%s is missing %q", label, name)
		}
	}
	mustNotContain := func(name string, set map[string]struct{}, label string) {
		t.Helper()
		if _, ok := set[name]; ok {
			t.Errorf("%s unexpectedly contains %q", label, name)
		}
	}

	// Disallowed baseline.
	mustContain("EnterPlanMode", AllAgentDisallowedTools, "AllAgentDisallowedTools")
	mustContain("ExitPlanMode", AllAgentDisallowedTools, "AllAgentDisallowedTools")
	mustContain("AskUserQuestion", AllAgentDisallowedTools, "AllAgentDisallowedTools")
	mustContain("Task", AllAgentDisallowedTools, "AllAgentDisallowedTools")

	// Custom set inherits from baseline.
	for k := range AllAgentDisallowedTools {
		mustContain(k, CustomAgentDisallowedTools, "CustomAgentDisallowedTools")
	}

	// Async allowed.
	mustContain("Read", AsyncAgentAllowedTools, "AsyncAgentAllowedTools")
	mustContain("Bash", AsyncAgentAllowedTools, "AsyncAgentAllowedTools")
	mustContain("Edit", AsyncAgentAllowedTools, "AsyncAgentAllowedTools")
	mustNotContain("Task", AsyncAgentAllowedTools, "AsyncAgentAllowedTools")
	mustNotContain("AskUserQuestion", AsyncAgentAllowedTools, "AsyncAgentAllowedTools")

	// Teammate carve-out.
	mustContain("SendMessage", InProcessTeammateAllowedTools, "InProcessTeammateAllowedTools")

	// Coordinator allow-list.
	mustContain("Task", CoordinatorModeAllowedTools, "CoordinatorModeAllowedTools")
	mustContain("SendMessage", CoordinatorModeAllowedTools, "CoordinatorModeAllowedTools")
}

func TestIsOneShotAgent(t *testing.T) {
	if !IsOneShotAgent("statusline-setup") {
		t.Fatal("statusline-setup must be one-shot")
	}
	if IsOneShotAgent("general-purpose") {
		t.Fatal("general-purpose must not be one-shot")
	}
}

func TestUnionNameSets(t *testing.T) {
	a := makeNameSet([]string{"x", "y"})
	b := makeNameSet([]string{"y", "z"})
	u := unionNameSets(a, b)
	for _, n := range []string{"x", "y", "z"} {
		if _, ok := u[n]; !ok {
			t.Errorf("union missing %q", n)
		}
	}
	if len(u) != 3 {
		t.Fatalf("union len = %d", len(u))
	}
	// Nil safety.
	if got := unionNameSets(nil, nil); len(got) != 0 {
		t.Fatalf("union of nils must be empty, got %v", got)
	}
}
