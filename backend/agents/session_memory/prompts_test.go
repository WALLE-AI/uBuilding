package session_memory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultSessionMemoryTemplate_HasAllSections(t *testing.T) {
	expected := []string{
		"# Session Title",
		"# Current State",
		"# Task specification",
		"# Files and Functions",
		"# Workflow",
		"# Errors & Corrections",
		"# Codebase and System Documentation",
		"# Learnings",
		"# Key results",
		"# Worklog",
	}
	for _, section := range expected {
		assert.Contains(t, DefaultSessionMemoryTemplate, section)
	}
}

func TestLoadSessionMemoryTemplate_Default(t *testing.T) {
	tmpl := LoadSessionMemoryTemplate("")
	assert.Equal(t, DefaultSessionMemoryTemplate, tmpl)
}

func TestLoadSessionMemoryTemplate_Custom(t *testing.T) {
	dir := t.TempDir()
	customPath := filepath.Join(dir, "session-memory", "config", "template.md")
	require.NoError(t, os.MkdirAll(filepath.Dir(customPath), 0o755))
	require.NoError(t, os.WriteFile(customPath, []byte("# Custom\n"), 0o644))

	tmpl := LoadSessionMemoryTemplate(dir)
	assert.Equal(t, "# Custom\n", tmpl)
}

func TestLoadSessionMemoryTemplate_MissingFallsBack(t *testing.T) {
	tmpl := LoadSessionMemoryTemplate(filepath.Join(t.TempDir(), "nope"))
	assert.Equal(t, DefaultSessionMemoryTemplate, tmpl)
}

func TestSubstituteVariables(t *testing.T) {
	template := "File: {{notesPath}}\nContent: {{currentNotes}}\nUnknown: {{other}}"
	vars := map[string]string{
		"notesPath":    "/tmp/notes.md",
		"currentNotes": "hello world",
	}
	result := SubstituteVariables(template, vars)
	assert.Contains(t, result, "/tmp/notes.md")
	assert.Contains(t, result, "hello world")
	assert.Contains(t, result, "{{other}}", "unknown vars left as-is")
}

func TestRoughTokenCount(t *testing.T) {
	assert.Equal(t, 0, RoughTokenCount(""))
	assert.Equal(t, 2, RoughTokenCount("12345678"))
}

func TestSectionSizes(t *testing.T) {
	content := `# Title
short

# Details
` + strings.Repeat("x", 8000) + `

# Empty
`
	sizes := SectionSizes(content)
	assert.Contains(t, sizes, "# Title")
	assert.Contains(t, sizes, "# Details")
	assert.Greater(t, sizes["# Details"], sizes["# Title"])
}

func TestGenerateSectionReminders_AllGood(t *testing.T) {
	sizes := map[string]int{"# A": 500, "# B": 1000}
	assert.Empty(t, GenerateSectionReminders(sizes, 5000))
}

func TestGenerateSectionReminders_OverBudget(t *testing.T) {
	sizes := map[string]int{"# A": 500}
	result := GenerateSectionReminders(sizes, 15000)
	assert.Contains(t, result, "CRITICAL")
	assert.Contains(t, result, "15000")
}

func TestGenerateSectionReminders_OversizedSection(t *testing.T) {
	sizes := map[string]int{"# Details": 3000}
	result := GenerateSectionReminders(sizes, 5000)
	assert.Contains(t, result, "MUST be condensed")
	assert.Contains(t, result, "# Details")
}

func TestBuildSessionMemoryUpdatePrompt(t *testing.T) {
	prompt := BuildSessionMemoryUpdatePrompt("# Title\nsome notes", "/tmp/notes.md", "")
	assert.Contains(t, prompt, "some notes", "currentNotes substituted")
	assert.Contains(t, prompt, "/tmp/notes.md", "notesPath substituted")
	assert.Contains(t, prompt, "Edit tool", "contains editing instructions")
}

func TestIsSessionMemoryEmpty_MatchesTemplate(t *testing.T) {
	assert.True(t, IsSessionMemoryEmpty(DefaultSessionMemoryTemplate, ""))
}

func TestIsSessionMemoryEmpty_WithContent(t *testing.T) {
	content := DefaultSessionMemoryTemplate + "\nSome actual content here"
	assert.False(t, IsSessionMemoryEmpty(content, ""))
}

func TestTruncateSessionMemoryForCompact_NoTruncation(t *testing.T) {
	content := "# Title\nShort content\n# Other\nAlso short"
	truncated, was := TruncateSessionMemoryForCompact(content)
	assert.False(t, was)
	assert.Equal(t, content, truncated)
}

func TestTruncateSessionMemoryForCompact_LongSection(t *testing.T) {
	// MaxSectionLength * 4 = 8000 chars.
	longContent := strings.Repeat("x\n", 5000)
	content := "# Title\nshort\n# Details\n" + longContent
	truncated, was := TruncateSessionMemoryForCompact(content)
	assert.True(t, was)
	assert.Contains(t, truncated, "[... section truncated for length ...]")
	assert.Less(t, len(truncated), len(content))
}
