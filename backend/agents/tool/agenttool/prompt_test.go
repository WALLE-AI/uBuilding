package agenttool

import (
	"strings"
	"testing"

	"github.com/wall-ai/ubuilding/backend/agents"
	"github.com/wall-ai/ubuilding/backend/agents/tool"
)

func catalogWith() []*agents.AgentDefinition {
	return []*agents.AgentDefinition{
		{AgentType: "general-purpose", WhenToUse: "Fallback agent", Tools: []string{"*"}},
		{AgentType: "Explore", WhenToUse: "Read-only search", Tools: []string{"Read", "Grep"}},
	}
}

func TestAgentPrompt_ExternalBaseline(t *testing.T) {
	at := New(WithAgentCatalog(catalogWith))
	p := at.Prompt(tool.PromptOptions{})
	for _, want := range []string{
		"Launch a new agent to handle complex, multi-step tasks autonomously.",
		"Available agent types and the tools they have access to:",
		"- general-purpose: Fallback agent (Tools: *)",
		"When using the Task tool, specify a subagent_type parameter",
		"When NOT to use the Task tool",
		"Usage notes:",
		"- Always include a short description (3-5 words)",
		"Launch multiple agents concurrently whenever possible",
		"run_in_background parameter",
		"Foreground vs background",
		"To continue a previously spawned agent",
		"isolation: \"worktree\"",
		"## Writing the prompt",
		"Never delegate understanding",
		"Example usage:",
		"test-runner",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("external prompt missing %q", want)
		}
	}
	// Should not include the ant-only remote bullet.
	if strings.Contains(p, "isolation: \"remote\"") {
		t.Error("external prompt leaked ant-only remote bullet")
	}
	// Should not include the fork section.
	if strings.Contains(p, "## When to fork") {
		t.Error("external prompt leaked fork section")
	}
}

func TestAgentPrompt_AntRemoteBullet(t *testing.T) {
	at := New(WithAgentCatalog(catalogWith))
	p := at.Prompt(tool.PromptOptions{UserType: "ant"})
	if !strings.Contains(p, "isolation: \"remote\"") {
		t.Error("ant branch must include the remote isolation bullet")
	}
}

func TestAgentPrompt_CoordinatorSlim(t *testing.T) {
	at := New(WithAgentCatalog(catalogWith))
	p := at.Prompt(tool.PromptOptions{AgentToolIsCoordinator: true})
	if strings.Contains(p, "Usage notes:") {
		t.Error("coordinator prompt must not include Usage notes")
	}
	if strings.Contains(p, "## Writing the prompt") {
		t.Error("coordinator prompt must not include Writing the prompt")
	}
	if !strings.Contains(p, "Launch a new agent") {
		t.Error("coordinator prompt must still include the shared header")
	}
}

func TestAgentPrompt_ListViaAttachmentHidesInline(t *testing.T) {
	at := New(WithAgentCatalog(catalogWith))
	p := at.Prompt(tool.PromptOptions{AgentListViaAttachment: true})
	if !strings.Contains(p, "Available agent types are listed in <system-reminder>") {
		t.Error("attachment mode must reference the reminder placeholder")
	}
	if strings.Contains(p, "- general-purpose:") {
		t.Error("attachment mode must NOT render the inline catalog")
	}
	// Concurrency tip is only inline when not using attachment.
	if strings.Contains(p, "Launch multiple agents concurrently whenever possible") {
		t.Error("concurrency tip must be suppressed in attachment mode")
	}
}

func TestAgentPrompt_ProSubscriptionHidesConcurrencyTip(t *testing.T) {
	at := New(WithAgentCatalog(catalogWith))
	p := at.Prompt(tool.PromptOptions{SubscriptionType: "pro"})
	if strings.Contains(p, "Launch multiple agents concurrently whenever possible") {
		t.Error("pro tier must not see concurrency tip")
	}
}

func TestAgentPrompt_DisableBackgroundTasks(t *testing.T) {
	at := New(WithAgentCatalog(catalogWith))
	p := at.Prompt(tool.PromptOptions{DisableBackgroundTasks: true})
	if strings.Contains(p, "run_in_background parameter") {
		t.Error("background block must be hidden when DisableBackgroundTasks=true")
	}
}

func TestAgentPrompt_InProcessTeammate(t *testing.T) {
	at := New(WithAgentCatalog(catalogWith))
	p := at.Prompt(tool.PromptOptions{IsInProcessTeammate: true})
	if strings.Contains(p, "run_in_background parameter") {
		t.Error("in-process teammate must not see background block")
	}
	if !strings.Contains(p, "run_in_background, name, team_name, and mode parameters are not available") {
		t.Error("in-process teammate must include the unavailability line")
	}
}

func TestAgentPrompt_Teammate(t *testing.T) {
	at := New(WithAgentCatalog(catalogWith))
	p := at.Prompt(tool.PromptOptions{IsTeammate: true})
	if !strings.Contains(p, "name, team_name, and mode parameters are not available in this context") {
		t.Error("teammate must include the unavailability line")
	}
	if !strings.Contains(p, "run_in_background parameter") {
		t.Error("plain teammate still sees background bullet")
	}
}

func TestAgentPrompt_ForkEnabled(t *testing.T) {
	at := New(WithAgentCatalog(catalogWith))
	p := at.Prompt(tool.PromptOptions{ForkEnabled: true})
	for _, want := range []string{
		"## When to fork",
		"omit it to fork yourself",
		"output_file",
		"Writing a fork prompt",
		"ship-audit",
		"migration-review",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("fork-enabled prompt missing %q", want)
		}
	}
	// Fork-enabled removes the "When NOT to use" block.
	if strings.Contains(p, "When NOT to use the Task tool") {
		t.Error("fork-enabled must drop the when-not-to-use block")
	}
	// Fork-enabled also suppresses the background paragraph.
	if strings.Contains(p, "run_in_background parameter") {
		t.Error("fork-enabled must drop the run_in_background paragraph")
	}
}

func TestAgentPrompt_EmbeddedSearchToolsHint(t *testing.T) {
	at := New(WithAgentCatalog(catalogWith))
	p := at.Prompt(tool.PromptOptions{EmbeddedSearchTools: true})
	if !strings.Contains(p, "`find` via the Bash tool") {
		t.Error("embedded branch must mention `find` via the Bash tool")
	}
	if !strings.Contains(p, "`grep` via the Bash tool") {
		t.Error("embedded branch must mention `grep` via the Bash tool")
	}
}
