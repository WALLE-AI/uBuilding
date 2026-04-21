package browser

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"time"

	utls "github.com/refraction-networking/utls"
)

// TLSProfile represents a browser TLS fingerprint to impersonate.
// Source concept: Scrapling static.py curl_cffi impersonate parameter.
type TLSProfile string

const (
	TLSProfileChrome136  TLSProfile = "chrome_136"
	TLSProfileChrome131  TLSProfile = "chrome_131"
	TLSProfileChrome120  TLSProfile = "chrome_120"
	TLSProfileFirefox133 TLSProfile = "firefox_133"
	TLSProfileEdge136    TLSProfile = "edge_136"
	TLSProfileDefault    TLSProfile = "chrome_136" // default to latest Chrome
)

// tlsProfileMap maps profile names to utls ClientHelloID.
var tlsProfileMap = map[TLSProfile]*utls.ClientHelloID{
	TLSProfileChrome136:  &utls.HelloChrome_Auto,
	TLSProfileChrome131:  &utls.HelloChrome_Auto,
	TLSProfileChrome120:  &utls.HelloChrome_120,
	TLSProfileFirefox133: &utls.HelloFirefox_Auto,
	TLSProfileEdge136:    &utls.HelloEdge_Auto,
}

// TLSClient wraps an http.Client with TLS fingerprint impersonation.
type TLSClient struct {
	client  *http.Client
	profile TLSProfile
}

// NewTLSClient creates an HTTP client that impersonates the given TLS profile.
// If profile is empty or unknown, falls back to Chrome latest.
// Source: Scrapling static.py _ConfigurationLogic._merge_request_args (impersonate param)
func NewTLSClient(profile TLSProfile, jar http.CookieJar, timeout time.Duration) *TLSClient {
	if profile == "" {
		profile = TLSProfileDefault
	}
	helloID, ok := tlsProfileMap[profile]
	if !ok {
		helloID = &utls.HelloChrome_Auto
		profile = TLSProfileDefault
	}

	if jar == nil {
		jar, _ = cookiejar.New(nil)
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	transport := &http.Transport{
		DialTLSContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return dialTLS(ctx, network, addr, helloID)
		},
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}

	return &TLSClient{
		client: &http.Client{
			Transport: transport,
			Jar:       jar,
			Timeout:   timeout,
		},
		profile: profile,
	}
}

// dialTLS creates a TLS connection with the specified utls ClientHelloID fingerprint.
func dialTLS(ctx context.Context, network, addr string, helloID *utls.ClientHelloID) (net.Conn, error) {
	// Extract host for SNI
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}

	dialer := &net.Dialer{Timeout: 10 * time.Second}
	rawConn, err := dialer.DialContext(ctx, network, addr)
	if err != nil {
		return nil, fmt.Errorf("dial failed: %w", err)
	}

	tlsConfig := &utls.Config{
		ServerName:         host,
		InsecureSkipVerify: false,
	}

	uConn := utls.UClient(rawConn, tlsConfig, *helloID)
	if err := uConn.HandshakeContext(ctx); err != nil {
		rawConn.Close()
		return nil, fmt.Errorf("TLS handshake failed: %w", err)
	}

	return uConn, nil
}

// Do executes an HTTP request using the TLS-impersonating client.
func (tc *TLSClient) Do(req *http.Request) (*http.Response, error) {
	return tc.client.Do(req)
}

// CloseIdleConnections closes idle connections in the underlying transport.
func (tc *TLSClient) CloseIdleConnections() {
	tc.client.CloseIdleConnections()
}

// Profile returns the TLS profile name in use.
func (tc *TLSClient) Profile() TLSProfile {
	return tc.profile
}

// ResolveTLSProfile normalizes a user-supplied profile string to a TLSProfile.
// Supported values: "chrome_136", "chrome_131", "chrome_120", "firefox_133", "edge_136", "chrome", "firefox", "edge".
func ResolveTLSProfile(s string) TLSProfile {
	s = strings.ToLower(strings.TrimSpace(s))
	switch s {
	case "chrome_136", "chrome136":
		return TLSProfileChrome136
	case "chrome_131", "chrome131":
		return TLSProfileChrome131
	case "chrome_120", "chrome120":
		return TLSProfileChrome120
	case "firefox_133", "firefox133":
		return TLSProfileFirefox133
	case "edge_136", "edge136":
		return TLSProfileEdge136
	case "chrome", "":
		return TLSProfileDefault
	case "firefox":
		return TLSProfileFirefox133
	case "edge":
		return TLSProfileEdge136
	default:
		return TLSProfileDefault
	}
}

// doTLSRequest performs an HTTP request using the TLS client with fingerprint headers applied.
func doTLSRequest(s *BrowserSession, method, rawURL, body string, headers map[string]string) string {
	s.mu.RLock()
	tc := s.tlsClient
	fp := s.fingerprint
	extraHeaders := make(map[string]string)
	for k, v := range s.extraHeaders {
		extraHeaders[k] = v
	}
	s.mu.RUnlock()

	if tc == nil {
		return doHTTPRequest(s, method, rawURL, body, headers)
	}

	var bodyReader io.Reader
	if body != "" {
		bodyReader = strings.NewReader(body)
	}

	req, err := http.NewRequest(method, rawURL, bodyReader)
	if err != nil {
		return fmt.Sprintf("TLS request creation failed: %v", err)
	}

	// Apply fingerprint headers first (baseline identity)
	if fp != nil {
		fp.ApplyToRequest(req)
	}
	// Apply session extra headers
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
	resp, err := tc.Do(req)
	if err != nil {
		return fmt.Sprintf("TLS %s %s failed: %v", method, rawURL, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 100_000))
	if err != nil {
		return fmt.Sprintf("TLS %s response read error: %v", method, err)
	}

	duration := time.Since(start).Milliseconds()

	var respHeaders []string
	for k := range resp.Header {
		respHeaders = append(respHeaders, fmt.Sprintf("  %s: %s", k, resp.Header.Get(k)))
	}

	result := fmt.Sprintf("TLS/%s %s %s\n  Status: %d %s\n  Duration: %dms\n  Headers:\n%s\n  Body (%d bytes):\n%s",
		tc.Profile(), method, rawURL,
		resp.StatusCode, resp.Status,
		duration,
		strings.Join(respHeaders, "\n"),
		len(respBody),
		truncStr(string(respBody), 5000))

	return result
}
