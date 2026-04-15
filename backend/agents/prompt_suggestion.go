package agents

import (
	"context"
	"fmt"
	"strings"
)

// ---------------------------------------------------------------------------
// Prompt Suggestion Service
// Maps to TypeScript services/promptSuggestion.ts
//
// After each assistant response, suggests follow-up prompts the user might
// want to send. Runs as a background task (see background_tasks.go).
// ---------------------------------------------------------------------------

// PromptSuggestion is a suggested follow-up prompt.
type PromptSuggestion struct {
	// Text is the suggested prompt text.
	Text string `json:"text"`
	// Category classifies the suggestion (e.g., "continue", "debug", "explore").
	Category string `json:"category,omitempty"`
}

// PromptSuggestionConfig configures the suggestion service.
type PromptSuggestionConfig struct {
	// Enabled controls whether suggestions are generated.
	Enabled bool
	// MaxSuggestions is the maximum number of suggestions to return.
	MaxSuggestions int
	// Provider is the LLM provider for generating suggestions (optional).
	// When nil, rule-based suggestions are used.
	Provider interface{ CallModelForSuggestions(ctx context.Context, prompt string) (string, error) }
}

// DefaultPromptSuggestionConfig returns defaults.
func DefaultPromptSuggestionConfig() PromptSuggestionConfig {
	return PromptSuggestionConfig{
		Enabled:       false,
		MaxSuggestions: 3,
	}
}

// GeneratePromptSuggestions produces follow-up suggestions based on the
// conversation state. Uses rule-based heuristics when no LLM provider
// is configured.
func GeneratePromptSuggestions(
	ctx context.Context,
	messages []Message,
	config PromptSuggestionConfig,
) []PromptSuggestion {
	if !config.Enabled || len(messages) == 0 {
		return nil
	}

	// Find the last assistant message
	var lastAssistant *Message
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Type == MessageTypeAssistant {
			lastAssistant = &messages[i]
			break
		}
	}
	if lastAssistant == nil {
		return nil
	}

	// Extract assistant text for analysis
	var assistantText string
	for _, b := range lastAssistant.Content {
		if b.Type == ContentBlockText {
			assistantText += b.Text
		}
	}

	// Rule-based suggestions
	suggestions := ruleBasedSuggestions(assistantText, messages)

	// Cap at max
	if len(suggestions) > config.MaxSuggestions {
		suggestions = suggestions[:config.MaxSuggestions]
	}

	return suggestions
}

// ruleBasedSuggestions generates suggestions using keyword heuristics.
func ruleBasedSuggestions(assistantText string, messages []Message) []PromptSuggestion {
	lower := strings.ToLower(assistantText)
	var suggestions []PromptSuggestion

	// Error-related suggestions
	if strings.Contains(lower, "error") || strings.Contains(lower, "failed") {
		suggestions = append(suggestions, PromptSuggestion{
			Text:     "Can you fix this error?",
			Category: "debug",
		})
		suggestions = append(suggestions, PromptSuggestion{
			Text:     "Show me the full error details",
			Category: "debug",
		})
	}

	// Code changes
	if strings.Contains(lower, "created") || strings.Contains(lower, "modified") || strings.Contains(lower, "updated") {
		suggestions = append(suggestions, PromptSuggestion{
			Text:     "Run the tests to verify the changes",
			Category: "continue",
		})
	}

	// Task completion
	if strings.Contains(lower, "complete") || strings.Contains(lower, "done") || strings.Contains(lower, "finished") {
		suggestions = append(suggestions, PromptSuggestion{
			Text:     "What should we work on next?",
			Category: "explore",
		})
	}

	// Default fallback
	if len(suggestions) == 0 {
		suggestions = append(suggestions, PromptSuggestion{
			Text:     "Continue",
			Category: "continue",
		})
	}

	return suggestions
}

// FormatSuggestionsForDisplay formats suggestions for terminal display.
func FormatSuggestionsForDisplay(suggestions []PromptSuggestion) string {
	if len(suggestions) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("Suggested prompts:\n")
	for i, s := range suggestions {
		b.WriteString(fmt.Sprintf("  %d. %s\n", i+1, s.Text))
	}
	return b.String()
}
