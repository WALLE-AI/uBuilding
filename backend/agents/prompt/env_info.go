package prompt

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// ---------------------------------------------------------------------------
// Environment Info — maps to computeSimpleEnvInfo() + computeEnvInfo() in prompts.ts
// ---------------------------------------------------------------------------

// EnvInfoConfig holds parameters for environment info generation.
type EnvInfoConfig struct {
	Cwd             string
	IsGit           bool
	Platform        string // e.g., "linux", "darwin", "windows"
	Shell           string
	OSVersion       string
	Model           string
	KnowledgeCutoff string
	AdditionalDirs  []string
	IsAnt           bool
	IsUndercover    bool
	IsWorktree      bool
}

// ComputeSimpleEnvInfo generates the full environment info section.
// Maps to computeSimpleEnvInfo() in prompts.ts — the "simple" (non-legacy) format.
func ComputeSimpleEnvInfo(cfg EnvInfoConfig) string {
	if cfg.Platform == "" {
		cfg.Platform = runtime.GOOS
	}
	if cfg.Shell == "" {
		cfg.Shell = detectShell()
	}
	if cfg.OSVersion == "" {
		cfg.OSVersion = getUnameSR()
	}

	var envItems []string

	envItems = append(envItems, fmt.Sprintf("Primary working directory: %s", cfg.Cwd))

	if cfg.IsWorktree {
		envItems = append(envItems, "This is a git worktree \u2014 an isolated copy of the repository. Run all commands from this directory. Do NOT `cd` to the original repository root.")
	}

	envItems = append(envItems, fmt.Sprintf("Is a git repository: %v", cfg.IsGit))

	if len(cfg.AdditionalDirs) > 0 {
		envItems = append(envItems, "Additional working directories:")
		for _, dir := range cfg.AdditionalDirs {
			envItems = append(envItems, fmt.Sprintf("  %s", dir))
		}
	}

	envItems = append(envItems, fmt.Sprintf("Platform: %s", cfg.Platform))
	envItems = append(envItems, getShellInfoLine(cfg.Shell, cfg.Platform))
	envItems = append(envItems, fmt.Sprintf("OS Version: %s", cfg.OSVersion))

	// Model description (suppressed in undercover mode)
	if !(cfg.IsAnt && cfg.IsUndercover) {
		modelDesc := getModelDescription(cfg.Model)
		if modelDesc != "" {
			envItems = append(envItems, modelDesc)
		}
	}

	// Knowledge cutoff
	if cfg.KnowledgeCutoff == "" {
		cfg.KnowledgeCutoff = GetKnowledgeCutoff(cfg.Model)
	}
	if cfg.KnowledgeCutoff != "" {
		envItems = append(envItems, fmt.Sprintf("Assistant knowledge cutoff is %s.", cfg.KnowledgeCutoff))
	}

	// Model family info (suppressed in undercover mode)
	if !(cfg.IsAnt && cfg.IsUndercover) {
		envItems = append(envItems, fmt.Sprintf(
			"The most recent Claude model family is Claude 4.5/4.6. Model IDs — Opus 4.6: '%s', Sonnet 4.6: '%s', Haiku 4.5: '%s'. When building AI applications, default to the latest and most capable Claude models.",
			ModelFamilyIDs.Opus, ModelFamilyIDs.Sonnet, ModelFamilyIDs.Haiku,
		))
		envItems = append(envItems,
			`Claude Code is available as a CLI in the terminal, desktop app (Mac/Windows), web app (claude.ai/code), and IDE extensions (VS Code, JetBrains).`,
		)
		envItems = append(envItems, fmt.Sprintf(
			`Fast mode for Claude Code uses the same %s model with faster output. It does NOT switch to a different model. It can be toggled with /fast.`,
			FrontierModelName,
		))
	}

	// Build bullet list
	var bullets []string
	for _, item := range envItems {
		if strings.HasPrefix(item, "  ") {
			bullets = append(bullets, fmt.Sprintf("  - %s", strings.TrimSpace(item)))
		} else {
			bullets = append(bullets, fmt.Sprintf(" - %s", item))
		}
	}

	return "# Environment\nYou have been invoked in the following environment: \n" + strings.Join(bullets, "\n")
}

// ComputeEnvInfo generates the legacy (XML) format environment info.
// Maps to computeEnvInfo() in prompts.ts — the <env> block format.
func ComputeEnvInfo(cfg EnvInfoConfig) string {
	if cfg.Platform == "" {
		cfg.Platform = runtime.GOOS
	}
	if cfg.Shell == "" {
		cfg.Shell = detectShell()
	}
	if cfg.OSVersion == "" {
		cfg.OSVersion = getUnameSR()
	}

	var modelDescription string
	if !(cfg.IsAnt && cfg.IsUndercover) {
		modelDescription = getModelDescription(cfg.Model)
	}

	additionalDirsInfo := ""
	if len(cfg.AdditionalDirs) > 0 {
		additionalDirsInfo = fmt.Sprintf("Additional working directories: %s\n", strings.Join(cfg.AdditionalDirs, ", "))
	}

	isGitStr := "No"
	if cfg.IsGit {
		isGitStr = "Yes"
	}

	if cfg.KnowledgeCutoff == "" {
		cfg.KnowledgeCutoff = GetKnowledgeCutoff(cfg.Model)
	}
	knowledgeCutoffMsg := ""
	if cfg.KnowledgeCutoff != "" {
		knowledgeCutoffMsg = fmt.Sprintf("\n\nAssistant knowledge cutoff is %s.", cfg.KnowledgeCutoff)
	}

	return fmt.Sprintf(`Here is useful information about the environment you are running in:
<env>
Working directory: %s
Is directory a git repo: %s
%sPlatform: %s
%s
OS Version: %s
</env>
%s%s`,
		cfg.Cwd,
		isGitStr,
		additionalDirsInfo,
		cfg.Platform,
		getShellInfoLine(cfg.Shell, cfg.Platform),
		cfg.OSVersion,
		modelDescription,
		knowledgeCutoffMsg,
	)
}

// GetKnowledgeCutoff returns the knowledge cutoff date for a model ID.
// Maps to getKnowledgeCutoff() in prompts.ts.
func GetKnowledgeCutoff(modelID string) string {
	id := strings.ToLower(modelID)
	switch {
	case strings.Contains(id, "claude-sonnet-4-6"):
		return "August 2025"
	case strings.Contains(id, "claude-opus-4-6"):
		return "May 2025"
	case strings.Contains(id, "claude-opus-4-5"):
		return "May 2025"
	case strings.Contains(id, "claude-haiku-4"):
		return "February 2025"
	case strings.Contains(id, "claude-opus-4") || strings.Contains(id, "claude-sonnet-4"):
		return "January 2025"
	default:
		return ""
	}
}

// ---------------------------------------------------------------------------
// Agent prompt enhancement
// ---------------------------------------------------------------------------

// EnhanceSystemPromptWithEnvDetails adds env info and agent notes to a subagent prompt.
// Maps to enhanceSystemPromptWithEnvDetails() in prompts.ts.
// enabledToolNames is optional — when provided, DiscoverSkills guidance is
// conditionally appended (matching TS L777-783).
func EnhanceSystemPromptWithEnvDetails(existingPrompt []string, cfg EnvInfoConfig, enabledToolNames map[string]bool) []string {
	notes := `Notes:
- Agent threads always have their cwd reset between bash calls, as a result please only use absolute file paths.
- In your final response, share file paths (always absolute, never relative) that are relevant to the task. Include code snippets only when the exact text is load-bearing (e.g., a bug you found, a function signature the caller asked for) — do not recap code you merely read.
- For clear communication with the user the assistant MUST avoid using emojis.
- Do not use a colon before tool calls. Text like "Let me read the file:" followed by a read tool call should just be "Let me read the file." with a period.`

	// Subagents use the legacy XML <env> format (TS uses computeEnvInfo, not computeSimpleEnvInfo)
	envInfo := ComputeEnvInfo(cfg)

	// DiscoverSkills guidance for subagents (TS L777-783)
	var discoverSkillsGuidance string
	if enabledToolNames != nil && enabledToolNames[DiscoverSkillsToolName] {
		discoverSkillsGuidance = GetDiscoverSkillsGuidance()
	}

	result := make([]string, 0, len(existingPrompt)+4)
	result = append(result, existingPrompt...)
	result = append(result, notes)
	if discoverSkillsGuidance != "" {
		result = append(result, discoverSkillsGuidance)
	}
	if envInfo != "" {
		result = append(result, envInfo)
	}
	return result
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func getModelDescription(modelID string) string {
	if modelID == "" {
		return ""
	}
	marketingName := getMarketingNameForModel(modelID)
	if marketingName != "" {
		return fmt.Sprintf("You are powered by the model named %s. The exact model ID is %s.", marketingName, modelID)
	}
	return fmt.Sprintf("You are powered by the model %s.", modelID)
}

// getMarketingNameForModel returns a human-readable model name.
// Maps to getMarketingNameForModel() in utils/model/model.ts.
func getMarketingNameForModel(modelID string) string {
	id := strings.ToLower(modelID)
	switch {
	case strings.Contains(id, "claude-opus-4-6"):
		return "Claude Opus 4.6"
	case strings.Contains(id, "claude-sonnet-4-6"):
		return "Claude Sonnet 4.6"
	case strings.Contains(id, "claude-opus-4-5"):
		return "Claude Opus 4.5"
	case strings.Contains(id, "claude-sonnet-4-5"):
		return "Claude Sonnet 4.5"
	case strings.Contains(id, "claude-opus-4"):
		return "Claude Opus 4"
	case strings.Contains(id, "claude-sonnet-4"):
		return "Claude Sonnet 4"
	case strings.Contains(id, "claude-haiku-4"):
		return "Claude Haiku 4.5"
	case strings.Contains(id, "claude-3-5-sonnet"):
		return "Claude 3.5 Sonnet"
	case strings.Contains(id, "claude-3-5-haiku"):
		return "Claude 3.5 Haiku"
	default:
		return ""
	}
}

func getShellInfoLine(shell, platform string) string {
	if shell == "" {
		shell = detectShell()
	}
	shellName := shell
	if strings.Contains(shell, "zsh") {
		shellName = "zsh"
	} else if strings.Contains(shell, "bash") {
		shellName = "bash"
	}
	if platform == "windows" {
		shellName = windowsPathToPosixPath(shellName)
		return fmt.Sprintf("Shell: %s (use Unix shell syntax, not Windows — e.g., /dev/null not NUL, forward slashes in paths)", shellName)
	}
	return fmt.Sprintf("Shell: %s", shellName)
}

// windowsPathToPosixPath converts a Windows-style path to POSIX format.
// Maps to windowsPathToPosixPath() in prompts.ts.
// Example: "C:\\Windows\\System32\\bash.exe" → "/c/Windows/System32/bash.exe"
func windowsPathToPosixPath(p string) string {
	if len(p) < 2 || p[1] != ':' {
		return strings.ReplaceAll(p, "\\", "/")
	}
	drive := strings.ToLower(string(p[0]))
	rest := strings.ReplaceAll(p[2:], "\\", "/")
	return "/" + drive + rest
}

func detectShell() string {
	shell := os.Getenv("SHELL")
	if shell != "" {
		return shell
	}
	if runtime.GOOS == "windows" {
		return "powershell"
	}
	return "/bin/bash"
}

// getUnameSR returns the OS type and release, equivalent to `uname -sr`.
// Maps to getUnameSR() in prompts.ts.
func getUnameSR() string {
	if runtime.GOOS == "windows" {
		// On Windows, use "ver" or runtime info
		cmd := exec.Command("cmd", "/c", "ver")
		out, err := cmd.Output()
		if err == nil {
			return strings.TrimSpace(string(out))
		}
		return fmt.Sprintf("Windows %s", runtime.GOARCH)
	}
	cmd := exec.Command("uname", "-sr")
	out, err := cmd.Output()
	if err != nil {
		return fmt.Sprintf("%s %s", runtime.GOOS, runtime.GOARCH)
	}
	return strings.TrimSpace(string(out))
}
