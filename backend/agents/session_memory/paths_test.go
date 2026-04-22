package session_memory

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetSessionMemoryDir(t *testing.T) {
	dir := GetSessionMemoryDir("/root/sessions/abc")
	assert.Equal(t, filepath.Join("/root/sessions/abc", SessionMemoryDirName), dir)
}

func TestGetSessionMemoryPath(t *testing.T) {
	path := GetSessionMemoryPath("/root/sessions/abc")
	assert.Equal(t, filepath.Join("/root/sessions/abc", SessionMemoryDirName, SessionMemoryFileName), path)
}

func TestGetSessionMemoryContent_Missing(t *testing.T) {
	content := GetSessionMemoryContent(filepath.Join(t.TempDir(), "nope"))
	assert.Empty(t, content)
}

func TestGetSessionMemoryContent_Exists(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, SessionMemoryDirName)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, SessionMemoryFileName), []byte("# Notes"), 0o644))

	content := GetSessionMemoryContent(root)
	assert.Equal(t, "# Notes", content)
}
