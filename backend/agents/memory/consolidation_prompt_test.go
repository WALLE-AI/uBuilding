package memory

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// ---------------------------------------------------------------------------
// M14.T3 · Consolidation prompt tests.
// ---------------------------------------------------------------------------

func TestBuildConsolidationPrompt_ContainsAllPhases(t *testing.T) {
	prompt := BuildConsolidationPrompt("/mem/root", "/transcripts", "")

	phases := []string{
		"Phase 1",
		"Phase 2",
		"Phase 3",
		"Phase 4",
	}
	for _, phase := range phases {
		assert.Contains(t, prompt, phase, "prompt must include "+phase)
	}
}

func TestBuildConsolidationPrompt_SubstitutesPathArgs(t *testing.T) {
	prompt := BuildConsolidationPrompt("/my/memory", "/my/transcripts", "")

	assert.Contains(t, prompt, "`/my/memory`")
	assert.Contains(t, prompt, "`/my/transcripts`")
}

func TestBuildConsolidationPrompt_ContainsGuidanceAndConstants(t *testing.T) {
	prompt := BuildConsolidationPrompt("/mem", "/trans", "")

	assert.Contains(t, prompt, DirExistsGuidance)
	assert.Contains(t, prompt, autoMemEntrypoint)
	assert.Contains(t, prompt, "200") // MaxEntrypointLines
}

func TestBuildConsolidationPrompt_NoExtra(t *testing.T) {
	prompt := BuildConsolidationPrompt("/mem", "/trans", "")

	assert.NotContains(t, prompt, "## Additional context")
}

func TestBuildConsolidationPrompt_WithExtra(t *testing.T) {
	prompt := BuildConsolidationPrompt("/mem", "/trans", "Focus on Go patterns")

	assert.Contains(t, prompt, "## Additional context")
	assert.Contains(t, prompt, "Focus on Go patterns")
	// Extra should come after the main prompt.
	mainEnd := strings.Index(prompt, "If nothing changed")
	extraStart := strings.Index(prompt, "## Additional context")
	assert.Greater(t, extraStart, mainEnd)
}

func TestBuildConsolidationPrompt_DreamHeader(t *testing.T) {
	prompt := BuildConsolidationPrompt("/mem", "/trans", "")

	assert.True(t, strings.HasPrefix(prompt, "# Dream: Memory Consolidation"),
		"prompt should start with the Dream header")
}
