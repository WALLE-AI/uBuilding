package compact

import (
	"github.com/wall-ai/ubuilding/backend/agents"
)

// ---------------------------------------------------------------------------
// Post-Compact Cleanup
// Maps to TypeScript services/compact/postCompactCleanup.ts
//
// After compaction (auto or manual), various caches and tracking state
// must be reset because the message history they reference no longer exists.
// ---------------------------------------------------------------------------

// CleanupFunc is a function called during post-compact cleanup.
type CleanupFunc func()

// PostCompactCleaner manages cleanup hooks that run after compaction.
type PostCompactCleaner struct {
	hooks []CleanupFunc
}

// NewPostCompactCleaner creates a new cleaner.
func NewPostCompactCleaner() *PostCompactCleaner {
	return &PostCompactCleaner{}
}

// Register adds a cleanup hook.
func (c *PostCompactCleaner) Register(fn CleanupFunc) {
	c.hooks = append(c.hooks, fn)
}

// Run executes all registered cleanup hooks.
func (c *PostCompactCleaner) Run() {
	for _, fn := range c.hooks {
		fn()
	}
}

// ---------------------------------------------------------------------------
// EnsureToolResultPairing — repairs orphaned tool_use / tool_result blocks
// after compaction splits messages at group boundaries.
// ---------------------------------------------------------------------------

// EnsureToolResultPairing scans messages for orphaned tool_use blocks
// (tool_use without a matching tool_result) and adds synthetic error
// results so the API contract is satisfied.
func EnsureToolResultPairing(messages []agents.Message) []agents.Message {
	if len(messages) == 0 {
		return messages
	}

	// Collect all tool_use IDs
	toolUseIDs := make(map[string]bool)
	for _, msg := range messages {
		if msg.Type != agents.MessageTypeAssistant {
			continue
		}
		for _, block := range msg.Content {
			if block.Type == agents.ContentBlockToolUse && block.ID != "" {
				toolUseIDs[block.ID] = true
			}
		}
	}

	// Collect all tool_result IDs
	for _, msg := range messages {
		if msg.Type != agents.MessageTypeUser {
			continue
		}
		for _, block := range msg.Content {
			if block.Type == agents.ContentBlockToolResult && block.ToolUseID != "" {
				delete(toolUseIDs, block.ToolUseID)
			}
		}
	}

	// No orphans
	if len(toolUseIDs) == 0 {
		return messages
	}

	// Add synthetic tool_result for each orphaned tool_use
	var syntheticBlocks []agents.ContentBlock
	for id := range toolUseIDs {
		syntheticBlocks = append(syntheticBlocks, agents.ContentBlock{
			Type:      agents.ContentBlockToolResult,
			ToolUseID: id,
			Content:   "[Tool result unavailable — conversation was compacted]",
			IsError:   true,
		})
	}

	syntheticMsg := agents.Message{
		Type:    agents.MessageTypeUser,
		Content: syntheticBlocks,
	}

	return append(messages, syntheticMsg)
}

// RemoveEmptyMessages filters out messages with no content blocks.
func RemoveEmptyMessages(messages []agents.Message) []agents.Message {
	result := make([]agents.Message, 0, len(messages))
	for _, msg := range messages {
		if len(msg.Content) > 0 {
			result = append(result, msg)
		}
	}
	return result
}
