package browser

// stealthLauncherFlags contains all Chrome launcher flags for anti-detection,
// ported from Scrapling constants.py: STEALTH_ARGS + DEFAULT_ARGS.
// These flags make the browser faster and less detectable.
var stealthLauncherFlags = map[string]string{
	// === DEFAULT_ARGS (performance optimization) ===
	// Source: Scrapling constants.py DEFAULT_ARGS
	"no-pings":                            "",
	"no-first-run":                        "",
	"disable-infobars":                    "",
	"disable-breakpad":                    "",
	"no-service-autorun":                  "",
	"homepage":                            "about:blank",
	"password-store":                      "basic",
	"disable-hang-monitor":                "",
	"no-default-browser-check":            "",
	"disable-session-crashed-bubble":      "",
	"disable-search-engine-choice-screen": "",

	// === STEALTH_ARGS (anti-detection core) ===
	// Source: Scrapling constants.py STEALTH_ARGS
	// Reference: https://peter.sh/experiments/chromium-command-line-switches/
	"test-type":                                           "",
	"lang":                                                "en-US",
	"mute-audio":                                          "",
	"disable-sync":                                        "",
	"hide-scrollbars":                                     "",
	"disable-logging":                                     "",
	"start-maximized":                                     "", // headless check bypass
	"enable-async-dns":                                    "",
	"accept-lang":                                         "en-US",
	"use-mock-keychain":                                   "",
	"disable-translate":                                   "",
	"disable-voice-input":                                 "",
	"window-position":                                     "0,0",
	"disable-wake-on-wifi":                                "",
	"ignore-gpu-blocklist":                                "",
	"enable-tcp-fast-open":                                "",
	"enable-web-bluetooth":                                "",
	"disable-cloud-import":                                "",
	"disable-print-preview":                               "",
	"disable-dev-shm-usage":                               "",
	"metrics-recording-only":                              "",
	"disable-crash-reporter":                              "",
	"disable-partial-raster":                              "",
	"disable-gesture-typing":                              "",
	"disable-checker-imaging":                             "",
	"disable-prompt-on-repost":                            "",
	"force-color-profile":                                 "srgb",
	"font-render-hinting":                                 "none",
	"aggressive-cache-discard":                            "",
	"disable-cookie-encryption":                           "",
	"disable-domain-reliability":                          "",
	"disable-threaded-animation":                          "",
	"disable-threaded-scrolling":                          "",
	"enable-simple-cache-backend":                         "",
	"disable-background-networking":                       "",
	"enable-surface-synchronization":                      "",
	"disable-image-animation-resync":                      "",
	"disable-renderer-backgrounding":                      "",
	"disable-ipc-flooding-protection":                     "",
	"prerender-from-omnibox":                              "disabled",
	"safebrowsing-disable-auto-update":                    "",
	"disable-offer-upload-credit-cards":                   "",
	"disable-background-timer-throttling":                 "",
	"disable-new-content-rendering-timeout":               "",
	"run-all-compositor-stages-before-draw":               "",
	"disable-client-side-phishing-detection":              "",
	"disable-backgrounding-occluded-windows":              "",
	"disable-layer-tree-host-memory-pressure":             "",
	"autoplay-policy":                                     "user-gesture-required",
	"disable-offer-store-unmasked-wallet-cards":           "",
	"disable-blink-features":                              "AutomationControlled",
	"disable-component-extensions-with-background-pages":  "",
	"enable-features":                                     "NetworkService,NetworkServiceInProcess,TrustTokens,TrustTokensAlwaysAllowIssuance",
	"blink-settings":                                      "primaryHoverType=2,availableHoverTypes=2,primaryPointerType=4,availablePointerTypes=4",
	"disable-features":                                    "AudioServiceOutOfProcess,TranslateUI,BlinkGenPropertyTrees,IsolateOrigins,site-per-process",
}

// harmfulFlags lists flags that should be removed from the launcher to avoid detection.
// Source: Scrapling constants.py HARMFUL_ARGS
var harmfulFlags = []string{
	"enable-automation",
	"disable-popup-blocking",
	"disable-component-update",
	"disable-default-apps",
	"disable-extensions",
}

// ConditionalStealthFlags returns additional launcher flags based on optional stealth settings.
// Source: Scrapling _base.py StealthySessionMixin.__generate_stealth_options
func ConditionalStealthFlags(blockWebRTC, hideCanvas, disableWebGL bool) map[string]string {
	flags := make(map[string]string)
	if blockWebRTC {
		flags["webrtc-ip-handling-policy"] = "disable_non_proxied_udp"
		flags["force-webrtc-ip-handling-policy"] = ""
	}
	if hideCanvas {
		flags["fingerprinting-canvas-image-data-noise"] = ""
	}
	if disableWebGL {
		flags["disable-webgl"] = ""
		flags["disable-webgl-image-chromium"] = ""
		flags["disable-webgl2"] = ""
	}
	return flags
}
