package websearch

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

const (
	braveBaseURL    = "https://api.search.brave.com/res/v1/web/search"
	braveHTTPTimeout = 15 * time.Second
)

// BraveProvider implements SearchProvider using the Brave Search API.
// Free tier: 2000 queries/month. Sign up at https://brave.com/search/api/
type BraveProvider struct {
	apiKey string
	client *http.Client
}

// NewBraveProvider creates a Brave Search provider.
func NewBraveProvider(apiKey string) *BraveProvider {
	return &BraveProvider{
		apiKey: apiKey,
		client: &http.Client{Timeout: braveHTTPTimeout},
	}
}

func (p *BraveProvider) Name() string { return "Brave" }

func (p *BraveProvider) Search(ctx context.Context, query string, maxResults int, allowedDomains, blockedDomains []string) ([]SearchHit, error) {
	if maxResults <= 0 {
		maxResults = defaultMaxResults
	}
	if maxResults > 20 {
		maxResults = 20 // Brave API max per request
	}

	params := url.Values{
		"q":     {query},
		"count": {fmt.Sprintf("%d", maxResults)},
	}

	reqURL := braveBaseURL + "?" + params.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("brave: create request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Accept-Encoding", "gzip")
	req.Header.Set("X-Subscription-Token", p.apiKey)

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("brave: request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("brave: HTTP %d: %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("brave: read body: %w", err)
	}

	return parseBraveResponse(body, allowedDomains, blockedDomains)
}

// braveResponse is the top-level Brave Search API response.
type braveResponse struct {
	Web *braveWebResults `json:"web"`
}

type braveWebResults struct {
	Results []braveWebResult `json:"results"`
}

type braveWebResult struct {
	Title       string `json:"title"`
	URL         string `json:"url"`
	Description string `json:"description"`
}

func parseBraveResponse(body []byte, allowedDomains, blockedDomains []string) ([]SearchHit, error) {
	var resp braveResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("brave: parse response: %w", err)
	}

	if resp.Web == nil || len(resp.Web.Results) == 0 {
		return nil, nil
	}

	allowed := toSet(allowedDomains)
	blocked := toSet(blockedDomains)

	var hits []SearchHit
	for _, r := range resp.Web.Results {
		if r.URL == "" || r.Title == "" {
			continue
		}
		if !domainMatchesFilter(r.URL, allowed, blocked) {
			continue
		}
		hits = append(hits, SearchHit{
			Title:   r.Title,
			URL:     r.URL,
			Snippet: r.Description,
		})
	}

	return hits, nil
}
