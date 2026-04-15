package compact

import (
	"context"
	"fmt"
	"time"

	"github.com/wall-ai/ubuilding/backend/agents"
)

// ---------------------------------------------------------------------------
// AutoCompactor — LLM-powered summarization compaction
// Maps to TypeScript autoCompact.ts
// ---------------------------------------------------------------------------

// AutoCompactor performs LLM-powered context summarization when the
// message history exceeds the compaction threshold.
type AutoCompactor struct {
	// CallModel is a function that calls the LLM for summarization.
	CallModel func(ctx context.Context, params agents.CallModelParams) (<-chan agents.StreamEvent, error)
}

// NewAutoCompactor creates a new AutoCompactor.
func NewAutoCompactor(callModel func(ctx context.Context, params agents.CallModelParams) (<-chan agents.StreamEvent, error)) *AutoCompactor {
	return &AutoCompactor{CallModel: callModel}
}

// Compact applies LLM-powered summarization to the message history.
// It identifies a compaction boundary, summarizes messages before it,
// and returns the compressed history.
func (c *AutoCompactor) Compact(ctx context.Context, messages []agents.Message, systemPrompt string, querySource string) *CompactResult {
	if len(messages) == 0 {
		return nil
	}

	// Estimate current token usage
	totalChars := len(systemPrompt)
	for _, msg := range messages {
		for _, block := range msg.Content {
			totalChars += len(block.Text) + len(block.Thinking) + len(string(block.Input))
			if contentStr, ok := block.Content.(string); ok {
				totalChars += len(contentStr)
			}
		}
	}
	estimatedTokens := totalChars / 4

	// Only compact if above threshold
	if float64(estimatedTokens) < float64(DefaultContextWindow)*AutoCompactThreshold {
		return nil
	}

	// Find compaction boundary: keep the most recent 30% of messages
	boundary := len(messages) * 7 / 10
	if boundary < 2 {
		return nil // too few messages to compact
	}

	// Messages to summarize (before boundary)
	toSummarize := messages[:boundary]

	// Build summarization prompt
	summaryPrompt := buildSummarizationPrompt(toSummarize)

	// Call LLM for summarization
	summary, err := c.callForSummary(ctx, summaryPrompt)
	if err != nil {
		// If summarization fails, don't compact
		return nil
	}

	// Build compacted messages: summary + recent messages
	compactedMessages := make([]agents.Message, 0, len(messages)-boundary+1)

	// Add summary as a system message
	compactedMessages = append(compactedMessages, agents.Message{
		Type: agents.MessageTypeUser,
		Content: []agents.ContentBlock{
			{
				Type: agents.ContentBlockText,
				Text: fmt.Sprintf("[Context compacted. Summary of previous %d messages:]\n\n%s", boundary, summary),
			},
		},
		IsMeta: true,
	})

	// Add recent messages (after boundary)
	compactedMessages = append(compactedMessages, messages[boundary:]...)

	savedChars := 0
	for _, msg := range toSummarize {
		for _, block := range msg.Content {
			savedChars += len(block.Text) + len(block.Thinking) + len(string(block.Input))
		}
	}

	return &CompactResult{
		Messages:    compactedMessages,
		TokensSaved: savedChars / 4,
		Applied:     true,
		Summary:     summary,
	}
}

// buildSummarizationPrompt constructs the prompt for the summarization LLM call.
func buildSummarizationPrompt(messages []agents.Message) string {
	prompt := "Please provide a concise summary of the following conversation, " +
		"preserving key decisions, file changes, tool results, and important context:\n\n"

	for _, msg := range messages {
		role := string(msg.Type)
		for _, block := range msg.Content {
			switch block.Type {
			case agents.ContentBlockText:
				if block.Text != "" {
					prompt += fmt.Sprintf("[%s]: %s\n", role, truncateString(block.Text, 2000))
				}
			case agents.ContentBlockToolUse:
				prompt += fmt.Sprintf("[%s tool_use]: %s(%s)\n", role, block.Name, truncateString(string(block.Input), 500))
			case agents.ContentBlockToolResult:
				contentStr, _ := block.Content.(string)
				prompt += fmt.Sprintf("[tool_result %s]: %s\n", block.ToolUseID, truncateString(contentStr, 1000))
			}
		}
	}

	return prompt
}

// callForSummary calls the LLM to generate a summary.
func (c *AutoCompactor) callForSummary(ctx context.Context, prompt string) (string, error) {
	if c.CallModel == nil {
		return "", fmt.Errorf("no CallModel function configured")
	}

	params := agents.CallModelParams{
		Messages: []agents.Message{
			{
				Type: agents.MessageTypeUser,
				Content: []agents.ContentBlock{
					{Type: agents.ContentBlockText, Text: prompt},
				},
			},
		},
		SystemPrompt: "You are a conversation summarizer. Provide concise, factual summaries preserving key technical details, decisions, and file changes. Output only the summary.",
	}

	ch, err := c.CallModel(ctx, params)
	if err != nil {
		return "", err
	}

	var summary string
	for event := range ch {
		if event.Type == agents.EventTextDelta {
			summary += event.Text
		}
		if event.Type == agents.EventAssistant && event.Message != nil {
			for _, block := range event.Message.Content {
				if block.Type == agents.ContentBlockText {
					summary = block.Text
				}
			}
		}
	}

	if summary == "" {
		return "", fmt.Errorf("empty summary from LLM")
	}
	return summary, nil
}

// CompactWithTracking applies auto-compaction with tracking state and circuit breaker.
// This corresponds to TS autoCompactIfNeeded() with full tracking support.
func (c *AutoCompactor) CompactWithTracking(
	ctx context.Context,
	messages []agents.Message,
	systemPrompt string,
	querySource string,
	tracking *AutoCompactTrackingState,
	force bool,
) *agents.AutocompactResult {
	// Circuit breaker: skip if too many consecutive failures
	if tracking != nil && tracking.ConsecutiveFailures >= MaxConsecutiveCompactFailures && !force {
		return &agents.AutocompactResult{Messages: messages, Applied: false}
	}

	// Only compact if above threshold (unless forced)
	if !force {
		totalChars := len(systemPrompt)
		for _, msg := range messages {
			for _, block := range msg.Content {
				totalChars += len(block.Text) + len(block.Thinking) + len(string(block.Input))
				if contentStr, ok := block.Content.(string); ok {
					totalChars += len(contentStr)
				}
			}
		}
		estimatedTokens := totalChars / 4
		if float64(estimatedTokens) < float64(DefaultContextWindow)*AutoCompactThreshold {
			return &agents.AutocompactResult{Messages: messages, Applied: false}
		}
	}

	if len(messages) < 3 {
		return &agents.AutocompactResult{Messages: messages, Applied: false}
	}

	// Find compaction boundary: keep the most recent 30% of messages
	boundary := len(messages) * 7 / 10
	if boundary < 2 {
		return &agents.AutocompactResult{Messages: messages, Applied: false}
	}

	toSummarize := messages[:boundary]
	preserved := messages[boundary:]

	// Build summarization prompt
	summaryPrompt := buildSummarizationPrompt(toSummarize)

	// Call LLM for summarization
	summary, err := c.callForSummary(ctx, summaryPrompt)
	if err != nil {
		if tracking != nil {
			tracking.ConsecutiveFailures++
		}
		return &agents.AutocompactResult{Messages: messages, Applied: false}
	}

	// Reset failure counter on success
	if tracking != nil {
		tracking.ConsecutiveFailures = 0
		tracking.Compacted = true
		tracking.TurnCounter++
	}

	// Determine preserved segment UUIDs
	var headUUID, tailUUID string
	if len(preserved) > 0 {
		headUUID = preserved[0].UUID
		tailUUID = preserved[len(preserved)-1].UUID
	}

	// Build compacted messages: summary + preserved
	compactedMessages := make([]agents.Message, 0, len(preserved)+1)
	compactedMessages = append(compactedMessages, agents.Message{
		Type:             agents.MessageTypeUser,
		IsCompactSummary: true,
		IsMeta:           true,
		Timestamp:        time.Now(),
		Content: []agents.ContentBlock{
			{
				Type: agents.ContentBlockText,
				Text: fmt.Sprintf("[Context compacted. Summary of previous %d messages:]\n\n%s", boundary, summary),
			},
		},
	})
	compactedMessages = append(compactedMessages, preserved...)

	// Calculate tokens saved
	savedChars := 0
	for _, msg := range toSummarize {
		for _, block := range msg.Content {
			savedChars += len(block.Text) + len(block.Thinking) + len(string(block.Input))
		}
	}

	metadata := &agents.CompactMetadata{
		Summary:     summary,
		TokensSaved: savedChars / 4,
		Trigger:     "auto",
	}
	if headUUID != "" || tailUUID != "" {
		metadata.PreservedSegment = &agents.PreservedSegment{
			HeadUUID: headUUID,
			TailUUID: tailUUID,
		}
	}

	return &agents.AutocompactResult{
		Messages:    compactedMessages,
		Applied:     true,
		Summary:     summary,
		TokensSaved: savedChars / 4,
		Metadata:    metadata,
	}
}

func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
