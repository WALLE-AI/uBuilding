package compact

import (
	"github.com/wall-ai/ubuilding/backend/agents"
)

// ---------------------------------------------------------------------------
// MicroCompactor — local micro-compaction (zero API cost)
// Maps to TypeScript microCompact.ts
// ---------------------------------------------------------------------------

// MicroCompactor performs local, zero-cost compaction by folding
// repeated read/search tool calls into condensed summaries.
type MicroCompactor struct{}

// NewMicroCompactor creates a new MicroCompactor.
func NewMicroCompactor() *MicroCompactor {
	return &MicroCompactor{}
}

// Compact applies micro-compaction rules to the message history.
// This is a pure local operation with no LLM API calls.
//
// Rules (from TypeScript microCompact.ts):
// 1. Fold consecutive identical FileRead calls to the same file
// 2. Fold consecutive Grep/Glob calls with identical results
// 3. Truncate excessively long tool_result text blocks
func (c *MicroCompactor) Compact(messages []agents.Message, systemPrompt string) *CompactResult {
	if len(messages) == 0 {
		return nil
	}

	result := make([]agents.Message, 0, len(messages))
	tokensSaved := 0
	applied := false

	for i, msg := range messages {
		// Check for foldable repeated tool results
		if msg.Type == agents.MessageTypeUser && i > 0 {
			if folded, saved := c.tryFoldToolResult(msg, messages[:i]); folded != nil {
				result = append(result, *folded)
				tokensSaved += saved
				applied = true
				continue
			}
		}

		// Truncate oversized tool results
		if msg.Type == agents.MessageTypeUser {
			truncated, saved := c.truncateToolResult(msg)
			if saved > 0 {
				result = append(result, truncated)
				tokensSaved += saved
				applied = true
				continue
			}
		}

		result = append(result, msg)
	}

	if !applied {
		return nil
	}

	return &CompactResult{
		Messages:    result,
		TokensSaved: tokensSaved,
		Applied:     true,
	}
}

// tryFoldToolResult checks if this tool result is a duplicate of a recent one.
func (c *MicroCompactor) tryFoldToolResult(msg agents.Message, history []agents.Message) (*agents.Message, int) {
	if len(msg.Content) == 0 {
		return nil, 0
	}

	for _, block := range msg.Content {
		if block.Type != agents.ContentBlockToolResult {
			continue
		}

		// Look back through recent messages for identical tool results
		contentStr, ok := block.Content.(string)
		if !ok || len(contentStr) < 100 {
			continue
		}

		for j := len(history) - 1; j >= 0 && j >= len(history)-10; j-- {
			prev := history[j]
			if prev.Type != agents.MessageTypeUser {
				continue
			}
			for _, prevBlock := range prev.Content {
				if prevBlock.Type == agents.ContentBlockToolResult {
					prevContent, ok := prevBlock.Content.(string)
					if ok && prevContent == contentStr && len(contentStr) > 500 {
						// Fold: replace with a short reference
						folded := msg
						for k := range folded.Content {
							if folded.Content[k].Type == agents.ContentBlockToolResult {
								folded.Content[k].Content = "[Same content as previous tool result - folded by microcompact]"
							}
						}
						saved := (len(contentStr) - 60) / 4 // rough token estimate
						return &folded, saved
					}
				}
			}
		}
	}

	return nil, 0
}

// MaxToolResultChars is the threshold for truncating tool results.
const MaxToolResultChars = 100000

// truncateToolResult truncates oversized tool result blocks.
func (c *MicroCompactor) truncateToolResult(msg agents.Message) (agents.Message, int) {
	saved := 0
	result := msg
	newContent := make([]agents.ContentBlock, len(msg.Content))
	copy(newContent, msg.Content)

	for i, block := range newContent {
		if block.Type != agents.ContentBlockToolResult {
			continue
		}
		contentStr, ok := block.Content.(string)
		if !ok || len(contentStr) <= MaxToolResultChars {
			continue
		}
		// Truncate and add marker
		truncated := contentStr[:MaxToolResultChars] + "\n\n[Output truncated by microcompact — " +
			string(rune(len(contentStr)-MaxToolResultChars)) + " chars omitted]"
		newContent[i].Content = truncated
		saved += (len(contentStr) - MaxToolResultChars) / 4
	}

	result.Content = newContent
	return result, saved
}
