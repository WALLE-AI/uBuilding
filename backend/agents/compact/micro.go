package compact

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/wall-ai/ubuilding/backend/agents"
)

// ---------------------------------------------------------------------------
// MicroCompactor — local micro-compaction (zero API cost)
// Maps to TypeScript microCompact.ts
// ---------------------------------------------------------------------------

// MicroCompactor performs local, zero-cost compaction by folding
// repeated read/search tool calls into condensed summaries.
type MicroCompactor struct {
	// TimeBasedThreshold is the minimum gap (minutes) since the last assistant
	// message before time-based clearing triggers. 0 disables.
	TimeBasedThreshold int
	// TimeBasedKeepRecent is how many recent compactable tool results to keep
	// when time-based clearing fires. Default: 3.
	TimeBasedKeepRecent int
}

// CompactableTools is the set of tool names eligible for micro-compaction.
var CompactableTools = map[string]bool{
	"Read":      true,
	"FileRead":  true,
	"Bash":      true,
	"Grep":      true,
	"Glob":      true,
	"WebSearch": true,
	"WebFetch":  true,
	"FileEdit":  true,
	"FileWrite": true,
	"Write":     true,
}

// TimeBasedMCClearedMessage is the replacement content for time-cleared results.
const TimeBasedMCClearedMessage = "[Old tool result content cleared]"

// NewMicroCompactor creates a new MicroCompactor.
func NewMicroCompactor() *MicroCompactor {
	return &MicroCompactor{
		TimeBasedThreshold:  0, // disabled by default
		TimeBasedKeepRecent: 3,
	}
}

// Compact applies micro-compaction rules to the message history.
// This is a pure local operation with no LLM API calls.
//
// Rules (from TypeScript microCompact.ts):
// 1. Fold repeated FileRead calls to the same file
// 2. Fold repeated Grep/Glob calls with identical patterns
// 3. Fold empty/trivial tool results
// 4. Truncate excessively long tool_result text blocks
// 5. Time-based clearing of old tool results
func (c *MicroCompactor) Compact(messages []agents.Message, systemPrompt string) *CompactResult {
	if len(messages) == 0 {
		return nil
	}

	// Phase 0: Time-based clearing (runs first, short-circuits)
	if timeResult := c.maybeTimeBasedClear(messages); timeResult != nil {
		return timeResult
	}

	result := make([]agents.Message, 0, len(messages))
	tokensSaved := 0
	applied := false

	// Build tool-use index: map tool_use_id -> tool name from assistant messages
	toolNameIndex := buildToolNameIndex(messages)

	// Track seen file paths and search patterns for dedup
	seenFileReads := make(map[string]int) // filePath -> count
	seenSearches := make(map[string]int)  // "toolName:pattern" -> count

	for i, msg := range messages {
		// Rule 1-3: Fold repeated/empty tool results in user messages
		if msg.Type == agents.MessageTypeUser && i > 0 {
			if folded, saved := c.tryFoldRepeatedToolUse(msg, toolNameIndex, seenFileReads, seenSearches); folded != nil {
				result = append(result, *folded)
				tokensSaved += saved
				applied = true
				continue
			}
		}

		// Check for foldable repeated tool results (legacy exact-match)
		if msg.Type == agents.MessageTypeUser && i > 0 {
			if folded, saved := c.tryFoldToolResult(msg, messages[:i]); folded != nil {
				result = append(result, *folded)
				tokensSaved += saved
				applied = true
				continue
			}
		}

		// Rule 3: Fold empty tool results
		if msg.Type == agents.MessageTypeUser {
			if folded, saved := c.foldEmptyToolResults(msg, toolNameIndex); folded != nil {
				result = append(result, *folded)
				tokensSaved += saved
				applied = true
				continue
			}
		}

		// Rule 4: Truncate oversized tool results
		if msg.Type == agents.MessageTypeUser {
			truncated, saved := c.truncateToolResult(msg)
			if saved > 0 {
				result = append(result, truncated)
				tokensSaved += saved
				applied = true
				continue
			}
		}

		// Track tool uses from assistant messages for dedup
		if msg.Type == agents.MessageTypeAssistant {
			c.trackToolUses(msg, seenFileReads, seenSearches)
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

// ---------------------------------------------------------------------------
// New micro-compaction rules
// ---------------------------------------------------------------------------

// buildToolNameIndex creates a map of tool_use_id -> tool name from assistant messages.
func buildToolNameIndex(messages []agents.Message) map[string]string {
	idx := make(map[string]string)
	for _, msg := range messages {
		if msg.Type != agents.MessageTypeAssistant {
			continue
		}
		for _, block := range msg.Content {
			if block.Type == agents.ContentBlockToolUse && block.Name != "" && block.ID != "" {
				idx[block.ID] = block.Name
			}
		}
	}
	return idx
}

// trackToolUses records file paths and search patterns from assistant tool_use blocks.
func (c *MicroCompactor) trackToolUses(
	msg agents.Message,
	seenFileReads map[string]int,
	seenSearches map[string]int,
) {
	for _, block := range msg.Content {
		if block.Type != agents.ContentBlockToolUse {
			continue
		}
		toolName := block.Name
		inputMap := parseToolInput(block.Input)
		if inputMap == nil {
			continue
		}

		switch {
		case toolName == "Read" || toolName == "FileRead":
			if path, ok := inputMap["file_path"].(string); ok && path != "" {
				seenFileReads[path]++
			}
		case toolName == "Grep":
			if pattern, ok := inputMap["pattern"].(string); ok && pattern != "" {
				key := "Grep:" + pattern
				seenSearches[key]++
			}
		case toolName == "Glob":
			if pattern, ok := inputMap["pattern"].(string); ok && pattern != "" {
				key := "Glob:" + pattern
				seenSearches[key]++
			}
		}
	}
}

// parseToolInput extracts a map from tool input (which may be json.RawMessage or map).
func parseToolInput(input json.RawMessage) map[string]interface{} {
	if len(input) == 0 {
		return nil
	}
	var m map[string]interface{}
	if err := json.Unmarshal(input, &m); err != nil {
		return nil
	}
	return m
}

// tryFoldRepeatedToolUse detects repeated file reads and search patterns.
func (c *MicroCompactor) tryFoldRepeatedToolUse(
	msg agents.Message,
	toolNameIndex map[string]string,
	seenFileReads map[string]int,
	seenSearches map[string]int,
) (*agents.Message, int) {
	if len(msg.Content) == 0 {
		return nil, 0
	}

	anyFolded := false
	totalSaved := 0
	newContent := make([]agents.ContentBlock, len(msg.Content))
	copy(newContent, msg.Content)

	for i, block := range newContent {
		if block.Type != agents.ContentBlockToolResult {
			continue
		}

		toolName := toolNameIndex[block.ToolUseID]
		if toolName == "" || !CompactableTools[toolName] {
			continue
		}

		contentStr, ok := block.Content.(string)
		if !ok || len(contentStr) < 200 {
			continue
		}

		// Check for repeated file read
		if toolName == "Read" || toolName == "FileRead" {
			// Extract file_path from the tool result or matching tool_use
			for _, prev := range seenFileReads {
				if prev >= 2 {
					summary := fmt.Sprintf("[Repeated file read — content same as previous read, folded by microcompact (%d chars)]", len(contentStr))
					newContent[i].Content = summary
					totalSaved += (len(contentStr) - len(summary)) / 4
					anyFolded = true
					break
				}
			}
		}

		// Check for repeated search
		if toolName == "Grep" || toolName == "Glob" {
			for _, prev := range seenSearches {
				if prev >= 2 && len(contentStr) > 500 {
					summary := fmt.Sprintf("[Repeated %s search — results same as previous, folded by microcompact (%d chars)]", toolName, len(contentStr))
					newContent[i].Content = summary
					totalSaved += (len(contentStr) - len(summary)) / 4
					anyFolded = true
					break
				}
			}
		}
	}

	if !anyFolded {
		return nil, 0
	}

	folded := msg
	folded.Content = newContent
	return &folded, totalSaved
}

// foldEmptyToolResults replaces empty or trivial tool results with a short marker.
func (c *MicroCompactor) foldEmptyToolResults(
	msg agents.Message,
	toolNameIndex map[string]string,
) (*agents.Message, int) {
	if len(msg.Content) == 0 {
		return nil, 0
	}

	anyFolded := false
	totalSaved := 0
	newContent := make([]agents.ContentBlock, len(msg.Content))
	copy(newContent, msg.Content)

	for i, block := range newContent {
		if block.Type != agents.ContentBlockToolResult {
			continue
		}

		toolName := toolNameIndex[block.ToolUseID]
		if !CompactableTools[toolName] {
			continue
		}

		contentStr, ok := block.Content.(string)
		if !ok {
			continue
		}

		trimmed := strings.TrimSpace(contentStr)
		if trimmed == "" || trimmed == "null" || trimmed == "{}" || trimmed == "[]" {
			newContent[i].Content = "[Empty result]"
			saved := len(contentStr) / 4
			if saved > 0 {
				totalSaved += saved
				anyFolded = true
			}
		}
	}

	if !anyFolded {
		return nil, 0
	}

	folded := msg
	folded.Content = newContent
	return &folded, totalSaved
}

// maybeTimeBasedClear clears old compactable tool results when the gap since
// the last assistant message exceeds the threshold.
// Maps to TypeScript's maybeTimeBasedMicrocompact.
func (c *MicroCompactor) maybeTimeBasedClear(messages []agents.Message) *CompactResult {
	if c.TimeBasedThreshold <= 0 {
		return nil
	}

	// Find the last assistant message timestamp
	var lastAssistantTime time.Time
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Type == agents.MessageTypeAssistant && !messages[i].Timestamp.IsZero() {
			lastAssistantTime = messages[i].Timestamp
			break
		}
	}
	if lastAssistantTime.IsZero() {
		return nil
	}

	gapMinutes := time.Since(lastAssistantTime).Minutes()
	if gapMinutes < float64(c.TimeBasedThreshold) {
		return nil
	}

	// Collect compactable tool IDs in order
	toolNameIndex := buildToolNameIndex(messages)
	var compactableIDs []string
	for _, msg := range messages {
		if msg.Type != agents.MessageTypeAssistant {
			continue
		}
		for _, block := range msg.Content {
			if block.Type == agents.ContentBlockToolUse && CompactableTools[block.Name] {
				compactableIDs = append(compactableIDs, block.ID)
			}
		}
	}

	// Keep the most recent N
	keepRecent := c.TimeBasedKeepRecent
	if keepRecent < 1 {
		keepRecent = 1
	}
	keepSet := make(map[string]bool)
	if len(compactableIDs) > keepRecent {
		for _, id := range compactableIDs[len(compactableIDs)-keepRecent:] {
			keepSet[id] = true
		}
	} else {
		// Nothing to clear
		return nil
	}

	clearSet := make(map[string]bool)
	for _, id := range compactableIDs {
		if !keepSet[id] {
			clearSet[id] = true
		}
	}
	if len(clearSet) == 0 {
		return nil
	}

	tokensSaved := 0
	result := make([]agents.Message, len(messages))
	for i, msg := range messages {
		if msg.Type != agents.MessageTypeUser {
			result[i] = msg
			continue
		}

		touched := false
		newContent := make([]agents.ContentBlock, len(msg.Content))
		copy(newContent, msg.Content)

		for j, block := range newContent {
			if block.Type != agents.ContentBlockToolResult {
				continue
			}
			if !clearSet[block.ToolUseID] {
				continue
			}
			_ = toolNameIndex // already built
			contentStr, ok := block.Content.(string)
			if !ok || contentStr == TimeBasedMCClearedMessage {
				continue
			}
			tokensSaved += len(contentStr) / 4
			newContent[j].Content = TimeBasedMCClearedMessage
			touched = true
		}

		if touched {
			msg.Content = newContent
		}
		result[i] = msg
	}

	if tokensSaved == 0 {
		return nil
	}

	return &CompactResult{
		Messages:    result,
		TokensSaved: tokensSaved,
		Applied:     true,
	}
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
		omitted := len(contentStr) - MaxToolResultChars
		truncated := contentStr[:MaxToolResultChars] + fmt.Sprintf(
			"\n\n[Output truncated by microcompact — %d chars omitted]", omitted)
		newContent[i].Content = truncated
		saved += (len(contentStr) - MaxToolResultChars) / 4
	}

	result.Content = newContent
	return result, saved
}
