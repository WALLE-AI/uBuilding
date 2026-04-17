package websearch

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/html"

	"github.com/wall-ai/ubuilding/backend/agents"
	"github.com/wall-ai/ubuilding/backend/agents/tool"
)

// ---------------------------------------------------------------------------
// WebSearchTool — provider-agnostic web search. Maps to claude-code-main's
// tools/WebSearchTool (name, prompt, Sources-section requirement, current-
// month injection). The actual search is delegated to a pluggable
// SearchProvider; the Anthropic server-tool path (web_search_20250305) is not
// used here to keep the tool usable with any LLM provider.
// ---------------------------------------------------------------------------

const (
	defaultMaxResults = 10
	maxResultsCap     = 50
	httpTimeout       = 15 * time.Second
	maxQueryTrimChars = 80
	// MaxResultChars is the upper bound on total serialized output size.
	MaxResultChars = 100_000
)

// Input matches claude-code's WebSearch input shape.
type Input struct {
	Query          string   `json:"query"`
	MaxResults     int      `json:"max_results,omitempty"`
	AllowedDomains []string `json:"allowed_domains,omitempty"`
	BlockedDomains []string `json:"blocked_domains,omitempty"`
}

// SearchHit is a single search result row.
type SearchHit struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet,omitempty"`
}

// Output is the structured result of a WebSearch invocation.
type Output struct {
	Query           string      `json:"query"`
	Results         []SearchHit `json:"results"`
	DurationSeconds float64     `json:"durationSeconds"`
}

// WebSearchTool implements tool.Tool.
type WebSearchTool struct {
	apiKey   string
	baseURL  string
	client   *http.Client
	provider SearchProvider
}

// New constructs a WebSearchTool with an auto-selected provider (Brave if an
// API key is in env, otherwise DuckDuckGo HTML scraping).
func New(apiKey, baseURL string) *WebSearchTool {
	if baseURL == "" {
		baseURL = "https://html.duckduckgo.com"
	}
	t := &WebSearchTool{
		apiKey:  apiKey,
		baseURL: baseURL,
		client:  &http.Client{Timeout: httpTimeout},
	}
	t.provider = ResolveProvider(t)
	return t
}

// SetProvider swaps the underlying SearchProvider.
func (t *WebSearchTool) SetProvider(p SearchProvider) { t.provider = p }

// ── tool.Tool interface ────────────────────────────────────────────────────

func (t *WebSearchTool) Name() string                          { return "WebSearch" }
func (t *WebSearchTool) Aliases() []string                     { return nil }
func (t *WebSearchTool) IsEnabled() bool                       { return true }
func (t *WebSearchTool) IsReadOnly(_ json.RawMessage) bool     { return true }
func (t *WebSearchTool) IsConcurrencySafe(_ json.RawMessage) bool { return true }
func (t *WebSearchTool) IsDestructive(_ json.RawMessage) bool  { return false }
func (t *WebSearchTool) MaxResultSizeChars() int               { return MaxResultChars }

func (t *WebSearchTool) InputSchema() *tool.JSONSchema {
	return &tool.JSONSchema{
		Type: "object",
		Properties: map[string]*tool.SchemaProperty{
			"query":           {Type: "string", Description: "The search query to use."},
			"max_results":     {Type: "integer", Description: "Maximum number of results (default 10, max 50)."},
			"allowed_domains": {Type: "array", Description: "Only include search results from these domains."},
			"blocked_domains": {Type: "array", Description: "Never include search results from these domains."},
		},
		Required: []string{"query"},
	}
}

func (t *WebSearchTool) Description(input json.RawMessage) string {
	var in Input
	if err := json.Unmarshal(input, &in); err != nil || in.Query == "" {
		return "Search the web"
	}
	q := in.Query
	if len(q) > maxQueryTrimChars {
		q = q[:maxQueryTrimChars] + "\u2026"
	}
	return "Search the web: " + q
}

func (t *WebSearchTool) Prompt(_ tool.PromptOptions) string {
	currentMonthYear := time.Now().Format("January 2006")
	return fmt.Sprintf(`- Allows the agent to search the web and use the results to inform responses
- Provides up-to-date information for current events and recent data
- Returns search result information formatted as search result blocks, including links as markdown hyperlinks
- Use this tool for accessing information beyond the agent's knowledge cutoff
- Searches are performed automatically within a single API call

CRITICAL REQUIREMENT - You MUST follow this:
  - After answering the user's question, you MUST include a "Sources:" section at the end of your response
  - In the Sources section, list all relevant URLs from the search results as markdown hyperlinks: [Title](URL)
  - This is MANDATORY - never skip including sources in your response
  - Example format:

    [Your answer here]

    Sources:
    - [Source Title 1](https://example.com/1)
    - [Source Title 2](https://example.com/2)

Usage notes:
  - Domain filtering is supported to include or block specific websites
  - allowed_domains and blocked_domains are mutually exclusive (do not specify both)

IMPORTANT - Use the correct year in search queries:
  - The current month is %s. You MUST use this year when searching for recent information, documentation, or current events.
  - Example: If the user asks for "latest React docs", search for "React documentation" with the current year, NOT last year`, currentMonthYear)
}

func (t *WebSearchTool) ValidateInput(input json.RawMessage, _ *agents.ToolUseContext) *tool.ValidationResult {
	var in Input
	if err := json.Unmarshal(input, &in); err != nil {
		return &tool.ValidationResult{Valid: false, Message: fmt.Sprintf("invalid input: %v", err)}
	}
	if in.Query == "" {
		return &tool.ValidationResult{Valid: false, Message: "query must not be empty"}
	}
	if len(in.Query) < 2 {
		return &tool.ValidationResult{Valid: false, Message: "query must be at least 2 characters"}
	}
	if in.MaxResults < 0 {
		return &tool.ValidationResult{Valid: false, Message: "max_results must be non-negative"}
	}
	if in.MaxResults > maxResultsCap {
		return &tool.ValidationResult{Valid: false, Message: fmt.Sprintf("max_results exceeds maximum of %d", maxResultsCap)}
	}
	if len(in.AllowedDomains) > 0 && len(in.BlockedDomains) > 0 {
		return &tool.ValidationResult{Valid: false, Message: "allowed_domains and blocked_domains are mutually exclusive"}
	}
	return &tool.ValidationResult{Valid: true}
}

func (t *WebSearchTool) CheckPermissions(input json.RawMessage, _ *agents.ToolUseContext) (*tool.PermissionResult, error) {
	return &tool.PermissionResult{
		Behavior:       tool.PermissionAllow,
		UpdatedInput:   input,
		DecisionReason: "default-allow",
	}, nil
}

func (t *WebSearchTool) Call(ctx context.Context, input json.RawMessage, _ *agents.ToolUseContext) (*tool.ToolResult, error) {
	var in Input
	if err := json.Unmarshal(input, &in); err != nil {
		return nil, fmt.Errorf("invalid input: %w", err)
	}
	if in.MaxResults <= 0 {
		in.MaxResults = defaultMaxResults
	}

	start := time.Now()
	hits, err := t.provider.Search(ctx, in.Query, in.MaxResults, in.AllowedDomains, in.BlockedDomains)
	if err != nil {
		return nil, err
	}

	out := Output{
		Query:           in.Query,
		Results:         hits,
		DurationSeconds: time.Since(start).Seconds(),
	}
	return &tool.ToolResult{Data: out}, nil
}

// MapToolResultToParam formats the Output into the textual block claude-code
// uses: "Web search results for query: ...\n\nTitle: ...\nURL: ...\n...".
func (t *WebSearchTool) MapToolResultToParam(content interface{}, toolUseID string) *agents.ContentBlock {
	text := renderOutput(content)
	return &agents.ContentBlock{
		Type:      agents.ContentBlockToolResult,
		ToolUseID: toolUseID,
		Content:   text,
	}
}

// renderOutput turns either an Output value or a pre-serialized JSON string
// into the human-readable textual form that the model consumes.
func renderOutput(content interface{}) string {
	var out Output
	switch v := content.(type) {
	case Output:
		out = v
	case *Output:
		if v != nil {
			out = *v
		}
	case string:
		if err := json.Unmarshal([]byte(v), &out); err != nil || len(out.Results) == 0 {
			return v
		}
	case []byte:
		if err := json.Unmarshal(v, &out); err != nil || len(out.Results) == 0 {
			return string(v)
		}
	default:
		b, _ := json.Marshal(content)
		return string(b)
	}

	if len(out.Results) == 0 {
		return fmt.Sprintf("Web search for %q returned no results.", out.Query)
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "Web search results for query: %q\n\n", out.Query)
	for _, hit := range out.Results {
		fmt.Fprintf(&sb, "Title: %s\nURL: %s\n", hit.Title, hit.URL)
		if hit.Snippet != "" {
			fmt.Fprintf(&sb, "Snippet: %s\n", hit.Snippet)
		}
		sb.WriteString("\n")
	}
	sb.WriteString("REMINDER: You MUST include relevant sources from above in your response.")
	return sb.String()
}

// ── DuckDuckGo HTML provider ────────────────────────────────────────────────

type ddgProvider struct {
	tool *WebSearchTool
}

func (p *ddgProvider) Name() string { return "DuckDuckGo" }

func (p *ddgProvider) Search(ctx context.Context, query string, maxResults int, allowedDomains, blockedDomains []string) ([]SearchHit, error) {
	form := url.Values{"q": {query}}
	reqURL := p.tool.baseURL + "/html/"

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "text/html")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9,zh-CN;q=0.8,zh;q=0.7")

	resp, err := p.tool.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == 202 || resp.StatusCode == 403 {
			return nil, fmt.Errorf("DuckDuckGo blocked the request (HTTP %d, likely CAPTCHA). "+
				"Set AGENT_ENGINE_SEARCH_API_KEY with a Brave Search API key for reliable search", resp.StatusCode)
		}
		return nil, fmt.Errorf("DuckDuckGo returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
	if err != nil {
		return nil, err
	}

	hits, err := parseDDGHTML(body, maxResults, allowedDomains, blockedDomains)
	if err != nil {
		return nil, err
	}
	if len(hits) == 0 && detectDDGCaptcha(body) {
		slog.Warn("websearch: DuckDuckGo returned CAPTCHA page",
			slog.String("query", query), slog.Int("body_len", len(body)))
		return nil, fmt.Errorf("DuckDuckGo returned a CAPTCHA page instead of results. " +
			"Set AGENT_ENGINE_SEARCH_API_KEY with a Brave Search API key for reliable search")
	}
	return hits, nil
}

func parseDDGHTML(body []byte, maxResults int, allowedDomains, blockedDomains []string) ([]SearchHit, error) {
	doc, err := html.Parse(strings.NewReader(string(body)))
	if err != nil {
		return nil, fmt.Errorf("failed to parse HTML: %w", err)
	}
	allowed := toSet(allowedDomains)
	blocked := toSet(blockedDomains)

	var hits []SearchHit
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if len(hits) >= maxResults {
			return
		}
		if n.Type == html.ElementNode && n.Data == "div" && hasClass(n, "result") {
			hit := extractDDGResult(n)
			if hit.URL != "" && hit.Title != "" && domainMatchesFilter(hit.URL, allowed, blocked) {
				hits = append(hits, hit)
			}
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return hits, nil
}

func extractDDGResult(n *html.Node) SearchHit {
	var hit SearchHit
	var extract func(*html.Node)
	extract = func(node *html.Node) {
		if node.Type == html.ElementNode && node.Data == "a" && hasClass(node, "result__a") {
			hit.URL = getAttr(node, "href")
			hit.Title = textContent(node)
			if strings.Contains(hit.URL, "uddg=") {
				if u, err := url.Parse(hit.URL); err == nil {
					if actual := u.Query().Get("uddg"); actual != "" {
						hit.URL = actual
					}
				}
			}
		}
		if node.Type == html.ElementNode && node.Data == "a" && hasClass(node, "result__snippet") {
			hit.Snippet = textContent(node)
		}
		for c := node.FirstChild; c != nil; c = c.NextSibling {
			extract(c)
		}
	}
	extract(n)
	return hit
}

func hasClass(n *html.Node, class string) bool {
	for _, a := range n.Attr {
		if a.Key == "class" {
			for _, c := range strings.Fields(a.Val) {
				if c == class {
					return true
				}
			}
		}
	}
	return false
}

func getAttr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

func textContent(n *html.Node) string {
	var sb strings.Builder
	var collect func(*html.Node)
	collect = func(node *html.Node) {
		if node.Type == html.TextNode {
			sb.WriteString(node.Data)
		}
		for c := node.FirstChild; c != nil; c = c.NextSibling {
			collect(c)
		}
	}
	collect(n)
	return strings.TrimSpace(sb.String())
}

func toSet(ss []string) map[string]bool {
	if len(ss) == 0 {
		return nil
	}
	m := make(map[string]bool, len(ss))
	for _, s := range ss {
		m[strings.ToLower(s)] = true
	}
	return m
}

func domainMatchesFilter(rawURL string, allowed, blocked map[string]bool) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return true
	}
	host := strings.ToLower(u.Hostname())
	if len(allowed) > 0 {
		for d := range allowed {
			if host == d || strings.HasSuffix(host, "."+d) {
				return true
			}
		}
		return false
	}
	if len(blocked) > 0 {
		for d := range blocked {
			if host == d || strings.HasSuffix(host, "."+d) {
				return false
			}
		}
	}
	return true
}

func detectDDGCaptcha(body []byte) bool {
	s := strings.ToLower(string(body))
	for _, ind := range []string{"captcha", "challenge", "blocked", "unusual traffic", "robot"} {
		if strings.Contains(s, ind) {
			return true
		}
	}
	return false
}

// Compile-time assertion.
var _ tool.Tool = (*WebSearchTool)(nil)
