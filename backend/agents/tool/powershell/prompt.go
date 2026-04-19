package powershell

import (
	"fmt"
	"strings"

	"github.com/wall-ai/ubuilding/backend/agents/tool"
	"github.com/wall-ai/ubuilding/backend/agents/tool/shell"
)

// buildPrompt renders the PowerShell tool prompt. The structure mirrors
// the upstream Bash prompt (tool preferences, # Instructions, commit/PR
// guidance) but swaps in PowerShell-specific syntax, cmdlet-vs-alias
// advice, and an edition-aware warning block.
//
// PowerShell edition branching:
//
//   - desktop (Windows PowerShell 5.1): no pipeline chain operators
//     (`&&`/`||`), no ternary, ASCII output defaults. We warn the model
//     explicitly.
//   - core (PowerShell 7+): `&&`/`||`, ternary, UTF-8 output — we tell
//     the model it is safe to use them.
//   - unknown / not detected: we stay conservative and emit the 5.1
//     guidance.
func buildPrompt(opts tool.PromptOptions) string {
	shellRef := resolvePeer(opts, Name) // primary name (may be aliased to Bash on Windows)
	globRef := resolvePeer(opts, "Glob")
	grepRef := resolvePeer(opts, "Grep")
	readRef := resolvePeer(opts, "Read")
	editRef := resolvePeer(opts, "Edit")
	writeRef := resolvePeer(opts, "Write")
	todoRef := resolvePeer(opts, "TodoWrite")
	agentRef := resolvePeer(opts, "Task")

	edition := opts.PowerShellEdition
	if edition == "" {
		edition = shell.DetectPowerShellEdition()
	}

	var toolPreferenceItems []string
	toolPreferenceItems = append(toolPreferenceItems,
		fmt.Sprintf("File search: Use %s (NOT Get-ChildItem -Recurse for wildcard searches)", globRef),
		fmt.Sprintf("Content search: Use %s (NOT Select-String on large trees)", grepRef),
		fmt.Sprintf("Read files: Use %s (NOT Get-Content)", readRef),
		fmt.Sprintf("Edit files: Use %s (NOT -replace in-place)", editRef),
		fmt.Sprintf("Write files: Use %s (NOT Out-File / Set-Content)", writeRef),
		"Communication: Output text directly (NOT Write-Host)",
	)

	editionNotes := buildEditionNotes(edition)

	syntaxSubitems := []string{
		"Use PowerShell cmdlet syntax: `Get-ChildItem`, `Select-String`, `ForEach-Object`.",
		"Prefer explicit cmdlet names over aliases (ls/cat/grep) — aliases are 5.1-only or not always present.",
		"Always quote paths with spaces using double quotes: `Set-Location \"C:\\path with spaces\"`.",
		"Escape `$` inside double-quoted strings when you do not want variable expansion: use backtick (`` `$ ``) or single quotes.",
		"Exit codes: PowerShell sets `$LASTEXITCODE` for native binaries; cmdlets use exceptions. Check both when chaining.",
	}

	multipleCommandsSubitems := []string{
		fmt.Sprintf("If the commands are independent and can run in parallel, make multiple %s tool calls in a single message.", shellRef),
		"If the commands depend on each other and must run sequentially, use a single call and chain them.",
	}
	if edition == shell.EditionCore {
		multipleCommandsSubitems = append(multipleCommandsSubitems,
			"Chain with `&&` / `||` (pipeline chain operators) — supported in PowerShell 7+.",
			"Use `;` for unconditional sequencing (runs the next command regardless of exit status).",
		)
	} else {
		multipleCommandsSubitems = append(multipleCommandsSubitems,
			"`&&` / `||` are NOT available in Windows PowerShell 5.1 — use `if ($LASTEXITCODE -eq 0) { ... }` or `-and` / `-or` in script blocks.",
			"Use `;` for unconditional sequencing (runs the next command regardless of exit status).",
		)
	}
	multipleCommandsSubitems = append(multipleCommandsSubitems,
		"DO NOT use newlines to separate commands — pass a script block or chain on a single line.",
	)

	gitSubitems := []string{
		"Prefer creating a new commit over amending an existing one.",
		"Before running destructive operations (`git reset --hard`, `git push --force`, `git checkout --`), consider a safer alternative.",
		"Never skip hooks (`--no-verify`) or bypass signing (`--no-gpg-sign`) unless the user has explicitly asked.",
	}

	sleepSubitems := []string{
		"Do not `Start-Sleep` between commands that can run immediately — just run them.",
		"If your command is long running and you would like to be notified when it finishes — use `run_in_background`. No sleep needed.",
		"Do not retry failing commands in a sleep loop — diagnose the root cause.",
		"If waiting for a background task you started with `run_in_background`, you will be notified when it completes — do not poll.",
	}

	var instructionItems []interface{}
	instructionItems = append(instructionItems,
		"If your command will create new directories or files, first run `Get-ChildItem` on the parent to verify it exists.",
		"Always quote file paths that contain spaces with double quotes.",
		"Try to maintain your current working directory throughout the session by using absolute paths and avoiding `Set-Location`/`cd`.",
		fmt.Sprintf("You may specify an optional timeout in milliseconds (up to %dms / %d minutes). By default, your command will timeout after %dms (%d minutes).",
			MaxTimeoutMs, MaxTimeoutMs/60000, DefaultTimeoutMs, DefaultTimeoutMs/60000),
		"You can use the `run_in_background` parameter to run the command in the background. Do not use it if you need the result immediately.",
		"PowerShell syntax:",
		syntaxSubitems,
		"When issuing multiple commands:",
		multipleCommandsSubitems,
		"For git commands:",
		gitSubitems,
		"Avoid unnecessary `Start-Sleep` commands:",
		sleepSubitems,
	)

	var sb strings.Builder
	sb.WriteString("Executes a PowerShell command on Windows and returns its output.\n\n")
	sb.WriteString("The working directory persists between commands, but PowerShell session state (variables, imported modules, functions) does not. Each invocation starts a fresh `-NoProfile -NonInteractive` session.\n\n")
	if editionNotes != "" {
		sb.WriteString(editionNotes)
		sb.WriteString("\n")
	}
	fmt.Fprintf(&sb, "IMPORTANT: Avoid using this tool to run `Get-ChildItem`, `Get-Content`, `Select-String`, `Out-File`, or `Write-Host` commands, unless explicitly instructed or after you have verified that a dedicated tool cannot accomplish your task. Instead, use the appropriate dedicated tool as this will provide a much better experience for the user:\n\n")
	writeBullets(&sb, toolPreferenceItems, "")
	fmt.Fprintf(&sb, "While the %s tool can do similar things, it’s better to use the built-in tools as they provide a better user experience and make it easier to review tool calls and give permission.\n\n", shellRef)
	sb.WriteString("# Instructions\n")
	writeItems(&sb, instructionItems, "")

	// Commit & PR section — reuse the Bash-style long form, substituting
	// PowerShell-specific HEREDOC guidance (here-strings).
	sb.WriteString("\n")
	sb.WriteString(commitAndPRSection(shellRef, todoRef, agentRef))

	return strings.TrimRight(sb.String(), "\n")
}

// buildEditionNotes returns the edition-specific block that warns the
// model about 5.1 vs 7+ syntax differences. Returns "" when the caller
// prefers to skip the block (not currently used, but kept for parity).
func buildEditionNotes(edition string) string {
	switch edition {
	case shell.EditionCore:
		return "## PowerShell edition\nDetected PowerShell 7+ (`pwsh`). You may use pipeline chain operators (`&&` / `||`), the ternary operator (`a ? b : c`), and null-coalescing (`??`). Default output encoding is UTF-8."
	case shell.EditionDesktop:
		return "## PowerShell edition\nDetected Windows PowerShell 5.1 (`powershell.exe`). `&&` / `||` are NOT available — use `if ($LASTEXITCODE -eq 0) { ... }` or `-and` / `-or` in script blocks. Ternary and null-coalescing operators are also unavailable. Output defaults to the system code page; use `[Console]::OutputEncoding = [Text.Encoding]::UTF8` if you need UTF-8."
	default:
		return "## PowerShell edition\nEdition could not be detected. Assume Windows PowerShell 5.1 syntax: `&&` / `||` are NOT available, use `if ($LASTEXITCODE -eq 0) { ... }` or `-and` / `-or` instead. Ternary / null-coalescing operators are also unavailable."
	}
}

func writeBullets(sb *strings.Builder, items []string, indent string) {
	for _, it := range items {
		fmt.Fprintf(sb, "%s- %s\n", indent, it)
	}
}

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

// commitAndPRSection returns the PowerShell-flavoured commit + PR
// guidance. PowerShell uses here-strings (`@' '@`) instead of HEREDOC,
// and `gh.exe` is the reference binary on Windows.
func commitAndPRSection(shellRef, todoRef, agentRef string) string {
	return `# Committing changes with git

Only create commits when requested by the user. If unclear, ask first. When the user asks you to create a new git commit, follow these steps carefully:

Git Safety Protocol:
- NEVER update the git config
- NEVER run destructive git commands (push --force, reset --hard, checkout ., restore ., clean -f, branch -D) unless the user explicitly requests them
- NEVER skip hooks (--no-verify, --no-gpg-sign, etc) unless the user explicitly requests it
- NEVER run force push to main/master; warn the user if they request it
- CRITICAL: Always create NEW commits rather than amending. If a pre-commit hook fails, fix the issue, re-stage, and create a NEW commit
- When staging files, prefer adding specific files by name rather than ` + "`git add -A`" + ` / ` + "`git add .`" + `
- NEVER commit changes unless the user explicitly asks you to

1. Run the following git commands in parallel via ` + shellRef + `:
  - ` + "`git status`" + ` (do NOT pass -uall on large repos)
  - ` + "`git diff`" + ` (staged and unstaged)
  - ` + "`git log --oneline -n 20`" + ` for style reference
2. Analyze the changes and draft a concise, "why"-focused commit message.
3. Stage the relevant files, create the commit, and run ` + "`git status`" + ` to verify.
4. If the commit fails due to a pre-commit hook: fix the issue and create a NEW commit.

Use a PowerShell here-string to pass multi-line commit messages:
<example>
$msg = @'
Commit message here.
'@
git commit -m $msg
</example>

Important notes:
- NEVER run additional commands to read or explore code besides git commands
- NEVER use the ` + todoRef + ` or ` + agentRef + ` tools
- DO NOT push unless the user explicitly asks
- IMPORTANT: Never use git commands with the -i flag (interactive) since they require input
- If there are no changes to commit, do not create an empty commit

# Creating pull requests
Use ` + "`gh`" + ` (or ` + "`gh.exe`" + `) via the ` + shellRef + ` tool for all GitHub tasks — issues, pull requests, checks, releases. If given a GitHub URL, use ` + "`gh`" + ` to fetch the info you need.

1. Run the following in parallel to understand branch state:
   - ` + "`git status`" + ` (no -uall)
   - ` + "`git diff`" + ` (staged + unstaged)
   - ` + "`git log`" + ` and ` + "`git diff [base]...HEAD`" + ` for the commit history since divergence
2. Draft a PR title (< 70 chars) and a markdown body with Summary + Test plan.
3. In parallel: create/push the branch (with -u), then open the PR. Use a here-string for the body:
<example>
$body = @'
## Summary
<1-3 bullet points>

## Test plan
[Bulleted markdown checklist of TODOs for testing the pull request...]
'@
gh pr create --title "the pr title" --body $body
</example>

Important:
- DO NOT use the ` + todoRef + ` or ` + agentRef + ` tools
- Return the PR URL when you're done, so the user can see it

# Other common operations
- View comments on a GitHub PR: ` + "`gh api repos/foo/bar/pulls/123/comments`"
}
