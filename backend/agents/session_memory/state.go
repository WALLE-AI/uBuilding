package session_memory

import (
	"sync"
	"time"
)

// ---------------------------------------------------------------------------
// M10.I2 · SessionMemory per-session mutable state.
//
// Ports the module-level state variables from sessionMemoryUtils.ts.
// ---------------------------------------------------------------------------

// SessionStateManager provides thread-safe access to per-session
// mutable state for the extraction pipeline.
type SessionStateManager struct {
	mu    sync.Mutex
	state SessionState
}

// NewSessionStateManager creates a new manager with zero-value state.
func NewSessionStateManager() *SessionStateManager {
	return &SessionStateManager{}
}

// GetState returns a snapshot of the current state.
func (m *SessionStateManager) GetState() SessionState {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.state
}

// GetLastSummarizedMessageID returns the last summarized message ID.
func (m *SessionStateManager) GetLastSummarizedMessageID() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.state.LastSummarizedMessageID
}

// SetLastSummarizedMessageID updates the last summarized message ID.
func (m *SessionStateManager) SetLastSummarizedMessageID(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.state.LastSummarizedMessageID = id
}

// MarkExtractionStarted records that extraction has begun.
func (m *SessionStateManager) MarkExtractionStarted() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.state.ExtractionStartedAt = time.Now()
}

// MarkExtractionCompleted clears the extraction-in-progress flag.
func (m *SessionStateManager) MarkExtractionCompleted() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.state.ExtractionStartedAt = time.Time{}
}

// IsExtractionInProgress returns true if an extraction is currently
// running and is not stale (older than ExtractionStaleThreshold).
func (m *SessionStateManager) IsExtractionInProgress() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.state.ExtractionStartedAt.IsZero() {
		return false
	}
	if time.Since(m.state.ExtractionStartedAt) > ExtractionStaleThreshold {
		return false // stale — treat as not in progress
	}
	return true
}

// WaitForExtraction blocks until no extraction is in progress or the
// timeout elapses. Returns immediately if no extraction is running or
// if the extraction is stale.
func (m *SessionStateManager) WaitForExtraction() {
	deadline := time.Now().Add(ExtractionWaitTimeout)
	for {
		if !m.IsExtractionInProgress() {
			return
		}
		if time.Now().After(deadline) {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// RecordExtractionTokenCount records the token count at extraction time.
func (m *SessionStateManager) RecordExtractionTokenCount(tokens int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.state.TokensAtLastExtraction = tokens
}

// GetTokensAtLastExtraction returns the recorded token count.
func (m *SessionStateManager) GetTokensAtLastExtraction() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.state.TokensAtLastExtraction
}

// IsInitialized returns whether session memory has been initialized.
func (m *SessionStateManager) IsInitialized() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.state.Initialized
}

// MarkInitialized sets the initialized flag to true.
func (m *SessionStateManager) MarkInitialized() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.state.Initialized = true
}

// Reset clears all state back to zero values (for testing).
func (m *SessionStateManager) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.state = SessionState{}
}
