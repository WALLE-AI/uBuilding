package session_memory

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"

	"github.com/wall-ai/ubuilding/backend/agents"
)

// ---------------------------------------------------------------------------
// M11.I1 · SessionMemory extractor — the post-sampling hook that
// decides when to extract session notes and dispatches the extraction.
//
// Ports `src/services/SessionMemory/sessionMemory.ts`.
//
// In Go we replace the forked subagent with a SideQueryFn call +
// direct file write, matching the pattern established in
// extract_memories.go.
// ---------------------------------------------------------------------------

// SideQueryFn is the same type used throughout the memory package.
type SideQueryFn func(ctx context.Context, system, userMessage string) (string, error)

// SessionMemoryExtractor orchestrates session memory extraction.
type SessionMemoryExtractor struct {
	mu                    sync.Mutex
	cfg                   agents.EngineConfig
	state                 *SessionStateManager
	sideQuery             SideQueryFn
	notesPath             string
	configHome            string
	lastMemoryMessageUUID string
	logger                *slog.Logger
}

// NewSessionMemoryExtractor creates a new session memory extractor.
func NewSessionMemoryExtractor(
	cfg agents.EngineConfig,
	sideQuery SideQueryFn,
	notesPath string,
	configHome string,
	logger *slog.Logger,
) *SessionMemoryExtractor {
	if logger == nil {
		logger = slog.Default()
	}
	return &SessionMemoryExtractor{
		cfg:        cfg,
		state:      NewSessionStateManager(),
		sideQuery:  sideQuery,
		notesPath:  notesPath,
		configHome: configHome,
		logger:     logger,
	}
}

// State returns the session state manager for external inspection.
func (e *SessionMemoryExtractor) State() *SessionStateManager {
	return e.state
}

// ShouldExtractMemory determines if session memory extraction should
// run based on token thresholds and tool-call counts.
func (e *SessionMemoryExtractor) ShouldExtractMemory(
	messages []agents.Message,
	currentTokenCount int,
) bool {
	// Initialization gate.
	if !e.state.IsInitialized() {
		if !HasMetInitializationThreshold(currentTokenCount) {
			return false
		}
		e.state.MarkInitialized()
	}

	// Token threshold gate.
	hasMetTokens := HasMetUpdateThreshold(currentTokenCount, e.state.GetTokensAtLastExtraction())

	// Tool-call threshold gate.
	toolCalls := countToolCallsSince(messages, e.lastMemoryMessageUUID)
	hasMetToolCalls := toolCalls >= GetToolCallsBetweenUpdates()

	// Last-turn tool-call check.
	hasToolCallsInLastTurn := hasToolCallsInLastAssistantTurn(messages)

	shouldExtract := (hasMetTokens && hasMetToolCalls) ||
		(hasMetTokens && !hasToolCallsInLastTurn)

	if shouldExtract {
		if last := lastMessageUUID(messages); last != "" {
			e.lastMemoryMessageUUID = last
		}
		return true
	}
	return false
}

// Extract runs the session memory extraction via SideQueryFn.
// It reads the current notes.md, builds the update prompt, calls the
// LLM, and writes the response back to notes.md.
func (e *SessionMemoryExtractor) Extract(ctx context.Context, messages []agents.Message, currentTokenCount int) error {
	if e.state.IsExtractionInProgress() {
		return nil // already running
	}

	e.state.MarkExtractionStarted()
	defer e.state.MarkExtractionCompleted()

	// Ensure notes file exists with template.
	if err := e.ensureNotesFile(); err != nil {
		return fmt.Errorf("session_memory: setup: %w", err)
	}

	// Read current notes.
	currentNotes, err := os.ReadFile(e.notesPath)
	if err != nil {
		return fmt.Errorf("session_memory: read notes: %w", err)
	}

	// Build the update prompt.
	updatePrompt := BuildSessionMemoryUpdatePrompt(
		string(currentNotes),
		e.notesPath,
		e.configHome,
	)

	// Build conversation context for the side query.
	conversationCtx := buildConversationContext(messages, e.lastMemoryMessageUUID)

	// Call the LLM.
	e.logger.Info("session_memory: extraction starting",
		"notes_path", e.notesPath,
		"current_notes_len", len(currentNotes),
		"token_count", currentTokenCount)

	response, err := e.sideQuery(ctx, updatePrompt, conversationCtx)
	if err != nil {
		return fmt.Errorf("session_memory: side query: %w", err)
	}

	// Write the response as updated notes.
	// The LLM response should contain the actual edits; we write
	// the full response as the new notes content.
	if response != "" {
		if err := os.WriteFile(e.notesPath, []byte(response), 0o600); err != nil {
			return fmt.Errorf("session_memory: write notes: %w", err)
		}
	}

	// Record extraction state.
	e.state.RecordExtractionTokenCount(currentTokenCount)
	if last := lastMessageUUID(messages); last != "" {
		e.state.SetLastSummarizedMessageID(last)
	}

	e.logger.Info("session_memory: extraction complete",
		"response_len", len(response))

	return nil
}

// ensureNotesFile creates the notes.md file with the template if it
// does not already exist.
func (e *SessionMemoryExtractor) ensureNotesFile() error {
	dir := filepath.Dir(e.notesPath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}

	if _, err := os.Stat(e.notesPath); err == nil {
		return nil // already exists
	}

	template := LoadSessionMemoryTemplate(e.configHome)
	return os.WriteFile(e.notesPath, []byte(template), 0o600)
}

// ---------------------------------------------------------------------------
// Message helpers
// ---------------------------------------------------------------------------

// countToolCallsSince counts tool_use blocks in assistant messages
// after the given UUID.
func countToolCallsSince(messages []agents.Message, sinceUUID string) int {
	count := 0
	found := sinceUUID == ""

	for _, msg := range messages {
		if !found {
			if msg.UUID == sinceUUID {
				found = true
			}
			continue
		}
		if msg.Type == agents.MessageTypeAssistant {
			for _, block := range msg.Content {
				if block.Type == agents.ContentBlockToolUse {
					count++
				}
			}
		}
	}
	return count
}

// hasToolCallsInLastAssistantTurn checks if the last assistant message
// contains any tool_use blocks.
func hasToolCallsInLastAssistantTurn(messages []agents.Message) bool {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Type == agents.MessageTypeAssistant {
			for _, block := range messages[i].Content {
				if block.Type == agents.ContentBlockToolUse {
					return true
				}
			}
			return false
		}
	}
	return false
}

// lastMessageUUID returns the UUID of the last message, or "".
func lastMessageUUID(messages []agents.Message) string {
	if len(messages) == 0 {
		return ""
	}
	return messages[len(messages)-1].UUID
}

// buildConversationContext constructs a condensed string summary of
// recent messages for the side query's user message.
func buildConversationContext(messages []agents.Message, sinceUUID string) string {
	found := sinceUUID == ""
	var parts []string

	for _, msg := range messages {
		if !found {
			if msg.UUID == sinceUUID {
				found = true
			}
			continue
		}
		for _, block := range msg.Content {
			if block.Type == agents.ContentBlockText && block.Text != "" {
				prefix := string(msg.Type)
				parts = append(parts, fmt.Sprintf("[%s] %s", prefix, block.Text))
			}
		}
	}

	if len(parts) == 0 {
		return "(no recent messages)"
	}
	return fmt.Sprintf("Recent conversation (%d turns):\n\n%s",
		len(parts), joinTruncated(parts, 50000))
}

// joinTruncated joins strings with newlines, truncating to maxLen chars.
func joinTruncated(parts []string, maxLen int) string {
	total := 0
	var kept []string
	for _, p := range parts {
		if total+len(p) > maxLen {
			kept = append(kept, "[... truncated ...]")
			break
		}
		kept = append(kept, p)
		total += len(p) + 1
	}
	result := ""
	for i, k := range kept {
		if i > 0 {
			result += "\n"
		}
		result += k
	}
	return result
}
