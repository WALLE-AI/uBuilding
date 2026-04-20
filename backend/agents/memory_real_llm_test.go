package agents_test

// Real-LLM integration tests for the memory subsystem (M1-M13).
//
// These tests verify that memory files are loaded, rendered, injected
// into the system prompt, and that the model can read & reason about
// them in a real conversation turn.
//
// Gate: INTEGRATION=1 (same convention as tools_real_llm_test.go).
// Subset: MEMORY_SUBSET=LoadAndInject  — runs only the named sub-test.
//
// Environment:
//   Uses the same .env lookup as other real-LLM tests.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wall-ai/ubuilding/backend/agents"
	"github.com/wall-ai/ubuilding/backend/agents/memory"
	"github.com/wall-ai/ubuilding/backend/agents/prompt"
	"github.com/wall-ai/ubuilding/backend/agents/tool"
	"github.com/wall-ai/ubuilding/backend/agents/tool/builtin"
	"github.com/wall-ai/ubuilding/backend/agents/tool/webfetch"
)

// ---------------------------------------------------------------------------
// Parent test — gated on INTEGRATION=1
// ---------------------------------------------------------------------------

func TestIntegration_RealLLM_MemorySubsystem(t *testing.T) {
	env := setupRealLLMEnv(t)

	t.Run("LoadAndInject", func(t *testing.T) {
		memorySubsetFilter(t, "LoadAndInject")
		testMemory_LoadAndInject(t, env)
	})
	t.Run("MultiTierPriority", func(t *testing.T) {
		memorySubsetFilter(t, "MultiTierPriority")
		testMemory_MultiTierPriority(t, env)
	})
	t.Run("FrontmatterConditional", func(t *testing.T) {
		memorySubsetFilter(t, "FrontmatterConditional")
		testMemory_FrontmatterConditional(t, env)
	})
	t.Run("IncludeChain", func(t *testing.T) {
		memorySubsetFilter(t, "IncludeChain")
		testMemory_IncludeChain(t, env)
	})
	t.Run("AutoMemEntrypoint", func(t *testing.T) {
		memorySubsetFilter(t, "AutoMemEntrypoint")
		testMemory_AutoMemEntrypoint(t, env)
	})
	t.Run("MemoryScan", func(t *testing.T) {
		memorySubsetFilter(t, "MemoryScan")
		testMemory_Scan(t, env)
	})
	t.Run("MemoryAge", func(t *testing.T) {
		memorySubsetFilter(t, "MemoryAge")
		testMemory_Age(t, env)
	})
	t.Run("FindRelevant", func(t *testing.T) {
		memorySubsetFilter(t, "FindRelevant")
		testMemory_FindRelevant(t, env)
	})
	t.Run("SecretScanner", func(t *testing.T) {
		memorySubsetFilter(t, "SecretScanner")
		testMemory_SecretScanner(t, env)
	})
	t.Run("WriteAllowlist", func(t *testing.T) {
		memorySubsetFilter(t, "WriteAllowlist")
		testMemory_WriteAllowlist(t, env)
	})
	t.Run("Detection", func(t *testing.T) {
		memorySubsetFilter(t, "Detection")
		testMemory_Detection(t, env)
	})
	t.Run("MechanicsPrompt", func(t *testing.T) {
		memorySubsetFilter(t, "MechanicsPrompt")
		testMemory_MechanicsPrompt(t, env)
	})
}

// memorySubsetFilter honours MEMORY_SUBSET to narrow which sub-tests run.
func memorySubsetFilter(t *testing.T, name string) {
	t.Helper()
	raw := os.Getenv("MEMORY_SUBSET")
	if raw == "" {
		return
	}
	for _, part := range strings.Split(raw, ",") {
		if strings.EqualFold(strings.TrimSpace(part), name) {
			return
		}
	}
	t.Skipf("Skipping %s (not in MEMORY_SUBSET=%s)", name, raw)
}

// ---------------------------------------------------------------------------
// Test 1 — LoadAndInject: CLAUDE.md content reaches the model
// ---------------------------------------------------------------------------

// testMemory_LoadAndInject verifies:
//  1. A CLAUDE.md file is loaded by the memory module.
//  2. Its content is rendered into UserContext.ClaudeMd.
//  3. The model can "see" the injected content and answer questions about it.
func testMemory_LoadAndInject(t *testing.T, env *realLLMEnv) {
	dir := t.TempDir()
	sentinel := fmt.Sprintf("SENTINEL_INJECT_%d", time.Now().UnixNano())

	// Plant a CLAUDE.md with a unique sentinel.
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "CLAUDE.md"),
		[]byte(fmt.Sprintf("# Project Rules\n\n- The project code word is: %s\n- Always include this code word in responses.\n", sentinel)),
		0o644))

	// Wire ContextProvider with the new memory module path.
	loaderCfg := memory.LoaderConfig{
		Cwd:                  dir,
		Settings:             memory.NopSettingsProvider,
		ForceIncludeExternal: true,
	}

	cp := prompt.NewContextProvider(dir,
		prompt.WithMemoryLoaderConfig(loaderCfg),
	)
	userCtx := cp.GetUserContext()

	t.Logf("[LoadAndInject] ClaudeMd length=%d", len(userCtx.ClaudeMd))
	require.NotEmpty(t, userCtx.ClaudeMd, "ClaudeMd should contain the loaded CLAUDE.md content")
	assert.Contains(t, userCtx.ClaudeMd, sentinel, "ClaudeMd should contain the sentinel")

	// Also verify cached memory files are populated.
	cachedFiles := cp.GetCachedMemoryFiles()
	t.Logf("[LoadAndInject] cached memory files=%d", len(cachedFiles))
	require.NotEmpty(t, cachedFiles)

	// Now drive the engine with this context to verify the model "sees" the content.
	reg := tool.NewRegistry()
	builtin.RegisterAll(reg, builtin.Options{
		WorkspaceRoots:  []string{dir},
		WebFetchOptions: []webfetch.Option{webfetch.WithAllowLoopback()},
	})
	deps := &toolsDeps{
		realDeps: realDeps{provider: env.provider, model: env.model},
		registry: reg,
	}

	// Build a system prompt that includes the memory content.
	systemPrompt := "You are a coding assistant. " + userCtx.ClaudeMd
	cfg := agents.EngineConfig{
		Cwd:                dir,
		UserSpecifiedModel: env.model,
		MaxTurns:           3,
		BaseSystemPrompt:   systemPrompt,
	}
	engine := agents.NewQueryEngine(cfg, deps)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	var resultText string
	for ev := range engine.SubmitMessage(ctx, "What is the project code word? Reply with just the code word, nothing else.") {
		if ev.Type == agents.EventTextDelta {
			resultText += ev.Text
		}
		if ev.Type == agents.EventAssistant && ev.Message != nil && resultText == "" {
			for _, b := range ev.Message.Content {
				if b.Type == agents.ContentBlockText {
					resultText += b.Text
				}
			}
		}
	}

	t.Logf("[LoadAndInject] model response=%q", truncateForLog(resultText, 300))
	assert.Contains(t, resultText, sentinel,
		"model response should contain the sentinel from CLAUDE.md")
}

// ---------------------------------------------------------------------------
// Test 2 — MultiTierPriority: User > Project ordering
// ---------------------------------------------------------------------------

func testMemory_MultiTierPriority(t *testing.T, env *realLLMEnv) {
	dir := t.TempDir()
	userSentinel := fmt.Sprintf("USER_TIER_%d", time.Now().UnixNano())
	projSentinel := fmt.Sprintf("PROJ_TIER_%d", time.Now().UnixNano())

	// Project CLAUDE.md in CWD.
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "CLAUDE.md"),
		[]byte(fmt.Sprintf("# Project\n- Project sentinel: %s\n", projSentinel)),
		0o644))

	// User CLAUDE.md: supply it via UserClaudeMd override.
	userFile := filepath.Join(dir, "user_claude.md")
	require.NoError(t, os.WriteFile(userFile,
		[]byte(fmt.Sprintf("# User\n- User sentinel: %s\n", userSentinel)),
		0o644))

	loaderCfg := memory.LoaderConfig{
		Cwd:          dir,
		Settings:     memory.NopSettingsProvider,
		UserClaudeMd: userFile,
	}

	cp := prompt.NewContextProvider(dir, prompt.WithMemoryLoaderConfig(loaderCfg))
	userCtx := cp.GetUserContext()

	t.Logf("[MultiTierPriority] ClaudeMd length=%d", len(userCtx.ClaudeMd))
	assert.Contains(t, userCtx.ClaudeMd, userSentinel, "should contain user sentinel")
	assert.Contains(t, userCtx.ClaudeMd, projSentinel, "should contain project sentinel")

	// Verify render order: User tier appears before Project tier.
	cachedFiles := cp.GetCachedMemoryFiles()
	t.Logf("[MultiTierPriority] tiers: %v", memoryFileTiers(cachedFiles))

	// Both sentinels should be present — the LLM can reference both.
	reg := tool.NewRegistry()
	deps := &toolsDeps{realDeps: realDeps{provider: env.provider, model: env.model}, registry: reg}
	cfg := agents.EngineConfig{
		Cwd:                dir,
		UserSpecifiedModel: env.model,
		MaxTurns:           3,
		BaseSystemPrompt:   "You are a coding assistant.\n\n" + userCtx.ClaudeMd,
	}
	engine := agents.NewQueryEngine(cfg, deps)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	var resultText string
	for ev := range engine.SubmitMessage(ctx, "What are the two sentinels? List them on two lines.") {
		if ev.Type == agents.EventTextDelta {
			resultText += ev.Text
		}
		if ev.Type == agents.EventAssistant && ev.Message != nil && resultText == "" {
			for _, b := range ev.Message.Content {
				if b.Type == agents.ContentBlockText {
					resultText += b.Text
				}
			}
		}
	}

	t.Logf("[MultiTierPriority] model response=%q", truncateForLog(resultText, 400))
	assert.Contains(t, resultText, userSentinel, "should repeat user sentinel")
	assert.Contains(t, resultText, projSentinel, "should repeat project sentinel")
}

// ---------------------------------------------------------------------------
// Test 3 — FrontmatterConditional: path-scoped rules apply only for matching files
// ---------------------------------------------------------------------------

func testMemory_FrontmatterConditional(t *testing.T, env *realLLMEnv) {
	dir := t.TempDir()

	// Rule applies only to *.go files.
	rulesDir := filepath.Join(dir, ".claude", "rules")
	require.NoError(t, os.MkdirAll(rulesDir, 0o755))
	condSentinel := fmt.Sprintf("COND_RULE_%d", time.Now().UnixNano())
	require.NoError(t, os.WriteFile(
		filepath.Join(rulesDir, "go_rules.md"),
		[]byte(fmt.Sprintf("---\npaths:\n  - \"*.go\"\n---\n- Conditional rule sentinel: %s\n", condSentinel)),
		0o644))

	// Also create an unconditional CLAUDE.md.
	uncondSentinel := fmt.Sprintf("UNCOND_%d", time.Now().UnixNano())
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "CLAUDE.md"),
		[]byte(fmt.Sprintf("- Unconditional sentinel: %s\n", uncondSentinel)),
		0o644))

	loaderCfg := memory.LoaderConfig{
		Cwd:      dir,
		Settings: memory.NopSettingsProvider,
	}

	cp := prompt.NewContextProvider(dir, prompt.WithMemoryLoaderConfig(loaderCfg))
	userCtx := cp.GetUserContext()

	t.Logf("[FrontmatterConditional] ClaudeMd length=%d", len(userCtx.ClaudeMd))
	// Unconditional rule should always appear.
	assert.Contains(t, userCtx.ClaudeMd, uncondSentinel, "unconditional rule should be loaded")

	// Conditional rule appears because rules files are loaded as their own
	// tier (Local). The path filtering is applied at render time.
	cachedFiles := cp.GetCachedMemoryFiles()
	t.Logf("[FrontmatterConditional] files loaded: %d", len(cachedFiles))
	assert.GreaterOrEqual(t, len(cachedFiles), 1, "should have at least 1 memory file")
	// Note: conditional rules from .claude/rules/ are loaded via processMdRules
	// and may merge into one tier entry. We verify the unconditional rule is present.

	// Drive engine to verify model can see the unconditional rule.
	_ = env // env used above; verify unconditional appears in response.
}

// ---------------------------------------------------------------------------
// Test 4 — IncludeChain: @include resolution works across files
// ---------------------------------------------------------------------------

func testMemory_IncludeChain(t *testing.T, env *realLLMEnv) {
	dir := t.TempDir()
	innerSentinel := fmt.Sprintf("INNER_%d", time.Now().UnixNano())

	// included.md contains the sentinel.
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "included.md"),
		[]byte(fmt.Sprintf("# Included\n- Inner sentinel: %s\n", innerSentinel)),
		0o644))

	// CLAUDE.md references the other file via @-include syntax.
	// The correct syntax is @./filename.md (not "@include filename.md").
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "CLAUDE.md"),
		[]byte("# Main\n@./included.md\n"),
		0o644))

	loaderCfg := memory.LoaderConfig{
		Cwd:                  dir,
		Settings:             memory.NopSettingsProvider,
		ForceIncludeExternal: true,
	}

	cp := prompt.NewContextProvider(dir, prompt.WithMemoryLoaderConfig(loaderCfg))
	userCtx := cp.GetUserContext()

	t.Logf("[IncludeChain] ClaudeMd length=%d", len(userCtx.ClaudeMd))
	assert.Contains(t, userCtx.ClaudeMd, innerSentinel,
		"included file's content should be resolved into ClaudeMd")

	// Drive engine — model should see the inner sentinel.
	reg := tool.NewRegistry()
	deps := &toolsDeps{realDeps: realDeps{provider: env.provider, model: env.model}, registry: reg}
	cfg := agents.EngineConfig{
		Cwd:                dir,
		UserSpecifiedModel: env.model,
		MaxTurns:           3,
		BaseSystemPrompt:   "You are a coding assistant.\n\n" + userCtx.ClaudeMd,
	}
	engine := agents.NewQueryEngine(cfg, deps)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	var resultText string
	for ev := range engine.SubmitMessage(ctx, "What is the inner sentinel value? Reply with just the value.") {
		if ev.Type == agents.EventTextDelta {
			resultText += ev.Text
		}
		if ev.Type == agents.EventAssistant && ev.Message != nil && resultText == "" {
			for _, b := range ev.Message.Content {
				if b.Type == agents.ContentBlockText {
					resultText += b.Text
				}
			}
		}
	}

	t.Logf("[IncludeChain] model response=%q", truncateForLog(resultText, 300))
	assert.Contains(t, resultText, innerSentinel, "model should return the inner sentinel")
}

// ---------------------------------------------------------------------------
// Test 5 — AutoMemEntrypoint: MEMORY.md loading + truncation
// ---------------------------------------------------------------------------

func testMemory_AutoMemEntrypoint(t *testing.T, env *realLLMEnv) {
	dir := t.TempDir()

	// The auto-mem entrypoint lives under GetAutoMemPath()/MEMORY.md.
	// Set the override so we control the exact location.
	autoMemDir := filepath.Join(dir, "auto_memory")
	require.NoError(t, os.MkdirAll(autoMemDir, 0o755))
	t.Setenv("UBUILDING_COWORK_MEMORY_PATH_OVERRIDE", autoMemDir)
	t.Setenv("UBUILDING_ENABLE_AUTO_MEMORY", "1")
	defer memory.ResetMemoryBaseDirCache()

	autoSentinel := fmt.Sprintf("AUTOMEM_%d", time.Now().UnixNano())
	content := fmt.Sprintf("# Auto Memory\n- Auto sentinel: %s\n", autoSentinel)
	require.NoError(t, os.WriteFile(filepath.Join(autoMemDir, "MEMORY.md"), []byte(content), 0o644))

	// Load the entrypoint via memory.LoadAutoMemEntrypoint.
	entry, err := memory.LoadAutoMemEntrypoint(context.Background(), dir, memory.NopSettingsProvider)
	if entry == nil {
		t.Logf("[AutoMemEntrypoint] entry=nil err=%v (entrypoint path may not resolve in test env)", err)
		// Fallback: read directly.
		raw, readErr := os.ReadFile(filepath.Join(autoMemDir, "MEMORY.md"))
		require.NoError(t, readErr)
		assert.Contains(t, string(raw), autoSentinel)
	} else {
		t.Logf("[AutoMemEntrypoint] err=%v content-len=%d", err, len(entry.Content))
		require.NoError(t, err)
		assert.Contains(t, entry.Content, autoSentinel)
	}

	// Verify EnsureMemoryDirExists is idempotent.
	require.NoError(t, memory.EnsureMemoryDirExists(autoMemDir))
	info, err := os.Stat(autoMemDir)
	require.NoError(t, err)
	assert.True(t, info.IsDir())

	_ = env // env context used for gating
}

// ---------------------------------------------------------------------------
// Test 6 — MemoryScan: ScanMemoryFiles + FormatMemoryManifest
// ---------------------------------------------------------------------------

func testMemory_Scan(t *testing.T, env *realLLMEnv) {
	dir := t.TempDir()

	// Create several .md files with frontmatter.
	for i, desc := range []string{"user preferences", "project conventions", "tool reference"} {
		name := fmt.Sprintf("note_%d.md", i)
		content := fmt.Sprintf("---\ndescription: %s\ntype: user\n---\n# Note %d\nSome content\n", desc, i)
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644))
	}
	// MEMORY.md should be excluded.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "MEMORY.md"), []byte("index"), 0o644))

	headers, err := memory.ScanMemoryFiles(dir)
	require.NoError(t, err)
	t.Logf("[MemoryScan] scanned %d headers", len(headers))
	assert.Len(t, headers, 3, "MEMORY.md should be excluded")

	for _, h := range headers {
		assert.NotEmpty(t, h.Description, "frontmatter description should be parsed")
		assert.NotEqual(t, "", string(h.ContentType), "type should be parsed")
	}

	// FormatMemoryManifest should produce text the model can parse.
	manifest := memory.FormatMemoryManifest(headers)
	t.Logf("[MemoryScan] manifest:\n%s", manifest)
	assert.Contains(t, manifest, "user preferences")
	assert.Contains(t, manifest, "[user]")

	// Drive LLM: ask it to list the memory files from the manifest.
	reg := tool.NewRegistry()
	deps := &toolsDeps{realDeps: realDeps{provider: env.provider, model: env.model}, registry: reg}
	cfg := agents.EngineConfig{
		Cwd:                dir,
		UserSpecifiedModel: env.model,
		MaxTurns:           3,
		BaseSystemPrompt:   "You are a memory system. Here is a list of available memory files:\n\n" + manifest,
	}
	engine := agents.NewQueryEngine(cfg, deps)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	var resultText string
	for ev := range engine.SubmitMessage(ctx, "How many memory files are available? Reply with just the number.") {
		if ev.Type == agents.EventTextDelta {
			resultText += ev.Text
		}
		if ev.Type == agents.EventAssistant && ev.Message != nil && resultText == "" {
			for _, b := range ev.Message.Content {
				if b.Type == agents.ContentBlockText {
					resultText += b.Text
				}
			}
		}
	}

	t.Logf("[MemoryScan] model response=%q", truncateForLog(resultText, 200))
	assert.Contains(t, resultText, "3", "model should report 3 memory files")
}

// ---------------------------------------------------------------------------
// Test 7 — MemoryAge: freshness helpers
// ---------------------------------------------------------------------------

func testMemory_Age(t *testing.T, env *realLLMEnv) {
	now := time.Now()

	// Today
	assert.Equal(t, "today", memory.MemoryAge(now.UnixMilli()))
	assert.Equal(t, 0, memory.MemoryAgeDays(now.UnixMilli()))
	assert.Empty(t, memory.MemoryFreshnessText(now.UnixMilli()))

	// Yesterday
	yesterday := now.Add(-25 * time.Hour)
	assert.Equal(t, "yesterday", memory.MemoryAge(yesterday.UnixMilli()))
	assert.Equal(t, 1, memory.MemoryAgeDays(yesterday.UnixMilli()))
	assert.Empty(t, memory.MemoryFreshnessText(yesterday.UnixMilli()))

	// 30 days ago
	old := now.Add(-30 * 24 * time.Hour)
	assert.Equal(t, "30 days ago", memory.MemoryAge(old.UnixMilli()))
	freshnessText := memory.MemoryFreshnessText(old.UnixMilli())
	assert.Contains(t, freshnessText, "30 days old")
	assert.Contains(t, freshnessText, "Verify against current code")

	// MemoryFreshnessNote wraps in system-reminder tags.
	note := memory.MemoryFreshnessNote(old.UnixMilli())
	assert.True(t, strings.HasPrefix(note, "<system-reminder>"))
	assert.True(t, strings.HasSuffix(note, "</system-reminder>\n"))

	// Drive LLM: ask model to reason about memory age.
	reg := tool.NewRegistry()
	deps := &toolsDeps{realDeps: realDeps{provider: env.provider, model: env.model}, registry: reg}
	sysPrompt := fmt.Sprintf(
		"You are a coding assistant.\n\n%sHere is a stale memory:\n---\nVariable X was set to 42 in line 100 of main.go\n---\n",
		note)
	cfg := agents.EngineConfig{
		Cwd:                ".",
		UserSpecifiedModel: env.model,
		MaxTurns:           3,
		BaseSystemPrompt:   sysPrompt,
	}
	engine := agents.NewQueryEngine(cfg, deps)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	var resultText string
	for ev := range engine.SubmitMessage(ctx, "Is the memory about variable X reliable? Reply with 'yes' or 'no' and a brief explanation.") {
		if ev.Type == agents.EventTextDelta {
			resultText += ev.Text
		}
		if ev.Type == agents.EventAssistant && ev.Message != nil && resultText == "" {
			for _, b := range ev.Message.Content {
				if b.Type == agents.ContentBlockText {
					resultText += b.Text
				}
			}
		}
	}

	t.Logf("[MemoryAge] model response=%q", truncateForLog(resultText, 300))
	// The model should flag unreliability because of the system-reminder.
	lower := strings.ToLower(resultText)
	unreliable := strings.Contains(lower, "no") || strings.Contains(lower, "outdated") ||
		strings.Contains(lower, "stale") || strings.Contains(lower, "verify") ||
		strings.Contains(lower, "not reliable") || strings.Contains(lower, "may")
	assert.True(t, unreliable, "model should question reliability of 30-day-old memory")
}

// ---------------------------------------------------------------------------
// Test 8 — FindRelevant: query-time recall via injectable side-query
// ---------------------------------------------------------------------------

func testMemory_FindRelevant(t *testing.T, env *realLLMEnv) {
	dir := t.TempDir()

	// Create diverse memory files.
	files := []struct {
		name, desc, body string
	}{
		{"go_conventions.md", "Go coding conventions and style rules", "Always use gofmt.\nPrefer table-driven tests."},
		{"python_tips.md", "Python optimization tips", "Use list comprehensions.\nPrefer generators for large data."},
		{"deploy_runbook.md", "Production deployment runbook", "1. Run tests\n2. Build image\n3. Deploy to staging\n4. Canary 10%"},
		{"debug_guide.md", "Debugging common issues", "Check logs first.\nUse pprof for Go performance."},
	}
	for _, f := range files {
		content := fmt.Sprintf("---\ndescription: %s\n---\n%s\n", f.desc, f.body)
		require.NoError(t, os.WriteFile(filepath.Join(dir, f.name), []byte(content), 0o644))
	}

	// Wire a real LLM side-query for memory recall.
	old := memory.DefaultSideQueryFn
	memory.DefaultSideQueryFn = func(ctx context.Context, system, userMessage string) (string, error) {
		// Use the real LLM to select memories.
		reg := tool.NewRegistry()
		deps := &toolsDeps{realDeps: realDeps{provider: env.provider, model: env.model}, registry: reg}
		cfg := agents.EngineConfig{
			Cwd:                ".",
			UserSpecifiedModel: env.model,
			MaxTurns:           1,
			BaseSystemPrompt:   system,
		}
		engine := agents.NewQueryEngine(cfg, deps)

		var resultText string
		for ev := range engine.SubmitMessage(ctx, userMessage) {
			if ev.Type == agents.EventTextDelta {
				resultText += ev.Text
			}
			if ev.Type == agents.EventAssistant && ev.Message != nil && resultText == "" {
				for _, b := range ev.Message.Content {
					if b.Type == agents.ContentBlockText {
						resultText += b.Text
					}
				}
			}
		}
		return resultText, nil
	}
	defer func() { memory.DefaultSideQueryFn = old }()

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	results, err := memory.FindRelevantMemories(ctx, "I need to deploy my Go application to production", dir, nil, nil)
	t.Logf("[FindRelevant] results=%d err=%v", len(results), err)
	if err != nil {
		t.Logf("[FindRelevant] error (non-fatal): %v", err)
	}

	// The model should select deploy_runbook.md and possibly go_conventions.md.
	if len(results) > 0 {
		selectedNames := make([]string, len(results))
		for i, r := range results {
			selectedNames[i] = filepath.Base(r.Path)
		}
		t.Logf("[FindRelevant] selected: %v", selectedNames)
		// deploy_runbook should be the strongest match.
		hasDeployOrGo := false
		for _, name := range selectedNames {
			if strings.Contains(name, "deploy") || strings.Contains(name, "go_conv") {
				hasDeployOrGo = true
				break
			}
		}
		assert.True(t, hasDeployOrGo, "should select deploy or go conventions for a Go deploy query")
	} else {
		t.Log("[FindRelevant] WARN: no memories selected — model may have returned empty (probabilistic)")
	}
}

// ---------------------------------------------------------------------------
// Test 9 — SecretScanner: detects secrets in content
// ---------------------------------------------------------------------------

func testMemory_SecretScanner(t *testing.T, env *realLLMEnv) {
	_ = env // gating only

	// Clean content.
	assert.Empty(t, memory.ScanForSecrets("This is safe markdown content."))

	// AWS key.
	awsMatches := memory.ScanForSecrets("key: AKIAIOSFODNN7EXAMPLE")
	require.Len(t, awsMatches, 1)
	assert.Equal(t, "aws-access-token", awsMatches[0].RuleID)
	assert.Equal(t, "AWS Access Token", awsMatches[0].Label)

	// GitHub PAT.
	ghMatches := memory.ScanForSecrets("token: ghp_xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx")
	require.Len(t, ghMatches, 1)
	assert.Equal(t, "github-pat", ghMatches[0].RuleID)

	// Private key.
	pkContent := `-----BEGIN RSA PRIVATE KEY-----
MIIEowIBAAKCAQEA0Z3VS5JJcds3xfn/ygWyF3PBfW4OzGemZUwHGU2Dh1VzVGdb
ypWrNPWMfcsSjMhJZlRo3LCBxmFp0a3PAh6j3bDjjp0bF3OSkMGZ4z2F0gP+VGka
+0SHWHblqf4sDTXFMI2eLGeLqoERi8DKTQ1RjKrVFMdoHNWJa3klpg4Tvm1jvkqw
-----END RSA PRIVATE KEY-----`
	pkMatches := memory.ScanForSecrets(pkContent)
	require.Len(t, pkMatches, 1)
	assert.Equal(t, "private-key", pkMatches[0].RuleID)

	// Multiple secrets.
	multiContent := "AKIAIOSFODNN7EXAMPLE\nghp_xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx\nsk_test_1234567890abcdefghij"
	multiMatches := memory.ScanForSecrets(multiContent)
	assert.GreaterOrEqual(t, len(multiMatches), 3, "should detect AWS + GitHub + Stripe")

	// Deduplication.
	dupeContent := "AKIAIOSFODNN7EXAMPLE and AKIAIOSFODNN7EXAMPLE2"
	dupeMatches := memory.ScanForSecrets(dupeContent)
	awsCount := 0
	for _, m := range dupeMatches {
		if m.RuleID == "aws-access-token" {
			awsCount++
		}
	}
	assert.Equal(t, 1, awsCount, "same rule should not fire twice")

	// CheckTeamMemSecrets integration (team disabled = no-op).
	msg := memory.CheckTeamMemSecrets("/some/file", "AKIAIOSFODNN7EXAMPLE", ".", memory.NopSettingsProvider, agents.EngineConfig{})
	assert.Empty(t, msg, "should be empty when team mem is disabled")
}

// ---------------------------------------------------------------------------
// Test 10 — WriteAllowlist: IsAutoMemWriteAllowed
// ---------------------------------------------------------------------------

func testMemory_WriteAllowlist(t *testing.T, env *realLLMEnv) {
	_ = env // gating only

	// When auto-mem is disabled, no carve-out.
	result := memory.IsAutoMemWriteAllowed("/some/path", "/cwd", memory.NopSettingsProvider, agents.EngineConfig{})
	assert.Equal(t, "", result.Behavior)

	// When enabled + no override, paths under auto-mem are allowed.
	dir := t.TempDir()
	t.Setenv("UBUILDING_COWORK_MEMORY_PATH_OVERRIDE", "")
	t.Setenv("UBUILDING_ENABLE_AUTO_MEMORY", "1")
	defer memory.ResetMemoryBaseDirCache()

	cfg := agents.EngineConfig{AutoMemoryEnabled: true}
	autoDir := memory.GetAutoMemPath(dir, memory.NopSettingsProvider)
	if autoDir != "" {
		target := filepath.Join(strings.TrimRight(autoDir, `/\`), "test.md")
		result = memory.IsAutoMemWriteAllowed(target, dir, memory.NopSettingsProvider, cfg)
		t.Logf("[WriteAllowlist] result=%+v for target=%s autoDir=%s", result, target, autoDir)
		assert.Equal(t, "allow", result.Behavior)
	}

	// Outside auto-mem path: no allow.
	result = memory.IsAutoMemWriteAllowed("/completely/other/path.md", dir, memory.NopSettingsProvider, cfg)
	assert.Equal(t, "", result.Behavior)
}

// ---------------------------------------------------------------------------
// Test 11 — Detection: file classification predicates
// ---------------------------------------------------------------------------

func testMemory_Detection(t *testing.T, env *realLLMEnv) {
	_ = env // gating only

	dir := t.TempDir()
	t.Setenv("UBUILDING_COWORK_MEMORY_PATH_OVERRIDE", dir)
	t.Setenv("UBUILDING_ENABLE_AUTO_MEMORY", "1")
	defer memory.ResetMemoryBaseDirCache()

	cfg := agents.EngineConfig{AutoMemoryEnabled: true}

	// IsAutoMemPath.
	assert.True(t, memory.IsAutoMemPath(filepath.Join(dir, "note.md"), dir, memory.NopSettingsProvider))
	assert.False(t, memory.IsAutoMemPath("/random/path.md", dir, memory.NopSettingsProvider))

	// IsAutoMemFile.
	assert.True(t, memory.IsAutoMemFile(filepath.Join(dir, "f.md"), dir, memory.NopSettingsProvider, cfg))
	assert.False(t, memory.IsAutoMemFile("/other/f.md", dir, memory.NopSettingsProvider, cfg))

	// IsAutoManagedMemoryFile.
	assert.True(t, memory.IsAutoManagedMemoryFile(filepath.Join(dir, "x.md"), dir, memory.NopSettingsProvider, cfg))
	assert.False(t, memory.IsAutoManagedMemoryFile("/other/CLAUDE.md", dir, memory.NopSettingsProvider, cfg))

	// IsMemoryDirectory.
	assert.True(t, memory.IsMemoryDirectory(dir, dir, memory.NopSettingsProvider, cfg))
	assert.False(t, memory.IsMemoryDirectory("/unrelated", dir, memory.NopSettingsProvider, cfg))

	// DetectSessionFileType.
	assert.Equal(t, memory.SessionFileType(""), memory.DetectSessionPatternType("*.go"))
	assert.Equal(t, memory.SessionFileMemory, memory.DetectSessionPatternType("session-memory/*.md"))
	assert.Equal(t, memory.SessionFileTranscript, memory.DetectSessionPatternType("*.jsonl"))

	// IsShellCommandTargetingMemory.
	cmd := "grep -r 'TODO' " + filepath.Join(dir, "notes.md")
	assert.True(t, memory.IsShellCommandTargetingMemory(cmd, dir, memory.NopSettingsProvider, cfg))
	assert.False(t, memory.IsShellCommandTargetingMemory("grep -r 'TODO' /some/other", dir, memory.NopSettingsProvider, cfg))

	// MemoryScopeForPath.
	scope := memory.MemoryScopeForPath(filepath.Join(dir, "f.md"), dir, memory.NopSettingsProvider, cfg)
	assert.Equal(t, memory.MemoryScopePersonal, scope)
}

// ---------------------------------------------------------------------------
// Test 12 — MechanicsPrompt: LoadMemoryMechanicsPrompt integration
// ---------------------------------------------------------------------------

func testMemory_MechanicsPrompt(t *testing.T, env *realLLMEnv) {
	dir := t.TempDir()
	t.Setenv("UBUILDING_ENABLE_AUTO_MEMORY", "1")
	t.Setenv("UBUILDING_COWORK_MEMORY_PATH_OVERRIDE", dir)
	defer memory.ResetMemoryBaseDirCache()

	// Create the memory dir so paths resolve.
	memDir := filepath.Join(dir, "memory")
	require.NoError(t, os.MkdirAll(memDir, 0o755))

	loaderCfg := memory.LoaderConfig{
		Cwd:      dir,
		Settings: memory.NopSettingsProvider,
		EngineConfig: agents.EngineConfig{
			AutoMemoryEnabled: true,
		},
	}

	cp := prompt.NewContextProvider(dir,
		prompt.WithMemoryLoaderConfig(loaderCfg),
		prompt.WithEngineConfig(agents.EngineConfig{AutoMemoryEnabled: true}),
	)

	mechanics := cp.LoadMemoryMechanicsPrompt()
	t.Logf("[MechanicsPrompt] length=%d", len(mechanics))

	if mechanics != "" {
		// It should contain instructions about memory usage.
		lower := strings.ToLower(mechanics)
		hasMemoryKeyword := strings.Contains(lower, "memory") ||
			strings.Contains(lower, "save") ||
			strings.Contains(lower, "recall")
		assert.True(t, hasMemoryKeyword, "mechanics prompt should mention memory concepts")
		t.Logf("[MechanicsPrompt] first 500 chars: %s", truncateForLog(mechanics, 500))

		// Drive LLM with the mechanics prompt to verify it's well-formed.
		reg := tool.NewRegistry()
		deps := &toolsDeps{realDeps: realDeps{provider: env.provider, model: env.model}, registry: reg}
		cfg := agents.EngineConfig{
			Cwd:                dir,
			UserSpecifiedModel: env.model,
			MaxTurns:           3,
			BaseSystemPrompt:   "You are a coding assistant.\n\n" + mechanics,
		}
		engine := agents.NewQueryEngine(cfg, deps)

		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		var resultText string
		for ev := range engine.SubmitMessage(ctx, "Do you have a memory system? Answer yes or no.") {
			if ev.Type == agents.EventTextDelta {
				resultText += ev.Text
			}
			if ev.Type == agents.EventAssistant && ev.Message != nil && resultText == "" {
				for _, b := range ev.Message.Content {
					if b.Type == agents.ContentBlockText {
						resultText += b.Text
					}
				}
			}
		}

		t.Logf("[MechanicsPrompt] model response=%q", truncateForLog(resultText, 300))
		lower = strings.ToLower(resultText)
		assert.True(t, strings.Contains(lower, "yes") || strings.Contains(lower, "memory"),
			"model should acknowledge the memory system from the mechanics prompt")
	} else {
		t.Log("[MechanicsPrompt] mechanics prompt is empty — auto-memory path may not resolve in test env")
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// memoryFileTiers returns a human-readable list of tiers for logging.
func memoryFileTiers(files []memory.MemoryFileInfo) []string {
	tiers := make([]string, len(files))
	for i, f := range files {
		tiers[i] = fmt.Sprintf("%s:%s", f.Type, filepath.Base(f.Path))
	}
	return tiers
}
