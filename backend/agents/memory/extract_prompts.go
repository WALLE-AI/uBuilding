package memory

import (
	"fmt"
	"strings"
)

// ---------------------------------------------------------------------------
// Extract-memories prompt builders. Ported from
// src/services/extractMemories/prompts.ts.
//
// These prompts are injected as the user message to the forked extraction
// agent, which runs in the background after each turn. The agent has a
// limited tool budget: Read/Grep/Glob unrestricted, Write/Edit only
// within the memory directory, no other tools.
// ---------------------------------------------------------------------------

// Tool name constants used in extraction prompts. These mirror the
// canonical tool names registered by the host.
const (
	extractToolRead  = "Read"
	extractToolWrite = "Write"
	extractToolEdit  = "Edit"
	extractToolGrep  = "Grep"
	extractToolGlob  = "Glob"
	extractToolBash  = "Bash"
)

// opener builds the first paragraph shared by both auto-only and combined
// extraction prompts. Mirrors TS `function opener(…)`.
func extractOpener(newMessageCount int, existingMemories string) string {
	var manifest string
	if existingMemories != "" {
		manifest = fmt.Sprintf(
			"\n\nExisting memories in the directory:\n%s",
			existingMemories,
		)
	}

	return strings.Join([]string{
		"Review the conversation above and extract any new durable memories worth persisting.",
		"",
		fmt.Sprintf("Available tools: %s (any file), %s/%s (memory directory only). %s rm is not permitted. All other tools — MCP, Agent, write-capable %s, etc — will be denied.",
			extractToolRead, extractToolWrite, extractToolEdit, extractToolBash, extractToolBash),
		"",
		fmt.Sprintf("You have a limited turn budget. %s requires a prior %s of the same file, so the efficient strategy is: turn 1 — issue all %s calls in parallel for every file you might update; turn 2 — issue all %s/%s calls in parallel. Do not interleave reads and writes across multiple turns.",
			extractToolEdit, extractToolRead, extractToolRead, extractToolWrite, extractToolEdit),
		"",
		fmt.Sprintf("You MUST only use content from the last ~%d messages to update your persistent memories. Do not waste any turns attempting to investigate or verify that content further — no grepping source files, no reading code to confirm a pattern exists, no git commands.%s",
			newMessageCount, manifest),
	}, "\n")
}

// BuildExtractAutoOnlyPrompt builds the extraction prompt for auto-only
// memory (no team memory). Four-type taxonomy, no scope guidance.
// Mirrors TS `buildExtractAutoOnlyPrompt`.
func BuildExtractAutoOnlyPrompt(newMessageCount int, existingMemories string, skipIndex bool) string {
	var howToSave []string
	if skipIndex {
		howToSave = []string{
			"## How to save memories",
			"",
			"Write each memory to its own file (e.g., `user_role.md`, `feedback_testing.md`) using this frontmatter format:",
			"",
		}
		howToSave = append(howToSave, MemoryFrontmatterExample...)
		howToSave = append(howToSave,
			"",
			"- Organize memory semantically by topic, not chronologically",
			"- Update or remove memories that turn out to be wrong or outdated",
			"- Do not write duplicate memories. First check if there is an existing memory you can update before writing a new one.",
		)
	} else {
		howToSave = []string{
			"## How to save memories",
			"",
			"Saving a memory is a two-step process:",
			"",
			"**Step 1** — write the memory to its own file (e.g., `user_role.md`, `feedback_testing.md`) using this frontmatter format:",
			"",
		}
		howToSave = append(howToSave, MemoryFrontmatterExample...)
		howToSave = append(howToSave,
			"",
			"**Step 2** — add a pointer to that file in `MEMORY.md`. `MEMORY.md` is an index, not a memory — each entry should be one line, under ~150 characters: `- [Title](file.md) — one-line hook`. It has no frontmatter. Never write memory content directly into `MEMORY.md`.",
			"",
			"- `MEMORY.md` is always loaded into your system prompt — lines after 200 will be truncated, so keep the index concise",
			"- Organize memory semantically by topic, not chronologically",
			"- Update or remove memories that turn out to be wrong or outdated",
			"- Do not write duplicate memories. First check if there is an existing memory you can update before writing a new one.",
		)
	}

	lines := []string{
		extractOpener(newMessageCount, existingMemories),
		"",
		"If the user explicitly asks you to remember something, save it immediately as whichever type fits best. If they ask you to forget something, find and remove the relevant entry.",
		"",
	}
	lines = append(lines, TypesSectionIndividual...)
	lines = append(lines, WhatNotToSaveSection...)
	lines = append(lines, "")
	lines = append(lines, howToSave...)

	return strings.Join(lines, "\n")
}

// BuildExtractCombinedPrompt builds the extraction prompt when both auto
// and team memory are enabled. Four-type taxonomy with per-type scope.
// Mirrors TS `buildExtractCombinedPrompt`.
func BuildExtractCombinedPrompt(newMessageCount int, existingMemories string, skipIndex bool) string {
	var howToSave []string
	if skipIndex {
		howToSave = []string{
			"## How to save memories",
			"",
			"Write each memory to its own file in the chosen directory (private or team, per the type's scope guidance) using this frontmatter format:",
			"",
		}
		howToSave = append(howToSave, MemoryFrontmatterExample...)
		howToSave = append(howToSave,
			"",
			"- Organize memory semantically by topic, not chronologically",
			"- Update or remove memories that turn out to be wrong or outdated",
			"- Do not write duplicate memories. First check if there is an existing memory you can update before writing a new one.",
		)
	} else {
		howToSave = []string{
			"## How to save memories",
			"",
			"Saving a memory is a two-step process:",
			"",
			"**Step 1** — write the memory to its own file in the chosen directory (private or team, per the type's scope guidance) using this frontmatter format:",
			"",
		}
		howToSave = append(howToSave, MemoryFrontmatterExample...)
		howToSave = append(howToSave,
			"",
			"**Step 2** — add a pointer to that file in the same directory's `MEMORY.md`. Each directory (private and team) has its own `MEMORY.md` index — each entry should be one line, under ~150 characters: `- [Title](file.md) — one-line hook`. They have no frontmatter. Never write memory content directly into a `MEMORY.md`.",
			"",
			"- Both `MEMORY.md` indexes are loaded into your system prompt — lines after 200 will be truncated, so keep them concise",
			"- Organize memory semantically by topic, not chronologically",
			"- Update or remove memories that turn out to be wrong or outdated",
			"- Do not write duplicate memories. First check if there is an existing memory you can update before writing a new one.",
		)
	}

	lines := []string{
		extractOpener(newMessageCount, existingMemories),
		"",
		"If the user explicitly asks you to remember something, save it immediately as whichever type fits best. If they ask you to forget something, find and remove the relevant entry.",
		"",
	}
	lines = append(lines, TypesSectionCombined...)
	lines = append(lines, WhatNotToSaveSection...)
	lines = append(lines,
		"- You MUST avoid saving sensitive data within shared team memories. For example, never save API keys or user credentials.",
		"",
	)
	lines = append(lines, howToSave...)

	return strings.Join(lines, "\n")
}
