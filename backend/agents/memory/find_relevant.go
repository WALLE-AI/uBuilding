package memory

import (
	"context"
	"fmt"
	"os"
	"strings"
)

// ---------------------------------------------------------------------------
// M12 · findRelevantMemories — query-time recall.
//
// Ports `src/memdir/findRelevantMemories.ts`:
//
//   - RelevantMemory          — result type (path + mtime)
//   - SelectMemoriesPrompt    — system prompt for the selector
//   - FindRelevantMemories    — orchestrator: scan → format → select
//   - SideQueryFn             — injectable LLM call
//
// The actual LLM call is injected via SideQueryFn so the memory package
// stays free of API-client imports. Hosts wire this via
//
//   memory.DefaultSideQueryFn = func(...) (string, error) { ... }
//
// When SideQueryFn is nil, FindRelevantMemories returns empty
// (graceful degradation).
// ---------------------------------------------------------------------------

// RelevantMemory is a single memory selected by the recall selector.
type RelevantMemory struct {
	// Path is the absolute file path.
	Path string
	// MtimeMs is the modification time in milliseconds since epoch,
	// threaded through so callers can surface freshness without a second stat.
	MtimeMs int64
}

// SelectMemoriesPrompt is the system prompt for the memory selector
// side-query. Mirrors TS SELECT_MEMORIES_SYSTEM_PROMPT.
const SelectMemoriesPrompt = `You are selecting memories that will be useful to Claude Code as it processes a user's query. You will be given the user's query and a list of available memory files with their filenames and descriptions.

Return a list of filenames for the memories that will clearly be useful to Claude Code as it processes the user's query (up to 5). Only include memories that you are certain will be helpful based on their name and description.
- If you are unsure if a memory will be useful in processing the user's query, then do not include it in your list. Be selective and discerning.
- If there are no memories in the list that would clearly be useful, feel free to return an empty list.
- If a list of recently-used tools is provided, do not select memories that are usage reference or API documentation for those tools (Claude Code is already exercising them). DO still select memories containing warnings, gotchas, or known issues about those tools — active use is exactly when those matter.
`

// SideQueryFn is the signature for a lightweight LLM call used by the
// memory selector. Hosts inject the concrete implementation so the
// memory package stays free of API-client imports.
//
// Parameters:
//   - ctx: cancellation / timeout context
//   - system: the system prompt (SelectMemoriesPrompt)
//   - userMessage: the formatted user message (query + manifest)
//
// Returns the raw text response from the model, or an error.
type SideQueryFn func(ctx context.Context, system, userMessage string) (string, error)

// DefaultSideQueryFn is the package-level side-query function. Hosts
// set this during init to wire the LLM client. Nil means recall is
// disabled (FindRelevantMemories returns empty).
var DefaultSideQueryFn SideQueryFn

// FindRelevantMemories scans the memory directory, asks a side-query
// (Sonnet) to select the most relevant files for the given query, and
// returns up to 5 absolute file paths + mtime.
//
//   - MEMORY.md is excluded (already loaded in system prompt).
//   - alreadySurfaced filters paths shown in prior turns.
//   - recentTools lets the selector suppress reference docs for
//     tools the model is already exercising.
//
// Returns empty (not error) on graceful degradation (no SideQueryFn,
// no memory files, selector failure, context cancellation).
func FindRelevantMemories(
	ctx context.Context,
	query string,
	memoryDir string,
	recentTools []string,
	alreadySurfaced map[string]bool,
) ([]RelevantMemory, error) {
	if DefaultSideQueryFn == nil {
		return nil, nil
	}

	// Scan memory files.
	headers, err := ScanMemoryFiles(memoryDir)
	if err != nil || len(headers) == 0 {
		return nil, nil
	}

	// Filter out already-surfaced files.
	if len(alreadySurfaced) > 0 {
		filtered := make([]MemoryHeader, 0, len(headers))
		for _, h := range headers {
			if !alreadySurfaced[h.FilePath] {
				filtered = append(filtered, h)
			}
		}
		headers = filtered
	}
	if len(headers) == 0 {
		return nil, nil
	}

	// Build the selector user message.
	manifest := FormatMemoryManifest(headers)
	var toolsSection string
	if len(recentTools) > 0 {
		toolsSection = "\n\nRecently used tools: " + strings.Join(recentTools, ", ")
	}
	userMessage := fmt.Sprintf("Query: %s\n\nAvailable memories:\n%s%s",
		query, manifest, toolsSection)

	// Call the side-query.
	response, err := DefaultSideQueryFn(ctx, SelectMemoriesPrompt, userMessage)
	if err != nil {
		// Graceful degradation — context cancellation or LLM error.
		return nil, nil
	}

	// Parse the response. We expect JSON like {"selected_memories": ["file1.md", "file2.md"]}.
	selectedFilenames := parseSelectedMemories(response)

	// Map filenames back to headers.
	byFilename := make(map[string]MemoryHeader, len(headers))
	for _, h := range headers {
		byFilename[h.Filename] = h
	}

	var results []RelevantMemory
	for _, fn := range selectedFilenames {
		if h, ok := byFilename[fn]; ok {
			results = append(results, RelevantMemory{
				Path:    h.FilePath,
				MtimeMs: h.MtimeMs,
			})
		}
	}
	return results, nil
}

// parseSelectedMemories extracts filenames from the selector response.
// Handles both JSON {"selected_memories": [...]} and plain text (one
// filename per line or comma-separated).
func parseSelectedMemories(response string) []string {
	response = strings.TrimSpace(response)

	// Try JSON parse.
	if strings.HasPrefix(response, "{") {
		filenames := extractJSONStringArray(response, "selected_memories")
		if len(filenames) > 0 {
			return filenames
		}
	}

	// Fallback: line-separated.
	var out []string
	for _, line := range strings.Split(response, "\n") {
		line = strings.TrimSpace(line)
		line = strings.TrimPrefix(line, "- ")
		line = strings.TrimPrefix(line, "* ")
		if line != "" && strings.HasSuffix(line, ".md") {
			out = append(out, line)
		}
	}
	return out
}

// extractJSONStringArray is a minimal JSON extraction for
// {"key": ["v1","v2"]} without importing encoding/json (keep
// dependencies light). Falls back to empty if the structure is
// unexpected.
func extractJSONStringArray(s, key string) []string {
	// Find "key": [ ... ]
	needle := fmt.Sprintf(`"%s"`, key)
	idx := strings.Index(s, needle)
	if idx < 0 {
		return nil
	}
	rest := s[idx+len(needle):]
	// Skip whitespace and colon.
	rest = strings.TrimLeft(rest, " \t\n\r:")
	if !strings.HasPrefix(rest, "[") {
		return nil
	}
	rest = rest[1:]
	end := strings.Index(rest, "]")
	if end < 0 {
		return nil
	}
	content := rest[:end]

	var out []string
	for _, part := range strings.Split(content, ",") {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, `"`) && strings.HasSuffix(part, `"`) {
			out = append(out, part[1:len(part)-1])
		}
	}
	return out
}

// ReadMemoryFileContent reads a memory file and returns its content
// with an optional freshness note prepended. This is the function
// callers use to inject selected memories into the conversation.
func ReadMemoryFileContent(path string, mtimeMs int64) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	content := string(data)
	freshnessNote := MemoryFreshnessNote(mtimeMs)
	if freshnessNote != "" {
		return freshnessNote + content, nil
	}
	return content, nil
}
