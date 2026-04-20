package memory

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// M10 · Memory directory scanning primitives.
//
// Ports `src/memdir/memoryScan.ts`:
//
//   - MemoryHeader           — per-file header extracted from frontmatter
//   - ScanMemoryFiles        — recursive .md scan with frontmatter parse
//   - FormatMemoryManifest   — text manifest for the selector prompt
//
// These are shared by findRelevantMemories (query-time recall) and
// extractMemories (pre-injects the listing so the extraction agent
// doesn't spend a turn on `ls`).
// ---------------------------------------------------------------------------

// MaxMemoryFiles caps the returned scan results. Matches TS
// MAX_MEMORY_FILES = 200.
const MaxMemoryFiles = 200

// FrontmatterMaxLines is the max lines read from the top of a file
// for frontmatter extraction. Matches TS FRONTMATTER_MAX_LINES = 30.
const FrontmatterMaxLines = 30

// MemoryHeader is the per-file header extracted by ScanMemoryFiles.
// Mirrors TS `MemoryHeader` type.
type MemoryHeader struct {
	// Filename is the relative path within the memory directory.
	Filename string

	// FilePath is the absolute filesystem path.
	FilePath string

	// MtimeMs is the modification time in milliseconds since epoch.
	MtimeMs int64

	// Description is the frontmatter `description` field, or "".
	Description string

	// ContentType is the parsed memory type from frontmatter, or "".
	ContentType MemoryContentType
}

// ScanMemoryFiles scans a memory directory for .md files, reads their
// frontmatter, and returns a header list sorted newest-first (capped at
// MaxMemoryFiles). MEMORY.md is excluded (it's already loaded in the
// system prompt).
//
// Single-pass: stat + readHead together, then sort. For the common case
// (N ≤ 200) this halves syscalls vs a separate stat round.
func ScanMemoryFiles(memoryDir string) ([]MemoryHeader, error) {
	var headers []MemoryHeader

	err := filepath.WalkDir(memoryDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable entries
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(strings.ToLower(d.Name()), ".md") {
			return nil
		}
		if strings.EqualFold(d.Name(), autoMemEntrypoint) {
			return nil // skip MEMORY.md
		}

		relPath, _ := filepath.Rel(memoryDir, path)
		if relPath == "" {
			relPath = d.Name()
		}
		// Normalize to forward slashes for consistency.
		relPath = strings.ReplaceAll(relPath, `\`, "/")

		header := readMemoryHeader(path, relPath)
		headers = append(headers, header)
		return nil
	})
	if err != nil {
		return nil, err
	}

	// Sort newest-first.
	sort.Slice(headers, func(i, j int) bool {
		return headers[i].MtimeMs > headers[j].MtimeMs
	})

	// Cap at MaxMemoryFiles.
	if len(headers) > MaxMemoryFiles {
		headers = headers[:MaxMemoryFiles]
	}
	return headers, nil
}

// readMemoryHeader reads just the frontmatter of a file and extracts
// the MemoryHeader fields.
func readMemoryHeader(absPath, relPath string) MemoryHeader {
	h := MemoryHeader{
		Filename: relPath,
		FilePath: absPath,
	}

	// Stat for mtime.
	info, err := os.Stat(absPath)
	if err == nil {
		h.MtimeMs = info.ModTime().UnixMilli()
	}

	// Read just the first FrontmatterMaxLines lines.
	data, err := os.ReadFile(absPath)
	if err != nil {
		return h
	}
	content := limitToLines(string(data), FrontmatterMaxLines)

	parsed := ParseFrontmatter(content)
	if parsed.Frontmatter.Description != "" {
		h.Description = parsed.Frontmatter.Description
	}
	if parsed.Frontmatter.Type != "" {
		if ct, ok := ParseMemoryContentType(parsed.Frontmatter.Type); ok {
			h.ContentType = ct
		}
	}
	return h
}

// limitToLines returns at most n lines from s.
func limitToLines(s string, n int) string {
	lines := strings.SplitN(s, "\n", n+1)
	if len(lines) <= n {
		return s
	}
	return strings.Join(lines[:n], "\n")
}

// ---------------------------------------------------------------------------
// M10.I2 · FormatMemoryManifest
// ---------------------------------------------------------------------------

// FormatMemoryManifest formats memory headers as a text manifest: one
// line per file with [type] filename (timestamp): description. Used by
// both the recall selector prompt and the extraction-agent prompt.
func FormatMemoryManifest(memories []MemoryHeader) string {
	var b strings.Builder
	for i, m := range memories {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString("- ")
		if m.ContentType != "" {
			fmt.Fprintf(&b, "[%s] ", m.ContentType)
		}
		ts := time.UnixMilli(m.MtimeMs).UTC().Format(time.RFC3339)
		if m.Description != "" {
			fmt.Fprintf(&b, "%s (%s): %s", m.Filename, ts, m.Description)
		} else {
			fmt.Fprintf(&b, "%s (%s)", m.Filename, ts)
		}
	}
	return b.String()
}
