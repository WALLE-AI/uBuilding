package browser

import (
	"net/http"
	"strings"
	"testing"
)

func TestGenerateFingerprintChrome(t *testing.T) {
	fp := GenerateFingerprint("chrome")
	if !strings.Contains(fp.UserAgent, "Chrome/") {
		t.Errorf("Chrome mode UA should contain 'Chrome/', got %q", fp.UserAgent)
	}
	if fp.BrowserName != "chrome" {
		t.Errorf("BrowserName should be 'chrome', got %q", fp.BrowserName)
	}
	if fp.SecChUAMobile != "?0" {
		t.Errorf("SecChUAMobile should be '?0', got %q", fp.SecChUAMobile)
	}
	if fp.Platform == "" {
		t.Error("Platform should not be empty")
	}
	if fp.AcceptLanguage == "" {
		t.Error("AcceptLanguage should not be empty")
	}
}

func TestGenerateFingerprintRandom(t *testing.T) {
	browsers := make(map[string]bool)
	oses := make(map[string]bool)
	for i := 0; i < 100; i++ {
		fp := GenerateFingerprint("")
		browsers[fp.BrowserName] = true
		oses[fp.OSName] = true
		if fp.UserAgent == "" {
			t.Fatal("UserAgent should not be empty")
		}
	}
	// Random mode should produce at least 2 different browsers or OS values over 100 iterations
	if len(browsers) < 2 && len(oses) < 1 {
		t.Errorf("Random mode should produce variety: browsers=%v, oses=%v", browsers, oses)
	}
}

func TestApplyToRequest(t *testing.T) {
	fp := GenerateFingerprint("chrome")
	req, _ := http.NewRequest("GET", "https://example.com", nil)

	fp.ApplyToRequest(req)

	if got := req.Header.Get("User-Agent"); got != fp.UserAgent {
		t.Errorf("User-Agent = %q, want %q", got, fp.UserAgent)
	}
	if got := req.Header.Get("Accept-Language"); got != fp.AcceptLanguage {
		t.Errorf("Accept-Language = %q, want %q", got, fp.AcceptLanguage)
	}
	if got := req.Header.Get("Sec-CH-UA"); got != fp.SecChUA {
		t.Errorf("Sec-CH-UA = %q, want %q", got, fp.SecChUA)
	}

	// Verify existing headers are not overwritten
	req2, _ := http.NewRequest("GET", "https://example.com", nil)
	req2.Header.Set("User-Agent", "CustomUA")
	fp.ApplyToRequest(req2)
	if got := req2.Header.Get("User-Agent"); got != "CustomUA" {
		t.Errorf("ApplyToRequest should not overwrite existing User-Agent, got %q", got)
	}
}

func TestParseSecChUA(t *testing.T) {
	input := `"Chromium";v="136", "Google Chrome";v="136", "Not-A.Brand";v="99"`
	result := parseSecChUA(input)
	if !strings.Contains(result, `"brand":"Chromium"`) {
		t.Errorf("Should contain Chromium brand, got %s", result)
	}
	if !strings.Contains(result, `"version":"136"`) {
		t.Errorf("Should contain version 136, got %s", result)
	}
	if !strings.HasPrefix(result, "[") || !strings.HasSuffix(result, "]") {
		t.Errorf("Should be JSON array, got %s", result)
	}
}

func TestNavigatorOverrideJS(t *testing.T) {
	fp := GenerateFingerprint("chrome")
	js := fp.NavigatorOverrideJS()
	if !strings.Contains(js, "navigator") {
		t.Error("JS should reference navigator")
	}
	if !strings.Contains(js, fp.Platform) {
		t.Errorf("JS should contain platform %q", fp.Platform)
	}
}
