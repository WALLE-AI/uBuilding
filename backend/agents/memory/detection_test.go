package memory

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/wall-ai/ubuilding/backend/agents"
)

// ---------------------------------------------------------------------------
// M9 · Detection predicate tests.
// ---------------------------------------------------------------------------

func TestIsAutoMemPath(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(EnvCoworkMemoryPathOverride, dir)
	defer ResetMemoryBaseDirCache()

	assert.True(t, IsAutoMemPath(filepath.Join(dir, "foo.md"), dir, NopSettingsProvider))
	assert.False(t, IsAutoMemPath("/some/other/path", dir, NopSettingsProvider))
}

func TestIsAutoMemFile_Disabled(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(EnvCoworkMemoryPathOverride, dir)
	t.Setenv(EnvDisableAutoMemory, "1")
	defer ResetMemoryBaseDirCache()

	cfg := agents.EngineConfig{AutoMemoryEnabled: false}
	assert.False(t, IsAutoMemFile(filepath.Join(dir, "f.md"), dir, NopSettingsProvider, cfg))
}

func TestIsAutoMemFile_Enabled(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(EnvCoworkMemoryPathOverride, dir)
	t.Setenv(EnvEnableAutoMemory, "1")
	defer ResetMemoryBaseDirCache()

	cfg := agents.EngineConfig{AutoMemoryEnabled: true}
	assert.True(t, IsAutoMemFile(filepath.Join(dir, "f.md"), dir, NopSettingsProvider, cfg))
	assert.False(t, IsAutoMemFile("/other/f.md", dir, NopSettingsProvider, cfg))
}

func TestMemoryScopeForPath_Personal(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(EnvCoworkMemoryPathOverride, dir)
	t.Setenv(EnvEnableAutoMemory, "1")
	defer ResetMemoryBaseDirCache()

	cfg := agents.EngineConfig{AutoMemoryEnabled: true}
	scope := MemoryScopeForPath(filepath.Join(dir, "f.md"), dir, NopSettingsProvider, cfg)
	assert.Equal(t, MemoryScopePersonal, scope)
}

func TestMemoryScopeForPath_None(t *testing.T) {
	cfg := agents.EngineConfig{}
	scope := MemoryScopeForPath("/random/path.md", "/cwd", NopSettingsProvider, cfg)
	assert.Equal(t, MemoryScope(""), scope)
}

func TestDetectSessionFileType(t *testing.T) {
	base := GetMemoryBaseDir()
	if base == "" {
		t.Skip("GetMemoryBaseDir() returned empty")
	}

	tests := []struct {
		path string
		want SessionFileType
	}{
		{filepath.Join(base, "session-memory", "test.md"), SessionFileMemory},
		{filepath.Join(base, "projects", "abc", "log.jsonl"), SessionFileTranscript},
		{filepath.Join(base, "projects", "abc", "note.md"), ""},
		{"/unrelated/path.md", ""},
	}
	for _, tt := range tests {
		got := DetectSessionFileType(tt.path)
		assert.Equal(t, tt.want, got, "path=%s", tt.path)
	}
}

func TestDetectSessionPatternType(t *testing.T) {
	assert.Equal(t, SessionFileMemory, DetectSessionPatternType("session-memory/*.md"))
	assert.Equal(t, SessionFileTranscript, DetectSessionPatternType("*.jsonl"))
	assert.Equal(t, SessionFileType(""), DetectSessionPatternType("*.go"))
}

func TestIsAutoManagedMemoryFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(EnvCoworkMemoryPathOverride, dir)
	t.Setenv(EnvEnableAutoMemory, "1")
	defer ResetMemoryBaseDirCache()

	cfg := agents.EngineConfig{AutoMemoryEnabled: true}
	assert.True(t, IsAutoManagedMemoryFile(filepath.Join(dir, "test.md"), dir, NopSettingsProvider, cfg))
	assert.False(t, IsAutoManagedMemoryFile("/other/CLAUDE.md", dir, NopSettingsProvider, cfg))
}

func TestIsMemoryDirectory(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(EnvCoworkMemoryPathOverride, dir)
	t.Setenv(EnvEnableAutoMemory, "1")
	defer ResetMemoryBaseDirCache()

	cfg := agents.EngineConfig{AutoMemoryEnabled: true}
	assert.True(t, IsMemoryDirectory(dir, dir, NopSettingsProvider, cfg))
	assert.True(t, IsMemoryDirectory(filepath.Join(dir, "sub"), dir, NopSettingsProvider, cfg))
	assert.False(t, IsMemoryDirectory("/totally/unrelated", dir, NopSettingsProvider, cfg))
}

func TestIsShellCommandTargetingMemory(t *testing.T) {
	dir := t.TempDir()
	memDir := filepath.Join(dir, "memory")
	_ = os.MkdirAll(memDir, 0755)
	t.Setenv(EnvCoworkMemoryPathOverride, memDir)
	t.Setenv(EnvEnableAutoMemory, "1")
	defer ResetMemoryBaseDirCache()

	cfg := agents.EngineConfig{AutoMemoryEnabled: true}

	cmd := "grep -r 'TODO' " + filepath.Join(memDir, "notes.md")
	assert.True(t, IsShellCommandTargetingMemory(cmd, dir, NopSettingsProvider, cfg))

	assert.False(t, IsShellCommandTargetingMemory("grep -r 'TODO' /some/other/dir", dir, NopSettingsProvider, cfg))
}

func TestIsAutoManagedMemoryPattern(t *testing.T) {
	assert.True(t, IsAutoManagedMemoryPattern("session-memory/*.md"))
	assert.True(t, IsAutoManagedMemoryPattern("projects/*.jsonl"))
	assert.False(t, IsAutoManagedMemoryPattern("src/*.go"))
}

func TestToComparable(t *testing.T) {
	result := toComparable(`C:\Users\Test`)
	assert.Contains(t, result, "/")
	assert.NotContains(t, result, `\`)
}

func TestPathStartsWith(t *testing.T) {
	assert.True(t, pathStartsWith("/a/b/c", "/a/b"))
	assert.True(t, pathStartsWith("/a/b", "/a/b"))
	assert.True(t, pathStartsWith("/a/b/", "/a/b/"))
	assert.False(t, pathStartsWith("/a/bc", "/a/b"))
	assert.False(t, pathStartsWith("/other", "/a/b"))
}
