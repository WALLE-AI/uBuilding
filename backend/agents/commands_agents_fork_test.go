package agents

import (
	"strings"
	"testing"
)

// --- /agents list -----------------------------------------------------------

func TestAgentsCommand_ListSortedAndTabular(t *testing.T) {
	reg := NewCommandRegistry()
	defs := &AgentDefinitions{
		ActiveAgents: []*AgentDefinition{
			{AgentType: "reviewer", Source: AgentSourceProject, WhenToUse: "Review PRs"},
			{AgentType: "Explore", Source: AgentSourceBuiltIn, WhenToUse: "Read-only scan"},
			{AgentType: "Plan", Source: AgentSourceBuiltIn, WhenToUse: "Plan an approach"},
		},
	}
	RegisterAgentAndForkCommands(reg, func() *AgentDefinitions { return defs })

	cmd := reg.Find("agents")
	if cmd == nil {
		t.Fatal("/agents not registered")
	}
	res, err := cmd.Call("", CommandContext{})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if res.Type != "text" {
		t.Fatalf("type = %s", res.Type)
	}
	// Header present + rows in alphabetical order.
	for _, want := range []string{"Type", "Source", "When to use", "Explore", "Plan", "reviewer"} {
		if !strings.Contains(res.Value, want) {
			t.Errorf("missing %q in:\n%s", want, res.Value)
		}
	}
	// Alphabetical order: Explore before Plan before reviewer.
	eIdx := strings.Index(res.Value, "Explore")
	pIdx := strings.Index(res.Value, "Plan")
	rIdx := strings.Index(res.Value, "reviewer")
	if !(eIdx < pIdx && pIdx < rIdx) {
		t.Fatalf("rows not sorted alphabetically: Explore=%d Plan=%d reviewer=%d", eIdx, pIdx, rIdx)
	}
}

func TestAgentsCommand_EmptyCatalog(t *testing.T) {
	reg := NewCommandRegistry()
	RegisterAgentAndForkCommands(reg, func() *AgentDefinitions { return nil })

	cmd := reg.Find("agents")
	res, err := cmd.Call("list", CommandContext{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Value, "No active agents") {
		t.Fatalf("got %q", res.Value)
	}
}

// --- /agents show ----------------------------------------------------------

func TestAgentsCommand_ShowExistingAgent(t *testing.T) {
	reg := NewCommandRegistry()
	defs := &AgentDefinitions{
		ActiveAgents: []*AgentDefinition{
			{
				AgentType:  "reviewer",
				Source:     AgentSourceProject,
				WhenToUse:  "Review code",
				Tools:      []string{"Read", "Grep"},
				Model:      "claude-sonnet",
				Memory:     AgentMemoryScopeProject,
				Skills:     []string{"/verify"},
				Background: true,
				Isolation:  "local",
			},
		},
	}
	defs.RefreshLegacy()
	RegisterAgentAndForkCommands(reg, func() *AgentDefinitions { return defs })

	cmd := reg.Find("agents")
	res, err := cmd.Call("show reviewer", CommandContext{})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Agent: reviewer",
		"Source: project",
		"Tools: Read, Grep",
		"Model: claude-sonnet",
		"Memory scope: project",
		"Skills: /verify",
		"Background: yes",
		"Isolation: local",
	} {
		if !strings.Contains(res.Value, want) {
			t.Errorf("missing %q in:\n%s", want, res.Value)
		}
	}
}

func TestAgentsCommand_ShowMissingAgent(t *testing.T) {
	reg := NewCommandRegistry()
	defs := &AgentDefinitions{}
	RegisterAgentAndForkCommands(reg, func() *AgentDefinitions { return defs })

	cmd := reg.Find("agents")
	res, _ := cmd.Call("show ghost", CommandContext{})
	if !strings.Contains(res.Value, "not found") {
		t.Fatalf("got %q", res.Value)
	}
}

func TestAgentsCommand_ShowMissingArg(t *testing.T) {
	reg := NewCommandRegistry()
	RegisterAgentAndForkCommands(reg, func() *AgentDefinitions { return &AgentDefinitions{} })

	cmd := reg.Find("agents")
	res, _ := cmd.Call("show", CommandContext{})
	if !strings.Contains(res.Value, "Usage") {
		t.Fatalf("expected usage message, got %q", res.Value)
	}
}

func TestAgentsCommand_UnknownSubcommand(t *testing.T) {
	reg := NewCommandRegistry()
	RegisterAgentAndForkCommands(reg, func() *AgentDefinitions { return nil })

	cmd := reg.Find("agents")
	res, _ := cmd.Call("zap", CommandContext{})
	if !strings.Contains(res.Value, "Unknown") {
		t.Fatalf("got %q", res.Value)
	}
}

// --- /fork ------------------------------------------------------------------

func TestForkCommand_RegistrationShape(t *testing.T) {
	reg := NewCommandRegistry()
	RegisterAgentAndForkCommands(reg, func() *AgentDefinitions { return nil })

	cmd := reg.Find("fork")
	if cmd == nil {
		t.Fatal("/fork not registered")
	}
	if cmd.Type != CommandTypePrompt {
		t.Fatalf("type = %s", cmd.Type)
	}
}

func TestForkCommand_DisabledWhenEnvOff(t *testing.T) {
	t.Setenv("UBUILDING_FORK_SUBAGENT", "")
	t.Setenv("UBUILDING_COORDINATOR_MODE", "")
	t.Setenv("UBUILDING_NON_INTERACTIVE", "")

	reg := NewCommandRegistry()
	RegisterAgentAndForkCommands(reg, func() *AgentDefinitions { return nil })

	cmd := reg.Find("fork")
	if cmd.IsEnabled == nil || cmd.IsEnabled() {
		t.Fatal("fork should be disabled when env is off")
	}
	// GetPrompt still returns a helpful hint when invoked while disabled.
	blocks, err := cmd.GetPrompt("audit main", CommandContext{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(blocks[0].Text, "disabled") {
		t.Fatalf("expected disabled hint, got %q", blocks[0].Text)
	}
}

func TestForkCommand_PromptsWhenEnabled(t *testing.T) {
	t.Setenv("UBUILDING_FORK_SUBAGENT", "1")
	t.Setenv("UBUILDING_COORDINATOR_MODE", "")
	t.Setenv("UBUILDING_NON_INTERACTIVE", "")

	reg := NewCommandRegistry()
	RegisterAgentAndForkCommands(reg, func() *AgentDefinitions { return nil })

	cmd := reg.Find("fork")
	if cmd.IsEnabled == nil || !cmd.IsEnabled() {
		t.Fatal("fork should be enabled when env is set")
	}
	blocks, err := cmd.GetPrompt("audit the feature branch", CommandContext{})
	if err != nil {
		t.Fatal(err)
	}
	if len(blocks) != 1 {
		t.Fatalf("blocks = %d", len(blocks))
	}
	if !strings.Contains(blocks[0].Text, "[FORK]") || !strings.Contains(blocks[0].Text, "audit the feature branch") {
		t.Fatalf("prompt body = %q", blocks[0].Text)
	}
	// Empty args → usage hint.
	blocks, _ = cmd.GetPrompt("", CommandContext{})
	if !strings.Contains(blocks[0].Text, "Usage") {
		t.Fatalf("expected usage hint for empty args: %q", blocks[0].Text)
	}
}

// --- registration error ---------------------------------------------------

func TestRegisterAgentAndForkCommands_NilProviderPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic for nil provider")
		}
	}()
	RegisterAgentAndForkCommands(NewCommandRegistry(), nil)
}

func TestRegisterAgentAndForkCommands_NilRegistryNoop(t *testing.T) {
	// Must not panic.
	RegisterAgentAndForkCommands(nil, func() *AgentDefinitions { return nil })
}
