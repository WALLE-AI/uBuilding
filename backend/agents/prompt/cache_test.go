package prompt

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSplitSystemPromptBlocks_EmptyInput(t *testing.T) {
	blocks := SplitSystemPromptBlocks(nil, DefaultCacheControlConfig())
	assert.Nil(t, blocks)

	blocks = SplitSystemPromptBlocks([]string{}, DefaultCacheControlConfig())
	assert.Nil(t, blocks)
}

func TestSplitSystemPromptBlocks_DefaultMode(t *testing.T) {
	config := DefaultCacheControlConfig()
	config.PrefixIdentifiers = []string{"You are Claude."}

	parts := []string{
		"x-anthropic-billing-header: abc",
		"You are Claude.",
		"Tool instructions go here.",
		"Environment context here.",
	}

	blocks := SplitSystemPromptBlocks(parts, config)
	require.Len(t, blocks, 3)

	// Attribution: no cache
	assert.Equal(t, "x-anthropic-billing-header: abc", blocks[0].Text)
	assert.Equal(t, CacheScopeNone, blocks[0].CacheScope)

	// Prefix: org cache
	assert.Equal(t, "You are Claude.", blocks[1].Text)
	assert.Equal(t, CacheScopeOrg, blocks[1].CacheScope)

	// Rest: org cache, joined
	assert.Equal(t, "Tool instructions go here.\n\nEnvironment context here.", blocks[2].Text)
	assert.Equal(t, CacheScopeOrg, blocks[2].CacheScope)
}

func TestSplitSystemPromptBlocks_GlobalCacheWithBoundary(t *testing.T) {
	config := CacheControlConfig{
		UseGlobalCache:    true,
		DynamicBoundary:   "---DYNAMIC_BOUNDARY---",
		AttributionPrefix: "x-anthropic-billing-header",
		PrefixIdentifiers: []string{"You are Claude."},
	}

	parts := []string{
		"x-anthropic-billing-header: abc",
		"You are Claude.",
		"Static tool info.",
		"---DYNAMIC_BOUNDARY---",
		"Dynamic env context.",
		"Dynamic user context.",
	}

	blocks := SplitSystemPromptBlocks(parts, config)
	require.Len(t, blocks, 4)

	// Attribution: no cache
	assert.Equal(t, CacheScopeNone, blocks[0].CacheScope)

	// Prefix: no cache (in global mode)
	assert.Equal(t, "You are Claude.", blocks[1].Text)
	assert.Equal(t, CacheScopeNone, blocks[1].CacheScope)

	// Static: global cache
	assert.Equal(t, "Static tool info.", blocks[2].Text)
	assert.Equal(t, CacheScopeGlobal, blocks[2].CacheScope)

	// Dynamic: no cache
	assert.Equal(t, "Dynamic env context.\n\nDynamic user context.", blocks[3].Text)
	assert.Equal(t, CacheScopeNone, blocks[3].CacheScope)
}

func TestSplitSystemPromptBlocks_GlobalCacheNoBoundary(t *testing.T) {
	config := CacheControlConfig{
		UseGlobalCache:    true,
		DynamicBoundary:   "---DYNAMIC_BOUNDARY---",
		AttributionPrefix: "x-anthropic-billing-header",
		PrefixIdentifiers: []string{"You are Claude."},
	}

	// No boundary marker present — falls through to org-level cache
	parts := []string{
		"You are Claude.",
		"Some content.",
	}

	blocks := SplitSystemPromptBlocks(parts, config)
	require.Len(t, blocks, 2)

	assert.Equal(t, CacheScopeOrg, blocks[0].CacheScope)
	assert.Equal(t, CacheScopeOrg, blocks[1].CacheScope)
}

func TestSplitSystemPromptBlocks_SkipGlobalForMCPTools(t *testing.T) {
	config := CacheControlConfig{
		UseGlobalCache:                 true,
		SkipGlobalCacheForSystemPrompt: true,
		DynamicBoundary:                "---DYNAMIC_BOUNDARY---",
		AttributionPrefix:              "x-anthropic-billing-header",
		PrefixIdentifiers:              []string{"You are Claude."},
	}

	parts := []string{
		"x-anthropic-billing-header: abc",
		"You are Claude.",
		"---DYNAMIC_BOUNDARY---",
		"Dynamic content.",
	}

	blocks := SplitSystemPromptBlocks(parts, config)
	require.Len(t, blocks, 3)

	// Attribution: no cache
	assert.Equal(t, CacheScopeNone, blocks[0].CacheScope)
	// Prefix: org (not global)
	assert.Equal(t, CacheScopeOrg, blocks[1].CacheScope)
	// Rest: org (not global)
	assert.Equal(t, CacheScopeOrg, blocks[2].CacheScope)
}

func TestSplitSystemPromptBlocks_EmptyPartsFiltered(t *testing.T) {
	config := DefaultCacheControlConfig()

	parts := []string{"", "Hello", "", "World", ""}
	blocks := SplitSystemPromptBlocks(parts, config)
	require.Len(t, blocks, 1)
	assert.Equal(t, "Hello\n\nWorld", blocks[0].Text)
}

func TestToCacheControl(t *testing.T) {
	// Global
	cc := ToCacheControl(CacheScopeGlobal)
	require.NotNil(t, cc)
	assert.Equal(t, "ephemeral", cc["type"])
	assert.Equal(t, "global", cc["scope"])

	// Org
	cc = ToCacheControl(CacheScopeOrg)
	require.NotNil(t, cc)
	assert.Equal(t, "ephemeral", cc["type"])
	_, hasScope := cc["scope"]
	assert.False(t, hasScope)

	// None
	cc = ToCacheControl(CacheScopeNone)
	assert.Nil(t, cc)
}

func TestBlocksToAPIFormat(t *testing.T) {
	blocks := []SystemPromptBlock{
		{Text: "Hello", CacheScope: CacheScopeOrg},
		{Text: "", CacheScope: CacheScopeOrg}, // empty, should be skipped
		{Text: "World", CacheScope: CacheScopeNone},
	}

	api := BlocksToAPIFormat(blocks)
	require.Len(t, api, 2)

	assert.Equal(t, "text", api[0]["type"])
	assert.Equal(t, "Hello", api[0]["text"])
	assert.NotNil(t, api[0]["cache_control"])

	assert.Equal(t, "World", api[1]["text"])
	_, hasCacheControl := api[1]["cache_control"]
	assert.False(t, hasCacheControl)
}

func TestBlocksToString(t *testing.T) {
	blocks := []SystemPromptBlock{
		{Text: "Part 1"},
		{Text: ""},
		{Text: "Part 2"},
	}
	assert.Equal(t, "Part 1\n\nPart 2", BlocksToString(blocks))
}
