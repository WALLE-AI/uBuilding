package browser

import (
	"fmt"
	"math/rand"
	"strings"
	"time"

	"github.com/go-rod/rod/lib/proto"
)

// Google challenge type constants.
const (
	GoogleChallengeNone          = "none"
	GoogleChallengeSorryPage     = "sorry_page"
	GoogleChallengeRecaptcha     = "recaptcha"
	GoogleChallengeConsent       = "consent"
	GoogleChallengeLoginRequired = "login_required"
)

// Google CAPTCHA / sorry page detection patterns.
var (
	googleSorryURLPatterns = []string{
		"sorry.google.com",
		"/sorry/",
		"google.com/sorry",
		"ipv4.google.com/sorry",
	}
	googleRecaptchaSelectors = []string{
		"iframe[src*='recaptcha']",
		"iframe[src*='google.com/recaptcha']",
		".g-recaptcha",
		"#recaptcha",
		"#captcha-form",
	}
	googleSorryTitlePatterns = []string{
		"unusual traffic",
		"our systems have detected",
		"sorry...",
		"before you continue",
		"are you a robot",
	}
	googleConsentSelectors = []string{
		"#consent-bump",
		"form[action*='consent.google']",
		"div[data-consent-dialog]",
	}
	googleConsentURLPatterns = []string{
		"consent.google.com",
		"consent.youtube.com",
		"myaccount.google.com/signinoptions",
	}
	googleConsentAcceptSelectors = []string{
		"button#L2AGLb",                  // Google "Accept all" button ID
		"button[jsname='b3VHJd']",        // Google consent accept button
		"button[aria-label*='Accept']",   // Accessibility label
		"button[aria-label*='accept']",   // Lowercase variant
		"button[data-id='EGO1Oe']",       // Alternative consent button
		"form[action*='consent'] button", // Generic consent form button
		"div.dbsFrd button",              // Google consent container
		"button[jsname='higCR']",         // "Reject all" fallback (not used first)
	}
	googleLoginSelectors = []string{
		"input[type='email'][name='identifier']",
		"#identifierId",
		"div[data-identifier]",
		"a[href*='accounts.google.com/ServiceLogin']",
	}
)

// detectGoogleCaptcha checks the current page for Google CAPTCHA/consent/login states.
// Returns the challenge type string.
// pageEvaler is the subset of rod.Page needed for JS evaluation.
type pageEvaler interface {
	Eval(string, ...interface{}) (*proto.RuntimeRemoteObject, error)
}

func detectGoogleCaptcha(page pageEvaler, info *proto.TargetTargetInfo) string {
	currentURL := ""
	title := ""
	if info != nil {
		currentURL = info.URL
		title = strings.ToLower(info.Title)
	}

	// 1. URL-based detection: sorry page
	for _, pat := range googleSorryURLPatterns {
		if strings.Contains(currentURL, pat) {
			return GoogleChallengeSorryPage
		}
	}

	// 2. URL-based detection: consent page
	for _, pat := range googleConsentURLPatterns {
		if strings.Contains(currentURL, pat) {
			return GoogleChallengeConsent
		}
	}

	// 3. Title-based detection: sorry/captcha
	for _, pat := range googleSorryTitlePatterns {
		if strings.Contains(title, pat) {
			return GoogleChallengeSorryPage
		}
	}

	// 4. DOM-based detection: reCAPTCHA iframe/element
	for _, sel := range googleRecaptchaSelectors {
		js := fmt.Sprintf(`() => { try { return !!document.querySelector(%q); } catch(e) { return false; } }`, sel)
		res, err := page.Eval(js)
		if err == nil && res != nil && res.Value.Bool() {
			return GoogleChallengeRecaptcha
		}
	}

	// 5. DOM-based detection: consent overlay
	for _, sel := range googleConsentSelectors {
		js := fmt.Sprintf(`() => { try { return !!document.querySelector(%q); } catch(e) { return false; } }`, sel)
		res, err := page.Eval(js)
		if err == nil && res != nil && res.Value.Bool() {
			return GoogleChallengeConsent
		}
	}

	// 6. DOM-based detection: login required
	for _, sel := range googleLoginSelectors {
		js := fmt.Sprintf(`() => { try { return !!document.querySelector(%q); } catch(e) { return false; } }`, sel)
		res, err := page.Eval(js)
		if err == nil && res != nil && res.Value.Bool() {
			// Only flag login_required if URL is accounts.google.com
			if strings.Contains(currentURL, "accounts.google.com") {
				return GoogleChallengeLoginRequired
			}
		}
	}

	return GoogleChallengeNone
}

// doDetectGoogleCaptcha detects the current Google challenge state.
func (t *BrowserTool) doDetectGoogleCaptcha(in *Input) string {
	_, page, err := t.getSessionAndPage(in)
	if err != nil {
		return errStr(err)
	}
	info := safeInfo(page)
	challengeType := detectGoogleCaptcha(page, info)

	return fmt.Sprintf("Google challenge detection result: %s\n  URL: %s\n  Title: %s",
		challengeType, info.URL, info.Title)
}

// doWaitGoogleChallenge waits for a Google CAPTCHA/sorry page to be resolved.
// Three-phase approach: detect → handle → verify (mirrors CF bypass architecture).
func (t *BrowserTool) doWaitGoogleChallenge(in *Input) string {
	s, page, err := t.getSessionAndPage(in)
	if err != nil {
		return errStr(err)
	}

	timeout := 60 * time.Second
	if in.GoogleChallengeTimeout > 0 {
		timeout = time.Duration(in.GoogleChallengeTimeout) * time.Millisecond
	}

	deadline := time.Now().Add(timeout)

	// Phase 1: Detect challenge type
	info := safeInfo(page)
	challengeType := detectGoogleCaptcha(page, info)

	if challengeType == GoogleChallengeNone {
		return fmt.Sprintf("No Google challenge detected. Page is accessible.\n  URL: %s\n  Title: %s",
			info.URL, info.Title)
	}

	// Phase 2: Attempt automatic handling based on type
	switch challengeType {
	case GoogleChallengeConsent:
		result := tryClickGoogleConsent(page)
		if result != "" {
			// Wait briefly for navigation after consent click
			time.Sleep(time.Duration(1500+rand.Intn(1000)) * time.Millisecond)
			info = safeInfo(page)
			return fmt.Sprintf("Google consent handled automatically.\n  Action: %s\n  URL: %s\n  Title: %s",
				result, info.URL, info.Title)
		}

	case GoogleChallengeSorryPage:
		// Sorry page — try waiting, then suggest mitigation
		// Attempt: wait for a manual solve or auto-retry with delay
		for time.Now().Before(deadline) {
			time.Sleep(time.Duration(2000+rand.Intn(1000)) * time.Millisecond)
			info = safeInfo(page)
			newType := detectGoogleCaptcha(page, info)
			if newType == GoogleChallengeNone {
				return fmt.Sprintf("Google sorry page resolved!\n  URL: %s\n  Title: %s\n  Elapsed: %v",
					info.URL, info.Title, time.Since(deadline.Add(-timeout)))
			}
		}
		// Timed out — provide actionable guidance
		return fmt.Sprintf("Google sorry page NOT resolved within %v.\n"+
			"  URL: %s\n  Title: %s\n"+
			"  Challenge type: %s\n\n"+
			"Recommendations:\n"+
			"  1. Use create_session(headless=false, user_data_dir=\"...\") with a real Chrome profile\n"+
			"  2. Use a proxy: create_session(proxy=\"http://...\")\n"+
			"  3. Reduce request frequency to Google\n"+
			"  4. Manually solve the CAPTCHA in the browser window (if headless=false)",
			timeout, info.URL, info.Title, challengeType)

	case GoogleChallengeRecaptcha:
		// reCAPTCHA — screenshot + wait for manual solve
		if !s.Headless {
			// Non-headless: wait for user to solve
			for time.Now().Before(deadline) {
				time.Sleep(2 * time.Second)
				info = safeInfo(page)
				newType := detectGoogleCaptcha(page, info)
				if newType == GoogleChallengeNone {
					return fmt.Sprintf("Google reCAPTCHA solved!\n  URL: %s\n  Title: %s",
						info.URL, info.Title)
				}
			}
		}
		return fmt.Sprintf("Google reCAPTCHA detected but NOT solved within %v.\n"+
			"  URL: %s\n"+
			"  Challenge type: %s\n\n"+
			"Recommendations:\n"+
			"  1. Use headless=false so you can manually solve the CAPTCHA\n"+
			"  2. Use user_data_dir with an already-authenticated Chrome profile\n"+
			"  3. Inject Google cookies via inject_google_cookies action",
			timeout, info.URL, challengeType)

	case GoogleChallengeLoginRequired:
		return fmt.Sprintf("Google login required.\n"+
			"  URL: %s\n\n"+
			"Recommendations:\n"+
			"  1. Use create_session(user_data_dir=\"path/to/chrome/profile\") with logged-in profile\n"+
			"  2. Inject Google auth cookies: inject_google_cookies(cookie_string=\"SID=...; HSID=...\")\n"+
			"  3. Use cdp_url to connect to a Chrome instance where you're already logged in",
			info.URL)
	}

	// Fallback: poll until resolved or timeout
	for time.Now().Before(deadline) {
		time.Sleep(time.Duration(1500+rand.Intn(1000)) * time.Millisecond)
		info = safeInfo(page)
		newType := detectGoogleCaptcha(page, info)
		if newType == GoogleChallengeNone {
			return fmt.Sprintf("Google challenge resolved!\n  URL: %s\n  Title: %s",
				info.URL, info.Title)
		}
	}

	info = safeInfo(page)
	return fmt.Sprintf("Google challenge timed out after %v.\n  URL: %s\n  Title: %s\n  Challenge type: %s",
		timeout, info.URL, info.Title, challengeType)
}

// doHandleGoogleConsent specifically handles Google consent/GDPR pages.
func (t *BrowserTool) doHandleGoogleConsent(in *Input) string {
	_, page, err := t.getSessionAndPage(in)
	if err != nil {
		return errStr(err)
	}

	info := safeInfo(page)

	// Check if we're on a consent page
	challengeType := detectGoogleCaptcha(page, info)
	if challengeType != GoogleChallengeConsent {
		return fmt.Sprintf("Not on a Google consent page (detected: %s).\n  URL: %s", challengeType, info.URL)
	}

	result := tryClickGoogleConsent(page)
	if result == "" {
		return fmt.Sprintf("Google consent page detected but no accept button found.\n  URL: %s\n  Title: %s\n"+
			"Try using smart_click with one of these selectors:\n"+
			"  - button#L2AGLb\n"+
			"  - text=Accept all\n"+
			"  - text=I agree",
			info.URL, info.Title)
	}

	// Wait for navigation after consent
	time.Sleep(time.Duration(1500+rand.Intn(1000)) * time.Millisecond)
	info = safeInfo(page)

	// Verify consent was accepted
	newType := detectGoogleCaptcha(page, info)
	if newType == GoogleChallengeConsent {
		return fmt.Sprintf("Consent button clicked (%s) but still on consent page.\n  URL: %s",
			result, info.URL)
	}

	return fmt.Sprintf("Google consent accepted successfully.\n  Button: %s\n  URL: %s\n  Title: %s",
		result, info.URL, info.Title)
}

// tryClickGoogleConsent attempts to click various Google consent accept buttons.
// Returns the selector that was clicked, or empty string if none found.
func tryClickGoogleConsent(page pageEvaler) string {
	for _, sel := range googleConsentAcceptSelectors {
		js := fmt.Sprintf(`() => {
			try {
				var btn = document.querySelector(%q);
				if (btn && btn.offsetParent !== null) {
					btn.click();
					return true;
				}
				return false;
			} catch(e) { return false; }
		}`, sel)
		res, err := page.Eval(js)
		if err == nil && res != nil && res.Value.Bool() {
			return sel
		}
	}

	// Fallback: try text-based matching
	textPatterns := []string{"Accept all", "I agree", "Alle akzeptieren", "Tout accepter", "Aceptar todo"}
	for _, text := range textPatterns {
		js := fmt.Sprintf(`() => {
			try {
				var buttons = document.querySelectorAll('button');
				for (var i = 0; i < buttons.length; i++) {
					if (buttons[i].textContent.trim().indexOf(%q) !== -1 && buttons[i].offsetParent !== null) {
						buttons[i].click();
						return true;
					}
				}
				return false;
			} catch(e) { return false; }
		}`, text)
		res, err := page.Eval(js)
		if err == nil && res != nil && res.Value.Bool() {
			return "text=" + text
		}
	}

	return ""
}

// doInjectGoogleCookies injects Google authentication cookies for the google.com domain.
func (t *BrowserTool) doInjectGoogleCookies(in *Input) string {
	_, page, err := t.getSessionAndPage(in)
	if err != nil {
		return errStr(err)
	}
	if in.CookieString == "" {
		return "Error: cookie_string is required (e.g. \"SID=...; HSID=...; SSID=...; NID=...\")"
	}

	pairs := strings.Split(in.CookieString, ";")
	var cookieParams []*proto.NetworkCookieParam
	googleDomains := []string{".google.com", ".youtube.com", ".googleapis.com"}

	for _, pair := range pairs {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		eqIdx := strings.Index(pair, "=")
		if eqIdx < 0 {
			continue
		}
		name := strings.TrimSpace(pair[:eqIdx])
		value := strings.TrimSpace(pair[eqIdx+1:])

		for _, domain := range googleDomains {
			cookieParams = append(cookieParams, &proto.NetworkCookieParam{
				Name:   name,
				Value:  value,
				Domain: domain,
				Path:   "/",
			})
		}
	}

	if len(cookieParams) == 0 {
		return "No valid cookies parsed from string."
	}

	err = page.SetCookies(cookieParams)
	if err != nil {
		return fmt.Sprintf("inject_google_cookies failed: %v", err)
	}

	return fmt.Sprintf("Injected %d Google cookie(s) across %d domain(s).\n"+
		"Domains: %s\n"+
		"Tip: navigate to Google again to use the injected cookies.",
		len(cookieParams)/len(googleDomains), len(googleDomains),
		strings.Join(googleDomains, ", "))
}
