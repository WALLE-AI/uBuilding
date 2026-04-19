package bash

import (
	"fmt"
	"os"
	"strings"

	"github.com/wall-ai/ubuilding/backend/agents/tool"
)

// buildPrompt assembles the full Bash.Prompt() text, mirroring the
// upstream `getSimplePrompt()` from
// opensource/claude-code-main/src/tools/BashTool/prompt.ts. The Go port
// keeps the external-user (non-ant) structure and skips the feature
// flags that aren't wired yet (MONITOR_TOOL, SandboxManager). Dynamic
// segments are toggled via PromptOptions so tests can freeze behaviour
// independently of ambient env.
func buildPrompt(opts tool.PromptOptions) string {
	bashRef := resolvePeer(opts, "Bash")
	globRef := resolvePeer(opts, "Glob")
	grepRef := resolvePeer(opts, "Grep")
	readRef := resolvePeer(opts, "Read")
	editRef := resolvePeer(opts, "Edit")
	writeRef := resolvePeer(opts, "Write")
	todoRef := resolvePeer(opts, "TodoWrite")
	agentRef := resolvePeer(opts, "Task")

	embedded := opts.EmbeddedSearchTools
	backgroundEnabled := !envTruthy("CLAUDE_CODE_DISABLE_BACKGROUND_TASKS")

	// Tool-preference bullets (after "use the appropriate dedicated
	// tool"). When search tools are embedded in the runtime the
	// find/grep warnings are dropped.
	var toolPreferenceItems []string
	if !embedded {
		toolPreferenceItems = append(toolPreferenceItems,
			fmt.Sprintf("File search: Use %s (NOT find or ls)", globRef),
			fmt.Sprintf("Content search: Use %s (NOT grep or rg)", grepRef),
		)
	}
	toolPreferenceItems = append(toolPreferenceItems,
		fmt.Sprintf("Read files: Use %s (NOT cat/head/tail)", readRef),
		fmt.Sprintf("Edit files: Use %s (NOT sed/awk)", editRef),
		fmt.Sprintf("Write files: Use %s (NOT echo >/cat <<EOF)", writeRef),
		"Communication: Output text directly (NOT echo/printf)",
	)

	avoidCommands := "`find`, `grep`, `cat`, `head`, `tail`, `sed`, `awk`, or `echo`"
	if embedded {
		avoidCommands = "`cat`, `head`, `tail`, `sed`, `awk`, or `echo`"
	}

	multipleCommandsSubitems := []string{
		fmt.Sprintf("If the commands are independent and can run in parallel, make multiple %s tool calls in a single message. Example: if you need to run \"git status\" and \"git diff\", send a single message with two %s tool calls in parallel.", bashRef, bashRef),
		fmt.Sprintf("If the commands depend on each other and must run sequentially, use a single %s call with '&&' to chain them together.", bashRef),
		"Use ';' only when you need to run commands sequentially but don't care if earlier commands fail.",
		"DO NOT use newlines to separate commands (newlines are ok in quoted strings).",
	}

	gitSubitems := []string{
		"Prefer to create a new commit rather than amending an existing commit.",
		"Before running destructive operations (e.g., git reset --hard, git push --force, git checkout --), consider whether there is a safer alternative that achieves the same goal. Only use destructive operations when they are truly the best approach.",
		"Never skip hooks (--no-verify) or bypass signing (--no-gpg-sign, -c commit.gpgsign=false) unless the user has explicitly asked for it. If a hook fails, investigate and fix the underlying issue.",
	}

	sleepSubitems := []string{
		"Do not sleep between commands that can run immediately — just run them.",
		"If your command is long running and you would like to be notified when it finishes — use `run_in_background`. No sleep needed.",
		"Do not retry failing commands in a sleep loop — diagnose the root cause.",
		"If waiting for a background task you started with `run_in_background`, you will be notified when it completes — do not poll.",
		"If you must poll an external process, use a check command (e.g. `gh run view`) rather than sleeping first.",
		"If you must sleep, keep the duration short (1-5 seconds) to avoid blocking the user.",
	}

	var instructionItems []interface{}
	instructionItems = append(instructionItems,
		"If your command will create new directories or files, first use this tool to run `ls` to verify the parent directory exists and is the correct location.",
		"Always quote file paths that contain spaces with double quotes in your command (e.g., cd \"path with spaces/file.txt\")",
		"Try to maintain your current working directory throughout the session by using absolute paths and avoiding usage of `cd`. You may use `cd` if the User explicitly requests it.",
		fmt.Sprintf("You may specify an optional timeout in milliseconds (up to %dms / %d minutes). By default, your command will timeout after %dms (%d minutes).",
			MaxTimeoutMs, MaxTimeoutMs/60000, DefaultTimeoutMs, DefaultTimeoutMs/60000),
	)
	if backgroundEnabled {
		instructionItems = append(instructionItems,
			"You can use the `run_in_background` parameter to run the command in the background. Only use this if you don't need the result immediately and are OK being notified when the command completes later. You do not need to check the output right away - you'll be notified when it finishes. You do not need to use '&' at the end of the command when using this parameter.",
		)
	}
	instructionItems = append(instructionItems,
		"When issuing multiple commands:",
		multipleCommandsSubitems,
		"For git commands:",
		gitSubitems,
		"Avoid unnecessary `sleep` commands:",
		sleepSubitems,
	)

	var sb strings.Builder
	sb.WriteString("Executes a given bash command and returns its output.\n\n")
	sb.WriteString("The working directory persists between commands, but shell state does not. The shell environment is initialized from the user's profile (bash or zsh).\n\n")
	fmt.Fprintf(&sb, "IMPORTANT: Avoid using this tool to run %s commands, unless explicitly instructed or after you have verified that a dedicated tool cannot accomplish your task. Instead, use the appropriate dedicated tool as this will provide a much better experience for the user:\n\n", avoidCommands)
	writeBullets(&sb, toolPreferenceItems, "")
	fmt.Fprintf(&sb, "While the %s tool can do similar things, it’s better to use the built-in tools as they provide a better user experience and make it easier to review tool calls and give permission.\n\n", bashRef)
	sb.WriteString("# Instructions\n")
	writeItems(&sb, instructionItems, "")

	// Optional sandbox section (host-controlled).
	if opts.SandboxEnabled {
		sb.WriteString(sandboxSection())
	}

	// Commit & PR section. We emit the external (non-ant) long form.
	sb.WriteString("\n")
	sb.WriteString(commitAndPRSection(bashRef, todoRef, agentRef))

	return strings.TrimRight(sb.String(), "\n")
}

// writeBullets emits a flat list of `- item` lines with an optional
// indent prefix.
func writeBullets(sb *strings.Builder, items []string, indent string) {
	for _, it := range items {
		fmt.Fprintf(sb, "%s- %s\n", indent, it)
	}
}

// writeItems emits instruction items where each element is either a
// string (flat bullet) or a []string (indented sub-bullets under the
// previous parent bullet). Mirrors TS `prependBullets` semantics.
func writeItems(sb *strings.Builder, items []interface{}, indent string) {
	for _, it := range items {
		switch v := it.(type) {
		case string:
			fmt.Fprintf(sb, "%s- %s\n", indent, v)
		case []string:
			for _, sub := range v {
				fmt.Fprintf(sb, "%s  - %s\n", indent, sub)
			}
		}
	}
}

// sandboxSection returns a minimal sandbox banner when opts.SandboxEnabled
// is true. The Go backend does not yet wire a real SandboxManager, so we
// keep this short and document the intent.
func sandboxSection() string {
	return `
## Command sandbox
By default, your command will be run in a sandbox. This sandbox controls which directories and network hosts commands may access or modify without an explicit override.

- You should always default to running commands within the sandbox. Do NOT attempt to bypass it unless a command just failed with evidence of sandbox restrictions.
- For temporary files, always use the ` + "`$TMPDIR`" + ` environment variable instead of ` + "`/tmp`" + ` directly — TMPDIR is automatically set to the sandbox-writable directory.
`
}

// commitAndPRSection returns the external-user commit + PR guidance. It
// intentionally mirrors the TS external variant in BashTool/prompt.ts.
func commitAndPRSection(bashRef, todoRef, agentRef string) string {
	return `# Committing changes with git

Only create commits when requested by the user. If unclear, ask first. When the user asks you to create a new git commit, follow these steps carefully:

You can call multiple tools in a single response. When multiple independent pieces of information are requested and all commands are likely to succeed, run multiple tool calls in parallel for optimal performance. The numbered steps below indicate which commands should be batched in parallel.

Git Safety Protocol:
- NEVER update the git config
- NEVER run destructive git commands (push --force, reset --hard, checkout ., restore ., clean -f, branch -D) unless the user explicitly requests these actions. Taking unauthorized destructive actions is unhelpful and can result in lost work, so it's best to ONLY run these commands when given direct instructions
- NEVER skip hooks (--no-verify, --no-gpg-sign, etc) unless the user explicitly requests it
- NEVER run force push to main/master, warn the user if they request it
- CRITICAL: Always create NEW commits rather than amending, unless the user explicitly requests a git amend. When a pre-commit hook fails, the commit did NOT happen — so --amend would modify the PREVIOUS commit, which may result in destroying work or losing previous changes. Instead, after hook failure, fix the issue, re-stage, and create a NEW commit
- When staging files, prefer adding specific files by name rather than using "git add -A" or "git add .", which can accidentally include sensitive files (.env, credentials) or large binaries
- NEVER commit changes unless the user explicitly asks you to. It is VERY IMPORTANT to only commit when explicitly asked, otherwise the user will feel that you are being too proactive

1. Run the following bash commands in parallel, each using the ` + bashRef + ` tool:
  - Run a git status command to see all untracked files. IMPORTANT: Never use the -uall flag as it can cause memory issues on large repos.
  - Run a git diff command to see both staged and unstaged changes that will be committed.
  - Run a git log command to see recent commit messages, so that you can follow this repository's commit message style.
2. Analyze all staged changes (both previously staged and newly added) and draft a commit message.
3. Run the following commands in parallel:
   - Add relevant untracked files to the staging area.
   - Create the commit with a message.
   - Run git status after the commit completes to verify success.
4. If the commit fails due to pre-commit hook: fix the issue and create a NEW commit.

Important notes:
- NEVER run additional commands to read or explore code, besides git bash commands
- NEVER use the ` + todoRef + ` or ` + agentRef + ` tools
- DO NOT push to the remote repository unless the user explicitly asks you to do so
- IMPORTANT: Never use git commands with the -i flag (like git rebase -i or git add -i) since they require interactive input which is not supported.
- IMPORTANT: Do not use --no-edit with git rebase commands, as the --no-edit flag is not a valid option for git rebase.
- If there are no changes to commit (i.e., no untracked files and no modifications), do not create an empty commit
- In order to ensure good formatting, ALWAYS pass the commit message via a HEREDOC, a la this example:
<example>
git commit -m "$(cat <<'EOF'
   Commit message here.
   EOF
   )"
</example>

# Creating pull requests
Use the gh command via the ` + bashRef + ` tool for ALL GitHub-related tasks including working with issues, pull requests, checks, and releases. If given a Github URL use the gh command to get the information needed.

IMPORTANT: When the user asks you to create a pull request, follow these steps carefully:

1. Run the following bash commands in parallel using the ` + bashRef + ` tool, in order to understand the current state of the branch since it diverged from the main branch.
2. Analyze all changes that will be included in the pull request and draft a PR title and summary (keep title under 70 characters; put details in the body).
3. Run the following commands in parallel: create a new branch if needed, push with -u, and create the PR with gh pr create. Pass the body via a HEREDOC:
<example>
gh pr create --title "the pr title" --body "$(cat <<'EOF'
## Summary
<1-3 bullet points>

## Test plan
[Bulleted markdown checklist of TODOs for testing the pull request...]
EOF
)"
</example>

Important:
- DO NOT use the ` + todoRef + ` or ` + agentRef + ` tools
- Return the PR URL when you're done, so the user can see it

# Other common operations
- View comments on a Github PR: gh api repos/foo/bar/pulls/123/comments`
}

// envTruthy mirrors `isEnvTruthy` in utils/envUtils.js — "1", "true",
// "yes", "on" (case-insensitive) count; empty / unset do not.
func envTruthy(name string) bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(name)))
	return v == "1" || v == "true" || v == "yes" || v == "on"
}
