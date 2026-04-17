package prompt_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wall-ai/ubuilding/backend/agents/prompt"
)

func TestGetSystemPrompt_ReturnsNonEmpty(t *testing.T) {
	cfg := prompt.GetSystemPromptConfig{
		Cwd:   "/tmp/test",
		Model: "claude-sonnet-4-20250514",
	}
	sections := prompt.GetSystemPrompt(cfg)
	require.True(t, len(sections) > 0, "system prompt should have at least one section")
}

func TestGetSystemPrompt_ContainsCyberRiskInstruction(t *testing.T) {
	cfg := prompt.GetSystemPromptConfig{
		Cwd:   "/tmp/test",
		Model: "claude-sonnet-4-20250514",
	}
	sections := prompt.GetSystemPrompt(cfg)
	combined := strings.Join(sections, "\n")
	assert.Contains(t, combined, "Assist with authorized security testing")
}

func TestBuildEffectiveSystemPrompt_OverrideTakesPriority(t *testing.T) {
	result := prompt.BuildEffectiveSystemPrompt(prompt.EffectiveSystemPromptConfig{
		DefaultSystemPrompt:  []string{"default"},
		CustomSystemPrompt:   "custom",
		AppendSystemPrompt:   "append",
		OverrideSystemPrompt: "override",
	})
	assert.Equal(t, "override", result)
}

func TestBuildEffectiveSystemPrompt_CoordinatorMode(t *testing.T) {
	result := prompt.BuildEffectiveSystemPrompt(prompt.EffectiveSystemPromptConfig{
		DefaultSystemPrompt:     []string{"default"},
		IsCoordinatorMode:       true,
		CoordinatorSystemPrompt: "coordinator prompt",
		AppendSystemPrompt:      "append",
	})
	assert.Contains(t, result, "coordinator prompt")
	assert.Contains(t, result, "append")
}

func TestBuildEffectiveSystemPrompt_AgentSystemPrompt(t *testing.T) {
	result := prompt.BuildEffectiveSystemPrompt(prompt.EffectiveSystemPromptConfig{
		DefaultSystemPrompt: []string{"default"},
		AgentSystemPrompt:   "agent prompt",
		AppendSystemPrompt:  "append",
	})
	assert.Contains(t, result, "agent prompt")
	assert.Contains(t, result, "append")
	assert.NotContains(t, result, "default")
}

func TestBuildEffectiveSystemPrompt_ProactiveMode(t *testing.T) {
	result := prompt.BuildEffectiveSystemPrompt(prompt.EffectiveSystemPromptConfig{
		DefaultSystemPrompt: []string{"default"},
		AgentSystemPrompt:   "agent prompt",
		IsProactiveMode:     true,
	})
	assert.Contains(t, result, "default")
	assert.Contains(t, result, "Custom Agent Instructions")
	assert.Contains(t, result, "agent prompt")
}

func TestBuildEffectiveSystemPrompt_CustomOverridesDefault(t *testing.T) {
	result := prompt.BuildEffectiveSystemPrompt(prompt.EffectiveSystemPromptConfig{
		DefaultSystemPrompt: []string{"default"},
		CustomSystemPrompt:  "custom",
	})
	assert.Contains(t, result, "custom")
	assert.NotContains(t, result, "default")
}

func TestBuildEffectiveSystemPrompt_DefaultFallback(t *testing.T) {
	result := prompt.BuildEffectiveSystemPrompt(prompt.EffectiveSystemPromptConfig{
		DefaultSystemPrompt: []string{"default part 1", "default part 2"},
	})
	assert.Contains(t, result, "default part 1")
	assert.Contains(t, result, "default part 2")
}

func TestSectionCache_Memoization(t *testing.T) {
	cache := prompt.NewSectionCache()
	callCount := 0
	sections := []prompt.SystemPromptSectionDef{
		prompt.NewSystemPromptSection("test", func() (string, error) {
			callCount++
			return "computed", nil
		}),
	}

	// First resolve — should compute
	vals, err := prompt.ResolveSystemPromptSections(sections, cache)
	require.NoError(t, err)
	assert.Equal(t, "computed", vals[0])
	assert.Equal(t, 1, callCount)

	// Second resolve — should use cache
	vals, err = prompt.ResolveSystemPromptSections(sections, cache)
	require.NoError(t, err)
	assert.Equal(t, "computed", vals[0])
	assert.Equal(t, 1, callCount) // NOT incremented

	// Clear and re-resolve — should re-compute
	cache.Clear()
	vals, err = prompt.ResolveSystemPromptSections(sections, cache)
	require.NoError(t, err)
	assert.Equal(t, "computed", vals[0])
	assert.Equal(t, 2, callCount)
}

func TestSectionCache_UncachedSection(t *testing.T) {
	cache := prompt.NewSectionCache()
	callCount := 0
	sections := []prompt.SystemPromptSectionDef{
		prompt.NewDangerousUncachedSection("uncached", func() (string, error) {
			callCount++
			return "fresh", nil
		}, "test reason"),
	}

	// Every resolve should compute
	vals, err := prompt.ResolveSystemPromptSections(sections, cache)
	require.NoError(t, err)
	assert.Equal(t, "fresh", vals[0])
	vals, err = prompt.ResolveSystemPromptSections(sections, cache)
	require.NoError(t, err)
	assert.Equal(t, "fresh", vals[0])
	assert.Equal(t, 2, callCount) // Called twice
}

func TestBuildFullSystemPrompt_EndToEnd(t *testing.T) {
	sys, uc, sc := prompt.BuildFullSystemPrompt(prompt.FullBuildConfig{
		PromptConfig: prompt.GetSystemPromptConfig{
			Cwd:   "/tmp/test",
			Model: "claude-sonnet-4-20250514",
		},
	})

	assert.NotEmpty(t, sys, "system prompt should not be empty")
	// userContext and systemContext may be nil/empty in test env
	_ = uc
	_ = sc
}

func TestBuildFullSystemPrompt_WithOverride(t *testing.T) {
	sys, _, _ := prompt.BuildFullSystemPrompt(prompt.FullBuildConfig{
		PromptConfig: prompt.GetSystemPromptConfig{
			Cwd:   "/tmp/test",
			Model: "claude-sonnet-4-20250514",
		},
		OverrideSystemPrompt: "my override",
	})
	assert.Equal(t, "my override", sys)
}

func TestBuildFullSystemPrompt_WithMemoryMechanics(t *testing.T) {
	sys, _, _ := prompt.BuildFullSystemPrompt(prompt.FullBuildConfig{
		PromptConfig: prompt.GetSystemPromptConfig{
			Cwd:   "/tmp/test",
			Model: "claude-sonnet-4-20250514",
		},
		OverrideSystemPrompt:  "base",
		MemoryMechanicsPrompt: "memory instructions",
	})
	assert.Contains(t, sys, "base")
	assert.Contains(t, sys, "memory instructions")
}

func TestAppendSystemContextToPrompt(t *testing.T) {
	ctx := map[string]string{
		"git_status": "on branch main",
	}
	result := prompt.AppendSystemContextToPrompt("base prompt", ctx)
	assert.Contains(t, result, "base prompt")
	assert.Contains(t, result, "<git_status>")
	assert.Contains(t, result, "on branch main")
	assert.Contains(t, result, "</git_status>")
}

func TestComputeSimpleEnvInfo(t *testing.T) {
	cfg := prompt.EnvInfoConfig{
		Cwd:      "/home/user/project",
		Platform: "linux",
		Shell:    "/bin/bash",
	}
	result := prompt.ComputeSimpleEnvInfo(cfg)
	assert.Contains(t, result, "/home/user/project")
	assert.Contains(t, result, "linux")
	assert.Contains(t, result, "bash")
}

func TestGetSessionSpecificGuidanceSection(t *testing.T) {
	tools := map[string]bool{
		"Read":  true,
		"Write": true,
	}
	result := prompt.GetSessionSpecificGuidanceSection(tools, false, false)
	// Should return some guidance string (may or may not be empty depending on tool set)
	_ = result
}

// ---------------------------------------------------------------------------
// E-1: Golden-text comparison tests
// ---------------------------------------------------------------------------

func TestGetSimpleIntroSection_ContainsKeyPhrases(t *testing.T) {
	result := prompt.GetSimpleIntroSection("")
	assert.Contains(t, result, "interactive agent")
	assert.Contains(t, result, "software engineering tasks")
	assert.Contains(t, result, "IMPORTANT: You must NEVER generate or guess URLs")
	assert.Contains(t, result, prompt.CyberRiskInstruction)
}

func TestGetSimpleIntroSection_WithOutputStyle(t *testing.T) {
	result := prompt.GetSimpleIntroSection("concise")
	assert.Contains(t, result, `Output Style`)
	assert.NotContains(t, result, "software engineering tasks")
}

func TestGetSimpleSystemSection_ContainsAllBullets(t *testing.T) {
	result := prompt.GetSimpleSystemSection()
	assert.Contains(t, result, "# System")
	assert.Contains(t, result, "Github-flavored markdown")
	assert.Contains(t, result, "permission mode")
	assert.Contains(t, result, "<system-reminder>")
	assert.Contains(t, result, "prompt injection")
	assert.Contains(t, result, "hooks")
	assert.Contains(t, result, "automatically compress")
}

func TestGetSimpleDoingTasksSection_External(t *testing.T) {
	result := prompt.GetSimpleDoingTasksSection(false)
	assert.Contains(t, result, "# Doing tasks")
	assert.Contains(t, result, "software engineering tasks")
	assert.NotContains(t, result, "Default to writing no comments")
	assert.NotContains(t, result, "recommend the appropriate slash command: /issue")
}

func TestGetSimpleDoingTasksSection_Ant(t *testing.T) {
	result := prompt.GetSimpleDoingTasksSection(true)
	assert.Contains(t, result, "Default to writing no comments")
	assert.Contains(t, result, "Report outcomes faithfully")
	assert.Contains(t, result, "/issue")
	assert.Contains(t, result, "/share")
}

func TestGetActionsSection_ContainsKeyPhrases(t *testing.T) {
	result := prompt.GetActionsSection()
	assert.Contains(t, result, "Executing actions with care")
	assert.Contains(t, result, "Destructive operations")
	assert.Contains(t, result, "measure twice, cut once")
}

func TestGetUsingYourToolsSection_WithAllTools(t *testing.T) {
	tools := map[string]bool{
		prompt.BashToolName:      true,
		prompt.FileReadToolName:  true,
		prompt.TodoWriteToolName: true,
	}
	result := prompt.GetUsingYourToolsSection(tools, false)
	assert.Contains(t, result, "# Using your tools")
	assert.Contains(t, result, prompt.FileReadToolName)
	assert.Contains(t, result, prompt.TodoWriteToolName)
	assert.Contains(t, result, "parallel")
}

func TestGetProactiveSection_ContainsAllSubsections(t *testing.T) {
	result := prompt.GetProactiveSection()
	assert.Contains(t, result, "# Autonomous work")
	assert.Contains(t, result, "## Pacing")
	assert.Contains(t, result, "## First wake-up")
	assert.Contains(t, result, "## What to do on subsequent wake-ups")
	assert.Contains(t, result, "## Staying responsive")
	assert.Contains(t, result, "## Bias toward action")
	assert.Contains(t, result, "## Be concise")
	assert.Contains(t, result, "## Terminal focus")
	assert.Contains(t, result, prompt.SleepToolName)
}

func TestGetOutputEfficiencySection_Ant(t *testing.T) {
	result := prompt.GetOutputEfficiencySection(true)
	assert.Contains(t, result, "Communicating with the user")
	assert.NotContains(t, result, "Output efficiency")
}

func TestGetOutputEfficiencySection_External(t *testing.T) {
	result := prompt.GetOutputEfficiencySection(false)
	assert.Contains(t, result, "Output efficiency")
	assert.Contains(t, result, "Go straight to the point")
}

// ---------------------------------------------------------------------------
// E-2: GetSystemPrompt assembly tests
// ---------------------------------------------------------------------------

func TestGetSystemPrompt_DefaultMode_SectionOrder(t *testing.T) {
	cfg := prompt.GetSystemPromptConfig{
		Cwd:      "/tmp/test",
		Model:    "claude-sonnet-4-20250514",
		Platform: "linux",
		Shell:    "/bin/bash",
	}
	sections := prompt.GetSystemPrompt(cfg)
	combined := strings.Join(sections, "\n")

	// Verify key sections present in order
	introIdx := strings.Index(combined, "interactive agent")
	systemIdx := strings.Index(combined, "# System")
	envIdx := strings.Index(combined, "# Environment")

	assert.True(t, introIdx < systemIdx, "intro should come before system")
	assert.True(t, systemIdx < envIdx, "system should come before env")
}

func TestGetSystemPrompt_ProactivePath(t *testing.T) {
	cfg := prompt.GetSystemPromptConfig{
		Cwd:               "/tmp/test",
		Model:             "claude-sonnet-4-20250514",
		Platform:          "linux",
		Shell:             "/bin/bash",
		IsProactiveActive: true,
		MemoryPrompt:      "my memories",
	}
	sections := prompt.GetSystemPrompt(cfg)
	combined := strings.Join(sections, "\n")

	// Proactive path should have different structure
	assert.Contains(t, combined, "autonomous agent")
	assert.Contains(t, combined, "# Autonomous work")
	assert.Contains(t, combined, "my memories")
	// Should NOT contain the full static sections
	assert.NotContains(t, combined, "# Doing tasks")
	assert.NotContains(t, combined, "# Using your tools")
}

func TestGetSystemPrompt_MemorySlot(t *testing.T) {
	cfg := prompt.GetSystemPromptConfig{
		Cwd:          "/tmp/test",
		Model:        "claude-sonnet-4-20250514",
		Platform:     "linux",
		Shell:        "/bin/bash",
		MemoryPrompt: "# CLAUDE.md\nRemember to use gofmt",
	}
	sections := prompt.GetSystemPrompt(cfg)
	combined := strings.Join(sections, "\n")
	assert.Contains(t, combined, "Remember to use gofmt")
}

func TestGetSystemPrompt_BriefSection(t *testing.T) {
	cfg := prompt.GetSystemPromptConfig{
		Cwd:          "/tmp/test",
		Model:        "claude-sonnet-4-20250514",
		Platform:     "linux",
		Shell:        "/bin/bash",
		BriefEnabled: true,
		BriefSection: "Use /brief to toggle brief mode.",
	}
	sections := prompt.GetSystemPrompt(cfg)
	combined := strings.Join(sections, "\n")
	assert.Contains(t, combined, "Use /brief to toggle brief mode.")
}

func TestGetSystemPrompt_BriefSkippedWhenProactive(t *testing.T) {
	cfg := prompt.GetSystemPromptConfig{
		Cwd:               "/tmp/test",
		Model:             "claude-sonnet-4-20250514",
		Platform:          "linux",
		Shell:             "/bin/bash",
		BriefEnabled:      true,
		BriefSection:      "Use /brief to toggle brief mode.",
		IsProactiveActive: true,
	}
	sections := prompt.GetSystemPrompt(cfg)
	combined := strings.Join(sections, "\n")
	// Brief section should not appear in proactive path
	assert.NotContains(t, combined, "Use /brief to toggle brief mode.")
}

func TestGetSystemPrompt_McpDeltaSkipsMcpInstructions(t *testing.T) {
	cfg := prompt.GetSystemPromptConfig{
		Cwd:      "/tmp/test",
		Model:    "claude-sonnet-4-20250514",
		Platform: "linux",
		Shell:    "/bin/bash",
		MCPClients: []prompt.MCPClient{
			{Name: "test-server", Instructions: "Use the test tool", Connected: true},
		},
		McpDeltaEnabled: true,
	}
	sections := prompt.GetSystemPrompt(cfg)
	combined := strings.Join(sections, "\n")
	// MCP instructions should NOT be present when delta enabled
	assert.NotContains(t, combined, "MCP Server Instructions")
}

func TestGetSystemPrompt_McpInstructionsPresent(t *testing.T) {
	cfg := prompt.GetSystemPromptConfig{
		Cwd:      "/tmp/test",
		Model:    "claude-sonnet-4-20250514",
		Platform: "linux",
		Shell:    "/bin/bash",
		MCPClients: []prompt.MCPClient{
			{Name: "test-server", Instructions: "Use the test tool", Connected: true},
		},
	}
	sections := prompt.GetSystemPrompt(cfg)
	combined := strings.Join(sections, "\n")
	assert.Contains(t, combined, "MCP Server Instructions")
	assert.Contains(t, combined, "test-server")
}

func TestGetSystemPrompt_DynamicBoundary(t *testing.T) {
	cfg := prompt.GetSystemPromptConfig{
		Cwd:            "/tmp/test",
		Model:          "claude-sonnet-4-20250514",
		Platform:       "linux",
		Shell:          "/bin/bash",
		UseGlobalCache: true,
	}
	sections := prompt.GetSystemPrompt(cfg)
	found := false
	for _, s := range sections {
		if s == prompt.SystemPromptDynamicBoundary {
			found = true
			break
		}
	}
	assert.True(t, found, "should contain dynamic boundary when UseGlobalCache is true")
}

func TestGetSystemPrompt_NoDynamicBoundary(t *testing.T) {
	cfg := prompt.GetSystemPromptConfig{
		Cwd:            "/tmp/test",
		Model:          "claude-sonnet-4-20250514",
		Platform:       "linux",
		Shell:          "/bin/bash",
		UseGlobalCache: false,
	}
	sections := prompt.GetSystemPrompt(cfg)
	for _, s := range sections {
		assert.NotEqual(t, prompt.SystemPromptDynamicBoundary, s)
	}
}

func TestGetSessionSpecificGuidanceSection_ForkAgent(t *testing.T) {
	tools := map[string]bool{
		prompt.AgentToolName: true,
	}
	resultFork := prompt.GetSessionSpecificGuidanceSection(tools, false, false, prompt.WithForkSubagent(true))
	assert.Contains(t, resultFork, "fork")
	assert.NotContains(t, resultFork, "specialized agents")

	resultNonFork := prompt.GetSessionSpecificGuidanceSection(tools, false, false, prompt.WithForkSubagent(false))
	assert.Contains(t, resultNonFork, "specialized agents")
}

// ---------------------------------------------------------------------------
// E-3: Additional env info and helper tests
// ---------------------------------------------------------------------------

func TestComputeSimpleEnvInfo_Worktree(t *testing.T) {
	cfg := prompt.EnvInfoConfig{
		Cwd:        "/home/user/project-wt",
		Platform:   "linux",
		Shell:      "/bin/bash",
		IsWorktree: true,
	}
	result := prompt.ComputeSimpleEnvInfo(cfg)
	assert.Contains(t, result, "git worktree")
	assert.Contains(t, result, "Do NOT")
}

func TestComputeSimpleEnvInfo_ModelFamilyIDs(t *testing.T) {
	cfg := prompt.EnvInfoConfig{
		Cwd:      "/tmp/test",
		Platform: "linux",
		Shell:    "/bin/bash",
		Model:    "claude-sonnet-4-6-20250514",
	}
	result := prompt.ComputeSimpleEnvInfo(cfg)
	assert.Contains(t, result, prompt.ModelFamilyIDs.Opus)
	assert.Contains(t, result, prompt.ModelFamilyIDs.Sonnet)
}

func TestComputeSimpleEnvInfo_Undercover(t *testing.T) {
	cfg := prompt.EnvInfoConfig{
		Cwd:          "/tmp/test",
		Platform:     "linux",
		Shell:        "/bin/bash",
		Model:        "claude-sonnet-4-6-20250514",
		IsAnt:        true,
		IsUndercover: true,
	}
	result := prompt.ComputeSimpleEnvInfo(cfg)
	// In undercover mode, model family IDs should be suppressed
	assert.NotContains(t, result, prompt.ModelFamilyIDs.Opus)
	assert.NotContains(t, result, "powered by")
}

func TestGetBriefSection(t *testing.T) {
	// Enabled and not proactive
	result := prompt.GetBriefSection(true, false, "brief text")
	assert.Equal(t, "brief text", result)

	// Disabled
	result = prompt.GetBriefSection(false, false, "brief text")
	assert.Equal(t, "", result)

	// Proactive active — should skip
	result = prompt.GetBriefSection(true, true, "brief text")
	assert.Equal(t, "", result)

	// Empty section text
	result = prompt.GetBriefSection(true, false, "")
	assert.Equal(t, "", result)
}

func TestGetKnowledgeCutoff(t *testing.T) {
	assert.Equal(t, "August 2025", prompt.GetKnowledgeCutoff("claude-sonnet-4-6-20250801"))
	assert.Equal(t, "May 2025", prompt.GetKnowledgeCutoff("claude-opus-4-6-20250601"))
	assert.Equal(t, "February 2025", prompt.GetKnowledgeCutoff("claude-haiku-4-5-20251001"))
	assert.Equal(t, "January 2025", prompt.GetKnowledgeCutoff("claude-sonnet-4-20250514"))
	assert.Equal(t, "", prompt.GetKnowledgeCutoff("unknown-model"))
}

func TestEnhanceSystemPromptWithEnvDetails(t *testing.T) {
	existing := []string{"base prompt"}
	cfg := prompt.EnvInfoConfig{
		Cwd:      "/tmp/test",
		Platform: "linux",
		Shell:    "/bin/bash",
	}
	result := prompt.EnhanceSystemPromptWithEnvDetails(existing, cfg, nil)

	assert.True(t, len(result) >= 3, "should have base + notes + env")
	assert.Equal(t, "base prompt", result[0])
	assert.Contains(t, result[1], "absolute file paths")
	// Should use legacy XML format for subagents
	found := false
	for _, s := range result {
		if strings.Contains(s, "<env>") {
			found = true
			break
		}
	}
	assert.True(t, found, "subagent should use legacy XML env format")
}

func TestEnhanceSystemPromptWithEnvDetails_WithDiscoverSkills(t *testing.T) {
	existing := []string{"base prompt"}
	cfg := prompt.EnvInfoConfig{
		Cwd:      "/tmp/test",
		Platform: "linux",
		Shell:    "/bin/bash",
	}
	tools := map[string]bool{
		prompt.DiscoverSkillsToolName: true,
	}
	result := prompt.EnhanceSystemPromptWithEnvDetails(existing, cfg, tools)
	combined := strings.Join(result, "\n")
	assert.Contains(t, combined, "Skills relevant to your task")
}

// ---------------------------------------------------------------------------
// Phase A7: Golden-substring tests per section — guard against regressions in
// prompt text. One assertion per section, targeting a load-bearing bullet.
// ---------------------------------------------------------------------------

func TestSection_Intro_ContainsIdentity(t *testing.T) {
	s := prompt.GetSimpleIntroSection("")
	assert.Contains(t, s, "You are an interactive agent")
	assert.Contains(t, s, "software engineering tasks")
	assert.Contains(t, s, "NEVER generate or guess URLs")
}

func TestSection_System_AllBullets(t *testing.T) {
	s := prompt.GetSimpleSystemSection()
	assert.Contains(t, s, "# System")
	assert.Contains(t, s, "Github-flavored markdown")
	assert.Contains(t, s, "user-selected permission mode")
	assert.Contains(t, s, "<system-reminder>")
	assert.Contains(t, s, "prompt injection")
	assert.Contains(t, s, "automatically compress prior messages")
}

func TestSection_DoingTasks_Baseline(t *testing.T) {
	s := prompt.GetSimpleDoingTasksSection(false)
	assert.Contains(t, s, "# Doing tasks")
	assert.Contains(t, s, "software engineering tasks")
	assert.Contains(t, s, "do not propose changes to code you haven't read")
	assert.Contains(t, s, "security vulnerabilities")
}

func TestSection_DoingTasks_AntAddsFaithfulReporting(t *testing.T) {
	s := prompt.GetSimpleDoingTasksSection(true)
	assert.Contains(t, s, "Report outcomes faithfully")
	assert.Contains(t, s, "collaborator")
}

func TestSection_Actions_RiskyBullets(t *testing.T) {
	s := prompt.GetActionsSection()
	assert.Contains(t, s, "# Executing actions with care")
	assert.Contains(t, s, "Destructive operations")
	assert.Contains(t, s, "Hard-to-reverse")
	assert.Contains(t, s, "measure twice, cut once")
}

func TestSection_UsingTools_DedicatedToolsGuidance(t *testing.T) {
	tools := map[string]bool{prompt.TodoWriteToolName: true}
	s := prompt.GetUsingYourToolsSection(tools, false)
	assert.Contains(t, s, "# Using your tools")
	assert.Contains(t, s, "Do NOT use the Bash")
	assert.Contains(t, s, "To read files use Read")
	assert.Contains(t, s, "To search for files use Glob")
	assert.Contains(t, s, "multiple tools in a single response")
}

func TestSection_UsingTools_EmbeddedSearchSkipsGlobGrep(t *testing.T) {
	s := prompt.GetUsingYourToolsSection(map[string]bool{}, true)
	assert.NotContains(t, s, "To search for files use Glob")
	assert.NotContains(t, s, "To search the content of files")
}

func TestSection_ToneAndStyle_Baseline(t *testing.T) {
	s := prompt.GetSimpleToneAndStyleSection(false)
	assert.Contains(t, s, "# Tone and style")
	assert.Contains(t, s, "Only use emojis")
	assert.Contains(t, s, "short and concise")
	assert.Contains(t, s, "file_path:line_number")
	assert.Contains(t, s, "owner/repo#123")
}

func TestSection_ToneAndStyle_AntSkipsShortConcise(t *testing.T) {
	s := prompt.GetSimpleToneAndStyleSection(true)
	assert.NotContains(t, s, "Your responses should be short and concise.")
}

func TestSection_OutputEfficiency_NonAnt(t *testing.T) {
	s := prompt.GetOutputEfficiencySection(false)
	assert.Contains(t, s, "# Output efficiency")
	assert.Contains(t, s, "Go straight to the point")
}

func TestSection_OutputEfficiency_Ant(t *testing.T) {
	s := prompt.GetOutputEfficiencySection(true)
	assert.Contains(t, s, "# Communicating with the user")
	assert.Contains(t, s, "inverted pyramid")
}

func TestSection_Hooks_TextParity(t *testing.T) {
	s := prompt.GetHooksSection()
	assert.Contains(t, s, "hooks")
	assert.Contains(t, s, "<user-prompt-submit-hook>")
}

func TestSection_SystemReminders_TwoBullets(t *testing.T) {
	s := prompt.GetSystemRemindersSection()
	assert.Contains(t, s, "<system-reminder>")
	assert.Contains(t, s, "unlimited context")
}

func TestSection_Language_EmptyWhenUnset(t *testing.T) {
	assert.Equal(t, "", prompt.GetLanguageSection(""))
	s := prompt.GetLanguageSection("中文")
	assert.Contains(t, s, "# Language")
	assert.Contains(t, s, "中文")
}

func TestSection_OutputStyle_EmptyWhenUnset(t *testing.T) {
	assert.Equal(t, "", prompt.GetOutputStyleSection("", ""))
	s := prompt.GetOutputStyleSection("Explanatory", "explain things")
	assert.Contains(t, s, "# Output Style: Explanatory")
	assert.Contains(t, s, "explain things")
}

func TestSection_McpInstructions_EmptyWhenNoClients(t *testing.T) {
	assert.Equal(t, "", prompt.GetMcpInstructionsSection(nil))
	clients := []prompt.MCPClient{
		{Name: "srv1", Instructions: "do X", Connected: true},
		{Name: "srv2", Instructions: "", Connected: true},      // filtered
		{Name: "srv3", Instructions: "do Z", Connected: false}, // filtered
	}
	s := prompt.GetMcpInstructionsSection(clients)
	assert.Contains(t, s, "# MCP Server Instructions")
	assert.Contains(t, s, "## srv1")
	assert.Contains(t, s, "do X")
	assert.NotContains(t, s, "srv2")
	assert.NotContains(t, s, "srv3")
}

func TestSection_Scratchpad_EmptyWhenUnset(t *testing.T) {
	assert.Equal(t, "", prompt.GetScratchpadInstructions(""))
	s := prompt.GetScratchpadInstructions("/var/run/scratch")
	assert.Contains(t, s, "# Scratchpad Directory")
	assert.Contains(t, s, "/var/run/scratch")
	assert.Contains(t, s, "Only use /tmp if the user explicitly requests it")
}

func TestSection_FRC_EmptyWhenDisabled(t *testing.T) {
	assert.Equal(t, "", prompt.GetFunctionResultClearingSection(false, 5))
	s := prompt.GetFunctionResultClearingSection(true, 7)
	assert.Contains(t, s, "# Function Result Clearing")
	assert.Contains(t, s, "7 most recent")
}

func TestSection_TokenBudget_TextParity(t *testing.T) {
	s := prompt.GetTokenBudgetSection()
	assert.Contains(t, s, "token target")
	assert.Contains(t, s, "hard minimum")
}

func TestSection_NumericLengthAnchors_TextParity(t *testing.T) {
	s := prompt.GetNumericLengthAnchorsSection()
	assert.Contains(t, s, "≤25 words")
	assert.Contains(t, s, "≤100 words")
}

func TestSection_Brief_GuardMatrix(t *testing.T) {
	// disabled → empty
	assert.Equal(t, "", prompt.GetBriefSection(false, false, "BRIEF"))
	// enabled but empty content → empty
	assert.Equal(t, "", prompt.GetBriefSection(true, false, ""))
	// enabled + proactive → empty (proactive path appends it)
	assert.Equal(t, "", prompt.GetBriefSection(true, true, "BRIEF"))
	// enabled normal
	assert.Equal(t, "BRIEF", prompt.GetBriefSection(true, false, "BRIEF"))
}

// ---------------------------------------------------------------------------
// Phase B4: computeSimpleEnvInfo line-ordering golden
// ---------------------------------------------------------------------------

func TestComputeSimpleEnvInfo_LineOrder(t *testing.T) {
	cfg := prompt.EnvInfoConfig{
		Cwd:             "/work/proj",
		IsGit:           true,
		Platform:        "linux",
		Shell:           "/bin/zsh",
		OSVersion:       "Linux 6.6.4",
		Model:           "claude-sonnet-4-6-20250801",
		KnowledgeCutoff: "August 2025",
		AdditionalDirs:  []string{"/work/a", "/work/b"},
	}
	result := prompt.ComputeSimpleEnvInfo(cfg)

	// Verify expected lines appear in this order.
	expected := []string{
		"# Environment",
		"Primary working directory: /work/proj",
		"Is a git repository: true",
		"Additional working directories:",
		"/work/a",
		"/work/b",
		"Platform: linux",
		"Shell: zsh",
		"OS Version: Linux 6.6.4",
		"You are powered by the model named Claude Sonnet 4.6",
		"Assistant knowledge cutoff is August 2025.",
		"most recent Claude model family is Claude 4.5/4.6",
		"Claude Code is available as a CLI",
		"Fast mode for Claude Code",
	}
	idx := 0
	for _, e := range expected {
		pos := strings.Index(result[idx:], e)
		if pos < 0 {
			t.Fatalf("expected substring %q missing or out-of-order; remaining output:\n%s", e, result[idx:])
		}
		idx += pos + len(e)
	}
}

func TestWindowsPathToPosixPath_ViaShellInfoLine(t *testing.T) {
	// When platform=windows, shell path is normalized; validate through getShellInfoLine.
	s := prompt.ComputeSimpleEnvInfo(prompt.EnvInfoConfig{
		Cwd:       "C:/work",
		Platform:  "windows",
		Shell:     `C:\Program Files\Git\bin\bash.exe`,
		OSVersion: "Windows 11",
	})
	assert.Contains(t, s, "use Unix shell syntax")
	// Path is normalized to posix "bash" or the drive form; assert forward slashes only.
	assert.NotContains(t, s, `C:\`)
}

// ---------------------------------------------------------------------------
// Phase G4: GetSystemPrompt golden snapshot — anchors the full assembly order.
// Stable-by-design: uses fixed config with no time/env bleed-through.
// ---------------------------------------------------------------------------

func TestGetSystemPrompt_GoldenAssembly(t *testing.T) {
	cfg := prompt.GetSystemPromptConfig{
		Cwd:               "/golden/cwd",
		Model:             "claude-sonnet-4-6-20250801",
		KnowledgeCutoff:   "August 2025",
		Platform:          "linux",
		Shell:             "/bin/bash",
		OSVersion:         "Linux 6.6.4",
		EnabledTools:      map[string]bool{prompt.TodoWriteToolName: true},
		HasEmbeddedSearch: false,
		IsAnt:             false,
		Language:          "",
		UseGlobalCache:    true,
	}
	sections := prompt.GetSystemPrompt(cfg)
	require.NotEmpty(t, sections)

	combined := strings.Join(sections, "\n---\n")

	// Required static sections (pre-boundary).
	wantBefore := []string{
		"You are an interactive agent",
		"# System",
		"# Doing tasks",
		"# Executing actions with care",
		"# Using your tools",
		"# Tone and style",
		"# Output efficiency",
	}

	// Required dynamic sections (post-boundary).
	wantAfter := []string{
		"# Environment",
		"Primary working directory: /golden/cwd",
		"claude-sonnet-4-6-20250801",
		"Assistant knowledge cutoff is August 2025",
	}

	// Anchor: DYNAMIC_BOUNDARY must appear exactly once and before every dynamic section.
	boundaryIdx := strings.Index(combined, prompt.SystemPromptDynamicBoundary)
	require.GreaterOrEqual(t, boundaryIdx, 0, "DYNAMIC_BOUNDARY missing")
	assert.Equal(t, boundaryIdx, strings.LastIndex(combined, prompt.SystemPromptDynamicBoundary), "DYNAMIC_BOUNDARY should appear once")

	// All static headings must precede the boundary.
	for _, s := range wantBefore {
		pos := strings.Index(combined, s)
		require.GreaterOrEqual(t, pos, 0, "missing static heading %q", s)
		assert.Less(t, pos, boundaryIdx, "static heading %q should be before DYNAMIC_BOUNDARY", s)
	}

	// All dynamic heading must follow the boundary.
	for _, s := range wantAfter {
		pos := strings.Index(combined, s)
		require.GreaterOrEqual(t, pos, 0, "missing dynamic content %q", s)
		assert.Greater(t, pos, boundaryIdx, "dynamic content %q should be after DYNAMIC_BOUNDARY", s)
	}

	// SummarizeToolResultsSection must appear (it's always included in dynamic zone).
	assert.Contains(t, combined, "write down any important information")
}

func TestBuildSubagentSystemPrompt_DefaultsToDefaultAgentPrompt(t *testing.T) {
	cfg := prompt.EnvInfoConfig{Cwd: "/tmp", Platform: "linux", Shell: "/bin/bash"}
	parts := prompt.BuildSubagentSystemPrompt("", cfg, nil)
	require.True(t, len(parts) >= 3)
	assert.Equal(t, prompt.DefaultAgentPrompt, parts[0])
	joined := strings.Join(parts, "\n")
	assert.Contains(t, joined, "Notes:")
	assert.Contains(t, joined, "<env>")
}

func TestBuildSubagentSystemPrompt_CustomOverridesDefault(t *testing.T) {
	cfg := prompt.EnvInfoConfig{Cwd: "/tmp", Platform: "linux", Shell: "/bin/bash"}
	parts := prompt.BuildSubagentSystemPrompt("CUSTOM SYSTEM", cfg, nil)
	assert.Equal(t, "CUSTOM SYSTEM", parts[0])
	assert.NotContains(t, parts[0], prompt.DefaultAgentPrompt)
}

func TestGetKnowledgeCutoff_AllFamilies(t *testing.T) {
	// B1 parity: every supported family resolves to a non-empty date.
	cases := map[string]string{
		"claude-sonnet-4-6-20250801": "August 2025",
		"claude-opus-4-6-20250601":   "May 2025",
		"claude-opus-4-5-20250505":   "May 2025",
		"claude-haiku-4-5-20251001":  "February 2025",
		"claude-opus-4-20250101":     "January 2025",
		"claude-sonnet-4-20250201":   "January 2025",
	}
	for model, want := range cases {
		assert.Equal(t, want, prompt.GetKnowledgeCutoff(model), "model=%s", model)
	}
	assert.Equal(t, "", prompt.GetKnowledgeCutoff("unknown-model"))
}
