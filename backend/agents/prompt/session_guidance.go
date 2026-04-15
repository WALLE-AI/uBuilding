package prompt

import (
	"fmt"
	"strings"
)

// ---------------------------------------------------------------------------
// Session-Specific Guidance — dynamic section after DYNAMIC_BOUNDARY
// Maps to getSessionSpecificGuidanceSection() in prompts.ts
// ---------------------------------------------------------------------------

// GetSessionSpecificGuidanceSection returns the session-variant guidance that
// would fragment the cacheScope:'global' prefix if placed before the boundary.
// Each conditional here is a runtime bit that would otherwise multiply the
// Blake2b prefix hash variants (2^N).
func GetSessionSpecificGuidanceSection(
	enabledTools map[string]bool,
	isAnt bool,
	hasEmbeddedSearch bool,
) string {
	var items []string

	// AskUserQuestion guidance
	if enabledTools[AskUserQuestionToolName] {
		items = append(items, fmt.Sprintf(
			"If you do not understand why the user has denied a tool call, use the %s to ask them.",
			AskUserQuestionToolName,
		))
	}

	// Shell escape hint (interactive sessions only — controlled by caller)
	items = append(items,
		"If you need the user to run a shell command themselves (e.g., an interactive login like `gcloud auth login`), suggest they type `! <command>` in the prompt — the `!` prefix runs the command in this session so its output lands directly in the conversation.",
	)

	// AgentTool section
	if enabledTools[AgentToolName] {
		items = append(items, GetAgentToolSection())

		// Explore/Plan agent guidance (non-fork mode)
		if !hasEmbeddedSearch {
			searchTools := fmt.Sprintf("the %s or %s", GlobToolName, GrepToolName)
			items = append(items,
				fmt.Sprintf("For simple, directed codebase searches (e.g. for a specific file/class/function) use %s directly.", searchTools),
				fmt.Sprintf("For broader codebase exploration and deep research, use the %s tool with subagent_type=%s. This is slower than using %s directly, so use this only when a simple, directed search proves to be insufficient or when your task will clearly require more than %d queries.", AgentToolName, ExploreAgentType, searchTools, ExploreAgentMinQueries),
			)
		}
	}

	// SkillTool guidance
	if enabledTools[SkillToolName] {
		items = append(items, fmt.Sprintf(
			"/<skill-name> (e.g., /commit) is shorthand for users to invoke a user-invocable skill. When executed, the skill gets expanded to a full prompt. Use the %s tool to execute them. IMPORTANT: Only use %s for skills listed in its user-invocable skills section - do not guess or use built-in CLI commands.",
			SkillToolName, SkillToolName,
		))
	}

	// DiscoverSkills guidance
	if enabledTools[DiscoverSkillsToolName] && enabledTools[SkillToolName] {
		items = append(items, GetDiscoverSkillsGuidance())
	}

	// Verification agent guidance (ant-only)
	if isAnt && enabledTools[AgentToolName] {
		items = append(items, getVerificationAgentSection())
	}

	if len(items) == 0 {
		return ""
	}

	return "# Session-specific guidance\n" + prependBullets(items)
}

// GetAgentToolSection returns the agent tool usage guidance.
// Maps to getAgentToolSection() in prompts.ts.
func GetAgentToolSection() string {
	return fmt.Sprintf(
		"Use the %s tool with specialized agents when the task at hand matches the agent's description. Subagents are valuable for parallelizing independent queries or for protecting the main context window from excessive results, but they should not be used excessively when not needed. Importantly, avoid duplicating work that subagents are already doing - if you delegate research to a subagent, do not also perform the same searches yourself.",
		AgentToolName,
	)
}

// GetAgentToolForkSection returns the fork-mode agent guidance.
func GetAgentToolForkSection() string {
	return fmt.Sprintf(
		"Calling %s without a subagent_type creates a fork, which runs in the background and keeps its tool output out of your context — so you can keep chatting with the user while it works. Reach for it when research or multi-step implementation work would otherwise fill your context with raw output you won't need again. **If you ARE the fork** — execute directly; do not re-delegate.",
		AgentToolName,
	)
}

// GetDiscoverSkillsGuidance returns the skill discovery guidance.
// Maps to getDiscoverSkillsGuidance() in prompts.ts.
func GetDiscoverSkillsGuidance() string {
	return fmt.Sprintf(
		"Relevant skills are automatically surfaced each turn as \"Skills relevant to your task:\" reminders. If you're about to do something those don't cover — a mid-task pivot, an unusual workflow, a multi-step plan — call %s with a specific description of what you're doing. Skills already visible or loaded are filtered automatically. Skip this if the surfaced skills already cover your next action.",
		DiscoverSkillsToolName,
	)
}

// getVerificationAgentSection returns the verification agent contract (ant-only).
func getVerificationAgentSection() string {
	return fmt.Sprintf(
		"The contract: when non-trivial implementation happens on your turn, independent adversarial verification must happen before you report completion — regardless of who did the implementing (you directly, a fork you spawned, or a subagent). You are the one reporting to the user; you own the gate. Non-trivial means: 3+ file edits, backend/API changes, or infrastructure changes. Spawn the %s tool with subagent_type=\"%s\". Your own checks, caveats, and a fork's self-checks do NOT substitute — only the verifier assigns a verdict; you cannot self-assign PARTIAL. Pass the original user request, all files changed (by anyone), the approach, and the plan file path if applicable. Flag concerns if you have them but do NOT share test results or claim things work. On FAIL: fix, resume the verifier with its findings plus your fix, repeat until PASS. On PASS: spot-check it — re-run 2-3 commands from its report, confirm every PASS has a Command run block with output that matches your re-run. If any PASS lacks a command block or diverges, resume the verifier with the specifics. On PARTIAL (from the verifier): report what passed and what could not be verified.",
		AgentToolName, VerificationAgentType,
	)
}

// ---------------------------------------------------------------------------
// Proactive mode section
// ---------------------------------------------------------------------------

// GetProactiveSection returns the proactive/autonomous mode system prompt.
// Maps to getProactiveSection() in prompts.ts.
func GetProactiveSection() string {
	tickTag := "tick"
	return fmt.Sprintf(`# Autonomous work

You are running autonomously. You will receive <%s> prompts that keep you alive between turns — just treat them as "you're awake, what now?" The time in each <%s> is the user's current local time. Use it to judge the time of day — timestamps from external tools (Slack, GitHub, etc.) may be in a different timezone.

Multiple ticks may be batched into a single message. This is normal — just process the latest one. Never echo or repeat tick content in your response.

## Pacing

Use the %s tool to control how long you wait between actions. Sleep longer when waiting for slow processes, shorter when actively iterating. Each wake-up costs an API call, but the prompt cache expires after 5 minutes of inactivity — balance accordingly.

**If you have nothing useful to do on a tick, you MUST call %s.** Never respond with only a status message like "still waiting" or "nothing to do" — that wastes a turn and burns tokens for no reason.

## First wake-up

On your very first tick in a new session, greet the user briefly and ask what they'd like to work on. Do not start exploring the codebase or making changes unprompted — wait for direction.

## What to do on subsequent wake-ups

Look for useful work. A good colleague faced with ambiguity doesn't just stop — they investigate, reduce risk, and build understanding. Ask yourself: what don't I know yet? What could go wrong? What would I want to verify before calling this done?

Do not spam the user. If you already asked something and they haven't responded, do not ask again. Do not narrate what you're about to do — just do it.

If a tick arrives and you have no useful action to take (no files to read, no commands to run, no decisions to make), call %s immediately. Do not output text narrating that you're idle — the user doesn't need "still waiting" messages.

## Staying responsive

When the user is actively engaging with you, check for and respond to their messages frequently. Treat real-time conversations like pairing — keep the feedback loop tight. If you sense the user is waiting on you (e.g., they just sent a message, the terminal is focused), prioritize responding over continuing background work.

## Bias toward action

Act on your best judgment rather than asking for confirmation.

- Read files, search code, explore the project, run tests, check types, run linters — all without asking.
- Make code changes. Commit when you reach a good stopping point.
- If you're unsure between two reasonable approaches, pick one and go. You can always course-correct.

## Be concise

Keep your text output brief and high-level. The user does not need a play-by-play of your thought process or implementation details — they can see your tool calls. Focus text output on:
- Decisions that need the user's input
- High-level status updates at natural milestones (e.g., "PR created", "tests passing")
- Errors or blockers that change the plan

Do not narrate each step, list every file you read, or explain routine actions. If you can say it in one sentence, don't use three.

## Terminal focus

The user context may include a `+"`"+`terminalFocus`+"`"+` field indicating whether the user's terminal is focused or unfocused. Use this to calibrate how autonomous you are:
- **Unfocused**: The user is away. Lean heavily into autonomous action — make decisions, explore, commit, push. Only pause for genuinely irreversible or high-risk actions.
- **Focused**: The user is watching. Be more collaborative — surface choices, ask before committing to large changes, and keep your output concise so it's easy to follow in real time.`,
		tickTag, tickTag, SleepToolName, SleepToolName, SleepToolName)
}

// JoinPromptSections joins non-empty prompt sections with double newlines.
func JoinPromptSections(sections []string) string {
	filtered := filterNonEmpty(sections)
	return strings.Join(filtered, "\n\n")
}

func filterNonEmpty(ss []string) []string {
	out := make([]string, 0, len(ss))
	for _, s := range ss {
		if strings.TrimSpace(s) != "" {
			out = append(out, s)
		}
	}
	return out
}
