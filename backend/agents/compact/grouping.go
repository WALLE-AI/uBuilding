package compact

import (
	"github.com/wall-ai/ubuilding/backend/agents"
)

// ---------------------------------------------------------------------------
// Message Grouping for Compact
// Maps to TypeScript services/compact/grouping.ts
//
// Groups messages at API-round boundaries for partial compaction.
// A boundary fires when a NEW assistant response begins (different UUID
// from the prior assistant). This is an API-safe split point because
// the API contract requires every tool_use to be resolved before the
// next assistant turn.
// ---------------------------------------------------------------------------

// MessageGroup is a slice of consecutive messages that belong to
// the same API round-trip.
type MessageGroup []agents.Message

// GroupMessagesByAPIRound splits messages into groups at assistant-turn
// boundaries. Each group contains one assistant response and any
// user/tool messages that follow it until the next assistant response.
func GroupMessagesByAPIRound(messages []agents.Message) []MessageGroup {
	if len(messages) == 0 {
		return nil
	}

	var groups []MessageGroup
	var current MessageGroup
	lastAssistantUUID := ""

	for _, msg := range messages {
		if msg.Type == agents.MessageTypeAssistant &&
			msg.UUID != lastAssistantUUID &&
			len(current) > 0 {
			groups = append(groups, current)
			current = MessageGroup{msg}
		} else {
			current = append(current, msg)
		}
		if msg.Type == agents.MessageTypeAssistant {
			lastAssistantUUID = msg.UUID
		}
	}

	if len(current) > 0 {
		groups = append(groups, current)
	}
	return groups
}

// GroupCount returns the total number of API rounds.
func GroupCount(messages []agents.Message) int {
	return len(GroupMessagesByAPIRound(messages))
}

// SplitAtGroup splits messages into two halves at the given group index.
// Returns (prefix, suffix) where prefix contains groups [0, splitAt) and
// suffix contains groups [splitAt, end).
func SplitAtGroup(messages []agents.Message, splitAt int) ([]agents.Message, []agents.Message) {
	groups := GroupMessagesByAPIRound(messages)
	if splitAt <= 0 || splitAt >= len(groups) {
		return messages, nil
	}

	var prefix, suffix []agents.Message
	for i, g := range groups {
		if i < splitAt {
			prefix = append(prefix, g...)
		} else {
			suffix = append(suffix, g...)
		}
	}
	return prefix, suffix
}
