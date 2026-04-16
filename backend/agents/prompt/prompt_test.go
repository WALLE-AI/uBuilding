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
