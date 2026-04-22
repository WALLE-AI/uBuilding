package session_memory

import (
	"os"

	"github.com/wall-ai/ubuilding/backend/agents"
	"github.com/wall-ai/ubuilding/backend/agents/compact"
)

// ---------------------------------------------------------------------------
// M12.I1 · Compact bridge — SM-compact orchestration that wires the
// compact primitives with session memory content.
//
// Ports the orchestration half of
// `src/services/compact/sessionMemoryCompact.ts`.
//
// When autocompact fires and session memory is active, the bridge:
//  1. Reads the current notes.md
//  2. Waits for any in-progress extraction
//  3. Truncates oversized sections
//  4. Selects a message suffix via TruncateForCompact
//  5. Returns the compact result to the caller
// ---------------------------------------------------------------------------

// CompactWithSessionMemory performs a session-memory-aware compact.
// It returns the truncation index into messages and the session memory
// content to inject into the compact summary. If session memory is
// empty or unavailable, it falls back to (0, "").
func CompactWithSessionMemory(
	messages []agents.Message,
	notesPath string,
	configHome string,
	stateManager *SessionStateManager,
) (startIndex int, sessionContent string) {
	// Wait for any in-flight extraction before reading notes.
	if stateManager != nil {
		stateManager.WaitForExtraction()
	}

	// Read current notes.
	raw, err := os.ReadFile(notesPath)
	if err != nil {
		return 0, ""
	}
	content := string(raw)

	// Check if notes are empty (just the template).
	if IsSessionMemoryEmpty(content, configHome) {
		return 0, ""
	}

	// Truncate oversized sections for compact insertion.
	truncated, _ := TruncateSessionMemoryForCompact(content)

	// Select message suffix using compact primitives.
	cfg := compact.GetSessionMemoryCompactConfig()
	startIndex, _ = compact.TruncateForCompact(messages, cfg)

	return startIndex, truncated
}

// ShouldUseSessionMemoryCompact determines whether the compact system
// should use session-memory-based compaction instead of LLM-based.
// Returns true when session memory is enabled, initialized, and has
// non-empty content.
func ShouldUseSessionMemoryCompact(
	engineCfg agents.EngineConfig,
	notesPath string,
	configHome string,
	stateManager *SessionStateManager,
) bool {
	if !IsSessionMemoryEnabled(engineCfg) {
		return false
	}
	if stateManager != nil && !stateManager.IsInitialized() {
		return false
	}

	raw, err := os.ReadFile(notesPath)
	if err != nil {
		return false
	}

	return !IsSessionMemoryEmpty(string(raw), configHome)
}
