package webfetch

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	htmltomarkdown "github.com/JohannesKaufmann/html-to-markdown/v2"

	"github.com/wall-ai/ubuilding/backend/agents"
	"github.com/wall-ai/ubuilding/backend/agents/tool"
	"github.com/wall-ai/ubuilding/backend/agents/util"
)

// ---------------------------------------------------------------------------
// WebFetchTool — provider-agnostic URL fetcher. Maps to claude-code-main's
// tools/WebFetchTool, retaining the preapproved host list, SSRF guard,
// blocklist, in-memory cache, cross-host redirect template, and optional
// SideQuerier summarization (with compliance guardrails).
//
// Dropped from the upstream implementation:
//   - Anthropic domain_info preflight (tight coupling, not portable)
//   - PDF inline extraction (ledongthuc/pdf) — returns a placeholder instead
//   - React progress UI hooks
// ---------------------------------------------------------------------------

const (
	maxBodyBytes   = 10 * 1024 * 1024 // 10 MB
	maxOutputChars = 100_000
	httpTimeout    = 60 * time.Second
	maxURLLength   = 2000
	cacheTTL       = 15 * time.Minute
	// MaxMarkdownPassthrough bounds the size of already-markdown content that
	// will be returned verbatim from preapproved hosts (no LLM summarization).
	MaxMarkdownPassthrough = 100_000
)

// Input matches claude-code's WebFetch input.
type Input struct {
	URL    string `json:"url"`
	Prompt string `json:"prompt,omitempty"`
	// Format controls post-processing: "html" | "markdown" | "text".
	// Default is "markdown" (HTML is converted via html-to-markdown).
	Format string `json:"format,omitempty"`
}

// Output is the structured result of a WebFetch call.
type Output struct {
	Bytes      int    `json:"bytes"`
	Code       int    `json:"code"`
	CodeText   string `json:"codeText"`
	Result     string `json:"result"`
	DurationMs int64  `json:"durationMs"`
	URL        string `json:"url"`
}

// SideQuerier performs an auxiliary LLM call to summarize/transform fetched
// content. Leaving it nil disables the summarization path — the raw content
// is returned directly.
type SideQuerier interface {
	Query(ctx context.Context, prompt string, opts SideQueryOpts) (*SideQueryResult, error)
}

// SideQueryOpts carries model/token hints to the SideQuerier.
type SideQueryOpts struct {
	Model        string
	MaxTokens    int
	SystemPrompt string
}

// SideQueryResult is the output of a SideQuerier invocation.
type SideQueryResult struct {
	Text string
}

// Option configures a WebFetchTool at construction time.
type Option func(*WebFetchTool)

// WithAllowLoopback permits fetching loopback addresses (127.0.0.0/8, ::1).
// Intended for httptest integration tests; metadata IP 169.254.169.254 is
// still blocked regardless.
func WithAllowLoopback() Option {
	return func(t *WebFetchTool) { t.allowLoopback = true }
}

// WithSideQuerier wires a side-LLM for content summarization.
func WithSideQuerier(sq SideQuerier) Option {
	return func(t *WebFetchTool) { t.sideQuerier = sq }
}

// WithHTTPClient replaces the underlying HTTP client (tests).
func WithHTTPClient(c *http.Client) Option {
	return func(t *WebFetchTool) { t.client = c }
}

// WebFetchTool implements tool.Tool.
type WebFetchTool struct {
	client        *http.Client
	cache         *FetchCache
	sideQuerier   SideQuerier
	allowLoopback bool
}

// New constructs a WebFetchTool with sensible defaults.
func New(opts ...Option) *WebFetchTool {
	t := &WebFetchTool{
		client: &http.Client{Timeout: httpTimeout},
		cache:  NewFetchCache(256),
	}
	for _, opt := range opts {
		opt(t)
	}
	return t
}

// ClearCache empties the URL cache (tests).
func (t *WebFetchTool) ClearCache() { t.cache.Clear() }

// ── tool.Tool interface ────────────────────────────────────────────────────

func (t *WebFetchTool) Name() string                             { return "WebFetch" }
func (t *WebFetchTool) Aliases() []string                        { return nil }
func (t *WebFetchTool) IsEnabled() bool                          { return true }
func (t *WebFetchTool) IsReadOnly(_ json.RawMessage) bool        { return true }
func (t *WebFetchTool) IsConcurrencySafe(_ json.RawMessage) bool { return true }
func (t *WebFetchTool) IsDestructive(_ json.RawMessage) bool     { return false }
func (t *WebFetchTool) MaxResultSizeChars() int                  { return maxOutputChars }

func (t *WebFetchTool) InputSchema() *tool.JSONSchema {
	return &tool.JSONSchema{
		Type: "object",
		Properties: map[string]*tool.SchemaProperty{
			"url":    {Type: "string", Description: "The URL to fetch content from."},
			"prompt": {Type: "string", Description: "The prompt to run on the fetched content."},
			"format": {Type: "string", Enum: []string{"html", "markdown", "text"}, Description: "Output format. Default: markdown."},
		},
		Required: []string{"url"},
	}
}

func (t *WebFetchTool) Description(input json.RawMessage) string {
	var in Input
	if err := json.Unmarshal(input, &in); err != nil || in.URL == "" {
		return "Fetch a web page"
	}
	u := in.URL
	if len(u) > 80 {
		u = u[:80] + "\u2026"
	}
	return "Fetch " + u
}

func (t *WebFetchTool) Prompt(_ tool.PromptOptions) string {
	return `IMPORTANT: WebFetch WILL FAIL for authenticated or private URLs. Before using this tool, check if the URL points to an authenticated service (e.g. Google Docs, Confluence, Jira, GitHub). If so, look for a specialized MCP tool that provides authenticated access.

- Fetches content from a specified URL and processes it
- Takes a URL and a prompt as input
- Fetches the URL content, converts HTML to markdown
- Returns the processed content
- Use this tool when you need to retrieve and analyze web content

Usage notes:
  - IMPORTANT: If an MCP-provided web fetch tool is available, prefer using that tool instead of this one, as it may have fewer restrictions.
  - The URL must be a fully-formed valid URL
  - HTTP URLs will be automatically upgraded to HTTPS
  - The prompt should describe what information you want to extract from the page
  - This tool is read-only and does not modify any files
  - Results may be summarized if the content is very large
  - Includes a self-cleaning 15-minute cache for faster responses when repeatedly accessing the same URL
  - When a URL redirects to a different host, the tool will inform you and provide the redirect URL. You should then make a new WebFetch request with the redirect URL to fetch the content.
  - For GitHub URLs, prefer using the gh CLI via Bash instead (e.g., gh pr view, gh issue view, gh api).
  - Supports HTML (converted to markdown) and plain text; binary content (PDF, images) is not inlined.`
}

func (t *WebFetchTool) ValidateInput(input json.RawMessage, _ *agents.ToolUseContext) *tool.ValidationResult {
	var in Input
	if err := json.Unmarshal(input, &in); err != nil {
		return invalid("invalid input: " + err.Error())
	}
	if in.URL == "" {
		return invalid("url must not be empty")
	}
	if len(in.URL) > maxURLLength {
		return invalid(fmt.Sprintf("URL exceeds maximum length of %d characters", maxURLLength))
	}
	u, err := url.Parse(in.URL)
	if err != nil {
		return invalid(fmt.Sprintf("invalid URL %q: could not be parsed", in.URL))
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return invalid(fmt.Sprintf("URL scheme must be http or https, got %q", u.Scheme))
	}
	if u.Host == "" {
		return invalid("URL must have a host")
	}
	if u.User != nil {
		return invalid("URL must not contain username or password")
	}
	parts := strings.Split(u.Hostname(), ".")
	if len(parts) < 2 {
		return invalid("URL hostname must contain at least two segments")
	}
	if in.Format != "" && in.Format != "html" && in.Format != "markdown" && in.Format != "text" {
		return invalid(`format must be "html", "markdown", or "text"`)
	}
	return &tool.ValidationResult{Valid: true}
}

func (t *WebFetchTool) CheckPermissions(input json.RawMessage, _ *agents.ToolUseContext) (*tool.PermissionResult, error) {
	var in Input
	if err := json.Unmarshal(input, &in); err != nil {
		return nil, fmt.Errorf("invalid input: %w", err)
	}
	if in.URL == "" {
		return &tool.PermissionResult{Behavior: tool.PermissionDeny, Message: "url must not be empty"}, nil
	}

	// SSRF defense first (loopback, private ranges, cloud metadata).
	if err := util.CheckSSRFWithOptions(in.URL, util.CheckSSRFOptions{AllowLoopback: t.allowLoopback}); err != nil {
		return &tool.PermissionResult{
			Behavior:       tool.PermissionDeny,
			Message:        err.Error(),
			DecisionReason: "ssrf",
			RiskLevel:      "high",
		}, nil
	}

	// Blocklist (credentials-leak-prone API endpoints).
	u, err := url.Parse(in.URL)
	if err == nil {
		if msg := CheckDomainBlocklist(u.Hostname()); msg != "" {
			return &tool.PermissionResult{
				Behavior:       tool.PermissionDeny,
				Message:        msg,
				DecisionReason: "blocklist",
				RiskLevel:      "high",
			}, nil
		}
	}

	// Preapproved hosts — fast allow without nagging the user.
	if IsPreapprovedURL(in.URL) {
		return &tool.PermissionResult{
			Behavior:       tool.PermissionAllow,
			UpdatedInput:   input,
			DecisionReason: "preapproved",
		}, nil
	}

	// Default allow (ask-to-allow permission rules not yet wired into engine).
	// TODO: when permission rule matching lands, return Behavior=Ask with
	// Suggestions for "always allow WebFetch for domain:<host>".
	return &tool.PermissionResult{
		Behavior:       tool.PermissionAllow,
		UpdatedInput:   input,
		DecisionReason: "default",
	}, nil
}

func (t *WebFetchTool) Call(ctx context.Context, input json.RawMessage, _ *agents.ToolUseContext) (*tool.ToolResult, error) {
	var in Input
	if err := json.Unmarshal(input, &in); err != nil {
		return nil, fmt.Errorf("invalid input: %w", err)
	}

	start := time.Now()

	// ── Cache hit ──────────────────────────────────────────────────────
	if cached := t.cache.Get(in.URL); cached != nil {
		result := cached.Body
		if len(result) > maxOutputChars {
			result = result[:maxOutputChars] + "\n[... truncated ...]"
		}
		return &tool.ToolResult{Data: Output{
			Bytes:      len(cached.Body),
			Code:       cached.StatusCode,
			CodeText:   http.StatusText(cached.StatusCode),
			Result:     result,
			DurationMs: time.Since(start).Milliseconds(),
			URL:        in.URL,
		}}, nil
	}

	// http → https upgrade. Skipped when loopback is explicitly allowed, so
	// httptest servers (which only speak HTTP) remain reachable.
	fetchURL := in.URL
	if !t.allowLoopback {
		if u, err := url.Parse(fetchURL); err == nil && u.Scheme == "http" {
			u.Scheme = "https"
			fetchURL = u.String()
		}
	}

	resp, body, redir, err := fetchWithRedirects(t.client, fetchURL, map[string]string{
		"User-Agent": "Mozilla/5.0 WalleAI-WebFetch/1.0",
	}, maxBodyBytes)
	if err != nil {
		return nil, err
	}

	// Cross-host redirect — return guidance instead of silently following.
	if redir != nil {
		statusText := http.StatusText(redir.StatusCode)
		msg := fmt.Sprintf("REDIRECT DETECTED: The URL redirects to a different host.\n\n"+
			"Original URL: %s\nRedirect URL: %s\nStatus: %d %s\n\n"+
			"To complete your request, please use WebFetch again with:\n- url: %q\n- prompt: %q",
			redir.OriginalURL, redir.RedirectURL, redir.StatusCode, statusText,
			redir.RedirectURL, in.Prompt)
		return &tool.ToolResult{Data: Output{
			Bytes:      len(msg),
			Code:       redir.StatusCode,
			CodeText:   statusText,
			Result:     msg,
			DurationMs: time.Since(start).Milliseconds(),
			URL:        in.URL,
		}}, nil
	}

	contentType := ""
	statusCode := 0
	statusText := ""
	if resp != nil {
		contentType = resp.Header.Get("Content-Type")
		statusCode = resp.StatusCode
		statusText = http.StatusText(resp.StatusCode)
	}

	output := processBody(body, contentType, in)

	// ── Cache the processed output ─────────────────────────────────────
	t.cache.Set(in.URL, &CacheEntry{
		Body:        output,
		ContentType: contentType,
		StatusCode:  statusCode,
		FetchedAt:   time.Now(),
		TTL:         cacheTTL,
	})

	// ── Preapproved + markdown passthrough (T-I1) ──────────────────────
	// Preapproved hosts returning already-markdown content small enough
	// to pass through directly skip the SideQuerier to save a round trip.
	preapproved := IsPreapprovedURL(in.URL)
	isMarkdown := strings.Contains(strings.ToLower(contentType), "text/markdown") ||
		(in.Format == "" || in.Format == "markdown") && strings.Contains(strings.ToLower(contentType), "text/html")

	passthrough := preapproved && isMarkdown && len(output) <= MaxMarkdownPassthrough

	// ── Optional summarization (T-I2 compliance guardrails) ────────────
	if !passthrough && t.sideQuerier != nil && in.Prompt != "" && len(output) > 0 {
		summary, sumErr := applyPromptToContent(ctx, t.sideQuerier, in.Prompt, output, preapproved)
		if sumErr == nil && summary != "" {
			output = summary
		}
	}

	if len(output) > maxOutputChars {
		output = output[:maxOutputChars] + "\n[... truncated ...]"
	}

	return &tool.ToolResult{Data: Output{
		Bytes:      len(body),
		Code:       statusCode,
		CodeText:   statusText,
		Result:     output,
		DurationMs: time.Since(start).Milliseconds(),
		URL:        in.URL,
	}}, nil
}

// processBody converts the raw HTTP body into the requested output format.
func processBody(body []byte, contentType string, in Input) string {
	ct := strings.ToLower(contentType)

	// Non-inlined binary content (PDFs, images) — claude-code persists these
	// to disk; we just describe them for the model.
	if strings.Contains(ct, "application/pdf") ||
		strings.HasSuffix(strings.ToLower(in.URL), ".pdf") ||
		strings.HasPrefix(ct, "image/") ||
		strings.HasPrefix(ct, "application/octet-stream") {
		return fmt.Sprintf("[Binary content (%s, %d bytes) — not inlined. Use a specialized tool to analyze this resource.]", contentTypeLabel(contentType), len(body))
	}

	format := in.Format
	if format == "" {
		format = "markdown"
	}
	switch format {
	case "html":
		return string(body)
	case "text":
		return stripHTML(string(body))
	default: // markdown
		if strings.Contains(ct, "text/html") {
			md, err := htmltomarkdown.ConvertString(string(body))
			if err != nil {
				return stripHTML(string(body))
			}
			return md
		}
		return string(body)
	}
}

// MapToolResultToParam returns the processed text as the tool-result content.
func (t *WebFetchTool) MapToolResultToParam(content interface{}, toolUseID string) *agents.ContentBlock {
	return &agents.ContentBlock{
		Type:      agents.ContentBlockToolResult,
		ToolUseID: toolUseID,
		Content:   extractResultText(content),
	}
}

func extractResultText(content interface{}) string {
	switch v := content.(type) {
	case Output:
		return v.Result
	case *Output:
		if v != nil {
			return v.Result
		}
		return ""
	case string:
		var out Output
		if err := json.Unmarshal([]byte(v), &out); err == nil && out.Result != "" {
			return out.Result
		}
		return v
	case []byte:
		var out Output
		if err := json.Unmarshal(v, &out); err == nil && out.Result != "" {
			return out.Result
		}
		return string(v)
	default:
		b, _ := json.Marshal(content)
		return string(b)
	}
}

// applyPromptToContent asks the SideQuerier to extract the requested info.
// The guidelines differ for preapproved vs. external hosts — external hosts
// get a stricter compliance template (claude-code's WebFetchTool approach).
func applyPromptToContent(ctx context.Context, sq SideQuerier, prompt, content string, preapproved bool) (string, error) {
	const maxContentForSummary = 50_000
	if len(content) > maxContentForSummary {
		content = content[:maxContentForSummary] + "\n[... truncated for summary ...]\n"
	}

	var sysPrompt string
	if preapproved {
		sysPrompt = "You are a content extraction assistant for trusted documentation sources. " +
			"Provide a concise response based on the content above. Include relevant details, " +
			"code examples, and documentation excerpts as needed."
	} else {
		sysPrompt = "You are a content extraction assistant for third-party web content. " +
			"Follow these guidelines strictly when processing the content:\n" +
			"- Quote directly from the source only when essential, and keep each quote under " +
			"125 characters.\n" +
			"- Summarize and paraphrase the rest in your own words.\n" +
			"- Never reproduce song lyrics, poetry, or other copyrighted creative work verbatim.\n" +
			"- Do not provide legal, financial, or medical advice beyond what the content " +
			"explicitly states; defer to qualified professionals for specific situations.\n" +
			"- Attribute statements to the source domain where relevant."
	}

	userMsg := fmt.Sprintf("Web page content:\n\n%s\n\n---\nUser request: %s", content, prompt)
	result, err := sq.Query(ctx, userMsg, SideQueryOpts{
		Model:        "",
		MaxTokens:    4096,
		SystemPrompt: sysPrompt,
	})
	if err != nil {
		return "", err
	}
	return result.Text, nil
}

// stripHTML is a tiny fallback tag remover for when html-to-markdown fails or
// content is non-HTML text.
func stripHTML(s string) string {
	inTag := false
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '<':
			inTag = true
		case r == '>':
			inTag = false
			b.WriteRune(' ')
		case !inTag:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func contentTypeLabel(ct string) string {
	if i := strings.Index(ct, ";"); i >= 0 {
		ct = ct[:i]
	}
	ct = strings.TrimSpace(ct)
	if ct == "" {
		return "unknown"
	}
	return ct
}

func invalid(msg string) *tool.ValidationResult {
	return &tool.ValidationResult{Valid: false, Message: msg}
}

// Compile-time assertion.
var _ tool.Tool = (*WebFetchTool)(nil)
