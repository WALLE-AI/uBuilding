package session_memory

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestSessionStateManager_ZeroValue(t *testing.T) {
	m := NewSessionStateManager()
	s := m.GetState()
	assert.False(t, s.Initialized)
	assert.True(t, s.ExtractionStartedAt.IsZero())
	assert.Empty(t, s.LastSummarizedMessageID)
	assert.Equal(t, 0, s.TokensAtLastExtraction)
}

func TestSessionStateManager_MessageID(t *testing.T) {
	m := NewSessionStateManager()
	m.SetLastSummarizedMessageID("msg-42")
	assert.Equal(t, "msg-42", m.GetLastSummarizedMessageID())
}

func TestSessionStateManager_ExtractionLifecycle(t *testing.T) {
	m := NewSessionStateManager()
	assert.False(t, m.IsExtractionInProgress())

	m.MarkExtractionStarted()
	assert.True(t, m.IsExtractionInProgress())

	m.MarkExtractionCompleted()
	assert.False(t, m.IsExtractionInProgress())
}

func TestSessionStateManager_StaleExtraction(t *testing.T) {
	m := NewSessionStateManager()
	m.mu.Lock()
	m.state.ExtractionStartedAt = time.Now().Add(-2 * time.Minute)
	m.mu.Unlock()

	assert.False(t, m.IsExtractionInProgress(), "stale extraction should not count as in-progress")
}

func TestSessionStateManager_TokenRecording(t *testing.T) {
	m := NewSessionStateManager()
	m.RecordExtractionTokenCount(15000)
	assert.Equal(t, 15000, m.GetTokensAtLastExtraction())
}

func TestSessionStateManager_Initialization(t *testing.T) {
	m := NewSessionStateManager()
	assert.False(t, m.IsInitialized())

	m.MarkInitialized()
	assert.True(t, m.IsInitialized())
}

func TestSessionStateManager_Reset(t *testing.T) {
	m := NewSessionStateManager()
	m.SetLastSummarizedMessageID("msg-1")
	m.MarkExtractionStarted()
	m.MarkInitialized()
	m.RecordExtractionTokenCount(5000)

	m.Reset()
	s := m.GetState()
	assert.Empty(t, s.LastSummarizedMessageID)
	assert.True(t, s.ExtractionStartedAt.IsZero())
	assert.False(t, s.Initialized)
	assert.Equal(t, 0, s.TokensAtLastExtraction)
}
