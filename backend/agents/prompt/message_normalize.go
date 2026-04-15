package prompt

import (
	"strings"

	"github.com/wall-ai/ubuilding/backend/agents"
)

// ---------------------------------------------------------------------------
// NormalizeMessagesForAPI — message serialization for API calls
// Maps to TypeScript utils/messages.ts + prompt/messageSerializer.ts
// ---------------------------------------------------------------------------

// NormalizeMessagesForAPI prepares the internal message history for an LLM API call.
// It enforces the following rules:
//
//  1. Messages must alternate between user and assistant roles
//  2. The first message must be a user message
//  3. Thinking blocks must follow the integrity rules:
//     a. thinking blocks can only appear at the start of an assistant turn
//     b. redacted thinking blocks are preserved as-is
//     c. thinking blocks must not be reordered or split
//  4. System and attachment messages are merged into adjacent user messages
//  5. Empty messages are removed
func NormalizeMessagesForAPI(messages []agents.Message) []agents.Message {
	if len(messages) == 0 {
		return messages
	}

	var result []agents.Message

	for _, msg := range messages {
		// Filter out non-API message types before processing
		if shouldFilterMessage(msg) {
			continue
		}

		switch msg.Type {
		case agents.MessageTypeAssistant:
			// Apply thinking rules
			normalized := normalizeThinkingBlocks(msg)
			// Filter out tombstone/summary content blocks
			normalized = filterContentBlocks(normalized)
			if len(normalized.Content) > 0 {
				result = append(result, normalized)
			}

		case agents.MessageTypeUser:
			filtered := filterContentBlocks(msg)
			if len(filtered.Content) > 0 {
				result = append(result, filtered)
			}

		case agents.MessageTypeSystem, agents.MessageTypeAttachment:
			// Convert system/attachment messages to user messages
			if len(msg.Content) > 0 {
				userMsg := agents.Message{
					Type:    agents.MessageTypeUser,
					UUID:    msg.UUID,
					Content: msg.Content,
					IsMeta:  true,
				}
				result = append(result, userMsg)
			}
		}
	}

	// Ensure alternation: merge consecutive same-role messages
	result = ensureAlternation(result)

	// Ensure first message is user
	if len(result) > 0 && result[0].Type != agents.MessageTypeUser {
		placeholder := agents.Message{
			Type: agents.MessageTypeUser,
			Content: []agents.ContentBlock{
				{Type: agents.ContentBlockText, Text: "[Conversation start]"},
			},
			IsMeta: true,
		}
		result = append([]agents.Message{placeholder}, result...)
	}

	return result
}

// normalizeThinkingBlocks ensures thinking block integrity rules.
// From TypeScript:
//   - Rule 1: thinking blocks only at the start of assistant turns
//   - Rule 2: redacted thinking preserved as-is
//   - Rule 3: no reordering
func normalizeThinkingBlocks(msg agents.Message) agents.Message {
	if msg.Type != agents.MessageTypeAssistant {
		return msg
	}

	// Check if there are any thinking blocks
	hasThinking := false
	for _, block := range msg.Content {
		if block.Type == agents.ContentBlockThinking {
			hasThinking = true
			break
		}
	}
	if !hasThinking {
		return msg
	}

	// Ensure thinking blocks are at the start
	var thinkingBlocks []agents.ContentBlock
	var otherBlocks []agents.ContentBlock

	for _, block := range msg.Content {
		if block.Type == agents.ContentBlockThinking {
			thinkingBlocks = append(thinkingBlocks, block)
		} else {
			otherBlocks = append(otherBlocks, block)
		}
	}

	// Reassemble: thinking first, then others
	result := msg
	result.Content = append(thinkingBlocks, otherBlocks...)
	return result
}

// GetMessagesAfterCompactBoundary returns only messages after the last
// compact_boundary message. If no compact boundary exists, returns all messages.
// This corresponds to TS getMessagesAfterLastCompactBoundary().
func GetMessagesAfterCompactBoundary(messages []agents.Message) []agents.Message {
	lastIdx := -1
	for i, msg := range messages {
		if msg.Type == agents.MessageTypeSystem && msg.Subtype == "compact_boundary" {
			lastIdx = i
		}
	}
	if lastIdx < 0 {
		return messages
	}
	// Include messages after the boundary (boundary itself is a marker, not content)
	if lastIdx+1 >= len(messages) {
		return nil
	}
	return messages[lastIdx+1:]
}

// StripSignatureBlocks removes thinking signatures from assistant messages.
// This is needed when switching models (fallback) to avoid API 400 errors
// because thinking blocks with signatures cannot be sent to a different model.
// Corresponds to TS stripSignatureBlocks().
func StripSignatureBlocks(messages []agents.Message) []agents.Message {
	result := make([]agents.Message, len(messages))
	for i, msg := range messages {
		if msg.Type != agents.MessageTypeAssistant {
			result[i] = msg
			continue
		}
		// Clone and strip signatures from thinking blocks
		stripped := msg
		hasThinking := false
		for _, b := range msg.Content {
			if b.Type == agents.ContentBlockThinking && b.Signature != "" {
				hasThinking = true
				break
			}
		}
		if hasThinking {
			newBlocks := make([]agents.ContentBlock, 0, len(msg.Content))
			for _, b := range msg.Content {
				if b.Type == agents.ContentBlockThinking {
					// Convert thinking blocks to redacted thinking (empty thinking + no signature)
					newBlocks = append(newBlocks, agents.ContentBlock{
						Type:     agents.ContentBlockThinking,
						Thinking: "",
						// Signature intentionally omitted
					})
				} else {
					newBlocks = append(newBlocks, b)
				}
			}
			stripped.Content = newBlocks
		}
		result[i] = stripped
	}
	return result
}

// PrependUserContext injects user context as XML-tagged content into the
// first user message. This corresponds to TS prependUserContext().
func PrependUserContext(messages []agents.Message, userContext string) []agents.Message {
	if userContext == "" || len(messages) == 0 {
		return messages
	}

	contextBlock := agents.ContentBlock{
		Type: agents.ContentBlockText,
		Text: "<user_context>\n" + strings.TrimSpace(userContext) + "\n</user_context>",
	}

	// Find first user message and prepend context
	result := make([]agents.Message, len(messages))
	copy(result, messages)
	for i, msg := range result {
		if msg.Type == agents.MessageTypeUser {
			// Prepend context block to the user message content
			newContent := make([]agents.ContentBlock, 0, len(msg.Content)+1)
			newContent = append(newContent, contextBlock)
			newContent = append(newContent, msg.Content...)
			result[i].Content = newContent
			break
		}
	}
	return result
}

// ---------------------------------------------------------------------------
// Message filtering — tombstone, snip_boundary, progress, stream_event
// ---------------------------------------------------------------------------

// shouldFilterMessage returns true for messages that should be excluded
// from API calls entirely.
func shouldFilterMessage(msg agents.Message) bool {
	// Filter progress messages — these are UI-only
	if msg.Type == agents.MessageTypeProgress {
		return true
	}

	// Filter tombstone messages — replaced content markers
	if msg.Subtype == "tombstone" {
		return true
	}

	// Filter stream_event messages — transient streaming UI events
	if msg.Subtype == "stream_event" {
		return true
	}

	// Filter microcompact_boundary — internal bookkeeping
	if msg.Subtype == "microcompact_boundary" {
		return true
	}

	return false
}

// filterContentBlocks removes non-API content blocks from a message.
// Removes: tool_use_summary blocks, tombstone blocks.
func filterContentBlocks(msg agents.Message) agents.Message {
	if len(msg.Content) == 0 {
		return msg
	}

	needsFilter := false
	for _, b := range msg.Content {
		if isFilteredBlock(b) {
			needsFilter = true
			break
		}
	}
	if !needsFilter {
		return msg
	}

	result := msg
	filtered := make([]agents.ContentBlock, 0, len(msg.Content))
	for _, b := range msg.Content {
		if !isFilteredBlock(b) {
			filtered = append(filtered, b)
		}
	}
	result.Content = filtered
	return result
}

// isFilteredBlock returns true for content blocks that should not be sent to the API.
func isFilteredBlock(b agents.ContentBlock) bool {
	// tool_use_summary is an internal compact marker
	if b.Type == "tool_use_summary" {
		return true
	}
	// tombstone content blocks
	if b.Type == "tombstone" {
		return true
	}
	return false
}

// GetMessagesAfterSnipBoundary returns messages after the last snip_boundary.
// If no snip boundary exists, returns all messages.
// Snip boundaries are inserted by SnipCompactor when old messages are removed.
func GetMessagesAfterSnipBoundary(messages []agents.Message) []agents.Message {
	lastIdx := -1
	for i, msg := range messages {
		if msg.Type == agents.MessageTypeSystem && msg.Subtype == "snip_boundary" {
			lastIdx = i
		}
	}
	if lastIdx < 0 {
		return messages
	}
	if lastIdx+1 >= len(messages) {
		return nil
	}
	return messages[lastIdx+1:]
}

// GetMessagesAfterAnyBoundary returns messages after the last compact or snip boundary.
func GetMessagesAfterAnyBoundary(messages []agents.Message) []agents.Message {
	lastIdx := -1
	for i, msg := range messages {
		if msg.Type == agents.MessageTypeSystem &&
			(msg.Subtype == "compact_boundary" || msg.Subtype == "snip_boundary") {
			lastIdx = i
		}
	}
	if lastIdx < 0 {
		return messages
	}
	if lastIdx+1 >= len(messages) {
		return nil
	}
	return messages[lastIdx+1:]
}

// ensureAlternation merges consecutive same-role messages to maintain
// the required user-assistant alternation pattern.
func ensureAlternation(messages []agents.Message) []agents.Message {
	if len(messages) <= 1 {
		return messages
	}

	var result []agents.Message
	result = append(result, messages[0])

	for i := 1; i < len(messages); i++ {
		prev := &result[len(result)-1]
		curr := messages[i]

		if prev.Type == curr.Type {
			// Merge: append current content to previous message
			prev.Content = append(prev.Content, curr.Content...)
		} else {
			result = append(result, curr)
		}
	}

	return result
}
