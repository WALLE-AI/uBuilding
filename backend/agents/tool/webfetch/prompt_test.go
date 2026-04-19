package webfetch

import (
	"strings"
	"testing"

	"github.com/wall-ai/ubuilding/backend/agents/tool"
)

func TestWebFetch_PromptMirrorsUpstream(t *testing.T) {
	p := New().Prompt(tool.PromptOptions{})
	for _, want := range []string{
		"WebFetch WILL FAIL for authenticated or private URLs",
		"Fetches content from a specified URL and processes it using an AI model",
		"Takes a URL and a prompt as input",
		"converts HTML to markdown",
		"Processes the content with the prompt using a small, fast model",
		"Returns the model's response about the content",
		"Usage notes:",
		"HTTP URLs will be automatically upgraded to HTTPS",
		"self-cleaning 15-minute cache",
		"provide the redirect URL in a special format",
		"For GitHub URLs, prefer using the gh CLI via Bash",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("WebFetch prompt missing %q", want)
		}
	}
}
