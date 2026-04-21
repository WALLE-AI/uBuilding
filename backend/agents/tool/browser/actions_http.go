package browser

import (
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"time"

	"github.com/go-rod/rod/lib/proto"
)

func (t *BrowserTool) doCookiesToHTTP(in *Input) string {
	s, page, err := t.getSessionAndPage(in)
	if err != nil {
		return errStr(err)
	}
	err = s.syncCookiesToHTTP(page)
	if err != nil {
		return fmt.Sprintf("cookies_to_http failed: %v", err)
	}
	cookies, _ := page.Cookies(nil)
	return fmt.Sprintf("Synced %d cookie(s) from browser to HTTP client.", len(cookies))
}

func (t *BrowserTool) doHTTPGet(in *Input) string {
	s, _, err := t.getSessionAndPage(in)
	if err != nil {
		return errStr(err)
	}
	if in.URL == "" {
		return "Error: url is required"
	}
	// Route through TLS client when available (Phase 3)
	if s.tlsClient != nil {
		return doTLSRequest(s, "GET", in.URL, "", in.HTTPHeaders)
	}
	return doHTTPRequest(s, "GET", in.URL, "", in.HTTPHeaders)
}

func (t *BrowserTool) doHTTPPost(in *Input) string {
	s, _, err := t.getSessionAndPage(in)
	if err != nil {
		return errStr(err)
	}
	if in.URL == "" {
		return "Error: url is required"
	}
	// Route through TLS client when available (Phase 3)
	if s.tlsClient != nil {
		return doTLSRequest(s, "POST", in.URL, in.HTTPBody, in.HTTPHeaders)
	}
	return doHTTPRequest(s, "POST", in.URL, in.HTTPBody, in.HTTPHeaders)
}

func (t *BrowserTool) doHTTPClose(in *Input) string {
	s, _, err := t.getSessionAndPage(in)
	if err != nil {
		return errStr(err)
	}
	s.mu.Lock()
	if s.httpClient != nil {
		s.httpClient.CloseIdleConnections()
	}
	jar, _ := cookiejar.New(nil)
	s.httpClient = &http.Client{Jar: jar, Timeout: 30 * time.Second}
	s.mu.Unlock()
	return "HTTP client reset. Cookies cleared."
}

func (t *BrowserTool) doHTTPToBrowserCookies(in *Input) string {
	s, page, err := t.getSessionAndPage(in)
	if err != nil {
		return errStr(err)
	}
	jar := s.httpClient.Jar
	if jar == nil {
		return "Error: HTTP client has no cookie jar."
	}

	// Get current page URL to extract cookies from jar
	info := safeInfo(page)
	u, parseErr := url.Parse(info.URL)
	if parseErr != nil {
		return fmt.Sprintf("Error parsing current URL: %v", parseErr)
	}

	httpCookies := jar.Cookies(u)
	if len(httpCookies) == 0 {
		return "No HTTP cookies to transfer for current domain."
	}

	var cookieParams []*proto.NetworkCookieParam
	for _, c := range httpCookies {
		cookieParams = append(cookieParams, &proto.NetworkCookieParam{
			Name:   c.Name,
			Value:  c.Value,
			Domain: u.Hostname(),
			Path:   "/",
		})
	}

	err = page.SetCookies(cookieParams)
	if err != nil {
		return fmt.Sprintf("http_to_browser_cookies failed: %v", err)
	}
	return fmt.Sprintf("Transferred %d cookie(s) from HTTP client to browser.", len(cookieParams))
}

// doHTTPRequest performs an HTTP request using the session's http.Client.
func doHTTPRequest(s *BrowserSession, method, rawURL, body string, headers map[string]string) string {
	s.mu.RLock()
	client := s.httpClient
	fp := s.fingerprint
	rotator := s.proxyRotator
	extraHeaders := make(map[string]string)
	for k, v := range s.extraHeaders {
		extraHeaders[k] = v
	}
	s.mu.RUnlock()

	// Apply proxy rotation if available
	if rotator != nil {
		proxy := rotator.GetProxy()
		proxyURL, _ := url.Parse(proxy.ProxyURL())
		if proxyURL != nil {
			if t, ok := client.Transport.(*http.Transport); ok {
				t.Proxy = http.ProxyURL(proxyURL)
			}
		}
	}

	var bodyReader io.Reader
	if body != "" {
		bodyReader = strings.NewReader(body)
	}

	req, err := http.NewRequest(method, rawURL, bodyReader)
	if err != nil {
		return fmt.Sprintf("HTTP request creation failed: %v", err)
	}

	// Apply fingerprint headers first (baseline identity)
	if fp != nil {
		fp.ApplyToRequest(req)
	}
	// Apply session extra headers (override fingerprint if set)
	for k, v := range extraHeaders {
		req.Header.Set(k, v)
	}
	// Apply per-request headers (highest priority)
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	if s.realUA != "" && req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", s.realUA)
	}

	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Sprintf("HTTP %s %s failed: %v", method, rawURL, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 100_000))
	if err != nil {
		return fmt.Sprintf("HTTP %s response read error: %v", method, err)
	}

	duration := time.Since(start).Milliseconds()

	var respHeaders []string
	for k := range resp.Header {
		respHeaders = append(respHeaders, fmt.Sprintf("  %s: %s", k, resp.Header.Get(k)))
	}

	result := fmt.Sprintf("HTTP %s %s\n  Status: %d %s\n  Duration: %dms\n  Headers:\n%s\n  Body (%d bytes):\n%s",
		method, rawURL,
		resp.StatusCode, resp.Status,
		duration,
		strings.Join(respHeaders, "\n"),
		len(respBody),
		truncStr(string(respBody), 5000))

	return result
}
