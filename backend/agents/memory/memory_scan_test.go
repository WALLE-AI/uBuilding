package memory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// M10 · Memory scan tests.
// ---------------------------------------------------------------------------

func TestScanMemoryFiles_Empty(t *testing.T) {
	dir := t.TempDir()
	headers, err := ScanMemoryFiles(dir)
	require.NoError(t, err)
	assert.Empty(t, headers)
}

func TestScanMemoryFiles_SkipsMEMORYmd(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "MEMORY.md"), []byte("index"), 0644)
	_ = os.WriteFile(filepath.Join(dir, "note.md"), []byte("hello"), 0644)

	headers, err := ScanMemoryFiles(dir)
	require.NoError(t, err)
	require.Len(t, headers, 1)
	assert.Equal(t, "note.md", headers[0].Filename)
}

func TestScanMemoryFiles_SkipsNonMd(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "data.json"), []byte("{}"), 0644)
	_ = os.WriteFile(filepath.Join(dir, "note.md"), []byte("hello"), 0644)

	headers, err := ScanMemoryFiles(dir)
	require.NoError(t, err)
	require.Len(t, headers, 1)
	assert.Equal(t, "note.md", headers[0].Filename)
}

func TestScanMemoryFiles_ParsesFrontmatter(t *testing.T) {
	dir := t.TempDir()
	content := "---\ndescription: My note\ntype: user\n---\n# Hello\nworld"
	_ = os.WriteFile(filepath.Join(dir, "note.md"), []byte(content), 0644)

	headers, err := ScanMemoryFiles(dir)
	require.NoError(t, err)
	require.Len(t, headers, 1)
	assert.Equal(t, "My note", headers[0].Description)
	assert.Equal(t, MemoryContentTypeUser, headers[0].ContentType)
}

func TestScanMemoryFiles_SortedNewestFirst(t *testing.T) {
	dir := t.TempDir()

	// Create two files with different mtimes.
	old := filepath.Join(dir, "old.md")
	_ = os.WriteFile(old, []byte("old"), 0644)
	oldTime := time.Now().Add(-24 * time.Hour)
	_ = os.Chtimes(old, oldTime, oldTime)

	newer := filepath.Join(dir, "newer.md")
	_ = os.WriteFile(newer, []byte("newer"), 0644)

	headers, err := ScanMemoryFiles(dir)
	require.NoError(t, err)
	require.Len(t, headers, 2)
	assert.Equal(t, "newer.md", headers[0].Filename)
	assert.Equal(t, "old.md", headers[1].Filename)
}

func TestScanMemoryFiles_Recursive(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")
	_ = os.MkdirAll(sub, 0755)
	_ = os.WriteFile(filepath.Join(sub, "deep.md"), []byte("deep"), 0644)
	_ = os.WriteFile(filepath.Join(dir, "top.md"), []byte("top"), 0644)

	headers, err := ScanMemoryFiles(dir)
	require.NoError(t, err)
	require.Len(t, headers, 2)
	// Both should be present; relative paths use forward slashes.
	filenames := []string{headers[0].Filename, headers[1].Filename}
	assert.Contains(t, filenames, "top.md")
	assert.Contains(t, filenames, "sub/deep.md")
}

func TestScanMemoryFiles_CapsAtMax(t *testing.T) {
	dir := t.TempDir()
	// Create MaxMemoryFiles + 5 files.
	for i := 0; i < MaxMemoryFiles+5; i++ {
		name := filepath.Join(dir, strings.Replace("note_NNN.md", "NNN", strings.Repeat("x", 3)+string(rune('a'+i%26)), 1))
		_ = os.WriteFile(name, []byte("content"), 0644)
	}

	headers, err := ScanMemoryFiles(dir)
	require.NoError(t, err)
	assert.LessOrEqual(t, len(headers), MaxMemoryFiles)
}

func TestScanMemoryFiles_MissingDir(t *testing.T) {
	headers, err := ScanMemoryFiles(filepath.Join(t.TempDir(), "nonexistent"))
	// WalkDir may return error or nil depending on platform/implementation.
	if err != nil {
		assert.Nil(t, headers)
	} else {
		assert.Empty(t, headers)
	}
}

// ---------------------------------------------------------------------------
// FormatMemoryManifest tests.
// ---------------------------------------------------------------------------

func TestFormatMemoryManifest_Empty(t *testing.T) {
	assert.Equal(t, "", FormatMemoryManifest(nil))
}

func TestFormatMemoryManifest_Basic(t *testing.T) {
	ts := time.Date(2025, 1, 15, 10, 30, 0, 0, time.UTC).UnixMilli()
	headers := []MemoryHeader{
		{Filename: "note.md", MtimeMs: ts, Description: "A note", ContentType: MemoryContentTypeUser},
		{Filename: "ref.md", MtimeMs: ts},
	}
	result := FormatMemoryManifest(headers)
	assert.Contains(t, result, "[user] note.md")
	assert.Contains(t, result, ": A note")
	assert.Contains(t, result, "ref.md (")
	// Two lines.
	lines := strings.Split(result, "\n")
	assert.Len(t, lines, 2)
}

func TestFormatMemoryManifest_NoDescription(t *testing.T) {
	ts := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
	headers := []MemoryHeader{
		{Filename: "bare.md", MtimeMs: ts},
	}
	result := FormatMemoryManifest(headers)
	assert.Contains(t, result, "bare.md (")
	// No description suffix — line ends with closing paren, no ": desc" part.
	assert.True(t, strings.HasSuffix(strings.TrimSpace(result), ")"),
		"should end with ) not a description")
}

// ---------------------------------------------------------------------------
// limitToLines tests.
// ---------------------------------------------------------------------------

func TestLimitToLines(t *testing.T) {
	input := "a\nb\nc\nd\ne"
	assert.Equal(t, "a\nb\nc", limitToLines(input, 3))
	assert.Equal(t, input, limitToLines(input, 10))
	assert.Equal(t, "a", limitToLines(input, 1))
}
