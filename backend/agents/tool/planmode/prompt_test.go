package planmode

import (
	"strings"
	"testing"

	"github.com/wall-ai/ubuilding/backend/agents/tool"
)

func TestEnterPrompt_External(t *testing.T) {
	p := NewEnter().Prompt(tool.PromptOptions{})
	for _, want := range []string{
		"Use this tool proactively",
		"When to Use This Tool",
		"New Feature Implementation",
		"Multiple Valid Approaches",
		"Multi-File Changes",
		"When NOT to Use This Tool",
		"What Happens in Plan Mode",
		"Glob, Grep, and Read tools",
		"AskUserQuestion",
		"ExitPlanMode",
		"GOOD - Use EnterPlanMode",
		"BAD - Don't use EnterPlanMode",
		"This tool REQUIRES user approval",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("external EnterPrompt missing %q", want)
		}
	}
	if !strings.Contains(p, "Prefer using EnterPlanMode") {
		t.Error("external prompt must keep the 'Prefer using EnterPlanMode' heading")
	}
}

func TestEnterPrompt_Ant(t *testing.T) {
	p := NewEnter().Prompt(tool.PromptOptions{UserType: "ant"})
	for _, want := range []string{
		"genuine ambiguity",
		"Significant Architectural Ambiguity",
		"High-Impact Restructuring",
		"When in doubt",
		"AskUserQuestion",
		"Redesign the data pipeline",
		"This tool REQUIRES user approval",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("ant EnterPrompt missing %q", want)
		}
	}
	if strings.Contains(p, "Prefer using EnterPlanMode") {
		t.Error("ant prompt must not contain the external 'Prefer using' heading")
	}
	if strings.Contains(p, "Multi-File Changes") {
		t.Error("ant prompt must not list the external 'Multi-File Changes' rubric")
	}
}

func TestEnterPrompt_InterviewPhaseHidesWhatHappens(t *testing.T) {
	on := NewEnter().Prompt(tool.PromptOptions{PlanModeInterviewEnabled: true})
	off := NewEnter().Prompt(tool.PromptOptions{PlanModeInterviewEnabled: false})
	if strings.Contains(on, "What Happens in Plan Mode") {
		t.Error("with interview phase enabled, What Happens section must be omitted")
	}
	if !strings.Contains(off, "What Happens in Plan Mode") {
		t.Error("with interview phase disabled, What Happens section must be present")
	}
}

func TestExitPrompt_Keywords(t *testing.T) {
	p := New().Prompt(tool.PromptOptions{})
	for _, want := range []string{
		"Use this tool when you are in plan mode",
		"How This Tool Works",
		"does NOT take the plan content as a parameter",
		"When to Use This Tool",
		"Before Using This Tool",
		"AskUserQuestion",
		"ExitPlanMode inherently requests user approval",
		"Examples",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("ExitPrompt missing %q", want)
		}
	}
}
