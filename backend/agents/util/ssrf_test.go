package util

import (
	"strings"
	"testing"
)

func TestCheckSSRF_BadScheme(t *testing.T) {
	cases := []string{
		"ftp://example.com/",
		"file:///etc/passwd",
		"javascript:alert(1)",
	}
	for _, u := range cases {
		if err := CheckSSRF(u); err == nil {
			t.Errorf("expected error for %q", u)
		}
	}
}

func TestCheckSSRF_Credentials(t *testing.T) {
	if err := CheckSSRF("http://admin:secret@example.com/"); err == nil {
		t.Fatal("expected error for URL with embedded credentials")
	}
}

func TestCheckSSRF_LoopbackLiteralIPv4(t *testing.T) {
	if err := CheckSSRF("http://127.0.0.1/"); err == nil {
		t.Fatal("expected loopback to be blocked by default")
	}
	if err := CheckSSRFWithOptions("http://127.0.0.1/", CheckSSRFOptions{AllowLoopback: true}); err != nil {
		t.Fatalf("expected loopback allowed with option, got %v", err)
	}
}

func TestCheckSSRF_LoopbackIPv6(t *testing.T) {
	if err := CheckSSRF("http://[::1]/"); err == nil {
		t.Fatal("expected ::1 loopback to be blocked")
	}
}

func TestCheckSSRF_PrivateRFC1918(t *testing.T) {
	for _, u := range []string{
		"http://10.0.0.1/",
		"http://172.16.5.5/",
		"http://192.168.1.1/",
		"http://100.64.0.1/", // CGNAT
	} {
		if err := CheckSSRF(u); err == nil {
			t.Errorf("expected private address %q to be blocked", u)
		}
	}
}

func TestCheckSSRF_MetadataAlwaysBlocked(t *testing.T) {
	err := CheckSSRFWithOptions("http://169.254.169.254/latest/meta-data/", CheckSSRFOptions{AllowLoopback: true})
	if err == nil {
		t.Fatal("expected cloud metadata IP to be blocked even with AllowLoopback")
	}
	if !strings.Contains(err.Error(), "metadata") {
		t.Fatalf("error should mention metadata, got %v", err)
	}
}

func TestCheckSSRF_LinkLocal(t *testing.T) {
	if err := CheckSSRF("http://169.254.1.1/"); err == nil {
		t.Fatal("expected link-local to be blocked")
	}
}

func TestCheckSSRF_MissingHost(t *testing.T) {
	if err := CheckSSRF("http:///foo"); err == nil {
		t.Fatal("expected error for missing host")
	}
}

func TestHostnameFromURL(t *testing.T) {
	if got := HostnameFromURL("https://Example.COM/path"); got != "example.com" {
		t.Errorf("expected lowercased host, got %q", got)
	}
	if got := HostnameFromURL("not a url"); got != "" {
		t.Errorf("expected empty string for bad URL, got %q", got)
	}
}
