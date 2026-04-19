package bash

import (
	"os"
	"strings"
	"testing"

	"github.com/wall-ai/ubuilding/backend/agents/tool"
)

func TestBash_PromptKeywords_External(t *testing.T) {
	// Freeze background toggle so the assertion is deterministic.
	os.Unsetenv("CLAUDE_CODE_DISABLE_BACKGROUND_TASKS")

	p := New().Prompt(tool.PromptOptions{})
	for _, want := range []string{
		"Executes a given bash command",
		"working directory persists",
		"File search: Use Glob",
		"Content search: Use Grep",
		"Read files: Use Read",
		"Edit files: Use Edit",
		"Write files: Use Write",
		"Communication: Output text directly",
		"# Instructions",
		"absolute paths and avoiding usage of `cd`",
		"optional timeout in milliseconds",
		"run_in_background",
		"When issuing multiple commands:",
		"For git commands:",
		"# Committing changes with git",
		"Git Safety Protocol",
		"# Creating pull requests",
		"gh pr create",
		"HEREDOC",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("Bash.Prompt() missing %q\n----\n%s", want, p)
		}
	}
}

func TestBash_PromptEmbeddedDropsFindGrep(t *testing.T) {
	p := New().Prompt(tool.PromptOptions{EmbeddedSearchTools: true})
	if strings.Contains(p, "File search: Use Glob") {
		t.Error("embedded prompt should not advertise Glob vs find")
	}
	if strings.Contains(p, "Content search: Use Grep") {
		t.Error("embedded prompt should not advertise Grep vs grep/rg")
	}
	if !strings.Contains(p, "Read files: Use Read") {
		t.Error("embedded prompt still needs the Read/Edit/Write bullets")
	}
	// Avoid-commands string should also drop `find` / `grep`.
	if strings.Contains(p, "`find`, `grep`") {
		t.Error("embedded prompt leaked find/grep into the avoid list")
	}
}

func TestBash_PromptBackgroundGate(t *testing.T) {
	// Upstream only gates the "usage note" paragraph — the sleep
	// subitems that reference `run_in_background` remain, mirroring
	// claude-code-main/BashTool/prompt.ts. Assert on the gated
	// paragraph specifically.
	const usageNoteMarker = "You can use the `run_in_background` parameter to run the command in the background."

	os.Unsetenv("CLAUDE_CODE_DISABLE_BACKGROUND_TASKS")
	on := New().Prompt(tool.PromptOptions{})
	if !strings.Contains(on, usageNoteMarker) {
		t.Error("background usage note missing when the feature is enabled")
	}

	os.Setenv("CLAUDE_CODE_DISABLE_BACKGROUND_TASKS", "1")
	defer os.Unsetenv("CLAUDE_CODE_DISABLE_BACKGROUND_TASKS")
	off := New().Prompt(tool.PromptOptions{})
	if strings.Contains(off, usageNoteMarker) {
		t.Error("background usage note leaked when CLAUDE_CODE_DISABLE_BACKGROUND_TASKS=1")
	}
}

func TestBash_PromptSandboxSectionOptional(t *testing.T) {
	off := New().Prompt(tool.PromptOptions{})
	on := New().Prompt(tool.PromptOptions{SandboxEnabled: true})
	if strings.Contains(off, "## Command sandbox") {
		t.Error("sandbox section leaked when SandboxEnabled=false")
	}
	if !strings.Contains(on, "## Command sandbox") {
		t.Error("sandbox section missing when SandboxEnabled=true")
	}
}

func TestBash_PromptCrossRefFallsBack(t *testing.T) {
	// Empty Tools means the helper falls back to canonical names. This
	// keeps the prompt readable even when a host filters a peer tool.
	p := New().Prompt(tool.PromptOptions{})
	for _, want := range []string{"Glob", "Grep", "Read", "Edit", "Write"} {
		if !strings.Contains(p, want) {
			t.Errorf("expected canonical fallback %q in prompt", want)
		}
	}
}
