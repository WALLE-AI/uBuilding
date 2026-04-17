package compact_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/wall-ai/ubuilding/backend/agents/compact"
)

func TestGetAPIContextManagement_EmptyWhenNoStrategies(t *testing.T) {
	assert.Nil(t, compact.GetAPIContextManagement(compact.APIContextOptions{}))
}

func TestGetAPIContextManagement_ThinkingKeepAll(t *testing.T) {
	cfg := compact.GetAPIContextManagement(compact.APIContextOptions{
		HasThinking: true,
	})
	if assert.NotNil(t, cfg) && assert.Len(t, cfg.Edits, 1) {
		e := cfg.Edits[0]
		assert.Equal(t, "clear_thinking_20251015", e.Type)
		if assert.NotNil(t, e.KeepThinking) {
			assert.Equal(t, compact.ThinkingKeepAll, e.KeepThinking.Mode)
		}
	}
}

func TestGetAPIContextManagement_ThinkingRedactedDisables(t *testing.T) {
	cfg := compact.GetAPIContextManagement(compact.APIContextOptions{
		HasThinking:            true,
		IsRedactThinkingActive: true,
	})
	assert.Nil(t, cfg)
}

func TestGetAPIContextManagement_ClearAllThinkingUsesValue1(t *testing.T) {
	cfg := compact.GetAPIContextManagement(compact.APIContextOptions{
		HasThinking:      true,
		ClearAllThinking: true,
	})
	if assert.NotNil(t, cfg) && assert.Len(t, cfg.Edits, 1) {
		k := cfg.Edits[0].KeepThinking
		if assert.NotNil(t, k) {
			assert.Equal(t, "thinking_turns", k.Type)
			assert.Equal(t, 1, k.Value)
		}
	}
}

func TestGetAPIContextManagement_ClearToolResults(t *testing.T) {
	cfg := compact.GetAPIContextManagement(compact.APIContextOptions{
		UseClearToolResults: true,
	})
	if assert.NotNil(t, cfg) && assert.Len(t, cfg.Edits, 1) {
		e := cfg.Edits[0]
		assert.Equal(t, "clear_tool_uses_20250919", e.Type)
		assert.NotNil(t, e.Trigger)
		assert.Equal(t, compact.DefaultMaxInputTokens, e.Trigger.Value)
		assert.NotNil(t, e.ClearAtLeast)
		assert.Equal(t, compact.DefaultMaxInputTokens-compact.DefaultTargetInputTokens, e.ClearAtLeast.Value)
		assert.ElementsMatch(t, compact.ToolsClearableResults, e.ClearToolInputs)
		assert.Empty(t, e.ExcludeTools)
	}
}

func TestGetAPIContextManagement_BothStrategiesAndCustomThresholds(t *testing.T) {
	cfg := compact.GetAPIContextManagement(compact.APIContextOptions{
		UseClearToolResults: true,
		UseClearToolUses:    true,
		MaxInputTokens:      100_000,
		TargetInputTokens:   20_000,
	})
	if assert.NotNil(t, cfg) && assert.Len(t, cfg.Edits, 2) {
		for _, e := range cfg.Edits {
			assert.Equal(t, 100_000, e.Trigger.Value)
			assert.Equal(t, 80_000, e.ClearAtLeast.Value)
		}
		// results strategy
		assert.ElementsMatch(t, compact.ToolsClearableResults, cfg.Edits[0].ClearToolInputs)
		// uses strategy
		assert.ElementsMatch(t, compact.ToolsClearableUses, cfg.Edits[1].ExcludeTools)
	}
}
