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
