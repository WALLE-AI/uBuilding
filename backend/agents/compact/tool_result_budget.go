package compact

import (
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"

	"github.com/wall-ai/ubuilding/backend/agents"
)

// ---------------------------------------------------------------------------
// Tool Result Budget — per-message aggregate budget enforcement
// Maps to enforceToolResultBudget() in utils/toolResultStorage.ts
// ---------------------------------------------------------------------------

const (
	// DefaultMaxToolResultsPerMessageChars is the default per-message budget.
	// Maps to MAX_TOOL_RESULTS_PER_MESSAGE_CHARS in toolLimits.ts.
	DefaultMaxToolResultsPerMessageChars = 200_000

	// DefaultMaxResultSizeChars is the default per-tool result threshold.
	// Maps to DEFAULT_MAX_RESULT_SIZE_CHARS in toolLimits.ts.
	DefaultMaxResultSizeChars = 50_000

	// MaxToolResultBytes is the byte limit for a single tool result.
	// Maps to MAX_TOOL_RESULT_BYTES in toolLimits.ts.
	MaxToolResultBytes = 500_000

	// PreviewSizeBytes is the size of the preview retained after truncation.
	PreviewSizeBytes = 2000

	// PersistedOutputTag marks content that was budget-truncated.
	PersistedOutputTag        = "<persisted-output>"
	PersistedOutputClosingTag = "</persisted-output>"

	// ToolResultClearedMessage is used when tool result content was cleared.
	ToolResultClearedMessage = "[Old tool result content cleared]"
)

// ContentReplacementState tracks replacement decisions across turns.
// Once a tool_use_id is "seen", its fate is frozen to preserve prompt cache.
// Maps to ContentReplacementState in toolResultStorage.ts.
type ContentReplacementState struct {
	mu           sync.Mutex
	seenIDs      map[string]bool   // all tool_use_ids that have been processed
	replacements map[string]string // tool_use_id -> replacement content (for re-apply)
}

// NewContentReplacementState creates a fresh state.
func NewContentReplacementState() *ContentReplacementState {
	return &ContentReplacementState{
		seenIDs:      make(map[string]bool),
		replacements: make(map[string]string),
	}
}

// CloneContentReplacementState creates a deep copy for cache-sharing forks.
func CloneContentReplacementState(src *ContentReplacementState) *ContentReplacementState {
	if src == nil {
		return nil
	}
	src.mu.Lock()
	defer src.mu.Unlock()

	dst := &ContentReplacementState{
		seenIDs:      make(map[string]bool, len(src.seenIDs)),
		replacements: make(map[string]string, len(src.replacements)),
	}
	for k, v := range src.seenIDs {
		dst.seenIDs[k] = v
	}
	for k, v := range src.replacements {
		dst.replacements[k] = v
	}
	return dst
}

// ToolResultReplacementRecord is the serializable record of a replacement.
// Maps to ToolResultReplacementRecord in toolResultStorage.ts.
type ToolResultReplacementRecord struct {
	Kind        string `json:"kind"` // always "tool-result"
	ToolUseID   string `json:"toolUseId"`
	Replacement string `json:"replacement"`
}

// toolResultCandidate is an eligible tool_result block for budget evaluation.
type toolResultCandidate struct {
	toolUseID string
	content   string
	size      int
}

// candidatePartition splits candidates by their prior state.
type candidatePartition struct {
	mustReapply []reapplyCandidate    // previously replaced — re-apply cached
	frozen      []toolResultCandidate // previously seen, unreplaced — off-limits
	fresh       []toolResultCandidate // never seen — eligible for replacement
}

type reapplyCandidate struct {
	toolResultCandidate
	replacement string
}

// ToolResultBudgetResult holds the outcome of budget enforcement.
type ToolResultBudgetResult struct {
	Messages       []agents.Message
	NewlyReplaced  []ToolResultReplacementRecord
	ReappliedCount int
}

// EnforceToolResultBudget applies the per-message aggregate budget.
// For each user message whose tool_result blocks exceed the limit, the largest
// FRESH results are truncated with previews. Previously-replaced results get
// the same replacement re-applied (byte-identical, no I/O).
//
// The state parameter is MUTATED: seenIDs and replacements are updated.
// Maps to enforceToolResultBudget() in toolResultStorage.ts.
func EnforceToolResultBudget(
	messages []agents.Message,
	state *ContentReplacementState,
	limit int,
	skipToolNames map[string]bool,
	logger *slog.Logger,
) *ToolResultBudgetResult {
	if state == nil {
		return &ToolResultBudgetResult{Messages: messages}
	}
	if limit <= 0 {
		limit = DefaultMaxToolResultsPerMessageChars
	}
	if logger == nil {
		logger = slog.Default()
	}

	state.mu.Lock()
	defer state.mu.Unlock()

	// Build tool name map for skip filtering
	nameByToolUseID := buildToolNameMap(messages)

	// Collect candidates grouped by API-level user message
	candidateGroups := collectCandidatesByMessage(messages)

	replacementMap := make(map[string]string)
	var newlyReplaced []ToolResultReplacementRecord
	reappliedCount := 0

	for _, candidates := range candidateGroups {
		partition := partitionByPriorDecision(candidates, state)

		// Re-apply cached replacements
		for _, c := range partition.mustReapply {
			replacementMap[c.toolUseID] = c.replacement
			reappliedCount++
		}

		if len(partition.fresh) == 0 {
			// All candidates already processed — update seenIDs
			for _, c := range candidates {
				state.seenIDs[c.toolUseID] = true
			}
			continue
		}

		// Filter out skip-listed tools
		var eligible []toolResultCandidate
		for _, c := range partition.fresh {
			toolName := nameByToolUseID[c.toolUseID]
			if skipToolNames != nil && skipToolNames[toolName] {
				state.seenIDs[c.toolUseID] = true
				continue
			}
			eligible = append(eligible, c)
		}

		frozenSize := 0
		for _, c := range partition.frozen {
			frozenSize += c.size
		}
		freshSize := 0
		for _, c := range eligible {
			freshSize += c.size
		}

		var selected []toolResultCandidate
		if frozenSize+freshSize > limit {
			selected = selectFreshToReplace(eligible, frozenSize, limit)
		}

		// Mark non-selected as seen
		selectedIDs := make(map[string]bool, len(selected))
		for _, c := range selected {
			selectedIDs[c.toolUseID] = true
		}
		for _, c := range candidates {
			if !selectedIDs[c.toolUseID] {
				state.seenIDs[c.toolUseID] = true
			}
		}

		// Build replacements for selected candidates
		for _, c := range selected {
			replacement := buildTruncatedReplacement(c)
			state.seenIDs[c.toolUseID] = true
			state.replacements[c.toolUseID] = replacement
			replacementMap[c.toolUseID] = replacement
			newlyReplaced = append(newlyReplaced, ToolResultReplacementRecord{
				Kind:        "tool-result",
				ToolUseID:   c.toolUseID,
				Replacement: replacement,
			})
		}
	}

	if len(replacementMap) == 0 {
		return &ToolResultBudgetResult{Messages: messages, ReappliedCount: reappliedCount}
	}

	if len(newlyReplaced) > 0 {
		logger.Info("tool result budget enforced",
			"newly_replaced", len(newlyReplaced),
			"reapplied", reappliedCount,
		)
	}

	return &ToolResultBudgetResult{
		Messages:       replaceToolResultContents(messages, replacementMap),
		NewlyReplaced:  newlyReplaced,
		ReappliedCount: reappliedCount,
	}
}

// ApplyToolResultBudget is the query-loop integration point.
// Gates on state being non-nil, applies enforcement, and fires optional
// transcript-write callback for new replacements.
// Maps to applyToolResultBudget() in toolResultStorage.ts.
func ApplyToolResultBudget(
	messages []agents.Message,
	state *ContentReplacementState,
	limit int,
	skipToolNames map[string]bool,
	writeToTranscript func(records []ToolResultReplacementRecord),
	logger *slog.Logger,
) []agents.Message {
	if state == nil {
		return messages
	}
	result := EnforceToolResultBudget(messages, state, limit, skipToolNames, logger)
	if len(result.NewlyReplaced) > 0 && writeToTranscript != nil {
		writeToTranscript(result.NewlyReplaced)
	}
	return result.Messages
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

// buildToolNameMap extracts tool_use_id -> tool name from assistant messages.
func buildToolNameMap(messages []agents.Message) map[string]string {
	m := make(map[string]string)
	for _, msg := range messages {
		if msg.Type != agents.MessageTypeAssistant {
			continue
		}
		for _, block := range msg.Content {
			if block.Type == agents.ContentBlockToolUse && block.ID != "" {
				m[block.ID] = block.Name
			}
		}
	}
	return m
}

// collectCandidatesByMessage groups eligible tool_result candidates by
// API-level user message boundaries (assistant messages create boundaries).
func collectCandidatesByMessage(messages []agents.Message) [][]toolResultCandidate {
	var groups [][]toolResultCandidate
	var current []toolResultCandidate
	seenAsstIDs := make(map[string]bool)

	flush := func() {
		if len(current) > 0 {
			groups = append(groups, current)
		}
		current = nil
	}

	for _, msg := range messages {
		switch msg.Type {
		case agents.MessageTypeUser:
			candidates := collectCandidatesFromMessage(msg)
			current = append(current, candidates...)

		case agents.MessageTypeAssistant:
			if !seenAsstIDs[msg.UUID] {
				flush()
				seenAsstIDs[msg.UUID] = true
			}
		}
	}
	flush()

	return groups
}

// collectCandidatesFromMessage extracts eligible tool_result blocks from a user message.
func collectCandidatesFromMessage(msg agents.Message) []toolResultCandidate {
	if msg.Type != agents.MessageTypeUser {
		return nil
	}
	var candidates []toolResultCandidate
	for _, block := range msg.Content {
		if block.Type != agents.ContentBlockToolResult {
			continue
		}
		content, ok := block.Content.(string)
		if !ok || content == "" {
			continue
		}
		// Skip already-compacted content
		if strings.HasPrefix(content, PersistedOutputTag) {
			continue
		}
		candidates = append(candidates, toolResultCandidate{
			toolUseID: block.ToolUseID,
			content:   content,
			size:      len(content),
		})
	}
	return candidates
}

// partitionByPriorDecision splits candidates into mustReapply/frozen/fresh.
func partitionByPriorDecision(candidates []toolResultCandidate, state *ContentReplacementState) candidatePartition {
	var p candidatePartition
	for _, c := range candidates {
		if replacement, ok := state.replacements[c.toolUseID]; ok {
			p.mustReapply = append(p.mustReapply, reapplyCandidate{
				toolResultCandidate: c,
				replacement:         replacement,
			})
		} else if state.seenIDs[c.toolUseID] {
			p.frozen = append(p.frozen, c)
		} else {
			p.fresh = append(p.fresh, c)
		}
	}
	return p
}

// selectFreshToReplace picks the largest fresh results to replace until
// the total is under budget.
func selectFreshToReplace(fresh []toolResultCandidate, frozenSize, limit int) []toolResultCandidate {
	sorted := make([]toolResultCandidate, len(fresh))
	copy(sorted, fresh)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].size > sorted[j].size // largest first
	})

	var selected []toolResultCandidate
	remaining := frozenSize
	for _, c := range fresh {
		remaining += c.size
	}

	for _, c := range sorted {
		if remaining <= limit {
			break
		}
		selected = append(selected, c)
		remaining -= c.size
	}
	return selected
}

// buildTruncatedReplacement creates an in-memory truncation with preview.
// Unlike the TS version, this doesn't persist to disk — the Go implementation
// keeps the preview in memory/message. For very large results, the content
// is replaced with a preview tag.
func buildTruncatedReplacement(c toolResultCandidate) string {
	preview, hasMore := generatePreview(c.content, PreviewSizeBytes)
	var sb strings.Builder
	sb.WriteString(PersistedOutputTag)
	sb.WriteString("\n")
	sb.WriteString(fmt.Sprintf("Output too large (%s). Content truncated.\n\n", formatSize(c.size)))
	sb.WriteString(fmt.Sprintf("Preview (first %s):\n", formatSize(PreviewSizeBytes)))
	sb.WriteString(preview)
	if hasMore {
		sb.WriteString("\n...\n")
	} else {
		sb.WriteString("\n")
	}
	sb.WriteString(PersistedOutputClosingTag)
	return sb.String()
}

// generatePreview truncates content at a newline boundary.
func generatePreview(content string, maxBytes int) (string, bool) {
	if len(content) <= maxBytes {
		return content, false
	}
	truncated := content[:maxBytes]
	lastNewline := strings.LastIndex(truncated, "\n")
	if lastNewline > maxBytes/2 {
		return content[:lastNewline], true
	}
	return truncated, true
}

// replaceToolResultContents returns new messages with replaced tool_result blocks.
func replaceToolResultContents(messages []agents.Message, replacementMap map[string]string) []agents.Message {
	result := make([]agents.Message, len(messages))
	for i, msg := range messages {
		if msg.Type != agents.MessageTypeUser {
			result[i] = msg
			continue
		}
		needsReplace := false
		for _, block := range msg.Content {
			if block.Type == agents.ContentBlockToolResult {
				if _, ok := replacementMap[block.ToolUseID]; ok {
					needsReplace = true
					break
				}
			}
		}
		if !needsReplace {
			result[i] = msg
			continue
		}
		// Clone message with replaced blocks
		newMsg := msg
		newMsg.Content = make([]agents.ContentBlock, len(msg.Content))
		copy(newMsg.Content, msg.Content)
		for j, block := range newMsg.Content {
			if block.Type == agents.ContentBlockToolResult {
				if replacement, ok := replacementMap[block.ToolUseID]; ok {
					newMsg.Content[j].Content = replacement
				}
			}
		}
		result[i] = newMsg
	}
	return result
}

// formatSize returns a human-readable size string.
func formatSize(bytes int) string {
	if bytes < 1024 {
		return fmt.Sprintf("%d B", bytes)
	}
	if bytes < 1024*1024 {
		return fmt.Sprintf("%.1f KB", float64(bytes)/1024)
	}
	return fmt.Sprintf("%.1f MB", float64(bytes)/(1024*1024))
}
