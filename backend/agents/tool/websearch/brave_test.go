package websearch

import (
	"testing"
)

func TestParseBraveResponse(t *testing.T) {
	t.Run("basic response", func(t *testing.T) {
		body := []byte(`{
			"web": {
				"results": [
					{"title": "Go Programming", "url": "https://go.dev/", "description": "The Go programming language"},
					{"title": "Go Wiki", "url": "https://en.wikipedia.org/wiki/Go", "description": "Go Wikipedia article"}
				]
			}
		}`)
		hits, err := parseBraveResponse(body, nil, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(hits) != 2 {
			t.Fatalf("expected 2 hits, got %d", len(hits))
		}
		if hits[0].Title != "Go Programming" {
			t.Errorf("expected title 'Go Programming', got %q", hits[0].Title)
		}
		if hits[0].URL != "https://go.dev/" {
			t.Errorf("expected URL 'https://go.dev/', got %q", hits[0].URL)
		}
		if hits[0].Snippet != "The Go programming language" {
			t.Errorf("expected snippet, got %q", hits[0].Snippet)
		}
	})

	t.Run("empty results", func(t *testing.T) {
		body := []byte(`{"web": {"results": []}}`)
		hits, err := parseBraveResponse(body, nil, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(hits) != 0 {
			t.Fatalf("expected 0 hits, got %d", len(hits))
		}
	})

	t.Run("no web field", func(t *testing.T) {
		body := []byte(`{}`)
		hits, err := parseBraveResponse(body, nil, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(hits) != 0 {
			t.Fatalf("expected 0 hits, got %d", len(hits))
		}
	})

	t.Run("allowed domains filter", func(t *testing.T) {
		body := []byte(`{
			"web": {
				"results": [
					{"title": "Go", "url": "https://go.dev/", "description": "Go lang"},
					{"title": "Wiki", "url": "https://en.wikipedia.org/wiki/Go", "description": "Wiki"}
				]
			}
		}`)
		hits, err := parseBraveResponse(body, []string{"go.dev"}, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(hits) != 1 {
			t.Fatalf("expected 1 hit, got %d", len(hits))
		}
		if hits[0].URL != "https://go.dev/" {
			t.Errorf("expected go.dev URL, got %q", hits[0].URL)
		}
	})

	t.Run("blocked domains filter", func(t *testing.T) {
		body := []byte(`{
			"web": {
				"results": [
					{"title": "Go", "url": "https://go.dev/", "description": "Go lang"},
					{"title": "Wiki", "url": "https://en.wikipedia.org/wiki/Go", "description": "Wiki"}
				]
			}
		}`)
		hits, err := parseBraveResponse(body, nil, []string{"en.wikipedia.org"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(hits) != 1 {
			t.Fatalf("expected 1 hit, got %d", len(hits))
		}
		if hits[0].URL != "https://go.dev/" {
			t.Errorf("expected go.dev URL, got %q", hits[0].URL)
		}
	})

	t.Run("skip entries with empty URL or title", func(t *testing.T) {
		body := []byte(`{
			"web": {
				"results": [
					{"title": "", "url": "https://go.dev/", "description": "No title"},
					{"title": "No URL", "url": "", "description": "Missing URL"},
					{"title": "Valid", "url": "https://example.com", "description": "OK"}
				]
			}
		}`)
		hits, err := parseBraveResponse(body, nil, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(hits) != 1 {
			t.Fatalf("expected 1 hit, got %d", len(hits))
		}
		if hits[0].Title != "Valid" {
			t.Errorf("expected title 'Valid', got %q", hits[0].Title)
		}
	})

	t.Run("invalid JSON", func(t *testing.T) {
		body := []byte(`not json`)
		_, err := parseBraveResponse(body, nil, nil)
		if err == nil {
			t.Fatal("expected error for invalid JSON")
		}
	})
}
