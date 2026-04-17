package compact_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/wall-ai/ubuilding/backend/agents/compact"
)

func TestDefaultTimeBasedMCConfig(t *testing.T) {
	c := compact.DefaultTimeBasedMCConfig()
	assert.False(t, c.Enabled)
	assert.Equal(t, compact.DefaultTimeBasedGapMinutes, c.GapThresholdMinutes)
	assert.Equal(t, compact.DefaultKeepRecent, c.KeepRecent)
}

func TestTimeBasedMCConfig_ShouldTrigger(t *testing.T) {
	now := time.Now()

	// Disabled — never triggers.
	c := compact.TimeBasedMCConfig{Enabled: false, GapThresholdMinutes: 1}
	assert.False(t, c.ShouldTrigger(now, now.Add(-2*time.Hour)))

	// Enabled, gap below threshold.
	c = compact.TimeBasedMCConfig{Enabled: true, GapThresholdMinutes: 60}
	assert.False(t, c.ShouldTrigger(now, now.Add(-30*time.Minute)))

	// Enabled, gap above threshold.
	assert.True(t, c.ShouldTrigger(now, now.Add(-61*time.Minute)))

	// Enabled, zero lastAssistantAt — skip.
	assert.False(t, c.ShouldTrigger(now, time.Time{}))
}

func TestTimeBasedMCConfig_EffectiveKeepRecent(t *testing.T) {
	assert.Equal(t, compact.DefaultKeepRecent, compact.TimeBasedMCConfig{}.EffectiveKeepRecent())
	assert.Equal(t, 3, compact.TimeBasedMCConfig{KeepRecent: 3}.EffectiveKeepRecent())
}
