package memory

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/wall-ai/ubuilding/backend/agents"
)

// ---------------------------------------------------------------------------
// M7 · Memory prompt assembly.
//
//   - M7.I1 · BuildMemoryLines         (individual mode)
//   - M7.I2 · BuildCombinedMemoryPrompt (team mode)
//   - M7.I3 · LoadMemoryPrompt         (dispatcher with mkdir side-effect)
//
// The functions are deliberately free of telemetry / growthbook hooks
// from the TS reference — those are host-specific concerns. The
// "Searching past context" section is left as an extension point
// (SearchingPastContextBuilder) so M8's prompt wiring can plug a
// concrete implementation once the host's Grep tool naming is known.
// ---------------------------------------------------------------------------

// EnvCoworkMemoryExtraGuidelines mirrors the TS
// CLAUDE_COWORK_MEMORY_EXTRA_GUIDELINES override. Extra guidance
// lines are appended to every memory prompt without invalidating the
// cached prompt section, so hosts can inject deployment-specific
// policy text (e.g. compliance reminders) at boot.
const EnvCoworkMemoryExtraGuidelines = "UBUILDING_MEMORY_EXTRA_GUIDELINES"

// EnvSkipMemoryIndex mirrors the TS `tengu_moth_copse` growthbook
// flag (skipIndex=true). Hosts that run with a Pinecone-style recall
// layer and do not need the MEMORY.md index can set this to skip the
// two-step "write file then update index" guidance.
const EnvSkipMemoryIndex = "UBUILDING_MEMORY_SKIP_INDEX"

// AutoMemDisplayName is the `# heading` for the individual-mode
// prompt. Kept const so tests can pin against it.
const AutoMemDisplayName = "auto memory"

// SearchingPastContextBuilder supplies the optional "Searching past
// context" section. Hosts wire this to their Grep-tool naming at
// startup; the default returns nil so prompts omit the section.
//
// Signature matches the TS reference: takes the directory to search
// and returns a slice of lines (no trailing newline).
var SearchingPastContextBuilder func(memoryDir string) []string

// ---------------------------------------------------------------------------
// M7.I1 · BuildMemoryLines — individual mode.
// ---------------------------------------------------------------------------

// BuildMemoryLines mirrors TS `buildMemoryLines`. Produces the full
// individual-mode prompt lines (no MEMORY.md content — that arrives
// via user-context injection in M8.I1).
//
// skipIndex=true drops the two-step Step1/Step2 wording and assumes
// the host handles recall itself.
func BuildMemoryLines(displayName, memoryDir string, extraGuidelines []string, skipIndex bool) []string {
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
			"- Keep the name, description, and type fields in memory files up-to-date with the content",
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
			fmt.Sprintf("**Step 2** — add a pointer to that file in `%s`. `%s` is an index, not a memory — each entry should be one line, under ~150 characters: `- [Title](file.md) — one-line hook`. It has no frontmatter. Never write memory content directly into `%s`.",
				autoMemEntrypoint, autoMemEntrypoint, autoMemEntrypoint),
			"",
			fmt.Sprintf("- `%s` is always loaded into your conversation context — lines after %d will be truncated, so keep the index concise",
				autoMemEntrypoint, MaxEntrypointLines),
			"- Keep the name, description, and type fields in memory files up-to-date with the content",
			"- Organize memory semantically by topic, not chronologically",
			"- Update or remove memories that turn out to be wrong or outdated",
			"- Do not write duplicate memories. First check if there is an existing memory you can update before writing a new one.",
		)
	}

	lines := []string{
		"# " + displayName,
		"",
		fmt.Sprintf("You have a persistent, file-based memory system at `%s`. %s",
			memoryDir, DirExistsGuidance),
		"",
		"You should build up this memory system over time so that future conversations can have a complete picture of who the user is, how they'd like to collaborate with you, what behaviors to avoid or repeat, and the context behind the work the user gives you.",
		"",
		"If the user explicitly asks you to remember something, save it immediately as whichever type fits best. If they ask you to forget something, find and remove the relevant entry.",
		"",
	}
	lines = append(lines, TypesSectionIndividual...)
	lines = append(lines, WhatNotToSaveSection...)
	lines = append(lines, "")
	lines = append(lines, howToSave...)
	lines = append(lines, "")
	lines = append(lines, WhenToAccessSection...)
	lines = append(lines, "")
	lines = append(lines, TrustingRecallSection...)
	lines = append(lines,
		"",
		"## Memory and other forms of persistence",
		"Memory is one of several persistence mechanisms available to you as you assist the user in a given conversation. The distinction is often that memory can be recalled in future conversations and should not be used for persisting information that is only useful within the scope of the current conversation.",
		"- When to use or update a plan instead of memory: If you are about to start a non-trivial implementation task and would like to reach alignment with the user on your approach you should use a Plan rather than saving this information to memory. Similarly, if you already have a plan within the conversation and you have changed your approach persist that change by updating the plan rather than saving a memory.",
		"- When to use or update tasks instead of memory: When you need to break your work in current conversation into discrete steps or keep track of your progress use tasks instead of saving to memory. Tasks are great for persisting information about the work that needs to be done in the current conversation, but memory should be reserved for information that will be useful in future conversations.",
		"",
	)
	if len(extraGuidelines) > 0 {
		lines = append(lines, extraGuidelines...)
		lines = append(lines, "")
	}
	if SearchingPastContextBuilder != nil {
		lines = append(lines, SearchingPastContextBuilder(memoryDir)...)
	}
	return lines
}

// ---------------------------------------------------------------------------
// M7.I2 · BuildCombinedMemoryPrompt — team mode.
// ---------------------------------------------------------------------------

// BuildCombinedMemoryPrompt mirrors TS `buildCombinedMemoryPrompt`.
// Returned string is ready to splice into the system prompt. autoDir
// / teamDir are the resolved directory paths as strings; callers use
// GetAutoMemPath + GetTeamMemPath.
func BuildCombinedMemoryPrompt(autoDir, teamDir string, extraGuidelines []string, skipIndex bool) string {
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
			"- Keep the name, description, and type fields in memory files up-to-date with the content",
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
			fmt.Sprintf("**Step 2** — add a pointer to that file in the same directory's `%s`. Each directory (private and team) has its own `%s` index — each entry should be one line, under ~150 characters: `- [Title](file.md) — one-line hook`. They have no frontmatter. Never write memory content directly into a `%s`.",
				autoMemEntrypoint, autoMemEntrypoint, autoMemEntrypoint),
			"",
			fmt.Sprintf("- Both `%s` indexes are loaded into your conversation context — lines after %d will be truncated, so keep them concise",
				autoMemEntrypoint, MaxEntrypointLines),
			"- Keep the name, description, and type fields in memory files up-to-date with the content",
			"- Organize memory semantically by topic, not chronologically",
			"- Update or remove memories that turn out to be wrong or outdated",
			"- Do not write duplicate memories. First check if there is an existing memory you can update before writing a new one.",
		)
	}

	lines := []string{
		"# Memory",
		"",
		fmt.Sprintf("You have a persistent, file-based memory system with two directories: a private directory at `%s` and a shared team directory at `%s`. %s",
			autoDir, teamDir, DirsExistGuidance),
		"",
		"You should build up this memory system over time so that future conversations can have a complete picture of who the user is, how they'd like to collaborate with you, what behaviors to avoid or repeat, and the context behind the work the user gives you.",
		"",
		"If the user explicitly asks you to remember something, save it immediately as whichever type fits best. If they ask you to forget something, find and remove the relevant entry.",
		"",
		"## Memory scope",
		"",
		"There are two scope levels:",
		"",
		fmt.Sprintf("- private: memories that are private between you and the current user. They persist across conversations with only this specific user and are stored at the root `%s`.", autoDir),
		fmt.Sprintf("- team: memories that are shared with and contributed by all of the users who work within this project directory. Team memories are synced at the beginning of every session and they are stored at `%s`.", teamDir),
		"",
	}
	lines = append(lines, TypesSectionCombined...)
	lines = append(lines, WhatNotToSaveSection...)
	lines = append(lines,
		"- You MUST avoid saving sensitive data within shared team memories. For example, never save API keys or user credentials.",
		"",
	)
	lines = append(lines, howToSave...)
	lines = append(lines,
		"",
		"## When to access memories",
		"- When memories (personal or team) seem relevant, or the user references prior work with them or others in their organization.",
		"- You MUST access memory when the user explicitly asks you to check, recall, or remember.",
		"- If the user says to *ignore* or *not use* memory: proceed as if MEMORY.md were empty. Do not apply remembered facts, cite, compare against, or mention memory content.",
		MemoryDriftCaveat,
		"",
	)
	lines = append(lines, TrustingRecallSection...)
	lines = append(lines,
		"",
		"## Memory and other forms of persistence",
		"Memory is one of several persistence mechanisms available to you as you assist the user in a given conversation. The distinction is often that memory can be recalled in future conversations and should not be used for persisting information that is only useful within the scope of the current conversation.",
		"- When to use or update a plan instead of memory: If you are about to start a non-trivial implementation task and would like to reach alignment with the user on your approach you should use a Plan rather than saving this information to memory. Similarly, if you already have a plan within the conversation and you have changed your approach persist that change by updating the plan rather than saving a memory.",
		"- When to use or update tasks instead of memory: When you need to break your work in current conversation into discrete steps or keep track of your progress use tasks instead of saving to memory. Tasks are great for persisting information about the work that needs to be done in the current conversation, but memory should be reserved for information that will be useful in future conversations.",
	)
	if len(extraGuidelines) > 0 {
		lines = append(lines, extraGuidelines...)
	}
	lines = append(lines, "")
	if SearchingPastContextBuilder != nil {
		lines = append(lines, SearchingPastContextBuilder(autoDir)...)
	}
	return strings.Join(lines, "\n")
}

// ---------------------------------------------------------------------------
// M7.I3 · LoadMemoryPrompt — dispatcher.
// ---------------------------------------------------------------------------

// LoadMemoryPrompt returns the rendered memory-section prompt for
// use inside the system prompt. Dispatch rules match TS
// `loadMemoryPrompt`:
//
//   - AutoMem disabled     → ("", nil) — caller omits the section.
//   - TeamMem enabled      → combined two-directory prompt.
//   - Only AutoMem enabled → individual-directory prompt.
//
// Side effect: ensures the relevant directory exists on disk so the
// model's subsequent Write tool calls don't race with the harness.
// Directory creation failures are logged (via slog) but do not
// prevent prompt generation — the model will discover the failure
// when it tries to write and the tool surfaces the real errno.
func LoadMemoryPrompt(
	ctx context.Context,
	cwd string,
	cfg agents.EngineConfig,
	settings SettingsProvider,
) (string, error) {
	_ = ctx
	if !IsAutoMemoryEnabled(cfg, settings) {
		return "", nil
	}
	extraGuidelines := readExtraGuidelines()
	skipIndex := isEnvTruthy(os.Getenv(EnvSkipMemoryIndex))

	// Team branch takes precedence when active.
	if IsTeamMemoryEnabled(cfg, settings) {
		autoDir := GetAutoMemPath(cwd, settings)
		teamDir := GetTeamMemPath(cwd, settings)
		if autoDir == "" || teamDir == "" {
			// Fall through to individual branch — paths unresolved.
		} else {
			// Creating teamDir implicitly creates autoDir via
			// MkdirAll; mirrors TS which relies on the same
			// recursive-create behaviour.
			if err := EnsureMemoryDirExists(teamDir); err != nil {
				debugf("EnsureMemoryDirExists(teamDir=%q): %v", teamDir, err)
			}
			return BuildCombinedMemoryPrompt(
				strings.TrimRight(autoDir, string(os.PathSeparator)),
				strings.TrimRight(teamDir, string(os.PathSeparator)),
				extraGuidelines, skipIndex), nil
		}
	}

	// Individual mode.
	autoDir := GetAutoMemPath(cwd, settings)
	if autoDir == "" {
		return "", nil
	}
	if err := EnsureMemoryDirExists(autoDir); err != nil {
		debugf("EnsureMemoryDirExists(autoDir=%q): %v", autoDir, err)
	}
	return strings.Join(
		BuildMemoryLines(AutoMemDisplayName,
			strings.TrimRight(autoDir, string(os.PathSeparator)),
			extraGuidelines, skipIndex),
		"\n",
	), nil
}

// ---------------------------------------------------------------------------
// Helpers.
// ---------------------------------------------------------------------------

// readExtraGuidelines pulls the CLAUDE_COWORK_MEMORY_EXTRA_GUIDELINES
// equivalent env var and wraps it in a single-element slice when set.
// Returns nil so downstream `if len(…) > 0` checks stay idiomatic.
func readExtraGuidelines() []string {
	v := strings.TrimSpace(os.Getenv(EnvCoworkMemoryExtraGuidelines))
	if v == "" {
		return nil
	}
	return []string{v}
}

// debugf is the lightweight logger used when prompt generation
// side-effects fail. Uses slog through the claudemd.go logger()
// helper would force a LoaderConfig to be threaded everywhere;
// instead we route through the default logger which is always
// available.
func debugf(format string, args ...interface{}) {
	// Intentionally silent in tests — slog is used by claudemd.go;
	// here we keep the helper host-controllable by writing through
	// the shared test-friendly bufferless logger.
	_, _ = fmt.Fprintf(os.Stderr, "[memory] "+format+"\n", args...)
}
