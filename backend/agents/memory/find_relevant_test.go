package memory

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// M12 · findRelevantMemories tests.
// ---------------------------------------------------------------------------

func TestFindRelevantMemories_NilSideQuery(t *testing.T) {
	old := DefaultSideQueryFn
	DefaultSideQueryFn = nil
	defer func() { DefaultSideQueryFn = old }()

	results, err := FindRelevantMemories(context.Background(), "query", t.TempDir(), nil, nil)
	assert.NoError(t, err)
	assert.Nil(t, results)
}

func TestFindRelevantMemories_EmptyDir(t *testing.T) {
	old := DefaultSideQueryFn
	DefaultSideQueryFn = func(_ context.Context, _, _ string) (string, error) {
		t.Fatal("side-query should not be called for empty dir")
		return "", nil
	}
	defer func() { DefaultSideQueryFn = old }()

	results, err := FindRelevantMemories(context.Background(), "query", t.TempDir(), nil, nil)
	assert.NoError(t, err)
	assert.Nil(t, results)
}

func TestFindRelevantMemories_JSONResponse(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "note1.md"), []byte("---\ndescription: First note\n---\ncontent1"), 0644)
	_ = os.WriteFile(filepath.Join(dir, "note2.md"), []byte("---\ndescription: Second note\n---\ncontent2"), 0644)
	_ = os.WriteFile(filepath.Join(dir, "note3.md"), []byte("---\ndescription: Third note\n---\ncontent3"), 0644)

	old := DefaultSideQueryFn
	DefaultSideQueryFn = func(_ context.Context, system, userMsg string) (string, error) {
		assert.Contains(t, system, "selecting memories")
		assert.Contains(t, userMsg, "First note")
		return `{"selected_memories": ["note1.md", "note3.md"]}`, nil
	}
	defer func() { DefaultSideQueryFn = old }()

	results, err := FindRelevantMemories(context.Background(), "test query", dir, nil, nil)
	require.NoError(t, err)
	require.Len(t, results, 2)
	filenames := []string{filepath.Base(results[0].Path), filepath.Base(results[1].Path)}
	assert.Contains(t, filenames, "note1.md")
	assert.Contains(t, filenames, "note3.md")
}

func TestFindRelevantMemories_AlreadySurfaced(t *testing.T) {
	dir := t.TempDir()
	note1 := filepath.Join(dir, "note1.md")
	_ = os.WriteFile(note1, []byte("content1"), 0644)
	_ = os.WriteFile(filepath.Join(dir, "note2.md"), []byte("content2"), 0644)

	old := DefaultSideQueryFn
	var capturedUserMsg string
	DefaultSideQueryFn = func(_ context.Context, _, userMsg string) (string, error) {
		capturedUserMsg = userMsg
		return `{"selected_memories": ["note2.md"]}`, nil
	}
	defer func() { DefaultSideQueryFn = old }()

	surfaced := map[string]bool{note1: true}
	results, err := FindRelevantMemories(context.Background(), "query", dir, nil, surfaced)
	require.NoError(t, err)

	// note1 should not appear in the manifest sent to the selector.
	assert.NotContains(t, capturedUserMsg, "note1.md")
	require.Len(t, results, 1)
	assert.Equal(t, "note2.md", filepath.Base(results[0].Path))
}

func TestFindRelevantMemories_RecentTools(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "note.md"), []byte("content"), 0644)

	old := DefaultSideQueryFn
	var capturedUserMsg string
	DefaultSideQueryFn = func(_ context.Context, _, userMsg string) (string, error) {
		capturedUserMsg = userMsg
		return `{"selected_memories": []}`, nil
	}
	defer func() { DefaultSideQueryFn = old }()

	_, _ = FindRelevantMemories(context.Background(), "query", dir, []string{"mcp__X__spawn"}, nil)
	assert.Contains(t, capturedUserMsg, "Recently used tools: mcp__X__spawn")
}

func TestFindRelevantMemories_SideQueryError(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "note.md"), []byte("content"), 0644)

	old := DefaultSideQueryFn
	DefaultSideQueryFn = func(_ context.Context, _, _ string) (string, error) {
		return "", errors.New("api error")
	}
	defer func() { DefaultSideQueryFn = old }()

	results, err := FindRelevantMemories(context.Background(), "query", dir, nil, nil)
	assert.NoError(t, err, "should degrade gracefully")
	assert.Nil(t, results)
}

func TestFindRelevantMemories_PlaintextResponse(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "alpha.md"), []byte("a"), 0644)
	_ = os.WriteFile(filepath.Join(dir, "beta.md"), []byte("b"), 0644)

	old := DefaultSideQueryFn
	DefaultSideQueryFn = func(_ context.Context, _, _ string) (string, error) {
		return "- alpha.md\n- beta.md\n", nil
	}
	defer func() { DefaultSideQueryFn = old }()

	results, err := FindRelevantMemories(context.Background(), "query", dir, nil, nil)
	require.NoError(t, err)
	assert.Len(t, results, 2)
}

// ---------------------------------------------------------------------------
// parseSelectedMemories tests.
// ---------------------------------------------------------------------------

func TestParseSelectedMemories_JSON(t *testing.T) {
	r := parseSelectedMemories(`{"selected_memories": ["a.md", "b.md"]}`)
	assert.Equal(t, []string{"a.md", "b.md"}, r)
}

func TestParseSelectedMemories_EmptyJSON(t *testing.T) {
	r := parseSelectedMemories(`{"selected_memories": []}`)
	// Empty array — falls through to line parse which also returns nil.
	assert.Empty(t, r)
}

func TestParseSelectedMemories_Plaintext(t *testing.T) {
	r := parseSelectedMemories("- note.md\n* ref.md\n")
	assert.Equal(t, []string{"note.md", "ref.md"}, r)
}

func TestParseSelectedMemories_Empty(t *testing.T) {
	r := parseSelectedMemories("")
	assert.Empty(t, r)
}

// ---------------------------------------------------------------------------
// extractJSONStringArray tests.
// ---------------------------------------------------------------------------

func TestExtractJSONStringArray(t *testing.T) {
	tests := []struct {
		input string
		key   string
		want  []string
	}{
		{`{"selected_memories": ["a.md", "b.md"]}`, "selected_memories", []string{"a.md", "b.md"}},
		{`{"selected_memories": []}`, "selected_memories", nil},
		{`{"other": ["x"]}`, "selected_memories", nil},
		{`not json`, "key", nil},
	}
	for _, tt := range tests {
		got := extractJSONStringArray(tt.input, tt.key)
		assert.Equal(t, tt.want, got, "input=%q key=%q", tt.input, tt.key)
	}
}

// ---------------------------------------------------------------------------
// ReadMemoryFileContent tests.
// ---------------------------------------------------------------------------

func TestReadMemoryFileContent_Fresh(t *testing.T) {
	f := filepath.Join(t.TempDir(), "note.md")
	_ = os.WriteFile(f, []byte("hello world"), 0644)

	content, err := ReadMemoryFileContent(f, time.Now().UnixMilli())
	require.NoError(t, err)
	assert.Equal(t, "hello world", content)
}

func TestReadMemoryFileContent_Stale(t *testing.T) {
	f := filepath.Join(t.TempDir(), "note.md")
	_ = os.WriteFile(f, []byte("hello world"), 0644)

	stale := time.Now().Add(-10 * 24 * time.Hour).UnixMilli()
	content, err := ReadMemoryFileContent(f, stale)
	require.NoError(t, err)
	assert.Contains(t, content, "<system-reminder>")
	assert.Contains(t, content, "10 days old")
	assert.Contains(t, content, "hello world")
}

func TestReadMemoryFileContent_Missing(t *testing.T) {
	_, err := ReadMemoryFileContent(filepath.Join(t.TempDir(), "nope.md"), 0)
	assert.Error(t, err)
}

// ---------------------------------------------------------------------------
// SelectMemoriesPrompt smoke test.
// ---------------------------------------------------------------------------

func TestSelectMemoriesPrompt(t *testing.T) {
	assert.Contains(t, SelectMemoriesPrompt, "selecting memories")
	assert.Contains(t, SelectMemoriesPrompt, "up to 5")
	assert.Contains(t, SelectMemoriesPrompt, "recently-used tools")
}
