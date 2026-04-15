package compact

import (
	"context"

	"github.com/wall-ai/ubuilding/backend/agents"
)

// ---------------------------------------------------------------------------
// ContextCollapser — incremental context collapse (CONTEXT_COLLAPSE gate)
// Maps to TypeScript contextCollapse/index.ts
// ---------------------------------------------------------------------------

// ContextCollapser manages incremental context collapse. It stages tool-result
// blocks for collapse and drains them when needed (overflow recovery).
//
// The TypeScript implementation works as follows:
//  1. applyCollapsesIfNeeded — scans messages for collapsible tool-result
//     blocks and replaces them with short summaries. This runs every turn.
//  2. recoverFromOverflow — when a prompt-too-long error occurs, drains all
//     staged collapses to maximally reduce context.
//  3. isWithheldPromptTooLong — checks if a 413 should be withheld for
//     collapse recovery (if there are pending collapses to drain).
type ContextCollapser struct {
	// StagedCollapses holds messages that have been staged for collapse.
	StagedCollapses []StagedCollapse

	// CollapseThresholdChars is the min tool-result size to consider for collapse.
	CollapseThresholdChars int

	// Enabled tracks whether context collapse is active.
	Enabled bool

	// CallModel is used for LLM-powered summarization of collapsed blocks.
	CallModel func(ctx context.Context, params agents.CallModelParams) (<-chan agents.StreamEvent, error)
}

// StagedCollapse represents a tool-result block staged for collapse.
type StagedCollapse struct {
	MessageIndex int    // index in the message array
	BlockIndex   int    // index within the message's content blocks
	ToolUseID    string // tool_use_id of the block
	OriginalSize int    // original content size in chars
	Summary      string // short summary (computed lazily)
}

// NewContextCollapser creates a new ContextCollapser.
func NewContextCollapser(
	callModel func(ctx context.Context, params agents.CallModelParams) (<-chan agents.StreamEvent, error),
) *ContextCollapser {
	return &ContextCollapser{
		CollapseThresholdChars: 2000, // tool results > 2k chars are candidates
		Enabled:                true,
		CallModel:              callModel,
	}
}

// ApplyCollapsesIfNeeded scans messages for large tool-result blocks and
// stages them for future collapse. On subsequent calls, already-staged
// blocks are replaced with their short summaries.
//
// Maps to TypeScript contextCollapse.applyCollapsesIfNeeded().
func (cc *ContextCollapser) ApplyCollapsesIfNeeded(
	_ context.Context,
	messages []agents.Message,
	_ *agents.ToolUseContext,
	_ string,
) *agents.ContextCollapseResult {
	if !cc.Enabled || len(messages) == 0 {
		return &agents.ContextCollapseResult{Messages: messages, Applied: false}
	}

	result := make([]agents.Message, len(messages))
	copy(result, messages)
	applied := false

	// Stage new large tool-result blocks for collapse
	for i, msg := range result {
		if msg.Type != agents.MessageTypeUser {
			continue
		}
		for j, block := range msg.Content {
			if block.Type != agents.ContentBlockToolResult {
				continue
			}
			contentStr, ok := block.Content.(string)
			if !ok || len(contentStr) < cc.CollapseThresholdChars {
				continue
			}

			// Check if already staged
			if cc.isStaged(i, j) {
				continue
			}

			// Stage this block
			summary := buildCollapseSummary(contentStr)
			cc.StagedCollapses = append(cc.StagedCollapses, StagedCollapse{
				MessageIndex: i,
				BlockIndex:   j,
				ToolUseID:    block.ToolUseID,
				OriginalSize: len(contentStr),
				Summary:      summary,
			})
		}
	}

	// Apply previously staged collapses that are old enough (not from current turn)
	// For simplicity in Go implementation, we apply all staged collapses except
	// those from the current (last) message
	for _, sc := range cc.StagedCollapses {
		if sc.MessageIndex >= len(result) {
			continue
		}
		// Don't collapse the most recent user message's blocks
		if sc.MessageIndex == len(result)-1 {
			continue
		}
		msg := &result[sc.MessageIndex]
		if sc.BlockIndex >= len(msg.Content) {
			continue
		}
		block := &msg.Content[sc.BlockIndex]
		if block.Type != agents.ContentBlockToolResult {
			continue
		}
		contentStr, ok := block.Content.(string)
		if !ok || len(contentStr) < cc.CollapseThresholdChars {
			continue
		}

		// Replace with collapsed summary
		newContent := make([]agents.ContentBlock, len(msg.Content))
		copy(newContent, msg.Content)
		newContent[sc.BlockIndex] = agents.ContentBlock{
			Type:      agents.ContentBlockToolResult,
			ToolUseID: sc.ToolUseID,
			Content:   "[Collapsed] " + sc.Summary,
		}
		result[sc.MessageIndex].Content = newContent
		applied = true
	}

	return &agents.ContextCollapseResult{Messages: result, Applied: applied}
}

// RecoverFromOverflow drains all staged collapses to maximally reduce context.
// This is called when a prompt-too-long error occurs and context collapse is
// the first recovery strategy (before reactive compact).
//
// Maps to TypeScript contextCollapse.recoverFromOverflow().
func (cc *ContextCollapser) RecoverFromOverflow(
	messages []agents.Message,
	_ string,
) *agents.ContextCollapseDrainResult {
	if !cc.Enabled || len(cc.StagedCollapses) == 0 {
		return &agents.ContextCollapseDrainResult{Messages: messages, Committed: 0}
	}

	result := make([]agents.Message, len(messages))
	copy(result, messages)
	committed := 0

	// Apply ALL staged collapses aggressively
	for _, sc := range cc.StagedCollapses {
		if sc.MessageIndex >= len(result) {
			continue
		}
		msg := &result[sc.MessageIndex]
		if sc.BlockIndex >= len(msg.Content) {
			continue
		}
		block := &msg.Content[sc.BlockIndex]
		if block.Type != agents.ContentBlockToolResult {
			continue
		}

		newContent := make([]agents.ContentBlock, len(msg.Content))
		copy(newContent, msg.Content)
		newContent[sc.BlockIndex] = agents.ContentBlock{
			Type:      agents.ContentBlockToolResult,
			ToolUseID: sc.ToolUseID,
			Content:   "[Collapsed] " + sc.Summary,
		}
		result[sc.MessageIndex].Content = newContent
		committed++
	}

	// Clear the staged list after draining
	cc.StagedCollapses = nil

	return &agents.ContextCollapseDrainResult{Messages: result, Committed: committed}
}

// IsWithheldPromptTooLong checks whether a prompt-too-long error should be
// withheld because context collapse can still recover by draining staged collapses.
func (cc *ContextCollapser) IsWithheldPromptTooLong() bool {
	return cc.Enabled && len(cc.StagedCollapses) > 0
}

// isStaged checks if a specific message/block pair is already staged.
func (cc *ContextCollapser) isStaged(msgIdx, blockIdx int) bool {
	for _, sc := range cc.StagedCollapses {
		if sc.MessageIndex == msgIdx && sc.BlockIndex == blockIdx {
			return true
		}
	}
	return false
}

// buildCollapseSummary creates a short summary of a tool-result content string.
// In the full TS implementation, this may use the LLM; here we use a heuristic.
func buildCollapseSummary(content string) string {
	// Take first 200 chars as a preview
	maxPreview := 200
	if len(content) < maxPreview {
		maxPreview = len(content)
	}
	preview := content[:maxPreview]
	if len(content) > maxPreview {
		preview += "..."
	}
	return preview
}
