package websearch

import (
	"strings"
	"testing"

	"github.com/wall-ai/ubuilding/backend/agents/tool"
)

func TestWebSearch_PromptKeywords(t *testing.T) {
	p := New("", "").Prompt(tool.PromptOptions{MonthYear: "March 2099"})
	for _, want := range []string{
		"search the web",
		"up-to-date information",
		"search result blocks",
		"knowledge cutoff",
		`CRITICAL REQUIREMENT`,
		"Sources:",
		`[Title](URL)`,
		"Usage notes:",
		"Domain filtering",
		"allowed_domains and blocked_domains are mutually exclusive",
		"The current month is March 2099",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("WebSearch prompt missing %q", want)
		}
	}
}

func TestWebSearch_PromptUsesAssembledMonthYear(t *testing.T) {
	// When opts.MonthYear is populated (AssemblePromptOptions path), the
	// prompt must honour it exactly and not fall back to time.Now().
	p := New("", "").Prompt(tool.PromptOptions{MonthYear: "January 1970"})
	if !strings.Contains(p, "The current month is January 1970.") {
		t.Errorf("MonthYear override missing from prompt:\n%s", p)
	}
}
