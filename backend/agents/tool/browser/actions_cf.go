package browser

import (
	"fmt"
	"log/slog"
	"math/rand"
	"strings"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

// CloudflareType enumerates the 4 Cloudflare challenge types.
// Source: Scrapling _stealth.py:_detect_cloudflare
type CloudflareType string

const (
	CFNone           CloudflareType = "none"
	CFNonInteractive CloudflareType = "non-interactive"
	CFManaged        CloudflareType = "managed"
	CFInteractive    CloudflareType = "interactive"
	CFEmbedded       CloudflareType = "embedded"
)

// detectCloudflareType inspects page content/DOM to classify the challenge type.
// Source: Scrapling _stealth.py:_detect_cloudflare
func detectCloudflareType(page *rod.Page) CloudflareType {
	info := safeInfo(page)
	title := strings.ToLower(info.Title)

	// Get page HTML for content-based detection
	htmlRes, _ := page.Eval(`() => document.documentElement.outerHTML`)
	html := ""
	if htmlRes != nil {
		html = htmlRes.Value.Str()
	}

	// Check for Turnstile embedded widget (no interstitial page)
	if strings.Contains(html, "cf_turnstile") || strings.Contains(html, "cf-turnstile") {
		// If the title is NOT "Just a moment", it's an embedded widget
		if !strings.Contains(title, "just a moment") {
			return CFEmbedded
		}
	}

	// Title-based: interstitial page
	if strings.Contains(title, "just a moment") ||
		strings.Contains(title, "attention required") ||
		strings.Contains(title, "checking your browser") {
		// Distinguish non-interactive vs interactive
		if strings.Contains(html, "Verifying you are human") {
			return CFInteractive
		}
		// Check for managed challenge iframe
		res, _ := page.Eval(`() => !!document.querySelector('iframe[src*="challenges.cloudflare.com"]')`)
		if res != nil && res.Value.Bool() {
			return CFManaged
		}
		// Default to non-interactive (auto-solve wait page)
		return CFNonInteractive
	}

	// DOM markers
	res, _ := page.Eval(`() => {
		return !!(document.querySelector('#challenge-running') ||
			document.querySelector('#challenge-form') ||
			document.querySelector('.cf-browser-verification') ||
			document.querySelector('#cf-challenge-running'));
	}`)
	if res != nil && res.Value.Bool() {
		return CFManaged
	}

	return CFNone
}

// doWaitCFChallenge waits for Cloudflare challenge to complete.
// Upgraded to detect 4 challenge types and apply type-specific solving.
// Source: Scrapling _stealth.py:_cloudflare_solver
func (t *BrowserTool) doWaitCFChallenge(in *Input) string {
	_, page, err := t.getSessionAndPage(in)
	if err != nil {
		return errStr(err)
	}

	timeout := 30 * time.Second
	if in.CFChallengeTimeout > 0 {
		timeout = time.Duration(in.CFChallengeTimeout) * time.Millisecond
	}

	deadline := time.Now().Add(timeout)

	// Wait briefly for network idle before detecting
	time.Sleep(1 * time.Second)

	// Detect challenge type
	cType := detectCloudflareType(page)
	if cType == CFNone {
		return "No Cloudflare challenge detected. Page may already be accessible."
	}
	slog.Info("browser: CF challenge detected", slog.String("type", string(cType)))

	switch cType {
	case CFNonInteractive:
		return solveCFNonInteractive(page, deadline)
	case CFManaged, CFInteractive:
		return solveCFInteractive(page, cType, deadline, in.CFScreenshotOnFail)
	case CFEmbedded:
		return solveCFEmbedded(page, deadline, in.CFScreenshotOnFail)
	}

	return fmt.Sprintf("Cloudflare challenge timed out after %v.", timeout)
}

// solveCFNonInteractive waits for the auto-solving "Just a moment" page to disappear.
// Source: Scrapling _cloudflare_solver non-interactive branch
func solveCFNonInteractive(page *rod.Page, deadline time.Time) string {
	for time.Now().Before(deadline) {
		title := strings.ToLower(safeInfo(page).Title)
		if !strings.Contains(title, "just a moment") {
			info := safeInfo(page)
			return fmt.Sprintf("Cloudflare non-interactive challenge passed!\n  URL: %s\n  Title: %s", info.URL, info.Title)
		}
		slog.Info("browser: waiting for CF non-interactive to clear")
		time.Sleep(1 * time.Second)
	}
	return "Cloudflare non-interactive challenge timed out."
}

// solveCFInteractive handles managed and interactive challenges by clicking the Turnstile iframe.
// Source: Scrapling _cloudflare_solver managed/interactive branch
func solveCFInteractive(page *rod.Page, cType CloudflareType, deadline time.Time, screenshotOnFail bool) string {
	// Wait for "Verifying you are human" to clear if interactive
	if cType == CFInteractive {
		for time.Now().Before(deadline) {
			res, _ := page.Eval(`() => document.documentElement.outerHTML`)
			if res != nil && !strings.Contains(res.Value.Str(), "Verifying you are human") {
				break
			}
			time.Sleep(500 * time.Millisecond)
		}
	}

	// Try to find and click the challenge iframe
	clickCFChallengeIframe(page)

	// Wait for title to change (up to 10s inner loop, aligned with Scrapling)
	attempts := 0
	for time.Now().Before(deadline) {
		title := strings.ToLower(safeInfo(page).Title)
		if !strings.Contains(title, "just a moment") {
			info := safeInfo(page)
			return fmt.Sprintf("Cloudflare %s challenge passed!\n  URL: %s\n  Title: %s", cType, info.URL, info.Title)
		}
		attempts++
		if attempts >= 100 {
			slog.Info("browser: CF page didn't clear after 10s, retrying click")
			clickCFChallengeIframe(page)
			attempts = 0
		}
		time.Sleep(100 * time.Millisecond)
	}

	if screenshotOnFail {
		_, _ = page.Screenshot(false, nil)
	}
	return fmt.Sprintf("Cloudflare %s challenge timed out.", cType)
}

// solveCFEmbedded handles Turnstile widgets embedded in the page (not interstitial).
func solveCFEmbedded(page *rod.Page, deadline time.Time, screenshotOnFail bool) string {
	// Click the embedded Turnstile widget
	clickCFChallengeIframe(page)

	for time.Now().Before(deadline) {
		// Check if Turnstile widget is resolved (iframe disappears or changes)
		res, _ := page.Eval(`() => {
			let w = document.querySelector('#cf_turnstile, #cf-turnstile, .turnstile');
			if (!w) return true;
			let resp = w.querySelector('input[name="cf-turnstile-response"]');
			return resp && resp.value && resp.value.length > 0;
		}`)
		if res != nil && res.Value.Bool() {
			return "Cloudflare embedded Turnstile solved!"
		}
		time.Sleep(1 * time.Second)
	}

	if screenshotOnFail {
		_, _ = page.Screenshot(false, nil)
	}
	return "Cloudflare embedded Turnstile timed out."
}

// clickCFChallengeIframe finds the Cloudflare challenge iframe and clicks it with randomized coordinates.
// Source: Scrapling _cloudflare_solver iframe click logic with randint offsets.
func clickCFChallengeIframe(page *rod.Page) {
	// Try iframe-based click first (crosses iframe boundary via CDP)
	res, err := page.Eval(`() => {
		let iframes = document.querySelectorAll('iframe[src*="challenges.cloudflare.com"]');
		if (iframes.length === 0) {
			// Fallback: try Turnstile container divs
			let divs = document.querySelectorAll('#cf_turnstile div, #cf-turnstile div, .turnstile>div>div, .main-content p+div>div>div');
			if (divs.length === 0) return null;
			let rect = divs[divs.length - 1].getBoundingClientRect();
			return { x: rect.x, y: rect.y };
		}
		let rect = iframes[0].getBoundingClientRect();
		return { x: rect.x, y: rect.y };
	}`)
	if err != nil || res == nil || res.Value.Nil() {
		// Fallback to original clickTurnstileCheckbox
		_ = clickTurnstileCheckbox(page)
		return
	}

	// Randomized click offset (aligned with Scrapling randint(26,28), randint(25,27))
	x := res.Value.Get("x").Num() + float64(26+rand.Intn(3))
	y := res.Value.Get("y").Num() + float64(25+rand.Intn(3))
	delay := 100 + rand.Intn(101) // 100-200ms delay

	_ = proto.InputDispatchMouseEvent{
		Type:       proto.InputDispatchMouseEventTypeMousePressed,
		X:          x,
		Y:          y,
		Button:     proto.InputMouseButtonLeft,
		ClickCount: 1,
	}.Call(page)
	time.Sleep(time.Duration(delay) * time.Millisecond)
	_ = proto.InputDispatchMouseEvent{
		Type:       proto.InputDispatchMouseEventTypeMouseReleased,
		X:          x,
		Y:          y,
		Button:     proto.InputMouseButtonLeft,
		ClickCount: 1,
	}.Call(page)
}

func (t *BrowserTool) doExtractCFClearance(in *Input) string {
	_, page, err := t.getSessionAndPage(in)
	if err != nil {
		return errStr(err)
	}
	cookies, err := page.Cookies(nil)
	if err != nil {
		return fmt.Sprintf("extract_cf_clearance failed: %v", err)
	}

	for _, c := range cookies {
		if c.Name == "cf_clearance" {
			return fmt.Sprintf("cf_clearance found.\n  value: %s\n  domain: %s\n  expires: %.0f\n  secure: %v",
				truncStr(c.Value, 80), c.Domain, c.Expires, c.Secure)
		}
	}
	return "cf_clearance cookie not found. Challenge may not have completed."
}

func (t *BrowserTool) doVerifyCFClearance(in *Input) string {
	_, page, err := t.getSessionAndPage(in)
	if err != nil {
		return errStr(err)
	}
	cookies, err := page.Cookies(nil)
	if err != nil {
		return fmt.Sprintf("verify_cf_clearance failed: %v", err)
	}

	for _, c := range cookies {
		if c.Name == "cf_clearance" {
			// Check if not expired
			if c.Expires > 0 && float64(c.Expires) < float64(time.Now().Unix()) {
				return "cf_clearance cookie exists but is expired."
			}
			return fmt.Sprintf("cf_clearance is valid.\n  domain: %s\n  expires: %.0f\n  value: %s",
				c.Domain, c.Expires, truncStr(c.Value, 40))
		}
	}
	return "cf_clearance not found — Cloudflare bypass not active."
}

// clickTurnstileCheckbox attempts to find and click the Cloudflare Turnstile checkbox.
// Uses CDP Input.dispatchMouseEvent to click at absolute coordinates, which can
// penetrate cross-origin iframes (unlike JS MouseEvent dispatch).
func clickTurnstileCheckbox(page *rod.Page) error {
	// Get the bounding rect of the Turnstile iframe via JS
	res, err := page.Eval(`() => {
		let iframes = document.querySelectorAll('iframe[src*="challenges.cloudflare.com"]');
		if (iframes.length === 0) return null;
		let rect = iframes[0].getBoundingClientRect();
		return { x: rect.left + rect.width / 2, y: rect.top + rect.height / 2 };
	}`)
	if err != nil || res == nil || res.Value.Nil() {
		return fmt.Errorf("turnstile iframe not found")
	}

	x := res.Value.Get("x").Num()
	y := res.Value.Get("y").Num()

	// CDP mouse click at absolute page coordinates (crosses iframe boundary)
	_ = proto.InputDispatchMouseEvent{
		Type:       proto.InputDispatchMouseEventTypeMousePressed,
		X:          x,
		Y:          y,
		Button:     proto.InputMouseButtonLeft,
		ClickCount: 1,
	}.Call(page)
	time.Sleep(50 * time.Millisecond)
	_ = proto.InputDispatchMouseEvent{
		Type:       proto.InputDispatchMouseEventTypeMouseReleased,
		X:          x,
		Y:          y,
		Button:     proto.InputMouseButtonLeft,
		ClickCount: 1,
	}.Call(page)

	return nil
}
