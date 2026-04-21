package browser

import (
	"testing"
)

func TestResolveTLSProfile(t *testing.T) {
	tests := []struct {
		input string
		want  TLSProfile
	}{
		{"chrome_136", TLSProfileChrome136},
		{"chrome136", TLSProfileChrome136},
		{"chrome_131", TLSProfileChrome131},
		{"chrome_120", TLSProfileChrome120},
		{"firefox_133", TLSProfileFirefox133},
		{"edge_136", TLSProfileEdge136},
		{"chrome", TLSProfileDefault},
		{"firefox", TLSProfileFirefox133},
		{"edge", TLSProfileEdge136},
		{"", TLSProfileDefault},
		{"CHROME_136", TLSProfileChrome136},
		{"unknown_profile", TLSProfileDefault},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := ResolveTLSProfile(tt.input)
			if got != tt.want {
				t.Errorf("ResolveTLSProfile(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestNewTLSClient(t *testing.T) {
	tc := NewTLSClient(TLSProfileChrome136, nil, 0)
	if tc == nil {
		t.Fatal("NewTLSClient returned nil")
	}
	if tc.Profile() != TLSProfileChrome136 {
		t.Errorf("Profile() = %q, want %q", tc.Profile(), TLSProfileChrome136)
	}
	if tc.client == nil {
		t.Error("internal client should not be nil")
	}
	if tc.client.Jar == nil {
		t.Error("cookie jar should be auto-created when nil")
	}
	tc.CloseIdleConnections()
}

func TestNewTLSClientUnknownProfile(t *testing.T) {
	tc := NewTLSClient("nonexistent", nil, 0)
	if tc == nil {
		t.Fatal("NewTLSClient returned nil for unknown profile")
	}
	if tc.Profile() != TLSProfileDefault {
		t.Errorf("unknown profile should fallback to default, got %q", tc.Profile())
	}
}

func TestTLSProfileMapComplete(t *testing.T) {
	profiles := []TLSProfile{
		TLSProfileChrome136,
		TLSProfileChrome131,
		TLSProfileChrome120,
		TLSProfileFirefox133,
		TLSProfileEdge136,
	}
	for _, p := range profiles {
		if _, ok := tlsProfileMap[p]; !ok {
			t.Errorf("tlsProfileMap missing entry for %q", p)
		}
	}
}
