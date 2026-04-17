package compact

import (
	"sync"

	"github.com/wall-ai/ubuilding/backend/agents"
)

// ---------------------------------------------------------------------------
// Session Memory Compaction — core primitives
// Maps to TypeScript src/services/compact/sessionMemoryCompact.ts (568 lines).
//
// Scope: this file ports the tokenization-independent primitives needed for
// session-memory compaction in uBuilding:
//   - SessionMemoryCompactConfig (defaults, getter/setter)
//   - HasTextBlocks / GetToolResultIDs / HasToolUseWithIDs helpers
//   - AdjustIndexToPreserveAPIInvariants — the API-invariant repair algorithm
//     that widens a truncation boundary so we never split tool_use/tool_result
//     pairs or thinking blocks that share a message.id with kept messages.
//
// The surrounding orchestration (Compact(), transcript/session storage, GB
// config) lives in the SessionMemory subsystem and is deliberately out of
// scope for this port — callers wire the primitives into their own pipeline.
// ---------------------------------------------------------------------------

// SessionMemoryCompactConfig holds the thresholds for session-memory compaction.
type SessionMemoryCompactConfig struct {
	// MinTokens is the minimum tokens to preserve after compaction.
	MinTokens int

	// MinTextBlockMessages is the minimum number of text-block-bearing
	// messages to keep (protects conversational context).
	MinTextBlockMessages int

	// MaxTokens is the hard cap on tokens preserved after compaction.
	MaxTokens int
}

// DefaultSessionMemoryCompactConfig mirrors TS DEFAULT_SM_COMPACT_CONFIG.
var DefaultSessionMemoryCompactConfig = SessionMemoryCompactConfig{
	MinTokens:            10_000,
	MinTextBlockMessages: 5,
	MaxTokens:            40_000,
}

var (
	smCompactConfigMu sync.RWMutex
	smCompactConfig   = DefaultSessionMemoryCompactConfig
)

// GetSessionMemoryCompactConfig returns a copy of the current config.
func GetSessionMemoryCompactConfig() SessionMemoryCompactConfig {
	smCompactConfigMu.RLock()
	defer smCompactConfigMu.RUnlock()
	return smCompactConfig
}

// SetSessionMemoryCompactConfig merges the given partial config into the
// current one. Only positive values override defaults (matches TS).
func SetSessionMemoryCompactConfig(partial SessionMemoryCompactConfig) {
	smCompactConfigMu.Lock()
	defer smCompactConfigMu.Unlock()
	if partial.MinTokens > 0 {
		smCompactConfig.MinTokens = partial.MinTokens
	}
	if partial.MinTextBlockMessages > 0 {
		smCompactConfig.MinTextBlockMessages = partial.MinTextBlockMessages
	}
	if partial.MaxTokens > 0 {
		smCompactConfig.MaxTokens = partial.MaxTokens
	}
}

// ResetSessionMemoryCompactConfig restores defaults (used by tests).
func ResetSessionMemoryCompactConfig() {
	smCompactConfigMu.Lock()
	defer smCompactConfigMu.Unlock()
	smCompactConfig = DefaultSessionMemoryCompactConfig
}

// HasTextBlocks reports whether the message carries any text content.
// Maps to hasTextBlocks() in sessionMemoryCompact.ts.
func HasTextBlocks(msg agents.Message) bool {
	for _, b := range msg.Content {
		if b.Type == agents.ContentBlockText && b.Text != "" {
			return true
		}
	}
	return false
}

// GetToolResultIDs extracts the tool_use_id of every tool_result block in
// the message. Returns an empty slice for non-user messages or messages
// without tool_result content. Maps to getToolResultIds() in sessionMemoryCompact.ts.
func GetToolResultIDs(msg agents.Message) []string {
	if msg.Type != agents.MessageTypeUser {
		return nil
	}
	var ids []string
	for _, b := range msg.Content {
		if b.Type == agents.ContentBlockToolResult && b.ToolUseID != "" {
			ids = append(ids, b.ToolUseID)
		}
	}
	return ids
}

// HasToolUseWithIDs reports whether the assistant message contains any
// tool_use block whose ID is present in the provided set.
// Maps to hasToolUseWithIds() in sessionMemoryCompact.ts.
func HasToolUseWithIDs(msg agents.Message, toolUseIDs map[string]struct{}) bool {
	if msg.Type != agents.MessageTypeAssistant {
		return false
	}
	for _, b := range msg.Content {
		if b.Type == agents.ContentBlockToolUse {
			if _, ok := toolUseIDs[b.ID]; ok {
				return true
			}
		}
	}
	return false
}

// AdjustIndexToPreserveAPIInvariants widens startIndex backwards so that:
//  1. Every kept tool_result has its matching tool_use (no orphan results).
//  2. Streaming-split assistant messages sharing a message.id (carrying
//     thinking + tool_use) stay together so downstream normalization can
//     merge them.
//
// Returns a non-negative index <= startIndex that satisfies both invariants.
// If startIndex is out of bounds (<=0 or >= len(messages)), it is returned
// unchanged. Maps to adjustIndexToPreserveAPIInvariants() in sessionMemoryCompact.ts.
//
// Note: thinking-block message-id coalescing requires a stable message.id
// across split content blocks. uBuilding's Message does not currently
// expose that field, so step 2 is implemented via UUID equality as a
// best-effort proxy when multiple consecutive assistant messages share the
// same UUID. If UUIDs are unique per message (the common case in this
// codebase), step 2 is a no-op — which is safe.
func AdjustIndexToPreserveAPIInvariants(messages []agents.Message, startIndex int) int {
	if startIndex <= 0 || startIndex >= len(messages) {
		return startIndex
	}

	adjusted := startIndex

	// Step 1: tool_use/tool_result pair preservation.
	//
	// Collect every tool_result ID appearing in the kept range.
	var toolResultIDs []string
	for i := adjusted; i < len(messages); i++ {
		toolResultIDs = append(toolResultIDs, GetToolResultIDs(messages[i])...)
	}

	if len(toolResultIDs) > 0 {
		// Track which of those are already matched by a tool_use in the kept range.
		toolUseIDsInKept := map[string]struct{}{}
		for i := adjusted; i < len(messages); i++ {
			m := messages[i]
			if m.Type != agents.MessageTypeAssistant {
				continue
			}
			for _, b := range m.Content {
				if b.Type == agents.ContentBlockToolUse && b.ID != "" {
					toolUseIDsInKept[b.ID] = struct{}{}
				}
			}
		}

		// Build the set of still-missing tool_use IDs.
		missing := map[string]struct{}{}
		for _, id := range toolResultIDs {
			if _, ok := toolUseIDsInKept[id]; !ok {
				missing[id] = struct{}{}
			}
		}

		// Walk backwards until every missing tool_use is included.
		for len(missing) > 0 && adjusted > 0 {
			adjusted--
			m := messages[adjusted]
			if m.Type != agents.MessageTypeAssistant {
				continue
			}
			for _, b := range m.Content {
				if b.Type == agents.ContentBlockToolUse {
					delete(missing, b.ID)
				}
			}
		}
	}

	// Step 2: coalesce streaming-split assistant messages sharing the same
	// identifier so thinking blocks aren't stranded. uBuilding proxies by UUID.
	if adjusted > 0 {
		keptID := messages[adjusted].UUID
		if keptID != "" && messages[adjusted].Type == agents.MessageTypeAssistant {
			for adjusted > 0 {
				prev := messages[adjusted-1]
				if prev.Type != agents.MessageTypeAssistant || prev.UUID != keptID {
					break
				}
				adjusted--
			}
		}
	}

	return adjusted
}

// TruncateForCompact selects a suffix of messages suitable for compaction,
// starting at the smallest index i such that messages[i:] satisfies:
//   - at least cfg.MinTextBlockMessages messages carry text content
//   - the start point respects tool_use/tool_result and thinking invariants
//     (see AdjustIndexToPreserveAPIInvariants).
//
// Returns the index i and the sliced tail. When the message history is
// shorter than the requirement, returns 0 and the full history.
//
// This is a lightweight stand-in for TS truncateSessionMemoryForCompact —
// it mirrors the "keep at least N text-bearing messages, then widen to a
// safe API boundary" contract without depending on token estimation.
func TruncateForCompact(messages []agents.Message, cfg SessionMemoryCompactConfig) (int, []agents.Message) {
	if cfg.MinTextBlockMessages <= 0 {
		cfg.MinTextBlockMessages = DefaultSessionMemoryCompactConfig.MinTextBlockMessages
	}

	// Walk from tail, count text-bearing messages, stop once we hit the floor.
	textCount := 0
	start := len(messages)
	for i := len(messages) - 1; i >= 0; i-- {
		if HasTextBlocks(messages[i]) {
			textCount++
		}
		if textCount >= cfg.MinTextBlockMessages {
			start = i
			break
		}
	}

	if start >= len(messages) {
		// Not enough text-bearing messages — keep everything.
		return 0, messages
	}

	start = AdjustIndexToPreserveAPIInvariants(messages, start)
	return start, messages[start:]
}
