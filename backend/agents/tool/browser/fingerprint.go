package browser

import (
	"fmt"
	"math/rand"
	"net/http"
	"runtime"
	"strings"
)

// BrowserFingerprint holds a consistent set of browser identity headers.
// Source concept: Scrapling toolbelt/fingerprints.py (browserforge HeaderGenerator)
type BrowserFingerprint struct {
	UserAgent       string
	AcceptLanguage  string
	AcceptEncoding  string
	Accept          string
	SecChUA         string
	SecChUAMobile   string
	SecChUAPlatform string
	SecFetchSite    string
	SecFetchMode    string
	SecFetchDest    string
	Platform        string // JS navigator.platform
	OSName          string // windows/macos/linux
	BrowserName     string // chrome/edge/firefox
	BrowserVersion  int
}

// browserProfile is a static fingerprint entry in our built-in database.
type browserProfile struct {
	browserName string
	minVersion  int
	maxVersion  int
	uaTemplate  string // %d replaced with version, %s with OS token
	secChUA     string // %d replaced with version
	platform    string // JS navigator.platform
}

// osProfile pairs an OS name with its UA token and Sec-CH-UA-Platform value.
type osProfile struct {
	name        string // windows/macos/linux
	uaToken     string // NT 10.0; Win64; x64 | Macintosh; Intel Mac OS X 10_15_7 | X11; Linux x86_64
	secPlatform string // "Windows" | "macOS" | "Linux"
	jsPlatform  string // Win32 | MacIntel | Linux x86_64
}

var osProfiles = []osProfile{
	{name: "windows", uaToken: "Windows NT 10.0; Win64; x64", secPlatform: `"Windows"`, jsPlatform: "Win32"},
	{name: "macos", uaToken: "Macintosh; Intel Mac OS X 10_15_7", secPlatform: `"macOS"`, jsPlatform: "MacIntel"},
	{name: "linux", uaToken: "X11; Linux x86_64", secPlatform: `"Linux"`, jsPlatform: "Linux x86_64"},
}

var browserProfiles = []browserProfile{
	{
		browserName: "chrome",
		minVersion:  120,
		maxVersion:  136,
		uaTemplate:  "Mozilla/5.0 (%s) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/%d.0.0.0 Safari/537.36",
		secChUA:     `"Chromium";v="%d", "Google Chrome";v="%d", "Not-A.Brand";v="99"`,
		platform:    "",
	},
	{
		browserName: "edge",
		minVersion:  120,
		maxVersion:  136,
		uaTemplate:  "Mozilla/5.0 (%s) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/%d.0.0.0 Safari/537.36 Edg/%d.0.0.0",
		secChUA:     `"Chromium";v="%d", "Microsoft Edge";v="%d", "Not-A.Brand";v="99"`,
		platform:    "",
	},
}

// getLocalOS returns the OS profile matching the current runtime.
func getLocalOS() osProfile {
	switch runtime.GOOS {
	case "windows":
		return osProfiles[0]
	case "darwin":
		return osProfiles[1]
	case "linux":
		return osProfiles[2]
	default:
		return osProfiles[0]
	}
}

// GenerateFingerprint generates a realistic browser fingerprint.
// If browserMode is "chrome", it pins to Chrome matching the local OS (for browser sessions).
// If browserMode is empty, it randomly picks browser and OS (for HTTP-only requests).
// Source: Scrapling fingerprints.py:generate_headers
func GenerateFingerprint(browserMode string) BrowserFingerprint {
	var os osProfile
	var bp browserProfile
	var version int

	if browserMode == "chrome" {
		// Browser mode: match local OS, pin to Chrome
		os = getLocalOS()
		bp = browserProfiles[0] // chrome
		version = bp.maxVersion
	} else {
		// HTTP mode: random OS + random browser
		os = osProfiles[rand.Intn(len(osProfiles))]
		bp = browserProfiles[rand.Intn(len(browserProfiles))]
		version = bp.minVersion + rand.Intn(bp.maxVersion-bp.minVersion+1)
	}

	// Build User-Agent
	var ua string
	if bp.browserName == "edge" {
		ua = fmt.Sprintf(bp.uaTemplate, os.uaToken, version, version)
	} else {
		ua = fmt.Sprintf(bp.uaTemplate, os.uaToken, version)
	}

	// Build Sec-CH-UA
	secChUA := fmt.Sprintf(bp.secChUA, version, version)

	return BrowserFingerprint{
		UserAgent:       ua,
		AcceptLanguage:  "en-US,en;q=0.9",
		AcceptEncoding:  "gzip, deflate, br",
		Accept:          "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8",
		SecChUA:         secChUA,
		SecChUAMobile:   "?0",
		SecChUAPlatform: os.secPlatform,
		SecFetchSite:    "none",
		SecFetchMode:    "navigate",
		SecFetchDest:    "document",
		Platform:        os.jsPlatform,
		OSName:          os.name,
		BrowserName:     bp.browserName,
		BrowserVersion:  version,
	}
}

// ApplyToRequest applies fingerprint headers to an http.Request.
// Existing headers are not overwritten.
func (fp *BrowserFingerprint) ApplyToRequest(req *http.Request) {
	setIfEmpty := func(key, val string) {
		if req.Header.Get(key) == "" && val != "" {
			req.Header.Set(key, val)
		}
	}
	setIfEmpty("User-Agent", fp.UserAgent)
	setIfEmpty("Accept-Language", fp.AcceptLanguage)
	setIfEmpty("Accept-Encoding", fp.AcceptEncoding)
	setIfEmpty("Accept", fp.Accept)
	setIfEmpty("Sec-CH-UA", fp.SecChUA)
	setIfEmpty("Sec-CH-UA-Mobile", fp.SecChUAMobile)
	setIfEmpty("Sec-CH-UA-Platform", fp.SecChUAPlatform)
	setIfEmpty("Sec-Fetch-Site", fp.SecFetchSite)
	setIfEmpty("Sec-Fetch-Mode", fp.SecFetchMode)
	setIfEmpty("Sec-Fetch-Dest", fp.SecFetchDest)
}

// NavigatorOverrideJS returns a JavaScript snippet that overrides navigator properties
// to match this fingerprint. Injected via page.EvalOnNewDocument.
func (fp *BrowserFingerprint) NavigatorOverrideJS() string {
	var sb strings.Builder
	sb.WriteString(`(function(){`)
	sb.WriteString(fmt.Sprintf(`Object.defineProperty(navigator,'platform',{get:()=>%q});`, fp.Platform))
	sb.WriteString(fmt.Sprintf(`Object.defineProperty(navigator,'userAgent',{get:()=>%q});`, fp.UserAgent))
	// Brands array for navigator.userAgentData
	if fp.SecChUA != "" {
		brands := parseSecChUA(fp.SecChUA)
		if len(brands) > 0 {
			sb.WriteString(fmt.Sprintf(`if(navigator.userAgentData){Object.defineProperty(navigator.userAgentData,'brands',{get:()=>%s});`, brands))
			sb.WriteString(`Object.defineProperty(navigator.userAgentData,'mobile',{get:()=>false});`)
			sb.WriteString(`}`)
		}
	}
	sb.WriteString(`})();`)
	return sb.String()
}

// parseSecChUA converts Sec-CH-UA header value into a JS array literal for navigator.userAgentData.brands.
// Input:  `"Chromium";v="136", "Google Chrome";v="136", "Not-A.Brand";v="99"`
// Output: `[{"brand":"Chromium","version":"136"},{"brand":"Google Chrome","version":"136"},{"brand":"Not-A.Brand","version":"99"}]`
func parseSecChUA(secChUA string) string {
	parts := strings.Split(secChUA, ", ")
	var entries []string
	for _, part := range parts {
		// "Chromium";v="136"
		part = strings.TrimSpace(part)
		idx := strings.Index(part, ";v=")
		if idx < 0 {
			continue
		}
		brand := strings.Trim(part[:idx], `"`)
		version := strings.Trim(part[idx+3:], `"`)
		entries = append(entries, fmt.Sprintf(`{"brand":"%s","version":"%s"}`, brand, version))
	}
	if len(entries) == 0 {
		return ""
	}
	return "[" + strings.Join(entries, ",") + "]"
}
