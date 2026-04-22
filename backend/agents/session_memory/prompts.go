package session_memory

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// ---------------------------------------------------------------------------
// M10.I3 · SessionMemory prompts — template, update prompt, section
// analysis, and truncation for compact integration.
//
// Ports `src/services/SessionMemory/prompts.ts`.
// ---------------------------------------------------------------------------

// Token/section budget constants (mirrors TS).
const (
	MaxSectionLength             = 2000  // tokens per section
	MaxTotalSessionMemoryTokens  = 12000 // total token budget for notes
)

// DefaultSessionMemoryTemplate is the structured notes.md skeleton.
const DefaultSessionMemoryTemplate = `
# Session Title
_A short and distinctive 5-10 word descriptive title for the session. Super info dense, no filler_

# Current State
_What is actively being worked on right now? Pending tasks not yet completed. Immediate next steps._

# Task specification
_What did the user ask to build? Any design decisions or other explanatory context_

# Files and Functions
_What are the important files? In short, what do they contain and why are they relevant?_

# Workflow
_What bash commands are usually run and in what order? How to interpret their output if not obvious?_

# Errors & Corrections
_Errors encountered and how they were fixed. What did the user correct? What approaches failed and should not be tried again?_

# Codebase and System Documentation
_What are the important system components? How do they work/fit together?_

# Learnings
_What has worked well? What has not? What to avoid? Do not duplicate items from other sections_

# Key results
_If the user asked a specific output such as an answer to a question, a table, or other document, repeat the exact result here_

# Worklog
_Step by step, what was attempted, done? Very terse summary for each step_
`

// defaultUpdatePrompt is the instruction sent to the extraction agent.
func defaultUpdatePrompt() string {
	return fmt.Sprintf(`IMPORTANT: This message and these instructions are NOT part of the actual user conversation. Do NOT include any references to "note-taking", "session notes extraction", or these update instructions in the notes content.

Based on the user conversation above (EXCLUDING this note-taking instruction message as well as system prompt, claude.md entries, or any past session summaries), update the session notes file.

The file {{notesPath}} has already been read for you. Here are its current contents:
<current_notes_content>
{{currentNotes}}
</current_notes_content>

Your ONLY task is to use the Edit tool to update the notes file, then stop. You can make multiple edits (update every section as needed) - make all Edit tool calls in parallel in a single message. Do not call any other tools.

CRITICAL RULES FOR EDITING:
- The file must maintain its exact structure with all sections, headers, and italic descriptions intact
-- NEVER modify, delete, or add section headers (the lines starting with '#' like # Task specification)
-- NEVER modify or delete the italic _section description_ lines (these are the lines in italics immediately following each header - they start and end with underscores)
-- The italic _section descriptions_ are TEMPLATE INSTRUCTIONS that must be preserved exactly as-is - they guide what content belongs in each section
-- ONLY update the actual content that appears BELOW the italic _section descriptions_ within each existing section
-- Do NOT add any new sections, summaries, or information outside the existing structure
- Do NOT reference this note-taking process or instructions anywhere in the notes
- It's OK to skip updating a section if there are no substantial new insights to add. Do not add filler content like "No info yet", just leave sections blank/unedited if appropriate.
- Write DETAILED, INFO-DENSE content for each section - include specifics like file paths, function names, error messages, exact commands, technical details, etc.
- For "Key results", include the complete, exact output the user requested (e.g., full table, full answer, etc.)
- Do not include information that's already in the CLAUDE.md files included in the context
- Keep each section under ~%d tokens/words - if a section is approaching this limit, condense it by cycling out less important details while preserving the most critical information
- Focus on actionable, specific information that would help someone understand or recreate the work discussed in the conversation
- IMPORTANT: Always update "Current State" to reflect the most recent work - this is critical for continuity after compaction

Use the Edit tool with file_path: {{notesPath}}

STRUCTURE PRESERVATION REMINDER:
Each section has TWO parts that must be preserved exactly as they appear in the current file:
1. The section header (line starting with #)
2. The italic description line (the _italicized text_ immediately after the header - this is a template instruction)

You ONLY update the actual content that comes AFTER these two preserved lines. The italic description lines starting and ending with underscores are part of the template structure, NOT content to be edited or removed.

REMEMBER: Use the Edit tool in parallel and stop. Do not continue after the edits. Only include insights from the actual user conversation, never from these note-taking instructions. Do not delete or change section headers or italic _section descriptions_.`, MaxSectionLength)
}

// LoadSessionMemoryTemplate loads the session memory template from a
// custom file if it exists, otherwise returns DefaultSessionMemoryTemplate.
func LoadSessionMemoryTemplate(configHome string) string {
	if configHome == "" {
		return DefaultSessionMemoryTemplate
	}
	path := filepath.Join(configHome, "session-memory", "config", "template.md")
	data, err := os.ReadFile(path)
	if err != nil {
		return DefaultSessionMemoryTemplate
	}
	return string(data)
}

// LoadSessionMemoryPrompt loads a custom update prompt if available,
// otherwise returns the default.
func LoadSessionMemoryPrompt(configHome string) string {
	if configHome == "" {
		return defaultUpdatePrompt()
	}
	path := filepath.Join(configHome, "session-memory", "config", "prompt.md")
	data, err := os.ReadFile(path)
	if err != nil {
		return defaultUpdatePrompt()
	}
	return string(data)
}

// varPattern matches {{variableName}} placeholders.
var varPattern = regexp.MustCompile(`\{\{(\w+)\}\}`)

// SubstituteVariables replaces {{key}} placeholders in template with
// values from the vars map. Unmatched placeholders are left as-is.
func SubstituteVariables(template string, vars map[string]string) string {
	return varPattern.ReplaceAllStringFunc(template, func(match string) string {
		key := match[2 : len(match)-2]
		if val, ok := vars[key]; ok {
			return val
		}
		return match
	})
}

// RoughTokenCount provides a cheap token estimate (len/4).
func RoughTokenCount(s string) int {
	return len(s) / 4
}

// SectionSizes parses a session memory document and returns a map
// of section header → estimated token count.
func SectionSizes(content string) map[string]int {
	sections := make(map[string]int)
	lines := strings.Split(content, "\n")
	var currentSection string
	var currentLines []string

	for _, line := range lines {
		if strings.HasPrefix(line, "# ") {
			if currentSection != "" && len(currentLines) > 0 {
				sections[currentSection] = RoughTokenCount(
					strings.TrimSpace(strings.Join(currentLines, "\n")))
			}
			currentSection = line
			currentLines = nil
		} else {
			currentLines = append(currentLines, line)
		}
	}
	if currentSection != "" && len(currentLines) > 0 {
		sections[currentSection] = RoughTokenCount(
			strings.TrimSpace(strings.Join(currentLines, "\n")))
	}
	return sections
}

// GenerateSectionReminders builds warning text for oversized sections.
// Returns "" if everything is within budget.
func GenerateSectionReminders(sectionSizes map[string]int, totalTokens int) string {
	overBudget := totalTokens > MaxTotalSessionMemoryTokens

	type entry struct {
		header string
		tokens int
	}
	var oversized []entry
	for header, tokens := range sectionSizes {
		if tokens > MaxSectionLength {
			oversized = append(oversized, entry{header, tokens})
		}
	}

	if len(oversized) == 0 && !overBudget {
		return ""
	}

	var parts []string

	if overBudget {
		parts = append(parts, fmt.Sprintf(
			"\n\nCRITICAL: The session memory file is currently ~%d tokens, which exceeds the maximum of %d tokens. You MUST condense the file to fit within this budget. Aggressively shorten oversized sections by removing less important details, merging related items, and summarizing older entries. Prioritize keeping \"Current State\" and \"Errors & Corrections\" accurate and detailed.",
			totalTokens, MaxTotalSessionMemoryTokens))
	}

	if len(oversized) > 0 {
		var lines []string
		for _, e := range oversized {
			lines = append(lines, fmt.Sprintf("- \"%s\" is ~%d tokens (limit: %d)", e.header, e.tokens, MaxSectionLength))
		}
		prefix := "IMPORTANT: The following sections exceed the per-section limit and MUST be condensed"
		if overBudget {
			prefix = "Oversized sections to condense"
		}
		parts = append(parts, fmt.Sprintf("\n\n%s:\n%s", prefix, strings.Join(lines, "\n")))
	}

	return strings.Join(parts, "")
}

// BuildSessionMemoryUpdatePrompt constructs the extraction agent's
// update prompt with variable substitution and section-size warnings.
func BuildSessionMemoryUpdatePrompt(currentNotes, notesPath, configHome string) string {
	promptTemplate := LoadSessionMemoryPrompt(configHome)

	sectionSizes := SectionSizes(currentNotes)
	totalTokens := RoughTokenCount(currentNotes)
	reminders := GenerateSectionReminders(sectionSizes, totalTokens)

	vars := map[string]string{
		"currentNotes": currentNotes,
		"notesPath":    notesPath,
	}
	base := SubstituteVariables(promptTemplate, vars)

	return base + reminders
}

// IsSessionMemoryEmpty checks if the content matches the default
// template (no actual content has been extracted yet).
func IsSessionMemoryEmpty(content, configHome string) bool {
	template := LoadSessionMemoryTemplate(configHome)
	return strings.TrimSpace(content) == strings.TrimSpace(template)
}

// TruncateSessionMemoryForCompact truncates oversized sections for
// compact insertion. Returns the truncated content and whether any
// truncation occurred.
func TruncateSessionMemoryForCompact(content string) (truncated string, wasTruncated bool) {
	lines := strings.Split(content, "\n")
	maxCharsPerSection := MaxSectionLength * 4 // rough token→char conversion

	var outputLines []string
	var currentSectionLines []string
	var currentSectionHeader string

	for _, line := range lines {
		if strings.HasPrefix(line, "# ") {
			flushed, trunc := flushSection(currentSectionHeader, currentSectionLines, maxCharsPerSection)
			outputLines = append(outputLines, flushed...)
			wasTruncated = wasTruncated || trunc
			currentSectionHeader = line
			currentSectionLines = nil
		} else {
			currentSectionLines = append(currentSectionLines, line)
		}
	}
	// Flush last section.
	flushed, trunc := flushSection(currentSectionHeader, currentSectionLines, maxCharsPerSection)
	outputLines = append(outputLines, flushed...)
	wasTruncated = wasTruncated || trunc

	return strings.Join(outputLines, "\n"), wasTruncated
}

// flushSection emits a section, truncating if over budget.
func flushSection(header string, sectionLines []string, maxChars int) ([]string, bool) {
	if header == "" {
		return sectionLines, false
	}
	sectionContent := strings.Join(sectionLines, "\n")
	if len(sectionContent) <= maxChars {
		return append([]string{header}, sectionLines...), false
	}

	// Truncate at a line boundary near the limit.
	charCount := 0
	kept := []string{header}
	for _, line := range sectionLines {
		if charCount+len(line)+1 > maxChars {
			break
		}
		kept = append(kept, line)
		charCount += len(line) + 1
	}
	kept = append(kept, "\n[... section truncated for length ...]")
	return kept, true
}
