package browser

import (
	"errors"
	"testing"
)

func TestNewProxyRotatorEmpty(t *testing.T) {
	_, err := NewProxyRotator(nil, nil)
	if err == nil {
		t.Error("should error on empty proxies")
	}
}

func TestCyclicRotation(t *testing.T) {
	proxies := []ProxyConfig{
		{Server: "http://p1:8080"},
		{Server: "http://p2:8080"},
		{Server: "http://p3:8080"},
	}
	rot, err := NewProxyRotator(proxies, CyclicRotation)
	if err != nil {
		t.Fatal(err)
	}
	if rot.Count() != 3 {
		t.Fatalf("Count() = %d, want 3", rot.Count())
	}

	// Should cycle: p1, p2, p3, p1, p2, ...
	for cycle := 0; cycle < 2; cycle++ {
		for i, want := range proxies {
			got := rot.GetProxy()
			if got.Server != want.Server {
				t.Errorf("cycle %d, index %d: got %q, want %q", cycle, i, got.Server, want.Server)
			}
		}
	}
}

func TestRandomRotation(t *testing.T) {
	proxies := []ProxyConfig{
		{Server: "http://p1:8080"},
		{Server: "http://p2:8080"},
	}
	rot, err := NewProxyRotator(proxies, RandomRotation)
	if err != nil {
		t.Fatal(err)
	}

	// Run many iterations; at least 2 different proxies should appear
	seen := make(map[string]bool)
	for i := 0; i < 50; i++ {
		p := rot.GetProxy()
		seen[p.Server] = true
	}
	if len(seen) < 2 {
		t.Errorf("random rotation should use multiple proxies, saw %d", len(seen))
	}
}

func TestParseProxyString(t *testing.T) {
	tests := []struct {
		input    string
		wantSvr  string
		wantUser string
		wantPass string
		wantErr  bool
	}{
		{"http://host:1234", "http://host:1234", "", "", false},
		{"socks5://user:pass@host:5678", "socks5://host:5678", "user", "pass", false},
		{"host:8080", "http://host:8080", "", "", false},
		{"", "", "", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			cfg, err := ParseProxyString(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Error("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if cfg.Server != tt.wantSvr {
				t.Errorf("Server = %q, want %q", cfg.Server, tt.wantSvr)
			}
			if cfg.Username != tt.wantUser {
				t.Errorf("Username = %q, want %q", cfg.Username, tt.wantUser)
			}
			if cfg.Password != tt.wantPass {
				t.Errorf("Password = %q, want %q", cfg.Password, tt.wantPass)
			}
		})
	}
}

func TestProxyURL(t *testing.T) {
	cfg := ProxyConfig{Server: "http://host:8080", Username: "user", Password: "pass"}
	got := cfg.ProxyURL()
	if got != "http://user:pass@host:8080" {
		t.Errorf("ProxyURL() = %q, want %q", got, "http://user:pass@host:8080")
	}

	cfg2 := ProxyConfig{Server: "http://host:8080"}
	if cfg2.ProxyURL() != "http://host:8080" {
		t.Errorf("ProxyURL() without creds = %q", cfg2.ProxyURL())
	}
}

func TestIsProxyError(t *testing.T) {
	if IsProxyError(nil) {
		t.Error("nil error should not be proxy error")
	}
	if !IsProxyError(errors.New("NET::ERR_PROXY_CONNECTION_FAILED")) {
		t.Error("should detect proxy error")
	}
	if !IsProxyError(errors.New("connection refused")) {
		t.Error("should detect connection refused")
	}
	if IsProxyError(errors.New("page not found")) {
		t.Error("should not detect page not found as proxy error")
	}
}
