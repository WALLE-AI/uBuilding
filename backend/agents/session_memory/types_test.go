package session_memory

import (
	"testing"

	"github.com/wall-ai/ubuilding/backend/agents/compact"
)

// TestSessionMemoryCompactConfig_ReExport guarantees the re-exported
// alias stays in lock-step with the canonical compact type. If the
// underlying type changes shape this test fails loudly.
func TestSessionMemoryCompactConfig_ReExport(t *testing.T) {
	var got SessionMemoryCompactConfig = compact.DefaultSessionMemoryCompactConfig
	if got.MinTokens != DefaultSessionMemoryCompactConfig.MinTokens ||
		got.MinTextBlockMessages != DefaultSessionMemoryCompactConfig.MinTextBlockMessages ||
		got.MaxTokens != DefaultSessionMemoryCompactConfig.MaxTokens {
		t.Fatalf("re-exported default mismatch: %+v vs %+v",
			got, DefaultSessionMemoryCompactConfig)
	}
}

// TestSessionState_ZeroValueSafe ensures the declared skeleton survives
// a plain `var s SessionState` — no panics, Initialized defaults to
// false, Time fields are zero.
func TestSessionState_ZeroValueSafe(t *testing.T) {
	var s SessionState
	if s.Initialized {
		t.Errorf("zero value Initialized = true; want false")
	}
	if !s.ExtractionStartedAt.IsZero() {
		t.Errorf("zero value ExtractionStartedAt = %v; want zero", s.ExtractionStartedAt)
	}
	if s.LastSummarizedMessageID != "" || s.TokensAtLastExtraction != 0 {
		t.Errorf("zero value has non-zero fields: %+v", s)
	}
}

// TestSessionMemoryConfig_ZeroValue verifies the skeleton struct can be
// constructed without panic; actual defaults arrive with M10.I1.
func TestSessionMemoryConfig_ZeroValue(t *testing.T) {
	var c SessionMemoryConfig
	if c.MinimumMessageTokensToInit != 0 ||
		c.MinimumTokensBetweenUpdate != 0 ||
		c.ToolCallsBetweenUpdates != 0 {
		t.Errorf("expected zero-value struct; got %+v", c)
	}
}
