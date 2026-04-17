package websearch

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
)

// FallbackProvider tries multiple search providers in order, falling back
// to the next one if the current one fails.
type FallbackProvider struct {
	providers []SearchProvider
}

// NewFallbackProvider creates a provider that tries each provider in order.
func NewFallbackProvider(providers ...SearchProvider) *FallbackProvider {
	return &FallbackProvider{providers: providers}
}

func (p *FallbackProvider) Name() string {
	names := make([]string, len(p.providers))
	for i, pr := range p.providers {
		names[i] = pr.Name()
	}
	return strings.Join(names, " → ")
}

func (p *FallbackProvider) Search(ctx context.Context, query string, maxResults int, allowedDomains, blockedDomains []string) ([]SearchHit, error) {
	var lastErr error
	for _, pr := range p.providers {
		hits, err := pr.Search(ctx, query, maxResults, allowedDomains, blockedDomains)
		if err == nil && len(hits) > 0 {
			return hits, nil
		}
		if err != nil {
			slog.Warn("websearch: provider failed, trying next",
				slog.String("provider", pr.Name()),
				slog.Any("err", err))
			lastErr = err
		} else {
			// No error but 0 results — still try next provider.
			slog.Warn("websearch: provider returned 0 results, trying next",
				slog.String("provider", pr.Name()),
				slog.String("query", query))
			lastErr = fmt.Errorf("%s returned 0 results for query %q", pr.Name(), query)
		}
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, fmt.Errorf("all search providers failed")
}

// ResolveProvider selects the best available search provider based on
// environment variables. Priority:
//  1. AGENT_ENGINE_SEARCH_API_KEY + AGENT_ENGINE_SEARCH_PROVIDER=brave → Brave
//  2. AGENT_ENGINE_SEARCH_API_KEY (default) → Brave
//  3. BRAVE_SEARCH_API_KEY → Brave
//  4. No API key → DuckDuckGo (with Brave fallback hint on failure)
//
// When an API-based provider is available, DDG is added as the last fallback.
func ResolveProvider(tool *WebSearchTool) SearchProvider {
	searchKey := os.Getenv("AGENT_ENGINE_SEARCH_API_KEY")
	searchProvider := strings.ToLower(os.Getenv("AGENT_ENGINE_SEARCH_PROVIDER"))

	// Also check dedicated Brave env var.
	if searchKey == "" {
		searchKey = os.Getenv("BRAVE_SEARCH_API_KEY")
		if searchKey != "" {
			searchProvider = "brave"
		}
	}

	ddg := &ddgProvider{tool: tool}

	if searchKey == "" {
		slog.Debug("websearch: no search API key configured, using DuckDuckGo only")
		return ddg
	}

	switch searchProvider {
	case "brave", "":
		brave := NewBraveProvider(searchKey)
		slog.Info("websearch: using Brave Search API with DDG fallback")
		return NewFallbackProvider(brave, ddg)
	default:
		slog.Warn("websearch: unknown search provider, falling back to DDG",
			slog.String("provider", searchProvider))
		return ddg
	}
}
