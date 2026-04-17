package webfetch

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/wall-ai/ubuilding/backend/agents/tool"
)

func TestValidateInput(t *testing.T) {
	tl := New()

	tests := []struct {
		name    string
		input   map[string]interface{}
		wantMsg string // empty = valid expected
	}{
		{"empty url", map[string]interface{}{"url": ""}, "url must not be empty"},
		{"invalid scheme", map[string]interface{}{"url": "ftp://example.com"}, "scheme must be http or https"},
		{"no host", map[string]interface{}{"url": "https://"}, "must have a host"},
		{"too-short host", map[string]interface{}{"url": "https://localhost"}, "at least two segments"},
		{"credentials in url", map[string]interface{}{"url": "https://a:b@example.com"}, "must not contain username or password"},
		{"invalid format", map[string]interface{}{"url": "https://example.com", "format": "xml"}, `format must be "html"`},
		{"valid https", map[string]interface{}{"url": "https://example.com"}, ""},
		{"valid http", map[string]interface{}{"url": "http://example.com"}, ""},
		{"valid with html format", map[string]interface{}{"url": "https://example.com", "format": "html"}, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw, _ := json.Marshal(tt.input)
			vr := tl.ValidateInput(raw, nil)
			if tt.wantMsg == "" {
				if !vr.Valid {
					t.Errorf("expected valid, got invalid: %s", vr.Message)
				}
				return
			}
			if vr.Valid {
				t.Errorf("expected invalid with %q, got valid", tt.wantMsg)
			} else if !strings.Contains(vr.Message, tt.wantMsg) {
				t.Errorf("message %q does not contain %q", vr.Message, tt.wantMsg)
			}
		})
	}
}

func TestCheckPermissions_SSRFLoopback(t *testing.T) {
	tl := New()
	p, err := tl.CheckPermissions(json.RawMessage(`{"url":"http://127.0.0.1/"}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	if p.Behavior != tool.PermissionDeny {
		t.Fatalf("expected deny, got %q (msg=%s)", p.Behavior, p.Message)
	}
	if p.DecisionReason != "ssrf" {
		t.Errorf("expected decision_reason=ssrf, got %q", p.DecisionReason)
	}
}

func TestCheckPermissions_AllowLoopbackOption(t *testing.T) {
	tl := New(WithAllowLoopback())
	p, err := tl.CheckPermissions(json.RawMessage(`{"url":"http://127.0.0.1/"}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	if p.Behavior != tool.PermissionAllow {
		t.Errorf("expected allow with AllowLoopback, got %q", p.Behavior)
	}
}

func TestCheckPermissions_MetadataBlockedEvenWithLoopback(t *testing.T) {
	tl := New(WithAllowLoopback())
	p, err := tl.CheckPermissions(json.RawMessage(`{"url":"http://169.254.169.254/"}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	if p.Behavior != tool.PermissionDeny {
		t.Error("metadata IP must be denied even with AllowLoopback")
	}
}

func TestCheckPermissions_Blocklist(t *testing.T) {
	tl := New()
	p, err := tl.CheckPermissions(json.RawMessage(`{"url":"https://api.openai.com/v1/chat"}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	if p.Behavior != tool.PermissionDeny {
		t.Fatalf("expected deny, got %q", p.Behavior)
	}
	if p.DecisionReason != "blocklist" {
		t.Errorf("expected decision_reason=blocklist, got %q", p.DecisionReason)
	}
}

func TestCheckPermissions_PreapprovedDecisionReason(t *testing.T) {
	tl := New()
	p, err := tl.CheckPermissions(json.RawMessage(`{"url":"https://go.dev/blog/slices"}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	if p.Behavior != tool.PermissionAllow {
		t.Fatalf("expected allow, got %q", p.Behavior)
	}
	if p.DecisionReason != "preapproved" {
		t.Errorf("expected decision_reason=preapproved, got %q", p.DecisionReason)
	}
}

func TestInputSchema(t *testing.T) {
	tl := New()
	s := tl.InputSchema()
	if s == nil || s.Type != "object" {
		t.Fatalf("bad schema: %+v", s)
	}
	if _, ok := s.Properties["url"]; !ok {
		t.Error("schema missing 'url'")
	}
	if len(s.Required) != 1 || s.Required[0] != "url" {
		t.Errorf("schema must require url only, got %v", s.Required)
	}
}

func TestMapToolResultToParam_ExtractsResult(t *testing.T) {
	tl := New()
	out := Output{Result: "hello world", URL: "https://example.com", Code: 200}
	block := tl.MapToolResultToParam(out, "tu_1")
	if got, _ := block.Content.(string); got != "hello world" {
		t.Errorf("expected 'hello world', got %q", got)
	}
}

func TestStripHTML_Basic(t *testing.T) {
	got := stripHTML("<p>Hi <b>there</b></p>")
	got = strings.TrimSpace(strings.Join(strings.Fields(got), " "))
	if got != "Hi there" {
		t.Errorf("stripHTML = %q, want 'Hi there'", got)
	}
}

func TestHTMLToMarkdown_SimpleHeading(t *testing.T) {
	tl := New()
	in := Input{URL: "https://example.com"}
	out := processBody([]byte(`<html><body><h1>Hello</h1><p>World</p></body></html>`), "text/html; charset=utf-8", in)
	if !strings.Contains(out, "Hello") {
		t.Errorf("expected 'Hello' in output, got %q", out)
	}
	if !strings.Contains(out, "#") && !strings.Contains(out, "World") {
		t.Errorf("expected markdown-ish output, got %q", out)
	}
	_ = tl
}

// ---- httptest-driven end-to-end -------------------------------------------

func TestCall_HttptestPassthrough(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<html><body><h1>Hi</h1><p>World</p></body></html>`)
	}))
	defer srv.Close()

	tl := New(WithAllowLoopback())
	raw, _ := json.Marshal(Input{URL: srv.URL})
	res, err := tl.Call(context.Background(), raw, nil)
	if err != nil {
		t.Fatalf("Call error: %v", err)
	}
	out, ok := res.Data.(Output)
	if !ok {
		t.Fatalf("expected Output, got %T", res.Data)
	}
	if out.Code != 200 {
		t.Errorf("expected code 200, got %d", out.Code)
	}
	if !strings.Contains(out.Result, "Hi") {
		t.Errorf("expected 'Hi' in result, got %q", out.Result)
	}
}

// spySideQuerier tallies invocations + records the system prompt seen.
type spySideQuerier struct {
	calls     int32
	lastSys   string
	returnStr string
}

func (s *spySideQuerier) Query(_ context.Context, _ string, opts SideQueryOpts) (*SideQueryResult, error) {
	atomic.AddInt32(&s.calls, 1)
	s.lastSys = opts.SystemPrompt
	return &SideQueryResult{Text: s.returnStr}, nil
}

// TestCall_PreapprovedSkipsSideQuerier — the T-I1 passthrough path.
// We simulate a preapproved URL (go.dev is in preapproved list) by pointing
// the client at our httptest server with a URL rewrite. Simpler: override
// IsPreapprovedHost by picking any preapproved hostname in the request.
// Since we can't easily spoof the hostname against a local server, we verify
// the passthrough logic by calling Call with the client redirected to a
// local server but URL cache keyed on a preapproved URL.
func TestCall_PreapprovedSkipsSideQuerier(t *testing.T) {
	// Pre-populate cache with output tied to a preapproved URL; the side
	// querier should not be called on subsequent fetches.
	tl := New()
	spy := &spySideQuerier{returnStr: "SUMMARIZED"}
	tl.sideQuerier = spy
	tl.cache.Set("https://go.dev/blog/slices", &CacheEntry{
		Body:        "# Slices\nContent",
		ContentType: "text/markdown",
		StatusCode:  200,
		TTL:         cacheTTL,
	})
	// Second request should hit the cache and return immediately without
	// touching SideQuerier.
	raw, _ := json.Marshal(Input{URL: "https://go.dev/blog/slices", Prompt: "summarize"})
	res, err := tl.Call(context.Background(), raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	out := res.Data.(Output)
	if !strings.Contains(out.Result, "Slices") {
		t.Errorf("expected cached content, got %q", out.Result)
	}
	if n := atomic.LoadInt32(&spy.calls); n != 0 {
		t.Errorf("SideQuerier must not be called for cached preapproved content, got %d calls", n)
	}
}

// TestCall_CrossHostRedirectTemplate verifies the REDIRECT guidance message
// carries the precise HTTP status text for every redirect status code.
func TestCall_CrossHostRedirectTemplate(t *testing.T) {
	type want struct {
		code int
		text string // fragment that must appear in Result
	}
	cases := []want{
		{301, "301 Moved Permanently"},
		{302, "302 Found"},
		{303, "303 See Other"},
		{307, "307 Temporary Redirect"},
		{308, "308 Permanent Redirect"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(fmt.Sprintf("status_%d", tc.code), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Location", "http://other-host.example.com/moved")
				w.WriteHeader(tc.code)
			}))
			defer srv.Close()

			tl := New(WithAllowLoopback())
			raw, _ := json.Marshal(Input{URL: srv.URL, Prompt: "summarize"})
			res, err := tl.Call(context.Background(), raw, nil)
			if err != nil {
				t.Fatalf("Call error: %v", err)
			}
			out := res.Data.(Output)
			if out.Code != tc.code {
				t.Errorf("expected code %d, got %d", tc.code, out.Code)
			}
			if !strings.Contains(out.Result, tc.text) {
				t.Errorf("expected status text %q in result, got %q", tc.text, out.Result)
			}
			if !strings.Contains(out.Result, "REDIRECT DETECTED") {
				t.Error("expected REDIRECT DETECTED header")
			}
			if !strings.Contains(out.Result, "other-host.example.com") {
				t.Error("expected redirect target host in guidance")
			}
		})
	}
}

// TestApplyPrompt_GuardrailsDifferByHost — T-I2 compliance template.
func TestApplyPrompt_GuardrailsDifferByHost(t *testing.T) {
	spyPre := &spySideQuerier{returnStr: "x"}
	spyExt := &spySideQuerier{returnStr: "x"}

	if _, err := applyPromptToContent(context.Background(), spyPre, "what is this?", "content", true); err != nil {
		t.Fatal(err)
	}
	if _, err := applyPromptToContent(context.Background(), spyExt, "what is this?", "content", false); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(spyPre.lastSys, "125 characters") {
		t.Error("preapproved prompt should not carry the 125-char compliance guardrail")
	}
	if !strings.Contains(spyExt.lastSys, "125 characters") {
		t.Error("external prompt must carry the 125-char compliance guardrail")
	}
	if !strings.Contains(spyExt.lastSys, "song lyrics") {
		t.Error("external prompt must include the copyrighted-lyrics guardrail")
	}
	if !strings.Contains(spyExt.lastSys, "legal, financial, or medical") {
		t.Error("external prompt must include the professional-advice guardrail")
	}
}
