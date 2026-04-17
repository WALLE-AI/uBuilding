// Package builtin exposes the default built-in tool set (WebSearch, WebFetch)
// and a convenience Register helper. It lives in a separate package to avoid
// an import cycle between `tool` and its sub-packages.
package builtin

import (
	"github.com/wall-ai/ubuilding/backend/agents/tool"
	"github.com/wall-ai/ubuilding/backend/agents/tool/webfetch"
	"github.com/wall-ai/ubuilding/backend/agents/tool/websearch"
)

// Options tunes the set of tools returned by Tools()/Register().
type Options struct {
	// WebSearchAPIKey overrides the search API key; empty falls back to env
	// (AGENT_ENGINE_SEARCH_API_KEY or BRAVE_SEARCH_API_KEY).
	WebSearchAPIKey string
	// WebSearchBaseURL overrides the DuckDuckGo fallback endpoint.
	WebSearchBaseURL string
	// WebFetchOptions are passed through to webfetch.New.
	WebFetchOptions []webfetch.Option
}

// Tools returns the default built-in tool set.
func Tools(opts ...Options) tool.Tools {
	var o Options
	if len(opts) > 0 {
		o = opts[0]
	}
	return tool.Tools{
		websearch.New(o.WebSearchAPIKey, o.WebSearchBaseURL),
		webfetch.New(o.WebFetchOptions...),
	}
}

// Register installs every built-in tool into r, tagged with WithBuiltin so
// AssembleToolPool places them in the cache-friendly prefix of the final pool.
func Register(r *tool.Registry, opts ...Options) {
	for _, t := range Tools(opts...) {
		r.Register(t, tool.WithBuiltin())
	}
}
