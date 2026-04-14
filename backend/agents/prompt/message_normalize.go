package prompt

import (
	"github.com/wall-ai/ubuilding/backend/agents"
)

// ---------------------------------------------------------------------------
// NormalizeMessagesForAPI — message serialization for API calls
// Maps to TypeScript utils/messages.ts + prompt/messageSerializer.ts
// ---------------------------------------------------------------------------

// NormalizeMessagesForAPI prepares the internal message history for an LLM API call.
// It enforces the following rules:
//
//   1. Messages must alternate between user and assistant roles
//   2. The first message must be a user message
//   3. Thinking blocks must follow the integrity rules:
//      a. thinking blocks can only appear at the start of an assistant turn
//      b. redacted thinking blocks are preserved as-is
//      c. thinking blocks must not be reordered or split
//   4. System and attachment messages are merged into adjacent user messages
//   5. Empty messages are removed
func NormalizeMessagesForAPI(messages []agents.Message) []agents.Message {
	if len(messages) == 0 {
		return messages
	}

	var result []agents.Message

	for _, msg := range messages {
		switch msg.Type {
		case agents.MessageTypeAssistant:
			// Apply thinking rules
			normalized := normalizeThinkingBlocks(msg)
			if len(normalized.Content) > 0 {
				result = append(result, normalized)
			}

		case agents.MessageTypeUser:
			if len(msg.Content) > 0 {
				result = append(result, msg)
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
