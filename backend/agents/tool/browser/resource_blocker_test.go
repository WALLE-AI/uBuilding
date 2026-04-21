package browser

import (
	"testing"
)

func TestNewResourceBlockerNil(t *testing.T) {
	rb := NewResourceBlocker(false, nil, false)
	if rb == nil {
		t.Fatal("should not return nil")
	}
	if rb.ShouldBlock("image", "https://example.com/img.png") {
		t.Error("should not block when all options are off")
	}
}

func TestBlockByResourceType(t *testing.T) {
	rb := NewResourceBlocker(true, nil, false)
	tests := []struct {
		resType string
		want    bool
	}{
		{"image", true},
		{"font", true},
		{"stylesheet", true},
		{"media", true},
		{"document", false},
		{"script", false},
		{"xhr", false},
	}
	for _, tt := range tests {
		got := rb.ShouldBlock(tt.resType, "https://example.com/x")
		if got != tt.want {
			t.Errorf("ShouldBlock(%q) = %v, want %v", tt.resType, got, tt.want)
		}
	}
}

func TestBlockByCustomDomain(t *testing.T) {
	rb := NewResourceBlocker(false, []string{"ads.example.com", "tracker.net"}, false)

	tests := []struct {
		url  string
		want bool
	}{
		{"https://ads.example.com/pixel.js", true},
		{"https://sub.ads.example.com/x", true},
		{"https://tracker.net/t.gif", true},
		{"https://safe.example.com/page", false},
		{"https://example.com/page", false},
	}
	for _, tt := range tests {
		got := rb.ShouldBlock("script", tt.url)
		if got != tt.want {
			t.Errorf("ShouldBlock(script, %q) = %v, want %v", tt.url, got, tt.want)
		}
	}
}

func TestBlockByAdDomains(t *testing.T) {
	rb := NewResourceBlocker(false, nil, true)

	tests := []struct {
		url  string
		want bool
	}{
		{"https://doubleclick.net/ad.js", true},
		{"https://cdn.doubleclick.net/ad.js", true},
		{"https://googlesyndication.com/x", true},
		{"https://hotjar.com/tracking.js", true},
		{"https://example.com/page", false},
		{"https://github.com/repo", false},
	}
	for _, tt := range tests {
		got := rb.ShouldBlock("script", tt.url)
		if got != tt.want {
			t.Errorf("ShouldBlock(script, %q) = %v, want %v", tt.url, got, tt.want)
		}
	}
}

func TestBlockCombined(t *testing.T) {
	rb := NewResourceBlocker(true, []string{"evil.com"}, true)

	// Resource type block
	if !rb.ShouldBlock("image", "https://safe.com/img.png") {
		t.Error("should block image resource type")
	}
	// Custom domain block
	if !rb.ShouldBlock("script", "https://evil.com/track.js") {
		t.Error("should block custom domain")
	}
	// Ad domain block
	if !rb.ShouldBlock("script", "https://criteo.com/bid.js") {
		t.Error("should block ad domain")
	}
	// Allow safe request
	if rb.ShouldBlock("document", "https://example.com/page") {
		t.Error("should not block safe document request")
	}
}

func TestNilResourceBlocker(t *testing.T) {
	var rb *ResourceBlocker
	if rb.ShouldBlock("image", "https://example.com/img.png") {
		t.Error("nil blocker should never block")
	}
}

func TestExtractDomain(t *testing.T) {
	tests := []struct {
		url  string
		want string
	}{
		{"https://example.com/path", "example.com"},
		{"http://sub.example.com:8080/path", "sub.example.com"},
		{"invalid-url", ""},
		{"ftp://files.example.com/a.zip", "files.example.com"},
	}
	for _, tt := range tests {
		got := extractDomain(tt.url)
		if got != tt.want {
			t.Errorf("extractDomain(%q) = %q, want %q", tt.url, got, tt.want)
		}
	}
}

func TestIsDomainBlocked(t *testing.T) {
	blocked := map[string]bool{
		"example.com": true,
		"tracker.net": true,
	}
	tests := []struct {
		domain string
		want   bool
	}{
		{"example.com", true},
		{"sub.example.com", true},
		{"deep.sub.example.com", true},
		{"notexample.com", false},
		{"tracker.net", true},
		{"safe.org", false},
	}
	for _, tt := range tests {
		got := isDomainBlocked(tt.domain, blocked)
		if got != tt.want {
			t.Errorf("isDomainBlocked(%q) = %v, want %v", tt.domain, got, tt.want)
		}
	}
}
