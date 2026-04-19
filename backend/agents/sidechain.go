// Package agents — sub-agent sidechain transcript + metadata + resume.
//
// Tasks D06 · D07 · D08 · D13 · D14 · port the sidechain persistence
// surface from src/utils/sessionStorage.ts. Each sub-agent spawn gets its
// own JSONL transcript under the project's .claude/subagents directory,
// paired with a meta.json that records the agent type + worktree +
// description so `ResumeAgent` can rehydrate the conversation.
//
// The on-disk layout mirrors TS (minus ant-only remote-agent fields):
//
//   <project>/.claude/subagents/<agentId>.jsonl   → transcript entries
//   <project>/.claude/subagents/<agentId>.meta.json → AgentMetadata
//
// Message filters (D13) drop records that would poison the prompt cache or
// confuse the model:
//
//   - FilterUnresolvedToolUses: drop tool_use blocks that lack a matching
//     tool_result in the tail.
//   - FilterOrphanedThinkingOnlyMessages: drop assistant turns that only
//     contain `thinking` blocks (happens when the API aborts mid-turn).
//   - FilterWhitespaceOnlyAssistantMessages: drop assistant turns with
//     blank text content.
//
// Resume (D08) + replacement-state reconstruction (D14) lean on the same
// filters so the rehydrated transcript matches what the engine would have
// produced had it run the turn uninterrupted.
package agents

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ---------------------------------------------------------------------------
// D06 · transcript I/O
// ---------------------------------------------------------------------------

// sidechainDir returns the directory containing sidechain transcripts for
// cwd's project. Callers typically leave cwd empty to fall back to
// os.Getwd.
func sidechainDir(cwd string) (string, error) {
	if cwd == "" {
		w, err := os.Getwd()
		if err != nil {
			return "", err
		}
		cwd = w
	}
	return filepath.Join(cwd, ".claude", "subagents"), nil
}

// sidechainPaths returns the (transcript, metadata) file paths for
// (cwd, agentID). The transcript is JSONL; metadata is JSON.
func sidechainPaths(cwd, agentID string) (string, string, error) {
	if agentID == "" {
		return "", "", errors.New("sidechain: agentID required")
	}
	dir, err := sidechainDir(cwd)
	if err != nil {
		return "", "", err
	}
	return filepath.Join(dir, agentID+".jsonl"),
		filepath.Join(dir, agentID+".meta.json"),
		nil
}

var sidechainMu sync.Mutex // serialise appends per process

// RecordSidechainTranscript atomically appends messages to a sub-agent's
// transcript file. parentUUID, when non-empty, is preserved so callers can
// rebuild the chain order during resume. Missing directories are created.
// Returns (appended count, error).
func RecordSidechainTranscript(
	cwd, agentID string,
	messages []Message,
	parentUUID string,
) (int, error) {
	if len(messages) == 0 {
		return 0, nil
	}
	sidechainMu.Lock()
	defer sidechainMu.Unlock()

	transcriptPath, _, err := sidechainPaths(cwd, agentID)
	if err != nil {
		return 0, err
	}
	if err := os.MkdirAll(filepath.Dir(transcriptPath), 0o755); err != nil {
		return 0, fmt.Errorf("sidechain mkdir: %w", err)
	}
	f, err := os.OpenFile(transcriptPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return 0, fmt.Errorf("sidechain open: %w", err)
	}
	defer f.Close()

	w := bufio.NewWriter(f)
	written := 0
	for _, m := range messages {
		entry := struct {
			ParentUUID  string    `json:"parent_uuid,omitempty"`
			RecordedAt  time.Time `json:"recorded_at"`
			Message     Message   `json:"message"`
		}{
			ParentUUID: parentUUID,
			RecordedAt: time.Now().UTC(),
			Message:    m,
		}
		buf, err := json.Marshal(entry)
		if err != nil {
			return written, err
		}
		if _, err := w.Write(buf); err != nil {
			return written, err
		}
		if err := w.WriteByte('\n'); err != nil {
			return written, err
		}
		written++
	}
	if err := w.Flush(); err != nil {
		return written, err
	}
	return written, nil
}

// GetAgentTranscript reads the full sidechain transcript for (cwd, agentID).
// Returns ("", nil) when the file doesn't exist so callers can distinguish
// "resume target absent" from "I/O failure".
func GetAgentTranscript(cwd, agentID string) ([]Message, error) {
	transcriptPath, _, err := sidechainPaths(cwd, agentID)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(transcriptPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var out []Message
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var entry struct {
			Message Message `json:"message"`
		}
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			return nil, fmt.Errorf("sidechain parse line: %w", err)
		}
		out = append(out, entry.Message)
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// D07 · metadata I/O
// ---------------------------------------------------------------------------

// AgentMetadata mirrors the TS AgentMetadata shape (minus ant-only fields).
type AgentMetadata struct {
	AgentType    string    `json:"agent_type"`
	Description  string    `json:"description,omitempty"`
	WorktreePath string    `json:"worktree_path,omitempty"`
	Prompt       string    `json:"prompt,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at,omitempty"`
}

// WriteAgentMetadata persists meta to disk atomically (tmp + rename).
func WriteAgentMetadata(cwd, agentID string, meta AgentMetadata) error {
	_, metaPath, err := sidechainPaths(cwd, agentID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(metaPath), 0o755); err != nil {
		return err
	}
	if meta.CreatedAt.IsZero() {
		meta.CreatedAt = time.Now().UTC()
	}
	meta.UpdatedAt = time.Now().UTC()
	buf, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	tmp := metaPath + ".tmp"
	if err := os.WriteFile(tmp, buf, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, metaPath)
}

// ReadAgentMetadata loads meta for (cwd, agentID). Returns (nil, nil)
// when the file doesn't exist.
func ReadAgentMetadata(cwd, agentID string) (*AgentMetadata, error) {
	_, metaPath, err := sidechainPaths(cwd, agentID)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(metaPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var out AgentMetadata
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("sidechain meta parse: %w", err)
	}
	return &out, nil
}

// ---------------------------------------------------------------------------
// D13 · message filters
// ---------------------------------------------------------------------------

// FilterUnresolvedToolUses drops tool_use blocks whose ID never appears in
// a tool_result downstream. Preserves the surrounding assistant turn; only
// the orphan blocks disappear. Matches TS filterUnresolvedToolUses.
func FilterUnresolvedToolUses(messages []Message) []Message {
	// Collect every tool_result id seen anywhere in the transcript.
	resolved := make(map[string]struct{}, 16)
	for _, m := range messages {
		for _, blk := range m.Content {
			if blk.Type == ContentBlockToolResult && blk.ToolUseID != "" {
				resolved[blk.ToolUseID] = struct{}{}
			}
		}
	}
	out := make([]Message, 0, len(messages))
	for _, m := range messages {
		if m.Type != MessageTypeAssistant {
			out = append(out, m)
			continue
		}
		cleaned := make([]ContentBlock, 0, len(m.Content))
		for _, blk := range m.Content {
			if blk.Type == ContentBlockToolUse {
				if _, ok := resolved[blk.ID]; !ok {
					continue // drop orphan
				}
			}
			cleaned = append(cleaned, blk)
		}
		copy := m
		copy.Content = cleaned
		out = append(out, copy)
	}
	return out
}

// FilterOrphanedThinkingOnlyMessages drops assistant turns that contain
// only thinking blocks (no text / tool_use / other user-visible content).
func FilterOrphanedThinkingOnlyMessages(messages []Message) []Message {
	out := make([]Message, 0, len(messages))
	for _, m := range messages {
		if m.Type == MessageTypeAssistant && onlyThinking(m.Content) {
			continue
		}
		out = append(out, m)
	}
	return out
}

func onlyThinking(blocks []ContentBlock) bool {
	if len(blocks) == 0 {
		return false
	}
	for _, blk := range blocks {
		if blk.Type != ContentBlockThinking {
			return false
		}
	}
	return true
}

// FilterWhitespaceOnlyAssistantMessages drops assistant messages where
// every block is blank (empty text, no tool_use). Safe to run alongside
// the other two filters in any order.
func FilterWhitespaceOnlyAssistantMessages(messages []Message) []Message {
	out := make([]Message, 0, len(messages))
	for _, m := range messages {
		if m.Type == MessageTypeAssistant && assistantIsBlank(m.Content) {
			continue
		}
		out = append(out, m)
	}
	return out
}

func assistantIsBlank(blocks []ContentBlock) bool {
	if len(blocks) == 0 {
		return true
	}
	for _, blk := range blocks {
		if blk.Type == ContentBlockToolUse {
			return false
		}
		if blk.Type == ContentBlockText && strings.TrimSpace(blk.Text) != "" {
			return false
		}
		if blk.Type == ContentBlockThinking && strings.TrimSpace(blk.Thinking) != "" {
			return false
		}
	}
	return true
}

// FilterResumedTranscript applies all three filters in the TS order:
//   FilterUnresolvedToolUses → FilterOrphanedThinkingOnlyMessages →
//   FilterWhitespaceOnlyAssistantMessages.
func FilterResumedTranscript(messages []Message) []Message {
	m := FilterUnresolvedToolUses(messages)
	m = FilterOrphanedThinkingOnlyMessages(m)
	m = FilterWhitespaceOnlyAssistantMessages(m)
	return m
}

// ---------------------------------------------------------------------------
// D08 · resume
// ---------------------------------------------------------------------------

// ResumeAgentOptions controls ResumeAgent.
type ResumeAgentOptions struct {
	// Cwd is the working directory used to resolve sidechain paths. Empty
	// falls back to os.Getwd.
	Cwd string

	// AgentID names the sidechain to resume. Required.
	AgentID string

	// Prompt is the new user turn appended after the rehydrated transcript.
	// Empty means "continue silently" — the resumed agent runs with its
	// existing history only.
	Prompt string
}

// ResumedAgent wraps the inputs needed to drive a QueryEngine from a
// sidechain transcript. Callers build a child engine and pass these values
// through UpdateMessages + SubmitMessage.
type ResumedAgent struct {
	Metadata        *AgentMetadata
	InitialMessages []Message
	ContinuePrompt  string
}

// ResumeAgent reads the sidechain transcript + metadata and returns a
// ResumedAgent ready for the caller to wire into a QueryEngine. Applies
// the D13 filters so the rehydrated history is free of incomplete tool
// chains / orphaned thinking blocks.
func ResumeAgent(opts ResumeAgentOptions) (*ResumedAgent, error) {
	if opts.AgentID == "" {
		return nil, errors.New("ResumeAgent: AgentID required")
	}
	meta, err := ReadAgentMetadata(opts.Cwd, opts.AgentID)
	if err != nil {
		return nil, fmt.Errorf("ResumeAgent metadata: %w", err)
	}
	msgs, err := GetAgentTranscript(opts.Cwd, opts.AgentID)
	if err != nil {
		return nil, fmt.Errorf("ResumeAgent transcript: %w", err)
	}
	filtered := FilterResumedTranscript(msgs)
	return &ResumedAgent{
		Metadata:        meta,
		InitialMessages: filtered,
		ContinuePrompt:  opts.Prompt,
	}, nil
}

// ---------------------------------------------------------------------------
// D14 · reconstructForSubagentResume
// ---------------------------------------------------------------------------

// ContentReplacementRecord is a stable record of a tool_use whose result
// was replaced/truncated during the original run. Persisted alongside the
// transcript so ResumeAgent reproduces the same budget decisions and
// keeps the prompt cache hit rate high.
type ContentReplacementRecord struct {
	ToolUseID string `json:"tool_use_id"`
	Replaced  string `json:"replaced"`
}

// ReconstructForSubagentResume rebuilds a ContentReplacementState-like map
// from the resumed transcript + a slice of previously recorded
// ContentReplacementRecord entries. Returned as interface{} so callers can
// re-attach it to ToolUseContext.ContentReplacementState without importing
// the compact package (which defines the concrete state type downstream).
//
// The helper is intentionally non-destructive: unknown records are
// preserved to survive future schema extensions.
func ReconstructForSubagentResume(
	records []ContentReplacementRecord,
	messages []Message,
) interface{} {
	if len(records) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(records))
	for _, m := range messages {
		for _, blk := range m.Content {
			if blk.Type == ContentBlockToolResult && blk.ToolUseID != "" {
				seen[blk.ToolUseID] = struct{}{}
			}
			if blk.Type == ContentBlockToolUse && blk.ID != "" {
				seen[blk.ID] = struct{}{}
			}
		}
	}
	// Keep only records that still line up with the transcript — orphan
	// replacement entries would otherwise inflate the cache key.
	out := make([]ContentReplacementRecord, 0, len(records))
	for _, r := range records {
		if _, ok := seen[r.ToolUseID]; ok {
			out = append(out, r)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
