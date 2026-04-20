package memory

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// ---------------------------------------------------------------------------
// M11 · Memory age tests.
// ---------------------------------------------------------------------------

func TestMemoryAgeDays_Today(t *testing.T) {
	now := time.Now().UnixMilli()
	assert.Equal(t, 0, MemoryAgeDays(now))
}

func TestMemoryAgeDays_Yesterday(t *testing.T) {
	yesterday := time.Now().Add(-25 * time.Hour).UnixMilli()
	assert.Equal(t, 1, MemoryAgeDays(yesterday))
}

func TestMemoryAgeDays_Old(t *testing.T) {
	old := time.Now().Add(-10 * 24 * time.Hour).UnixMilli()
	assert.Equal(t, 10, MemoryAgeDays(old))
}

func TestMemoryAgeDays_Future(t *testing.T) {
	future := time.Now().Add(24 * time.Hour).UnixMilli()
	assert.Equal(t, 0, MemoryAgeDays(future), "future clamps to 0")
}

func TestMemoryAge_Today(t *testing.T) {
	now := time.Now().UnixMilli()
	assert.Equal(t, "today", MemoryAge(now))
}

func TestMemoryAge_Yesterday(t *testing.T) {
	yesterday := time.Now().Add(-25 * time.Hour).UnixMilli()
	assert.Equal(t, "yesterday", MemoryAge(yesterday))
}

func TestMemoryAge_NDays(t *testing.T) {
	old := time.Now().Add(-47 * 24 * time.Hour).UnixMilli()
	assert.Equal(t, "47 days ago", MemoryAge(old))
}

func TestMemoryFreshnessText_Fresh(t *testing.T) {
	now := time.Now().UnixMilli()
	assert.Equal(t, "", MemoryFreshnessText(now))

	yesterday := time.Now().Add(-25 * time.Hour).UnixMilli()
	assert.Equal(t, "", MemoryFreshnessText(yesterday))
}

func TestMemoryFreshnessText_Stale(t *testing.T) {
	old := time.Now().Add(-5 * 24 * time.Hour).UnixMilli()
	text := MemoryFreshnessText(old)
	assert.Contains(t, text, "5 days old")
	assert.Contains(t, text, "point-in-time observations")
	assert.Contains(t, text, "Verify against current code")
}

func TestMemoryFreshnessNote_Fresh(t *testing.T) {
	now := time.Now().UnixMilli()
	assert.Equal(t, "", MemoryFreshnessNote(now))
}

func TestMemoryFreshnessNote_Stale(t *testing.T) {
	old := time.Now().Add(-3 * 24 * time.Hour).UnixMilli()
	note := MemoryFreshnessNote(old)
	assert.True(t, strings.HasPrefix(note, "<system-reminder>"))
	assert.True(t, strings.HasSuffix(note, "</system-reminder>\n"))
	assert.Contains(t, note, "3 days old")
}
