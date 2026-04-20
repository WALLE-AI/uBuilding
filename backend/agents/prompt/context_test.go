package prompt_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wall-ai/ubuilding/backend/agents"
	"github.com/wall-ai/ubuilding/backend/agents/memory"
	"github.com/wall-ai/ubuilding/backend/agents/prompt"
)

// ---------------------------------------------------------------------------
// M8 · ContextProvider memory-module integration tests.
// ---------------------------------------------------------------------------

// TestContextProvider_LegacyPath verifies the old loadClaudeMdFiles path
// still works when no MemoryLoaderConfig is set.
func TestContextProvider_LegacyPath(t *testing.T) {
	dir := t.TempDir()
	err := os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("legacy rule"), 0644)
	require.NoError(t, err)

	cp := prompt.NewContextProvider(dir)
	uc := cp.GetUserContext()
	require.NotNil(t, uc)
	assert.Contains(t, uc.ClaudeMd, "legacy rule")
	assert.Contains(t, uc.CurrentDate, "Today's date is")
}

// TestContextProvider_MemoryModulePath verifies that when a MemoryLoaderConfig
// is supplied, GetUserContext uses memory.GetMemoryFiles + BuildUserContextClaudeMd.
func TestContextProvider_MemoryModulePath(t *testing.T) {
	dir := t.TempDir()
	// Create a CLAUDE.md in the cwd so the memory module can find it.
	content := "# Project Rules\nUse gofmt always."
	err := os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte(content), 0644)
	require.NoError(t, err)

	loaderCfg := memory.LoaderConfig{
		Cwd:              dir,
		DisableUserTier:  true, // avoid home-dir side effects
		DisableLocalTier: true,
	}
	cp := prompt.NewContextProvider(dir,
		prompt.WithMemoryLoaderConfig(loaderCfg),
	)

	uc := cp.GetUserContext()
	require.NotNil(t, uc)
	// The rendered output should contain the file content with tier label.
	assert.Contains(t, uc.ClaudeMd, "Use gofmt always")
	assert.Contains(t, uc.ClaudeMd, "Project")
}

// TestContextProvider_MemoryModulePath_Empty verifies that ClaudeMd is empty
// when no memory files are found.
func TestContextProvider_MemoryModulePath_Empty(t *testing.T) {
	dir := t.TempDir()
	loaderCfg := memory.LoaderConfig{
		Cwd:              dir,
		DisableUserTier:  true,
		DisableLocalTier: true,
	}
	cp := prompt.NewContextProvider(dir,
		prompt.WithMemoryLoaderConfig(loaderCfg),
	)

	uc := cp.GetUserContext()
	require.NotNil(t, uc)
	assert.Empty(t, uc.ClaudeMd)
}

// TestContextProvider_DisableClaudeMd verifies that disableClaudeMd skips
// both the legacy and memory module paths.
func TestContextProvider_DisableClaudeMd(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("should not appear"), 0644)

	// With memory loader config but disabled.
	loaderCfg := memory.LoaderConfig{Cwd: dir}
	cp := prompt.NewContextProvider(dir,
		prompt.WithMemoryLoaderConfig(loaderCfg),
		prompt.WithDisableClaudeMd(true),
	)

	uc := cp.GetUserContext()
	require.NotNil(t, uc)
	assert.Empty(t, uc.ClaudeMd)
}

// TestContextProvider_GetCachedMemoryFiles verifies the accessor returns
// the same cached slice as used by GetUserContext.
func TestContextProvider_GetCachedMemoryFiles(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("cached content"), 0644)

	loaderCfg := memory.LoaderConfig{
		Cwd:              dir,
		DisableUserTier:  true,
		DisableLocalTier: true,
	}
	cp := prompt.NewContextProvider(dir,
		prompt.WithMemoryLoaderConfig(loaderCfg),
	)

	// Trigger load via GetUserContext.
	_ = cp.GetUserContext()

	files := cp.GetCachedMemoryFiles()
	require.NotEmpty(t, files)
	assert.Contains(t, files[0].Content, "cached content")
}

// TestContextProvider_GetCachedMemoryFiles_NoConfig returns nil when
// no memory loader config is set.
func TestContextProvider_GetCachedMemoryFiles_NoConfig(t *testing.T) {
	cp := prompt.NewContextProvider(t.TempDir())
	assert.Nil(t, cp.GetCachedMemoryFiles())
}

// TestContextProvider_Clear resets all memoized state.
func TestContextProvider_Clear(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("v1"), 0644)

	loaderCfg := memory.LoaderConfig{
		Cwd:              dir,
		DisableUserTier:  true,
		DisableLocalTier: true,
	}
	cp := prompt.NewContextProvider(dir,
		prompt.WithMemoryLoaderConfig(loaderCfg),
	)

	uc1 := cp.GetUserContext()
	assert.Contains(t, uc1.ClaudeMd, "v1")

	// Overwrite the file and clear the cache.
	_ = os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("v2"), 0644)
	cp.Clear()

	uc2 := cp.GetUserContext()
	assert.Contains(t, uc2.ClaudeMd, "v2")
}

// TestContextProvider_WithMemoryRenderOptions verifies custom render options.
func TestContextProvider_WithMemoryRenderOptions(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("project rule"), 0644)

	loaderCfg := memory.LoaderConfig{
		Cwd:              dir,
		DisableUserTier:  true,
		DisableLocalTier: true,
	}
	// SkipProjectLevel should suppress Project tier files.
	cp := prompt.NewContextProvider(dir,
		prompt.WithMemoryLoaderConfig(loaderCfg),
		prompt.WithMemoryRenderOptions(memory.ClaudeMdRenderOptions{
			SkipProjectLevel: true,
		}),
	)

	uc := cp.GetUserContext()
	require.NotNil(t, uc)
	assert.Empty(t, uc.ClaudeMd, "Project-tier content should be skipped")
}

// TestContextProvider_LoadMemoryMechanicsPrompt_Disabled verifies the
// helper returns "" when auto-memory is not enabled.
func TestContextProvider_LoadMemoryMechanicsPrompt_Disabled(t *testing.T) {
	cp := prompt.NewContextProvider(t.TempDir())
	assert.Empty(t, cp.LoadMemoryMechanicsPrompt())
}

// TestContextProvider_LoadMemoryMechanicsPrompt_Enabled verifies the
// helper returns the memory-mechanics prompt when auto-memory is on.
func TestContextProvider_LoadMemoryMechanicsPrompt_Enabled(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("UBUILDING_ENABLE_AUTO_MEMORY", "1")
	memory.ResetMemoryBaseDirCache()
	t.Cleanup(func() { memory.ResetMemoryBaseDirCache() })

	cp := prompt.NewContextProvider(dir,
		prompt.WithEngineConfig(agents.EngineConfig{
			AutoMemoryEnabled: true,
		}),
	)

	result := cp.LoadMemoryMechanicsPrompt()
	assert.NotEmpty(t, result, "should return memory mechanics prompt")
	assert.Contains(t, result, "auto memory")
}

// TestContextProvider_Memoization verifies that repeated calls return
// the same object.
func TestContextProvider_Memoization(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("memo"), 0644)

	loaderCfg := memory.LoaderConfig{
		Cwd:              dir,
		DisableUserTier:  true,
		DisableLocalTier: true,
	}
	cp := prompt.NewContextProvider(dir,
		prompt.WithMemoryLoaderConfig(loaderCfg),
	)

	uc1 := cp.GetUserContext()
	uc2 := cp.GetUserContext()
	assert.Same(t, uc1, uc2, "should return the same memoized object")
}

// TestContextProvider_MultipleTiers verifies that multiple tiers are
// rendered in the expected order.
func TestContextProvider_MultipleTiers(t *testing.T) {
	dir := t.TempDir()
	// Create project-level and .claude/CLAUDE.md files.
	_ = os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("project rule"), 0644)
	dotClaude := filepath.Join(dir, ".claude")
	_ = os.MkdirAll(dotClaude, 0755)
	_ = os.WriteFile(filepath.Join(dotClaude, "CLAUDE.md"), []byte("dot-claude rule"), 0644)

	loaderCfg := memory.LoaderConfig{
		Cwd:              dir,
		DisableUserTier:  true,
		DisableLocalTier: true,
	}
	cp := prompt.NewContextProvider(dir,
		prompt.WithMemoryLoaderConfig(loaderCfg),
	)

	uc := cp.GetUserContext()
	require.NotNil(t, uc)
	assert.Contains(t, uc.ClaudeMd, "project rule")
	assert.Contains(t, uc.ClaudeMd, "dot-claude rule")

	// Project rule should appear before dot-claude rule (same tier,
	// but CLAUDE.md is processed before .claude/CLAUDE.md in walk order).
	idx1 := strings.Index(uc.ClaudeMd, "project rule")
	idx2 := strings.Index(uc.ClaudeMd, "dot-claude rule")
	assert.True(t, idx1 < idx2, "project rule should appear before dot-claude rule")
}
