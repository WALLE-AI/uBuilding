package websearch

import (
	"context"
	"fmt"
	"os"
	"testing"
)

// mockProvider is a test SearchProvider that returns canned results or errors.
type mockProvider struct {
	name string
	hits []SearchHit
	err  error
}

func (m *mockProvider) Name() string { return m.name }
func (m *mockProvider) Search(_ context.Context, _ string, _ int, _, _ []string) ([]SearchHit, error) {
	return m.hits, m.err
}

func TestFallbackProvider(t *testing.T) {
	t.Run("first provider succeeds", func(t *testing.T) {
		p := NewFallbackProvider(
			&mockProvider{name: "primary", hits: []SearchHit{{Title: "A", URL: "https://a.com"}}},
			&mockProvider{name: "fallback", hits: []SearchHit{{Title: "B", URL: "https://b.com"}}},
		)
		hits, err := p.Search(context.Background(), "test", 10, nil, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(hits) != 1 || hits[0].Title != "A" {
			t.Fatalf("expected hit from primary, got %v", hits)
		}
	})

	t.Run("first fails, second succeeds", func(t *testing.T) {
		p := NewFallbackProvider(
			&mockProvider{name: "primary", err: fmt.Errorf("CAPTCHA")},
			&mockProvider{name: "fallback", hits: []SearchHit{{Title: "B", URL: "https://b.com"}}},
		)
		hits, err := p.Search(context.Background(), "test", 10, nil, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(hits) != 1 || hits[0].Title != "B" {
			t.Fatalf("expected hit from fallback, got %v", hits)
		}
	})

	t.Run("first returns 0 results, second succeeds", func(t *testing.T) {
		p := NewFallbackProvider(
			&mockProvider{name: "primary", hits: nil},
			&mockProvider{name: "fallback", hits: []SearchHit{{Title: "B", URL: "https://b.com"}}},
		)
		hits, err := p.Search(context.Background(), "test", 10, nil, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(hits) != 1 || hits[0].Title != "B" {
			t.Fatalf("expected hit from fallback, got %v", hits)
		}
	})

	t.Run("all providers fail", func(t *testing.T) {
		p := NewFallbackProvider(
			&mockProvider{name: "primary", err: fmt.Errorf("fail1")},
			&mockProvider{name: "fallback", err: fmt.Errorf("fail2")},
		)
		_, err := p.Search(context.Background(), "test", 10, nil, nil)
		if err == nil {
			t.Fatal("expected error when all providers fail")
		}
	})

	t.Run("name combines providers", func(t *testing.T) {
		p := NewFallbackProvider(
			&mockProvider{name: "Brave"},
			&mockProvider{name: "DuckDuckGo"},
		)
		if p.Name() != "Brave → DuckDuckGo" {
			t.Errorf("expected 'Brave → DuckDuckGo', got %q", p.Name())
		}
	})
}

func TestDetectDDGCaptcha(t *testing.T) {
	tests := []struct {
		name   string
		body   string
		expect bool
	}{
		{"normal results page", "<html><body><div class='result'>...</div></body></html>", false},
		{"captcha page", "<html><body><form>Please solve this captcha</form></body></html>", true},
		{"challenge page", "<html><body>Complete the challenge to continue</body></html>", true},
		{"blocked page", "<html><body>Your request was blocked</body></html>", true},
		{"robot detection", "<html><body>Please verify you are not a robot</body></html>", true},
		{"empty page", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := detectDDGCaptcha([]byte(tt.body))
			if got != tt.expect {
				t.Errorf("detectDDGCaptcha(%q) = %v, want %v", tt.name, got, tt.expect)
			}
		})
	}
}

func TestResolveProvider(t *testing.T) {
	tool := &WebSearchTool{
		baseURL: "https://html.duckduckgo.com",
		client:  nil,
	}

	t.Run("no env vars uses DDG", func(t *testing.T) {
		os.Unsetenv("AGENT_ENGINE_SEARCH_API_KEY")
		os.Unsetenv("AGENT_ENGINE_SEARCH_PROVIDER")
		os.Unsetenv("BRAVE_SEARCH_API_KEY")
		p := ResolveProvider(tool)
		if _, ok := p.(*ddgProvider); !ok {
			t.Errorf("expected ddgProvider, got %T", p)
		}
	})

	t.Run("AGENT_ENGINE_SEARCH_API_KEY selects Brave fallback", func(t *testing.T) {
		os.Setenv("AGENT_ENGINE_SEARCH_API_KEY", "test-key")
		defer os.Unsetenv("AGENT_ENGINE_SEARCH_API_KEY")
		os.Unsetenv("AGENT_ENGINE_SEARCH_PROVIDER")
		os.Unsetenv("BRAVE_SEARCH_API_KEY")
		p := ResolveProvider(tool)
		fp, ok := p.(*FallbackProvider)
		if !ok {
			t.Fatalf("expected FallbackProvider, got %T", p)
		}
		if len(fp.providers) != 2 {
			t.Errorf("expected 2 providers in chain, got %d", len(fp.providers))
		}
	})

	t.Run("BRAVE_SEARCH_API_KEY also works", func(t *testing.T) {
		os.Unsetenv("AGENT_ENGINE_SEARCH_API_KEY")
		os.Unsetenv("AGENT_ENGINE_SEARCH_PROVIDER")
		os.Setenv("BRAVE_SEARCH_API_KEY", "brave-key")
		defer os.Unsetenv("BRAVE_SEARCH_API_KEY")
		p := ResolveProvider(tool)
		_, ok := p.(*FallbackProvider)
		if !ok {
			t.Fatalf("expected FallbackProvider, got %T", p)
		}
	})
}
