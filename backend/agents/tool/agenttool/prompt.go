package agenttool

import (
	"fmt"
	"strings"

	"github.com/wall-ai/ubuilding/backend/agents"
	"github.com/wall-ai/ubuilding/backend/agents/tool"
)

// buildPrompt mirrors claude-code-main's AgentTool/prompt.ts::getPrompt.
// It honours:
//   - forkEnabled (swap "When to fork" section + fork examples)
//   - shouldInjectAgentListInMessages (AgentListViaAttachment)
//   - isCoordinator (slim coordinator prompt)
//   - hasEmbeddedSearchTools (embedded find/grep hint)
//   - isTeammate / isInProcessTeammate (hide name/team_name/mode)
//   - CLAUDE_CODE_DISABLE_BACKGROUND_TASKS (hide run_in_background)
//   - getSubscriptionType() (concurrency hint)
//   - USER_TYPE=="ant" (isolation: "remote" bullet)
func (a *AgentTool) buildPrompt(opts tool.PromptOptions) string {
	agentName := a.name
	fileReadRef := resolvePeer(opts, "Read")
	fileWriteRef := resolvePeer(opts, "Write")
	globRef := resolvePeer(opts, "Glob")
	sendMessageRef := resolvePeer(opts, "SendMessage")

	effective := a.catalog
	var defs []*agents.AgentDefinition
	if effective != nil {
		defs = effective()
		if a.catalogFilter != nil {
			defs = a.catalogFilter(defs)
		}
	}

	// Agent list section — either inline or via attachment reminder.
	agentListSection := ""
	if opts.AgentListViaAttachment {
		agentListSection = "Available agent types are listed in <system-reminder> messages in the conversation."
	} else {
		agentListSection = "Available agent types and the tools they have access to:\n" + formatAgentLines(defs)
	}

	forkEnabled := opts.ForkEnabled
	useSubagentOrFork := ""
	if forkEnabled {
		useSubagentOrFork = fmt.Sprintf(
			"When using the %s tool, specify a subagent_type to use a specialized agent, or omit it to fork yourself — a fork inherits your full conversation context.",
			agentName,
		)
	} else {
		useSubagentOrFork = fmt.Sprintf(
			"When using the %s tool, specify a subagent_type parameter to select which agent type to use. If omitted, the general-purpose agent is used.",
			agentName,
		)
	}

	shared := fmt.Sprintf(
		"Launch a new agent to handle complex, multi-step tasks autonomously.\n\n"+
			"The %s tool launches specialized agents (subprocesses) that autonomously handle complex tasks. "+
			"Each agent type has specific capabilities and tools available to it.\n\n"+
			"%s\n\n"+
			"%s",
		agentName, agentListSection, useSubagentOrFork,
	)

	// Coordinator mode returns just the shared slim prompt.
	if opts.AgentToolIsCoordinator {
		return shared
	}

	// When-not-to-use section (skipped when fork is enabled).
	whenNotToUseSection := ""
	if !forkEnabled {
		fileSearchHint := "the " + globRef + " tool"
		contentSearchHint := "the " + globRef + " tool"
		if opts.EmbeddedSearchTools {
			fileSearchHint = "`find` via the Bash tool"
			contentSearchHint = "`grep` via the Bash tool"
		}
		whenNotToUseSection = fmt.Sprintf(
			"\nWhen NOT to use the %s tool:\n"+
				"- If you want to read a specific file path, use the %s tool or %s instead of the %s tool, to find the match more quickly\n"+
				"- If you are searching for a specific class definition like \"class Foo\", use %s instead, to find the match more quickly\n"+
				"- If you are searching for code within a specific file or set of 2-3 files, use the %s tool instead of the %s tool, to find the match more quickly\n"+
				"- Other tasks that are not related to the agent descriptions above\n",
			agentName, fileReadRef, fileSearchHint, agentName,
			contentSearchHint,
			fileReadRef, agentName,
		)
	}

	// Concurrency note — only rendered inline and only for non-pro tiers when
	// not listing via attachment.
	concurrencyNote := ""
	if !opts.AgentListViaAttachment && !strings.EqualFold(opts.SubscriptionType, "pro") {
		concurrencyNote = "\n- Launch multiple agents concurrently whenever possible, to maximize performance; to do that, use a single message with multiple tool uses"
	}

	// Background-task paragraph gated by CLAUDE_CODE_DISABLE_BACKGROUND_TASKS +
	// isInProcessTeammate + forkEnabled.
	backgroundBlock := ""
	if !opts.DisableBackgroundTasks && !opts.IsInProcessTeammate && !forkEnabled {
		backgroundBlock = "\n- You can optionally run agents in the background using the run_in_background parameter. When an agent runs in the background, you will be automatically notified when it completes — do NOT sleep, poll, or proactively check on its progress. Continue with other work or respond to the user instead." +
			"\n- **Foreground vs background**: Use foreground (default) when you need the agent's results before you can proceed — e.g., research agents whose findings inform your next steps. Use background when you have genuinely independent work to do in parallel."
	}

	continueAgentLine := "- To continue a previously spawned agent, use " + sendMessageRef + " with the agent's ID or name as the `to` field. The agent resumes with its full context preserved. "
	if forkEnabled {
		continueAgentLine += "Each fresh Agent invocation with a subagent_type starts without context — provide a complete task description."
	} else {
		continueAgentLine += "Each Agent invocation starts fresh — provide a complete task description."
	}

	clearlyTellLine := "- Clearly tell the agent whether you expect it to write code or just to do research (search, file reads, web fetches, etc.)"
	if !forkEnabled {
		clearlyTellLine += ", since it is not aware of the user's intent"
	}

	// Parallel + isolation notes.
	parallelLine := fmt.Sprintf(
		"- If the user specifies that they want you to run agents \"in parallel\", you MUST send a single message with multiple %s tool use content blocks. For example, if you need to launch both a build-validator agent and a test-runner agent in parallel, send a single message with both tool calls.",
		agentName,
	)
	isolationLine := "- You can optionally set `isolation: \"worktree\"` to run the agent in a temporary git worktree, giving it an isolated copy of the repository. The worktree is automatically cleaned up if the agent makes no changes; if changes are made, the worktree path and branch are returned in the result."

	antRemoteLine := ""
	if strings.EqualFold(opts.UserType, "ant") {
		antRemoteLine = "\n- You can set `isolation: \"remote\"` to run the agent in a remote CCR environment. This is always a background task; you'll be notified when it completes. Use for long-running tasks that need a fresh sandbox."
	}

	teammateLine := ""
	switch {
	case opts.IsInProcessTeammate:
		teammateLine = "\n- The run_in_background, name, team_name, and mode parameters are not available in this context. Only synchronous subagents are supported."
	case opts.IsTeammate:
		teammateLine = "\n- The name, team_name, and mode parameters are not available in this context — teammates cannot spawn other teammates. Omit them to spawn a subagent."
	}

	whenToFork := ""
	if forkEnabled {
		whenToFork = whenToForkSection
	}
	writing := writingPromptSection(forkEnabled)

	examples := currentExamples(agentName, fileWriteRef)
	if forkEnabled {
		examples = forkExamples(agentName)
	}

	return fmt.Sprintf(
		"%s%s\n\n"+
			"Usage notes:\n"+
			"- Always include a short description (3-5 words) summarizing what the agent will do%s\n"+
			"- When the agent is done, it will return a single message back to you. The result returned by the agent is not visible to the user. To show the user the result, you should send a text message back to the user with a concise summary of the result.%s\n"+
			"%s\n"+
			"- The agent's outputs should generally be trusted\n"+
			"%s\n"+
			"- If the agent description mentions that it should be used proactively, then you should try your best to use it without the user having to ask for it first. Use your judgement.\n"+
			"%s\n"+
			"%s%s%s%s%s\n\n"+
			"%s",
		shared, whenNotToUseSection,
		concurrencyNote,
		backgroundBlock,
		continueAgentLine,
		clearlyTellLine,
		parallelLine,
		isolationLine, antRemoteLine, teammateLine,
		whenToFork, writing,
		examples,
	)
}

// formatAgentLines mirrors effectiveAgents.map(formatAgentLine).join('\n').
func formatAgentLines(defs []*agents.AgentDefinition) string {
	var b strings.Builder
	for _, d := range defs {
		if d == nil {
			continue
		}
		fmt.Fprintf(&b, "- %s: %s (Tools: %s)\n", d.AgentType, d.WhenToUse, toolsDescription(d))
	}
	return strings.TrimRight(b.String(), "\n")
}

const whenToForkSection = `

## When to fork

Fork yourself (omit ` + "`subagent_type`" + `) when the intermediate tool output isn't worth keeping in your context. The criterion is qualitative — "will I need this output again" — not task size.
- **Research**: fork open-ended questions. If research can be broken into independent questions, launch parallel forks in one message. A fork beats a fresh subagent for this — it inherits context and shares your cache.
- **Implementation**: prefer to fork implementation work that requires more than a couple of edits. Do research before jumping to implementation.

Forks are cheap because they share your prompt cache. Don't set ` + "`model`" + ` on a fork — a different model can't reuse the parent's cache. Pass a short ` + "`name`" + ` (one or two words, lowercase) so the user can see the fork in the teams panel and steer it mid-run.

**Don't peek.** The tool result includes an ` + "`output_file`" + ` path — do not Read or tail it unless the user explicitly asks for a progress check. You get a completion notification; trust it. Reading the transcript mid-flight pulls the fork's tool noise into your context, which defeats the point of forking.

**Don't race.** After launching, you know nothing about what the fork found. Never fabricate or predict fork results in any format — not as prose, summary, or structured output. The notification arrives as a user-role message in a later turn; it is never something you write yourself. If the user asks a follow-up before the notification lands, tell them the fork is still running — give status, not a guess.

**Writing a fork prompt.** Since the fork inherits your context, the prompt is a *directive* — what to do, not what the situation is. Be specific about scope: what's in, what's out, what another agent is handling. Don't re-explain background.
`

func writingPromptSection(forkEnabled bool) string {
	lead := "Brief the agent like a smart colleague who just walked into the room — it hasn't seen this conversation, doesn't know what you've tried, doesn't understand why this task matters."
	if forkEnabled {
		lead = "When spawning a fresh agent (with a `subagent_type`), it starts with zero context. " + lead
	}
	terseLead := "Terse"
	if forkEnabled {
		terseLead = "For fresh agents, terse"
	}
	return "\n\n## Writing the prompt\n\n" + lead + "\n" +
		"- Explain what you're trying to accomplish and why.\n" +
		"- Describe what you've already learned or ruled out.\n" +
		"- Give enough context about the surrounding problem that the agent can make judgment calls rather than just following a narrow instruction.\n" +
		"- If you need a short response, say so (\"report in under 200 words\").\n" +
		"- Lookups: hand over the exact command. Investigations: hand over the question — prescribed steps become dead weight when the premise is wrong.\n\n" +
		terseLead + " command-style prompts produce shallow, generic work.\n\n" +
		"**Never delegate understanding.** Don't write \"based on your findings, fix the bug\" or \"based on the research, implement it.\" Those phrases push synthesis onto the agent instead of doing it yourself. Write prompts that prove you understood: include file paths, line numbers, what specifically to change.\n"
}

func currentExamples(agentName, fileWriteRef string) string {
	return `Example usage:

<example_agent_descriptions>
"test-runner": use this agent after you are done writing code to run tests
"greeting-responder": use this agent to respond to user greetings with a friendly joke
</example_agent_descriptions>

<example>
user: "Please write a function that checks if a number is prime"
assistant: I'm going to use the ` + fileWriteRef + ` tool to write the following code:
<code>
function isPrime(n) {
  if (n <= 1) return false
  for (let i = 2; i * i <= n; i++) {
    if (n % i === 0) return false
  }
  return true
}
</code>
<commentary>
Since a significant piece of code was written and the task was completed, now use the test-runner agent to run the tests
</commentary>
assistant: Uses the ` + agentName + ` tool to launch the test-runner agent
</example>

<example>
user: "Hello"
<commentary>
Since the user is greeting, use the greeting-responder agent to respond with a friendly joke
</commentary>
assistant: "I'm going to use the ` + agentName + ` tool to launch the greeting-responder agent"
</example>
`
}

func forkExamples(agentName string) string {
	return `Example usage:

<example>
user: "What's left on this branch before we can ship?"
assistant: <thinking>Forking this — it's a survey question. I want the punch list, not the git output in my context.</thinking>
` + agentName + `({
  name: "ship-audit",
  description: "Branch ship-readiness audit",
  prompt: "Audit what's left before this branch can ship. Check: uncommitted changes, commits ahead of main, whether tests exist, whether the GrowthBook gate is wired up, whether CI-relevant files changed. Report a punch list — done vs. missing. Under 200 words."
})
assistant: Ship-readiness audit running.
<commentary>
Turn ends here. The coordinator knows nothing about the findings yet. What follows is a SEPARATE turn — the notification arrives from outside, as a user-role message. It is not something the coordinator writes.
</commentary>
[later turn — notification arrives as user message]
assistant: Audit's back. Three blockers: no tests for the new prompt path, GrowthBook gate wired but not in build_flags.yaml, and one uncommitted file.
</example>

<example>
user: "so is the gate wired up or not"
<commentary>
User asks mid-wait. The audit fork was launched to answer exactly this, and it hasn't returned. The coordinator does not have this answer. Give status, not a fabricated result.
</commentary>
assistant: Still waiting on the audit — that's one of the things it's checking. Should land shortly.
</example>

<example>
user: "Can you get a second opinion on whether this migration is safe?"
assistant: <thinking>I'll ask the code-reviewer agent — it won't see my analysis, so it can give an independent read.</thinking>
<commentary>
A subagent_type is specified, so the agent starts fresh. It needs full context in the prompt. The briefing explains what to assess and why.
</commentary>
` + agentName + `({
  name: "migration-review",
  description: "Independent migration review",
  subagent_type: "code-reviewer",
  prompt: "Review migration 0042_user_schema.sql for safety. Context: we're adding a NOT NULL column to a 50M-row table. Existing rows get a backfill default. I want a second opinion on whether the backfill approach is safe under concurrent writes — I've checked locking behavior but want independent verification. Report: is this safe, and if not, what specifically breaks?"
})
</example>
`
}
