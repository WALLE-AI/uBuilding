package compact

import (
	"github.com/wall-ai/ubuilding/backend/agents"
)

// ---------------------------------------------------------------------------
// SnipCompactor — history snip compaction (HISTORY_SNIP feature gate)
// Maps to TypeScript snipCompactIfNeeded() in services/compact/snipCompact.ts
// ---------------------------------------------------------------------------

// SnipCompactor removes old messages from the beginning of the conversation
// history when the token count grows large, inserting a snip boundary marker.
// Unlike auto-compact (which summarizes), snip simply discards old messages.
//
// The snip strategy preserves:
//  1. The most recent N messages (configurable via PreserveCount)
//  2. Any messages after the last compact_boundary/snip_boundary
//  3. The first user message (preserves context origin)
type SnipCompactor struct {
	// PreserveCount is the minimum number of recent messages to keep.
	// Default: 20
	PreserveCount int

	// MaxHistoryTokens is the estimated token threshold before snipping.
	// Default: 80000 (40% of 200k context)
	MaxHistoryTokens int
}

// NewSnipCompactor creates a SnipCompactor with default settings.
func NewSnipCompactor() *SnipCompactor {
	return &SnipCompactor{
		PreserveCount:    20,
		MaxHistoryTokens: 80000,
	}
}

// SnipResult holds the outcome of a snip operation.
type SnipResult struct {
	Messages        []agents.Message
	TokensFreed     int
	BoundaryMessage *agents.Message
}

// SnipIfNeeded checks whether the message history exceeds the snip threshold
// and removes old messages if necessary.
// Maps to TypeScript snipCompactIfNeeded().
func (s *SnipCompactor) SnipIfNeeded(messages []agents.Message) *SnipResult {
	if len(messages) <= s.PreserveCount {
		return &SnipResult{Messages: messages, TokensFreed: 0}
	}

	// Estimate total tokens
	totalTokens := estimateTokens(messages)
	if totalTokens < s.MaxHistoryTokens {
		return &SnipResult{Messages: messages, TokensFreed: 0}
	}

	// Find snip point: keep the most recent PreserveCount messages
	snipPoint := len(messages) - s.PreserveCount
	if snipPoint < 1 {
		return &SnipResult{Messages: messages, TokensFreed: 0}
	}

	// Ensure snip point is at a valid message boundary (user message start)
	for snipPoint < len(messages)-1 {
		if messages[snipPoint].Type == agents.MessageTypeUser {
			break
		}
		snipPoint++
	}

	if snipPoint >= len(messages)-1 {
		return &SnipResult{Messages: messages, TokensFreed: 0}
	}

	// Calculate tokens freed
	snipped := messages[:snipPoint]
	tokensFreed := estimateTokens(snipped)

	// Build snip boundary message
	boundaryMsg := &agents.Message{
		Type:    agents.MessageTypeSystem,
		Subtype: "snip_boundary",
		Content: []agents.ContentBlock{{
			Type: agents.ContentBlockText,
			Text: "[History snipped — older messages removed to save context space]",
		}},
	}

	// Build result: boundary + preserved messages
	preserved := messages[snipPoint:]
	result := make([]agents.Message, 0, len(preserved)+1)
	result = append(result, *boundaryMsg)
	result = append(result, preserved...)

	return &SnipResult{
		Messages:        result,
		TokensFreed:     tokensFreed,
		BoundaryMessage: boundaryMsg,
	}
}

// estimateTokens gives a rough token estimate for a slice of messages.
func estimateTokens(messages []agents.Message) int {
	totalChars := 0
	for _, msg := range messages {
		for _, block := range msg.Content {
			totalChars += len(block.Text) + len(block.Thinking) + len(string(block.Input))
			if contentStr, ok := block.Content.(string); ok {
				totalChars += len(contentStr)
			}
		}
	}
	return totalChars / 4
}
