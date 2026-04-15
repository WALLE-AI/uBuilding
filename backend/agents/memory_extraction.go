package agents

import (
	"context"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// Memory Extraction Service
// Maps to TypeScript services/memoryExtraction.ts
//
// Extracts key facts and decisions from conversation history for persistence
// into CLAUDE.md / memory files. Runs as a background task after each turn.
// ---------------------------------------------------------------------------

// ExtractedMemory is a single fact or decision extracted from conversation.
type ExtractedMemory struct {
	// Content is the extracted text.
	Content string `json:"content"`
	// Category classifies the memory (e.g., "preference", "decision", "fact").
	Category string `json:"category,omitempty"`
	// Source is a reference to where this was extracted from (message UUID).
	Source string `json:"source,omitempty"`
	// Timestamp when extracted.
	Timestamp time.Time `json:"timestamp"`
}

// MemoryExtractionConfig configures the extraction service.
type MemoryExtractionConfig struct {
	// Enabled controls whether extraction runs.
	Enabled bool
	// MaxMemories caps the number of memories to extract per turn.
	MaxMemories int
	// MinMessageCount is the minimum messages before extraction runs.
	MinMessageCount int
	// Provider is the LLM provider for extraction (optional).
	// When nil, rule-based extraction is used.
	Provider interface {
		CallModelForExtraction(ctx context.Context, prompt string) (string, error)
	}
}

// DefaultMemoryExtractionConfig returns defaults.
func DefaultMemoryExtractionConfig() MemoryExtractionConfig {
	return MemoryExtractionConfig{
		Enabled:         false,
		MaxMemories:     5,
		MinMessageCount: 10,
	}
}

// ExtractMemories analyzes conversation messages and extracts key facts.
// Uses rule-based heuristics when no LLM provider is configured.
func ExtractMemories(
	ctx context.Context,
	messages []Message,
	config MemoryExtractionConfig,
) []ExtractedMemory {
	if !config.Enabled || len(messages) < config.MinMessageCount {
		return nil
	}

	var memories []ExtractedMemory

	// Scan user messages for explicit preferences and decisions
	for _, msg := range messages {
		if msg.Type != MessageTypeUser {
			continue
		}

		for _, block := range msg.Content {
			if block.Type != ContentBlockText {
				continue
			}

			extracted := extractFromUserMessage(block.Text, msg.UUID)
			memories = append(memories, extracted...)
		}
	}

	// Cap at max
	if len(memories) > config.MaxMemories {
		memories = memories[:config.MaxMemories]
	}

	return memories
}

// extractFromUserMessage uses keyword heuristics to find memorable content.
func extractFromUserMessage(text string, sourceUUID string) []ExtractedMemory {
	lower := strings.ToLower(text)
	var memories []ExtractedMemory

	// Explicit preferences: "always", "never", "prefer", "don't"
	preferenceKeywords := []string{"always ", "never ", "prefer ", "don't ", "do not "}
	for _, kw := range preferenceKeywords {
		if strings.Contains(lower, kw) {
			memories = append(memories, ExtractedMemory{
				Content:   text,
				Category:  "preference",
				Source:    sourceUUID,
				Timestamp: time.Now(),
			})
			break
		}
	}

	// Technical decisions: "use X instead of Y", "we decided", "the approach"
	decisionKeywords := []string{"instead of", "we decided", "the approach", "let's use", "switch to"}
	for _, kw := range decisionKeywords {
		if strings.Contains(lower, kw) {
			memories = append(memories, ExtractedMemory{
				Content:   text,
				Category:  "decision",
				Source:    sourceUUID,
				Timestamp: time.Now(),
			})
			break
		}
	}

	// Remember requests: "remember", "keep in mind", "note that"
	rememberKeywords := []string{"remember ", "keep in mind", "note that", "important:"}
	for _, kw := range rememberKeywords {
		if strings.Contains(lower, kw) {
			memories = append(memories, ExtractedMemory{
				Content:   text,
				Category:  "fact",
				Source:    sourceUUID,
				Timestamp: time.Now(),
			})
			break
		}
	}

	return memories
}

// FormatMemoriesForCLAUDEMD formats extracted memories for writing to CLAUDE.md.
func FormatMemoriesForCLAUDEMD(memories []ExtractedMemory) string {
	if len(memories) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("## Extracted Memories\n\n")
	for _, m := range memories {
		prefix := ""
		switch m.Category {
		case "preference":
			prefix = "- **Preference**: "
		case "decision":
			prefix = "- **Decision**: "
		case "fact":
			prefix = "- **Note**: "
		default:
			prefix = "- "
		}
		b.WriteString(prefix + m.Content + "\n")
	}
	return b.String()
}
