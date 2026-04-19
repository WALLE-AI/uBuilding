package powershell

import (
	"strings"
	"testing"
	"time"

	"github.com/wall-ai/ubuilding/backend/agents/tool"
	"github.com/wall-ai/ubuilding/backend/agents/tool/shell"
)

func withEdition(t *testing.T, edition string, fn func()) {
	t.Helper()
	restore := shell.SetPowerShellEditionProbe(func(time.Duration) string { return edition })
	defer restore()
	fn()
}

func TestPowerShell_PromptKeywords_Desktop(t *testing.T) {
	withEdition(t, shell.EditionDesktop, func() {
		p := New().Prompt(tool.PromptOptions{})
		for _, want := range []string{
			"Executes a PowerShell command on Windows",
			"Windows PowerShell 5.1",
			"`&&` / `||` are NOT available",
			"File search: Use Glob",
			"Content search: Use Grep",
			"Read files: Use Read",
			"Edit files: Use Edit",
			"Write files: Use Write",
			"# Instructions",
			"# Committing changes with git",
			"here-string",
			"gh pr create",
		} {
			if !strings.Contains(p, want) {
				t.Errorf("PowerShell desktop prompt missing %q", want)
			}
		}
	})
}

func TestPowerShell_PromptKeywords_Core(t *testing.T) {
	withEdition(t, shell.EditionCore, func() {
		p := New().Prompt(tool.PromptOptions{})
		for _, want := range []string{
			"PowerShell 7+",
			"pipeline chain operators (`&&` / `||`)",
			"ternary operator",
			"UTF-8",
		} {
			if !strings.Contains(p, want) {
				t.Errorf("PowerShell core prompt missing %q", want)
			}
		}
		// 7+ prompt must NOT warn that && is unavailable.
		if strings.Contains(p, "`&&` / `||` are NOT available") {
			t.Error("core prompt still contains the 5.1 unavailability warning")
		}
	})
}

func TestPowerShell_PromptKeywords_UnknownDefaultsDesktopSafe(t *testing.T) {
	withEdition(t, shell.EditionUnknown, func() {
		p := New().Prompt(tool.PromptOptions{})
		if !strings.Contains(p, "Edition could not be detected") {
			t.Error("unknown edition prompt missing detection note")
		}
		if !strings.Contains(p, "`&&` / `||` are NOT available") {
			t.Error("unknown edition must default to 5.1-safe guidance")
		}
	})
}
