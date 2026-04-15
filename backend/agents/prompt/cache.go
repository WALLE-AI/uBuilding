package prompt

import (
	"strings"
)

// ---------------------------------------------------------------------------
// Prompt Cache System
// Maps to TypeScript utils/api.ts: CacheScope, SystemPromptBlock,
// splitSysPromptPrefix
//
// Anthropic's API supports cache_control on system prompt blocks.
// This module splits a multi-part system prompt into blocks with
// appropriate cache scopes for optimal prompt caching.
// ---------------------------------------------------------------------------

// CacheScope controls the cache visibility level for a prompt block.
type CacheScope string

const (
	// CacheScopeGlobal caches across all conversations (first-party only).
	CacheScopeGlobal CacheScope = "global"

	// CacheScopeOrg caches within the same organization.
	CacheScopeOrg CacheScope = "org"

	// CacheScopeNone disables caching for this block.
	CacheScopeNone CacheScope = ""
)

// SystemPromptBlock is a segment of the system prompt with cache control.
// Maps to TypeScript's SystemPromptBlock in utils/api.ts.
type SystemPromptBlock struct {
	// Text is the prompt content.
	Text string `json:"text"`

	// CacheScope controls caching. Empty string means no cache_control.
	CacheScope CacheScope `json:"cache_scope,omitempty"`
}

// CacheControlConfig configures how prompt blocks are split and cached.
type CacheControlConfig struct {
	// UseGlobalCache enables global-scope caching (first-party Anthropic only).
	UseGlobalCache bool

	// DynamicBoundary is a marker string that separates static from dynamic
	// content in the system prompt. Content before the boundary is static
	// (higher cache hit rate), content after is dynamic.
	DynamicBoundary string

	// SkipGlobalCacheForSystemPrompt disables global cache on the system
	// prompt when MCP tools are present (cache goes on tool definitions instead).
	SkipGlobalCacheForSystemPrompt bool

	// PrefixIdentifiers are strings that identify system prompt prefix blocks
	// (e.g., "You are Claude" variants). These get special cache handling.
	PrefixIdentifiers []string

	// AttributionPrefix identifies the billing attribution header.
	AttributionPrefix string
}

// DefaultCacheControlConfig returns a config with standard defaults.
func DefaultCacheControlConfig() CacheControlConfig {
	return CacheControlConfig{
		UseGlobalCache:    false,
		DynamicBoundary:   "---DYNAMIC_BOUNDARY---",
		AttributionPrefix: "x-anthropic-billing-header",
	}
}

// SplitSystemPromptBlocks splits a multi-part system prompt into blocks
// with appropriate cache scopes.
//
// Maps to TypeScript's splitSysPromptPrefix in utils/api.ts.
//
// Three modes:
//  1. SkipGlobalCacheForSystemPrompt: org-level caching, no global
//  2. UseGlobalCache + boundary found: static=global, dynamic=none
//  3. Default: org-level caching on prefix and rest
func SplitSystemPromptBlocks(parts []string, config CacheControlConfig) []SystemPromptBlock {
	if len(parts) == 0 {
		return nil
	}

	// Mode 1: MCP tools present — org-level cache only
	if config.UseGlobalCache && config.SkipGlobalCacheForSystemPrompt {
		return splitOrgCache(parts, config)
	}

	// Mode 2: Global cache with boundary
	if config.UseGlobalCache && config.DynamicBoundary != "" {
		blocks := splitGlobalCache(parts, config)
		if blocks != nil {
			return blocks
		}
		// Boundary not found — fall through to default
	}

	// Mode 3: Default — org-level cache
	return splitOrgCache(parts, config)
}

// splitOrgCache produces blocks with org-level caching.
func splitOrgCache(parts []string, config CacheControlConfig) []SystemPromptBlock {
	var attribution string
	var prefix string
	var rest []string

	for _, p := range parts {
		if p == "" || p == config.DynamicBoundary {
			continue
		}
		if config.AttributionPrefix != "" && strings.HasPrefix(p, config.AttributionPrefix) {
			attribution = p
		} else if isPrefix(p, config.PrefixIdentifiers) {
			prefix = p
		} else {
			rest = append(rest, p)
		}
	}

	var result []SystemPromptBlock
	if attribution != "" {
		result = append(result, SystemPromptBlock{Text: attribution, CacheScope: CacheScopeNone})
	}
	if prefix != "" {
		result = append(result, SystemPromptBlock{Text: prefix, CacheScope: CacheScopeOrg})
	}
	joined := strings.Join(rest, "\n\n")
	if joined != "" {
		result = append(result, SystemPromptBlock{Text: joined, CacheScope: CacheScopeOrg})
	}
	return result
}

// splitGlobalCache splits into static (global cache) and dynamic (no cache)
// blocks based on the boundary marker.
func splitGlobalCache(parts []string, config CacheControlConfig) []SystemPromptBlock {
	boundaryIdx := -1
	for i, p := range parts {
		if p == config.DynamicBoundary {
			boundaryIdx = i
			break
		}
	}
	if boundaryIdx == -1 {
		return nil // no boundary found
	}

	var attribution string
	var prefix string
	var staticBlocks []string
	var dynamicBlocks []string

	for i, p := range parts {
		if p == "" || p == config.DynamicBoundary {
			continue
		}
		if config.AttributionPrefix != "" && strings.HasPrefix(p, config.AttributionPrefix) {
			attribution = p
		} else if isPrefix(p, config.PrefixIdentifiers) {
			prefix = p
		} else if i < boundaryIdx {
			staticBlocks = append(staticBlocks, p)
		} else {
			dynamicBlocks = append(dynamicBlocks, p)
		}
	}

	var result []SystemPromptBlock
	if attribution != "" {
		result = append(result, SystemPromptBlock{Text: attribution, CacheScope: CacheScopeNone})
	}
	if prefix != "" {
		result = append(result, SystemPromptBlock{Text: prefix, CacheScope: CacheScopeNone})
	}
	staticJoined := strings.Join(staticBlocks, "\n\n")
	if staticJoined != "" {
		result = append(result, SystemPromptBlock{Text: staticJoined, CacheScope: CacheScopeGlobal})
	}
	dynamicJoined := strings.Join(dynamicBlocks, "\n\n")
	if dynamicJoined != "" {
		result = append(result, SystemPromptBlock{Text: dynamicJoined, CacheScope: CacheScopeNone})
	}
	return result
}

// isPrefix checks if s matches any of the known prefix identifiers.
func isPrefix(s string, identifiers []string) bool {
	for _, id := range identifiers {
		if s == id {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Helpers for API message construction
// ---------------------------------------------------------------------------

// ToCacheControl converts a CacheScope to the Anthropic API cache_control
// format. Returns nil if no cache control should be applied.
func ToCacheControl(scope CacheScope) map[string]interface{} {
	switch scope {
	case CacheScopeGlobal:
		return map[string]interface{}{
			"type":  "ephemeral",
			"scope": "global",
		}
	case CacheScopeOrg:
		return map[string]interface{}{
			"type": "ephemeral",
		}
	default:
		return nil
	}
}

// BlocksToAPIFormat converts SystemPromptBlocks to the Anthropic API
// system parameter format (array of content blocks with cache_control).
func BlocksToAPIFormat(blocks []SystemPromptBlock) []map[string]interface{} {
	if len(blocks) == 0 {
		return nil
	}

	result := make([]map[string]interface{}, 0, len(blocks))
	for _, b := range blocks {
		if b.Text == "" {
			continue
		}
		block := map[string]interface{}{
			"type": "text",
			"text": b.Text,
		}
		cc := ToCacheControl(b.CacheScope)
		if cc != nil {
			block["cache_control"] = cc
		}
		result = append(result, block)
	}
	return result
}

// BlocksToString concatenates all blocks into a single string.
// Useful for non-caching providers or logging.
func BlocksToString(blocks []SystemPromptBlock) string {
	var parts []string
	for _, b := range blocks {
		if b.Text != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n\n")
}
