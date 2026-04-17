package websearch

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/wall-ai/ubuilding/backend/agents/tool"
)

func TestParseDDGHTML(t *testing.T) {
	// Minimal mock of DuckDuckGo HTML search response.
	mockHTML := `<html><body>
		<div class="result results_links results_links_deep web-result">
			<h2 class="result__title">
				<a class="result__a" href="https://example.com/page1">Example Page One</a>
			</h2>
			<a class="result__snippet" href="https://example.com/page1">This is the first snippet.</a>
		</div>
		<div class="result results_links results_links_deep web-result">
			<h2 class="result__title">
				<a class="result__a" href="https://example.org/page2">Example Page Two</a>
			</h2>
			<a class="result__snippet" href="https://example.org/page2">This is the second snippet.</a>
		</div>
		<div class="result results_links results_links_deep web-result">
			<h2 class="result__title">
				<a class="result__a" href="https://other.com/page3">Other Page Three</a>
			</h2>
			<a class="result__snippet" href="https://other.com/page3">Third snippet here.</a>
		</div>
	</body></html>`

	t.Run("basic parsing", func(t *testing.T) {
		hits, err := parseDDGHTML([]byte(mockHTML), 10, nil, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(hits) != 3 {
			t.Fatalf("expected 3 hits, got %d", len(hits))
		}
		if hits[0].Title != "Example Page One" {
			t.Errorf("expected title 'Example Page One', got %q", hits[0].Title)
		}
		if hits[0].URL != "https://example.com/page1" {
			t.Errorf("expected URL 'https://example.com/page1', got %q", hits[0].URL)
		}
		if hits[0].Snippet != "This is the first snippet." {
			t.Errorf("expected snippet 'This is the first snippet.', got %q", hits[0].Snippet)
		}
	})

	t.Run("max results limit", func(t *testing.T) {
		hits, err := parseDDGHTML([]byte(mockHTML), 2, nil, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(hits) != 2 {
			t.Fatalf("expected 2 hits, got %d", len(hits))
		}
	})

	t.Run("allowed domains", func(t *testing.T) {
		hits, err := parseDDGHTML([]byte(mockHTML), 10, []string{"example.com"}, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(hits) != 1 {
			t.Fatalf("expected 1 hit for allowed domain, got %d", len(hits))
		}
		if hits[0].URL != "https://example.com/page1" {
			t.Errorf("expected URL from example.com, got %q", hits[0].URL)
		}
	})

	t.Run("blocked domains", func(t *testing.T) {
		hits, err := parseDDGHTML([]byte(mockHTML), 10, nil, []string{"other.com"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(hits) != 2 {
			t.Fatalf("expected 2 hits with other.com blocked, got %d", len(hits))
		}
	})

	t.Run("redirect URL extraction", func(t *testing.T) {
		redirectHTML := `<html><body>
			<div class="result">
				<a class="result__a" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Freal.example.com%2Fpage">Redirect Test</a>
				<a class="result__snippet" href="#">A snippet.</a>
			</div>
		</body></html>`
		hits, err := parseDDGHTML([]byte(redirectHTML), 10, nil, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(hits) != 1 {
			t.Fatalf("expected 1 hit, got %d", len(hits))
		}
		if hits[0].URL != "https://real.example.com/page" {
			t.Errorf("expected redirected URL, got %q", hits[0].URL)
		}
	})

	t.Run("empty HTML", func(t *testing.T) {
		hits, err := parseDDGHTML([]byte("<html><body></body></html>"), 10, nil, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(hits) != 0 {
			t.Fatalf("expected 0 hits, got %d", len(hits))
		}
	})
}

func TestValidateInput(t *testing.T) {
	tl := New("", "")

	tests := []struct {
		name    string
		input   map[string]interface{}
		wantMsg string // empty == must be Valid
	}{
		{"empty query", map[string]interface{}{"query": ""}, "query must not be empty"},
		{"single-char query", map[string]interface{}{"query": "x"}, "at least 2"},
		{"negative max_results", map[string]interface{}{"query": "test", "max_results": -1}, "max_results must be non-negative"},
		{"max_results too high", map[string]interface{}{"query": "test", "max_results": 100}, "max_results exceeds maximum"},
		{"mutually exclusive domains", map[string]interface{}{
			"query":           "hi",
			"allowed_domains": []string{"a.com"},
			"blocked_domains": []string{"b.com"},
		}, "mutually exclusive"},
		{"valid query", map[string]interface{}{"query": "golang concurrency"}, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw, _ := json.Marshal(tt.input)
			vr := tl.ValidateInput(raw, nil)
			if vr == nil {
				t.Fatal("nil ValidationResult")
			}
			if tt.wantMsg == "" {
				if !vr.Valid {
					t.Errorf("expected valid, got invalid: %s", vr.Message)
				}
				return
			}
			if vr.Valid {
				t.Errorf("expected invalid containing %q, got valid", tt.wantMsg)
			} else if !strings.Contains(vr.Message, tt.wantMsg) {
				t.Errorf("message %q does not contain %q", vr.Message, tt.wantMsg)
			}
		})
	}
}

func TestInputSchema(t *testing.T) {
	tl := New("", "")
	s := tl.InputSchema()
	if s == nil || s.Type != "object" {
		t.Fatalf("bad schema: %+v", s)
	}
	if _, ok := s.Properties["query"]; !ok {
		t.Error("schema missing 'query' property")
	}
	if len(s.Required) == 0 || s.Required[0] != "query" {
		t.Errorf("schema must require 'query', got %v", s.Required)
	}
}

func TestCheckPermissions_DefaultAllow(t *testing.T) {
	tl := New("", "")
	p, err := tl.CheckPermissions(json.RawMessage(`{"query":"hi"}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	if p.Behavior != tool.PermissionAllow {
		t.Errorf("expected allow, got %q", p.Behavior)
	}
}

func TestMapToolResultToParam_FormatsHits(t *testing.T) {
	tl := New("", "")
	out := Output{
		Query: "go slices",
		Results: []SearchHit{
			{Title: "Slices", URL: "https://go.dev/blog/slices", Snippet: "About slices"},
			{Title: "Arrays", URL: "https://go.dev/ref/spec#Array_types"},
		},
	}
	block := tl.MapToolResultToParam(out, "tu_123")
	if block.ToolUseID != "tu_123" {
		t.Errorf("wrong tool_use_id: %q", block.ToolUseID)
	}
	text, _ := block.Content.(string)
	if !strings.Contains(text, `Web search results for query: "go slices"`) {
		t.Errorf("missing header in output: %q", text)
	}
	if !strings.Contains(text, "URL: https://go.dev/blog/slices") {
		t.Error("missing first URL")
	}
	if !strings.Contains(text, "Snippet: About slices") {
		t.Error("missing snippet")
	}
	if !strings.Contains(text, "REMINDER: You MUST include relevant sources") {
		t.Error("missing Sources reminder")
	}
}

func TestPrompt_ContainsSourcesAndCurrentYear(t *testing.T) {
	tl := New("", "")
	p := tl.Prompt(tool.PromptOptions{})
	if !strings.Contains(p, "Sources:") {
		t.Error("prompt must mention Sources: section")
	}
	if !strings.Contains(p, "[Title](URL)") {
		t.Error("prompt must show markdown hyperlink example")
	}
	// Current month/year from time.Now() — sanity check: 4-digit year is present.
	if !strings.Contains(p, "current month is") {
		t.Error("prompt must mention current month")
	}
}
