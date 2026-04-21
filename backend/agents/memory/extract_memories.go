package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/wall-ai/ubuilding/backend/agents"
)

// ---------------------------------------------------------------------------
// Memory extraction service. Ported from
// src/services/extractMemories/extractMemories.ts.
//
// Runs as a background task after each turn. Uses the SideQueryFn (a
// lightweight LLM call) to analyse recent conversation and extract
// durable memories into the auto-memory directory.
//
// Unlike the TS reference which uses a full forked agent with tool
// execution, this Go implementation uses a two-pass approach:
//   1. SideQueryFn analyses the conversation and returns JSON
//      describing memories to create/update.
//   2. The service writes the files directly (no fork needed).
//
// This keeps the implementation simple while achieving the same outcome.
// ---------------------------------------------------------------------------

// EnvEnableExtractMemories gates the extraction system.
const EnvEnableExtractMemories = "UBUILDING_ENABLE_EXTRACT_MEMORIES"

// ExtractMemoriesService manages background memory extraction.
// It is goroutine-safe; the overlap guard ensures at most one
// extraction runs at a time.
type ExtractMemoriesService struct {
	mu sync.Mutex

	// Configuration
	cwd      string
	settings SettingsProvider
	cfg      agents.EngineConfig

	// Cursor: UUID of the last message processed.
	lastMessageUUID string

	// Overlap guard
	inProgress bool

	// Turn counter for throttling (extract every N turns).
	turnsSinceLastExtraction int
	extractEveryNTurns       int

	logger *slog.Logger
}

// NewExtractMemoriesService creates a new extraction service.
func NewExtractMemoriesService(
	cwd string,
	settings SettingsProvider,
	cfg agents.EngineConfig,
) *ExtractMemoriesService {
	n := 1 // default: extract every turn
	if v := os.Getenv("UBUILDING_EXTRACT_EVERY_N_TURNS"); v != "" {
		var parsed int
		if _, err := fmt.Sscanf(v, "%d", &parsed); err == nil && parsed > 0 {
			n = parsed
		}
	}
	return &ExtractMemoriesService{
		cwd:                cwd,
		settings:           settings,
		cfg:                cfg,
		extractEveryNTurns: n,
		logger:             slog.Default(),
	}
}

// IsEnabled reports whether the extraction system is active.
func (s *ExtractMemoriesService) IsEnabled() bool {
	if !isEnvTruthy(os.Getenv(EnvEnableExtractMemories)) {
		return false
	}
	return IsAutoMemoryEnabled(s.cfg, s.settings)
}

// OnTurnEnd is called by the stop hook after each model turn.
// It runs extraction asynchronously (fire-and-forget).
func (s *ExtractMemoriesService) OnTurnEnd(messages []agents.Message) {
	if !s.IsEnabled() {
		s.logger.Debug("memory: extraction skipped — not enabled")
		return
	}
	if DefaultSideQueryFn == nil {
		s.logger.Warn("memory: extraction skipped — SideQueryFn not wired")
		return
	}
	s.logger.Info("memory: OnTurnEnd fired", "msg_count", len(messages))

	s.mu.Lock()
	if s.inProgress {
		s.mu.Unlock()
		s.logger.Debug("memory: extraction skipped — already in progress")
		return
	}
	s.turnsSinceLastExtraction++
	if s.turnsSinceLastExtraction < s.extractEveryNTurns {
		s.mu.Unlock()
		s.logger.Debug("memory: extraction throttled", "turns", s.turnsSinceLastExtraction, "every", s.extractEveryNTurns)
		return
	}
	s.inProgress = true
	s.mu.Unlock()

	// Copy messages for background goroutine
	msgCopy := make([]agents.Message, len(messages))
	copy(msgCopy, messages)

	go func() {
		defer func() {
			if r := recover(); r != nil {
				s.logger.Error("memory: extraction panicked", "recover", r)
			}
			s.mu.Lock()
			s.inProgress = false
			s.turnsSinceLastExtraction = 0
			s.mu.Unlock()
		}()

		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		if err := s.runExtraction(ctx, msgCopy); err != nil {
			s.logger.Warn("memory: extraction failed", "err", err)
		}
	}()
}

// runExtraction performs the actual extraction.
func (s *ExtractMemoriesService) runExtraction(ctx context.Context, messages []agents.Message) error {
	memoryDir := GetAutoMemPath(s.cwd, s.settings)
	if memoryDir == "" {
		return fmt.Errorf("cannot resolve auto-memory path for cwd=%q", s.cwd)
	}

	// Count new model-visible messages since last extraction
	newMessageCount := countModelVisibleMessagesSince(messages, s.lastMessageUUID)
	if newMessageCount < 2 {
		s.logger.Info("memory: extraction skipped — not enough new messages",
			"new_count", newMessageCount, "cursor", s.lastMessageUUID, "total_msgs", len(messages))
		return nil
	}
	s.logger.Info("memory: extraction starting", "new_messages", newMessageCount)

	// Check if the main agent already wrote memories (mutual exclusion)
	if hasMemoryWritesSince(messages, s.lastMessageUUID, memoryDir) {
		s.logger.Info("memory: extraction skipped — conversation already wrote to memory dir")
		if last := lastMessageUUID(messages); last != "" {
			s.lastMessageUUID = last
		}
		return nil
	}

	// Scan existing memory files for the manifest
	headers, _ := ScanMemoryFiles(memoryDir)
	existingMemories := FormatMemoryManifest(headers)

	// Check team memory
	teamEnabled := IsTeamMemoryEnabled(s.cfg, s.settings)
	skipIndex := isEnvTruthy(os.Getenv(EnvSkipMemoryIndex))

	// Build the extraction prompt
	var userPrompt string
	if teamEnabled {
		userPrompt = BuildExtractCombinedPrompt(newMessageCount, existingMemories, skipIndex)
	} else {
		userPrompt = BuildExtractAutoOnlyPrompt(newMessageCount, existingMemories, skipIndex)
	}

	// Build conversation summary for the side query
	conversationSummary := buildConversationSummary(messages, s.lastMessageUUID)

	// System prompt for extraction
	systemPrompt := fmt.Sprintf(
		"You are a memory extraction agent. Analyse the conversation below and extract durable memories.\n\n"+
			"Memory directory: %s\n\n"+
			"Respond with a JSON object:\n"+
			"```json\n"+
			"{\n"+
			"  \"memories\": [\n"+
			"    {\n"+
			"      \"filename\": \"topic_name.md\",\n"+
			"      \"action\": \"create\" | \"update\",\n"+
			"      \"frontmatter\": { \"name\": \"...\", \"description\": \"...\", \"type\": \"user|feedback|project|reference\" },\n"+
			"      \"content\": \"memory content here\"\n"+
			"    }\n"+
			"  ],\n"+
			"  \"index_entries\": [\n"+
			"    \"- [Title](filename.md) — one-line hook\"\n"+
			"  ]\n"+
			"}\n"+
			"```\n\n"+
			"If there is nothing worth remembering, return {\"memories\": [], \"index_entries\": []}.\n\n"+
			"%s",
		strings.TrimRight(memoryDir, string(os.PathSeparator)),
		userPrompt,
	)

	// Call the side query
	s.logger.Info("memory: extraction calling LLM",
		"summary_len", len(conversationSummary), "prompt_len", len(systemPrompt))
	response, err := DefaultSideQueryFn(ctx, systemPrompt, conversationSummary)
	if err != nil {
		return fmt.Errorf("side query failed: %w", err)
	}
	s.logger.Info("memory: extraction LLM responded",
		"response_len", len(response), "response_head", truncate(response, 300))

	// Parse and write memories
	written, err := s.parseAndWriteMemories(memoryDir, response, skipIndex)
	if err != nil {
		return fmt.Errorf("write memories: %w", err)
	}

	// Advance cursor
	if last := lastMessageUUID(messages); last != "" {
		s.lastMessageUUID = last
	}

	s.logger.Info("memory: extraction complete", "files_written", written)
	return nil
}

// parseAndWriteMemories parses the LLM response and writes memory files.
func (s *ExtractMemoriesService) parseAndWriteMemories(memoryDir, response string, skipIndex bool) (int, error) {
	// Extract JSON from response (may be wrapped in ```json ... ```)
	jsonStr := extractJSON(response)
	if jsonStr == "" {
		s.logger.Info("memory: extraction LLM returned no JSON",
			"response_len", len(response), "response_head", truncate(response, 200))
		return 0, nil
	}

	// Simple JSON parsing without importing encoding/json to avoid
	// heavy dependencies — use the same approach as find_relevant.go
	type memoryEntry struct {
		Filename    string            `json:"filename"`
		Action      string            `json:"action"`
		Frontmatter map[string]string `json:"frontmatter"`
		Content     string            `json:"content"`
	}
	type extractResult struct {
		Memories     []memoryEntry `json:"memories"`
		IndexEntries []string      `json:"index_entries"`
	}

	var result extractResult
	if err := parseJSONResponse(jsonStr, &result); err != nil {
		s.logger.Info("memory: could not parse extraction response", "err", err,
			"json_head", truncate(jsonStr, 200))
		return 0, nil // Graceful degradation
	}

	if len(result.Memories) == 0 {
		s.logger.Info("memory: extraction parsed OK but memories array empty")
		return 0, nil
	}

	written := 0
	for _, mem := range result.Memories {
		if mem.Filename == "" || mem.Content == "" {
			continue
		}
		// Sanitise filename
		fname := filepath.Base(mem.Filename)
		if !strings.HasSuffix(fname, ".md") {
			fname += ".md"
		}

		// Build file content with frontmatter
		var b strings.Builder
		b.WriteString("---\n")
		if v, ok := mem.Frontmatter["name"]; ok && v != "" {
			b.WriteString(fmt.Sprintf("name: %s\n", v))
		}
		if v, ok := mem.Frontmatter["description"]; ok && v != "" {
			b.WriteString(fmt.Sprintf("description: %s\n", v))
		}
		if v, ok := mem.Frontmatter["type"]; ok && v != "" {
			b.WriteString(fmt.Sprintf("type: %s\n", v))
		}
		b.WriteString("---\n\n")
		b.WriteString(mem.Content)
		b.WriteString("\n")

		fpath := filepath.Join(strings.TrimRight(memoryDir, string(os.PathSeparator)), fname)
		if err := os.MkdirAll(filepath.Dir(fpath), 0o755); err != nil {
			s.logger.Warn("memory: mkdir failed", "path", filepath.Dir(fpath), "err", err)
			continue
		}
		if err := os.WriteFile(fpath, []byte(b.String()), 0o644); err != nil {
			s.logger.Warn("memory: write failed", "path", fpath, "err", err)
			continue
		}
		written++
	}

	// Update MEMORY.md index (unless skipIndex)
	if !skipIndex && len(result.IndexEntries) > 0 && written > 0 {
		entrypoint := filepath.Join(strings.TrimRight(memoryDir, string(os.PathSeparator)), autoMemEntrypoint)
		existing, _ := os.ReadFile(entrypoint)
		var b strings.Builder
		if len(existing) > 0 {
			b.Write(existing)
			if !strings.HasSuffix(string(existing), "\n") {
				b.WriteString("\n")
			}
		}
		for _, entry := range result.IndexEntries {
			entry = strings.TrimSpace(entry)
			if entry != "" {
				// Avoid duplicates
				if !strings.Contains(string(existing), entry) {
					b.WriteString(entry + "\n")
				}
			}
		}
		if err := os.WriteFile(entrypoint, []byte(b.String()), 0o644); err != nil {
			s.logger.Warn("memory: index update failed", "path", entrypoint, "err", err)
		}
	}

	return written, nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// countModelVisibleMessagesSince counts user+assistant messages after sinceUUID.
func countModelVisibleMessagesSince(messages []agents.Message, sinceUUID string) int {
	if sinceUUID == "" {
		n := 0
		for _, m := range messages {
			if m.Type == agents.MessageTypeUser || m.Type == agents.MessageTypeAssistant {
				n++
			}
		}
		return n
	}
	found := false
	n := 0
	for _, m := range messages {
		if !found {
			if m.UUID == sinceUUID {
				found = true
			}
			continue
		}
		if m.Type == agents.MessageTypeUser || m.Type == agents.MessageTypeAssistant {
			n++
		}
	}
	if !found {
		// sinceUUID removed (e.g. by compaction) — count all
		return countModelVisibleMessagesSince(messages, "")
	}
	return n
}

// hasMemoryWritesSince checks if any assistant message since sinceUUID
// contains a Write/Edit tool_use targeting the memory directory.
func hasMemoryWritesSince(messages []agents.Message, sinceUUID, memoryDir string) bool {
	found := sinceUUID == ""
	cleanDir := strings.TrimRight(memoryDir, string(os.PathSeparator))
	for _, m := range messages {
		if !found {
			if m.UUID == sinceUUID {
				found = true
			}
			continue
		}
		if m.Type != agents.MessageTypeAssistant {
			continue
		}
		for _, blk := range m.Content {
			if blk.Type != agents.ContentBlockToolUse {
				continue
			}
			if blk.Name != "Write" && blk.Name != "Edit" {
				continue
			}
			// Check if file_path in input targets memory dir
			var input map[string]interface{}
			if err := json.Unmarshal(blk.Input, &input); err == nil {
				if fp, ok := input["file_path"].(string); ok {
					if strings.HasPrefix(filepath.Clean(fp), cleanDir) {
						return true
					}
				}
			}
		}
	}
	return false
}

// lastMessageUUID returns the UUID of the last message.
func lastMessageUUID(messages []agents.Message) string {
	if len(messages) == 0 {
		return ""
	}
	return messages[len(messages)-1].UUID
}

// buildConversationSummary extracts recent user+assistant text for the extraction query.
func buildConversationSummary(messages []agents.Message, sinceUUID string) string {
	found := sinceUUID == ""
	var parts []string
	for _, m := range messages {
		if !found {
			if m.UUID == sinceUUID {
				found = true
			}
			continue
		}
		if m.Type != agents.MessageTypeUser && m.Type != agents.MessageTypeAssistant {
			continue
		}
		role := "user"
		if m.Type == agents.MessageTypeAssistant {
			role = "assistant"
		}
		for _, blk := range m.Content {
			if blk.Type == agents.ContentBlockText && blk.Text != "" {
				parts = append(parts, fmt.Sprintf("[%s]: %s", role, blk.Text))
			}
		}
	}
	// Cap at ~8000 chars to stay within side-query limits
	result := strings.Join(parts, "\n\n")
	if len(result) > 8000 {
		result = result[len(result)-8000:]
	}
	return result
}

// extractJSON pulls a JSON object from a response that may be wrapped
// in markdown code fences.
func extractJSON(s string) string {
	// Try to find ```json ... ```
	if idx := strings.Index(s, "```json"); idx >= 0 {
		start := idx + len("```json")
		if end := strings.Index(s[start:], "```"); end >= 0 {
			return strings.TrimSpace(s[start : start+end])
		}
	}
	// Try to find ``` ... ```
	if idx := strings.Index(s, "```"); idx >= 0 {
		start := idx + len("```")
		if end := strings.Index(s[start:], "```"); end >= 0 {
			return strings.TrimSpace(s[start : start+end])
		}
	}
	// Try raw JSON
	if idx := strings.Index(s, "{"); idx >= 0 {
		if end := strings.LastIndex(s, "}"); end > idx {
			return s[idx : end+1]
		}
	}
	return ""
}

// parseJSONResponse unmarshals a JSON string into the target.
func parseJSONResponse(data string, v interface{}) error {
	return json.Unmarshal([]byte(data), v)
}

// truncate returns at most maxLen characters of s, appending "…" if cut.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "…"
}
