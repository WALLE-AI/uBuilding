package browser

import "encoding/json"

// Action constants — all supported browser actions.
const (
	// Session management
	ActionCreateSession = "create_session"
	ActionCloseSession  = "close_session"
	ActionListSessions  = "list_sessions"
	ActionSetupDownload = "setup_download"

	// Navigation
	ActionNavigate    = "navigate"
	ActionBack        = "back"
	ActionForward     = "forward"
	ActionReload      = "reload"
	ActionWaitForLoad = "wait_for_load"

	// Element locating
	ActionFindElement    = "find_element"
	ActionFindElements   = "find_elements"
	ActionGetElementInfo = "get_element_info"

	// Element state
	ActionGetElementState = "get_element_state"

	// Smart interaction
	ActionSmartClick  = "smart_click"
	ActionHover       = "hover"
	ActionDoubleClick = "double_click"
	ActionRightClick  = "right_click"
	ActionDragDrop    = "drag_drop"
	ActionSelectOpt   = "select_option"
	ActionUploadFile  = "upload_file"
	ActionExecuteJS   = "execute_js"
	ActionKeyPress    = "key_press"
	ActionClearInput  = "clear_input"

	// Wait
	ActionWaitForElement     = "wait_for_element"
	ActionWaitForURL         = "wait_for_url"
	ActionWaitForTitle       = "wait_for_title"
	ActionWaitForNetworkIdle = "wait_for_network_idle"
	ActionWaitForAlert       = "wait_for_alert"
	ActionWaitForAnyElement  = "wait_for_any_element"

	// Network listening
	ActionNetworkListenStart = "network_listen_start"
	ActionNetworkListenWait  = "network_listen_wait"
	ActionNetworkListenGet   = "network_listen_get"
	ActionNetworkListenStop  = "network_listen_stop"
	ActionNetworkListenClear = "network_listen_clear"
	ActionNetworkListenSteps = "network_listen_steps"

	// Dialog
	ActionHandleAlert  = "handle_alert"
	ActionGetAlertText = "get_alert_text"
	ActionSetAutoAlert = "set_auto_alert"

	// IFrame
	ActionListIframes = "list_iframes"
	ActionEnterIframe = "enter_iframe"
	ActionExitIframe  = "exit_iframe"

	// Tabs
	ActionNewTab    = "new_tab"
	ActionListTabs  = "list_tabs"
	ActionSwitchTab = "switch_tab"
	ActionCloseTab  = "close_tab"

	// Data extraction
	ActionGetCookies     = "get_cookies"
	ActionSetCookies     = "set_cookies"
	ActionGetStorage     = "get_storage"
	ActionGetConsoleLogs = "get_console_logs"

	// Screenshot / PDF
	ActionScreenshot        = "screenshot"
	ActionScreenshotElement = "screenshot_element"
	ActionPDF               = "pdf"

	// Download
	ActionWaitDownloadStart = "wait_download_start"
	ActionWaitDownloadDone  = "wait_download_done"
	ActionListDownloads     = "list_downloads"

	// Screencast
	ActionScreencastStart = "screencast_start"
	ActionScreencastStop  = "screencast_stop"

	// Snapshot
	ActionSnapshot = "snapshot"

	// V2: Auth bypass
	ActionSetExtraHeaders    = "set_extra_headers"
	ActionSetUserAgent       = "set_user_agent"
	ActionSetHTTPAuth        = "set_http_auth"
	ActionRouteAdd           = "route_add"
	ActionRouteRemove        = "route_remove"
	ActionRouteList          = "route_list"
	ActionSetBlockedURLs     = "set_blocked_urls"
	ActionInjectCookieString = "inject_cookies_string"
	ActionInjectAuthToken    = "inject_auth_token"
	ActionSaveMHTML          = "save_mhtml"
	ActionGetBlobURL         = "get_blob_url"
	ActionSetLoadMode        = "set_load_mode"

	// P2: Supplemental
	ActionScroll            = "scroll"
	ActionInput             = "input"
	ActionGetHTML           = "get_html"
	ActionClickForNewTab    = "click_for_new_tab"
	ActionClickForURLChange = "click_for_url_change"
	ActionFindChild         = "find_child"

	// P3: Actions API
	ActionMoveToAction  = "action_move_to"
	ActionMoveAction    = "action_move"
	ActionClickAtAction = "action_click_at"
	ActionHoldAction    = "action_hold"
	ActionReleaseAction = "action_release"
	ActionScrollAt      = "action_scroll_at"
	ActionTypeAction    = "action_type"
	ActionKeyDown       = "action_key_down"
	ActionKeyUp         = "action_key_up"
	ActionDragIn        = "action_drag_in"

	// V3: CDP extension
	ActionSetStorage          = "set_storage"
	ActionClearStorage        = "clear_storage"
	ActionCDPSend             = "cdp_send"
	ActionFetchInterceptStart = "fetch_intercept_start"
	ActionFetchInterceptStop  = "fetch_intercept_stop"
	ActionNavigateWithHeaders = "navigate_with_headers"
	ActionExtractAuthNetwork  = "extract_auth_from_network"
	ActionClearCookies        = "clear_cookies"
	ActionSetGeolocation      = "set_geolocation"
	ActionSetTimezone         = "set_timezone"

	// V4: Dual mode
	ActionClearExtraHeaders   = "clear_extra_headers"
	ActionCookiesToHTTP       = "cookies_to_http"
	ActionHTTPGet             = "http_get"
	ActionHTTPPost            = "http_post"
	ActionHTTPClose           = "http_close"
	ActionHTTPToBrowserCookie = "http_to_browser_cookies"
	ActionFindElementShadow   = "find_element_shadow"
	ActionClearCache          = "clear_cache"
	ActionGetNavHistory       = "get_navigation_history"

	// V5: CDP capabilities
	ActionConnectExisting  = "connect_existing"
	ActionGetPerfMetrics   = "get_performance_metrics"
	ActionGetResponseBody  = "get_response_body"
	ActionSetDeviceMetrics = "set_device_metrics"
	ActionGetFullAXTree    = "get_full_ax_tree"
	ActionEnableBrowserLog = "enable_browser_log"
	ActionGetBrowserLogs   = "get_browser_logs"

	// V6: Cloudflare bypass
	ActionWaitCFChallenge   = "wait_cloudflare_challenge"
	ActionExtractCFClear    = "extract_cf_clearance"
	ActionVerifyCFClear     = "verify_cf_clearance"
	ActionCookiesToHTTPCffi = "cookies_to_http_cffi"

	// V7: Google bypass
	ActionDetectGoogleCaptcha = "detect_google_captcha"
	ActionWaitGoogleChallenge = "wait_google_challenge"
	ActionHandleGoogleConsent = "handle_google_consent"
	ActionInjectGoogleCookies = "inject_google_cookies"
)

// Input is the unified input struct for all browser actions.
type Input struct {
	// --- Core ---
	Action    string `json:"action"`
	SessionID string `json:"session_id,omitempty"`

	// --- Session creation ---
	Headless    *bool  `json:"headless,omitempty"`
	CDPURL      string `json:"cdp_url,omitempty"`
	UserDataDir string `json:"user_data_dir,omitempty"`
	Proxy       string `json:"proxy,omitempty"`
	Locale      string `json:"locale,omitempty"`
	ViewportW   int    `json:"viewport_width,omitempty"`
	ViewportH   int    `json:"viewport_height,omitempty"`
	IgnoreHTTPS bool   `json:"ignore_https_errors,omitempty"`

	// --- Navigation ---
	URL       string `json:"url,omitempty"`
	WaitUntil string `json:"wait_until,omitempty"`

	// --- Element locating ---
	Locator       string `json:"locator,omitempty"`
	LocatorType   string `json:"locator_type,omitempty"`
	ParentLocator string `json:"parent_locator,omitempty"`
	Index         int    `json:"index,omitempty"`
	MaxResults    int    `json:"max_results,omitempty"`

	// --- Element interaction ---
	Text          string   `json:"text,omitempty"`
	Value         string   `json:"value,omitempty"`
	FilePaths     []string `json:"file_paths,omitempty"`
	DragToLocator string   `json:"drag_to_locator,omitempty"`

	// --- JS execution ---
	Script string `json:"script,omitempty"`
	JSArgs []any  `json:"js_args,omitempty"`

	// --- Keyboard ---
	Key  string `json:"key,omitempty"`
	Keys string `json:"keys,omitempty"`

	// --- Wait ---
	Timeout      int      `json:"timeout,omitempty"`
	WaitState    string   `json:"wait_state,omitempty"`
	WaitExclude  bool     `json:"wait_exclude,omitempty"`
	WaitPattern  string   `json:"wait_pattern,omitempty"`
	WaitMultiple []string `json:"wait_multiple,omitempty"`

	// --- Network listening ---
	ListenTargets []string `json:"listen_targets,omitempty"`
	ListenIsRegex bool     `json:"listen_is_regex,omitempty"`
	ListenMethods []string `json:"listen_methods,omitempty"`
	ListenTypes   []string `json:"listen_types,omitempty"`
	ListenCount   int      `json:"listen_count,omitempty"`

	// --- Dialog ---
	AlertAction string `json:"alert_action,omitempty"`
	AlertText   string `json:"alert_text,omitempty"`
	AutoAlert   *bool  `json:"auto_alert,omitempty"`

	// --- IFrame ---
	IframeSelector string `json:"iframe_selector,omitempty"`

	// --- Tabs ---
	TabID string `json:"tab_id,omitempty"`

	// --- Cookies ---
	CookieName   string        `json:"cookie_name,omitempty"`
	CookieValue  string        `json:"cookie_value,omitempty"`
	CookieDomain string        `json:"cookie_domain,omitempty"`
	CookiePath   string        `json:"cookie_path,omitempty"`
	CookieString string        `json:"cookie_string,omitempty"`
	Cookies      []CookieParam `json:"cookies,omitempty"`

	// --- Storage ---
	StorageType string            `json:"storage_type,omitempty"`
	StorageData map[string]string `json:"storage_data,omitempty"`

	// --- Screenshot ---
	FullPage       bool   `json:"full_page,omitempty"`
	ScreenshotPath string `json:"screenshot_path,omitempty"`
	Quality        int    `json:"quality,omitempty"`
	Format         string `json:"format,omitempty"`

	// --- Download ---
	DownloadDir string `json:"download_dir,omitempty"`
	DownloadID  string `json:"download_id,omitempty"`

	// --- Screencast ---
	ScreencastDir    string `json:"screencast_dir,omitempty"`
	ScreencastFormat string `json:"screencast_format,omitempty"`

	// --- Headers / Auth ---
	Headers       map[string]string `json:"headers,omitempty"`
	UserAgent     string            `json:"user_agent,omitempty"`
	AuthUsername  string            `json:"auth_username,omitempty"`
	AuthPassword  string            `json:"auth_password,omitempty"`
	AuthToken     string            `json:"auth_token,omitempty"`
	AuthTokenType string            `json:"auth_token_type,omitempty"`
	HeaderKeys    []string          `json:"header_keys,omitempty"`

	// --- Route ---
	RoutePattern string            `json:"route_pattern,omitempty"`
	RouteAction  string            `json:"route_action,omitempty"`
	RouteID      string            `json:"route_id,omitempty"`
	MockStatus   int               `json:"mock_status,omitempty"`
	MockBody     string            `json:"mock_body,omitempty"`
	MockHeaders  map[string]string `json:"mock_headers,omitempty"`
	BlockedURLs  []string          `json:"blocked_urls,omitempty"`
	BlockPreset  string            `json:"block_preset,omitempty"`

	// --- Load mode ---
	LoadMode string `json:"load_mode,omitempty"`

	// --- Actions API (mouse/keyboard) ---
	X          float64 `json:"x,omitempty"`
	Y          float64 `json:"y,omitempty"`
	DeltaX     float64 `json:"delta_x,omitempty"`
	DeltaY     float64 `json:"delta_y,omitempty"`
	Button     string  `json:"button,omitempty"`
	Steps      int     `json:"steps,omitempty"`
	InputDelay float64 `json:"input_delay,omitempty"`
	TypeText   string  `json:"type_text,omitempty"`

	// --- Scroll ---
	ScrollDirection string `json:"scroll_direction,omitempty"`
	ScrollAmount    int    `json:"scroll_amount,omitempty"`

	// --- CDP direct ---
	CDPMethod string         `json:"cdp_method,omitempty"`
	CDPParams map[string]any `json:"cdp_params,omitempty"`

	// --- Fetch intercept ---
	FetchPatterns      []string          `json:"fetch_patterns,omitempty"`
	FetchInjectHeaders map[string]string `json:"fetch_inject_headers,omitempty"`
	FetchBlockPatterns []string          `json:"fetch_block_patterns,omitempty"`

	// --- Geolocation ---
	Latitude  float64 `json:"latitude,omitempty"`
	Longitude float64 `json:"longitude,omitempty"`
	Accuracy  float64 `json:"accuracy,omitempty"`
	Timezone  string  `json:"timezone,omitempty"`

	// --- HTTP dual mode ---
	HTTPMethod     string            `json:"http_method,omitempty"`
	HTTPBody       string            `json:"http_body,omitempty"`
	HTTPHeaders    map[string]string `json:"http_headers,omitempty"`
	HTTPFormat     string            `json:"http_format,omitempty"`
	TLSImpersonate string            `json:"http_tls_impersonate,omitempty"`

	// --- Shadow DOM ---
	ShadowSelector string `json:"shadow_selector,omitempty"`

	// --- Cache clear ---
	ClearTypes []string `json:"clear_types,omitempty"`

	// --- Device metrics ---
	DeviceWidth       int     `json:"device_width,omitempty"`
	DeviceHeight      int     `json:"device_height,omitempty"`
	DeviceScaleFactor float64 `json:"device_scale_factor,omitempty"`
	DeviceMobile      bool    `json:"device_mobile,omitempty"`

	// --- Response body ---
	NetworkRequestID string `json:"network_request_id,omitempty"`

	// --- Cloudflare ---
	CFChallengeTimeout int  `json:"cf_challenge_timeout,omitempty"`
	CFScreenshotOnFail bool `json:"cf_challenge_screenshot_on_fail,omitempty"`

	// --- V7: Google bypass ---
	GoogleChallengeTimeout int  `json:"google_challenge_timeout,omitempty"`
	GoogleAutoConsent      bool `json:"google_auto_consent,omitempty"`

	// --- V7: Navigate retry ---
	NavigateRetry         int `json:"navigate_retry,omitempty"`
	NavigateRetryInterval int `json:"navigate_retry_interval,omitempty"`

	// --- MHTML / Blob ---
	SavePath string `json:"save_path,omitempty"`
	BlobURL  string `json:"blob_url,omitempty"`

	// --- HTML ---
	HTMLOuter bool `json:"html_outer,omitempty"`

	// --- Drag file ---
	DragFiles []string `json:"drag_files,omitempty"`
	DragText  string   `json:"drag_text,omitempty"`

	// --- Stealth options (Phase 1) ---
	BlockWebRTC  bool `json:"block_webrtc,omitempty"`
	HideCanvas   bool `json:"hide_canvas,omitempty"`
	DisableWebGL bool `json:"disable_webgl,omitempty"`

	// --- Proxy rotation (Phase 5) ---
	Proxies       []string `json:"proxies,omitempty"`
	ProxyStrategy string   `json:"proxy_strategy,omitempty"`

	// --- Resource blocking (Phase 6) ---
	DisableResources bool     `json:"disable_resources,omitempty"`
	BlockedDomains   []string `json:"blocked_domains,omitempty"`
	BlockAds         bool     `json:"block_ads,omitempty"`
}

// CookieParam represents a single cookie to set.
type CookieParam struct {
	Name     string  `json:"name"`
	Value    string  `json:"value"`
	Domain   string  `json:"domain,omitempty"`
	Path     string  `json:"path,omitempty"`
	Expires  float64 `json:"expires,omitempty"`
	Secure   bool    `json:"secure,omitempty"`
	HTTPOnly bool    `json:"httpOnly,omitempty"`
	SameSite string  `json:"sameSite,omitempty"`
}

// inputSchema returns the JSON Schema for the browser tool input.
func inputSchema() json.RawMessage {
	return json.RawMessage(`{
	"type": "object",
	"properties": {
		"action": {
			"type": "string",
			"description": "The browser action to perform.",
			"enum": [
				"create_session","close_session","list_sessions","setup_download",
				"navigate","back","forward","reload","wait_for_load",
				"find_element","find_elements","get_element_info","get_element_state",
				"smart_click","hover","double_click","right_click","drag_drop",
				"select_option","upload_file","execute_js","key_press","clear_input",
				"wait_for_element","wait_for_url","wait_for_title","wait_for_network_idle","wait_for_alert","wait_for_any_element",
				"network_listen_start","network_listen_wait","network_listen_get","network_listen_stop","network_listen_clear","network_listen_steps",
				"handle_alert","get_alert_text","set_auto_alert",
				"list_iframes","enter_iframe","exit_iframe",
				"new_tab","list_tabs","switch_tab","close_tab",
				"get_cookies","set_cookies","get_storage","get_console_logs",
				"screenshot","screenshot_element","pdf",
				"list_downloads","snapshot",
				"set_extra_headers","clear_extra_headers","set_user_agent","set_http_auth",
				"route_add","route_remove","route_list","set_blocked_urls",
				"inject_cookies_string","inject_auth_token",
				"save_mhtml","get_blob_url","set_load_mode",
				"scroll","input","get_html",
				"click_for_new_tab","click_for_url_change","find_child",
				"action_move_to","action_click_at",
				"action_scroll_at","action_type","action_key_down","action_key_up",
				"set_storage","clear_storage","cdp_send",
				"fetch_intercept_start","fetch_intercept_stop",
				"navigate_with_headers","extract_auth_from_network",
				"clear_cookies","set_geolocation","set_timezone",
				"cookies_to_http","http_get","http_post","http_close","http_to_browser_cookies",
				"find_element_shadow","clear_cache","get_navigation_history",
				"get_performance_metrics","get_response_body","set_device_metrics",
				"get_full_ax_tree","enable_browser_log","get_browser_logs",
				"wait_cloudflare_challenge","extract_cf_clearance","verify_cf_clearance",
				"detect_google_captcha","wait_google_challenge","handle_google_consent","inject_google_cookies"
			]
		},
		"session_id":  {"type":"string","description":"Session ID. Auto-selected if omitted."},
		"url":         {"type":"string","description":"URL for navigate/http actions."},
		"locator":     {"type":"string","description":"Element locator. Supports CSS (#id,.class), XPath (//tag), text=, text:, @attr=, @@multi, tag:div@attr=val."},
		"text":        {"type":"string","description":"Text for input/select actions."},
		"script":      {"type":"string","description":"JavaScript code to execute."},
		"key":         {"type":"string","description":"Key name for key_press (e.g. Enter, Tab, Escape)."},
		"timeout":     {"type":"integer","description":"Timeout in milliseconds (default 30000)."},
		"wait_state":  {"type":"string","description":"Element state to wait for: visible/hidden/present/absent/enabled/clickable."},
		"headless":    {"type":"boolean","description":"Run browser in headless mode (default true)."},
		"cdp_url":     {"type":"string","description":"CDP WebSocket URL to connect to existing Chrome."},
		"headers":     {"type":"object","description":"HTTP headers to inject."},
		"cookies":     {"type":"array","description":"Cookies to set."},
		"cookie_string":{"type":"string","description":"Cookie string to inject (name=value; name2=value2)."},
		"cdp_method":  {"type":"string","description":"CDP method name for cdp_send."},
		"cdp_params":  {"type":"object","description":"CDP method parameters."},
		"x":           {"type":"number","description":"X coordinate for mouse actions."},
		"y":           {"type":"number","description":"Y coordinate for mouse actions."},
		"full_page":   {"type":"boolean","description":"Full page screenshot."},
		"scroll_direction":{"type":"string","description":"Scroll direction: up/down/left/right/top/bottom/into_view."},
		"scroll_amount":{"type":"integer","description":"Scroll pixels (default 300)."},
		"tab_id":      {"type":"string","description":"Tab ID for switch_tab/close_tab."},
		"proxy":       {"type":"string","description":"Proxy URL (http/socks5)."},
		"format":      {"type":"string","description":"Screenshot format: png/jpeg."},
		"quality":     {"type":"integer","description":"JPEG quality 0-100."},
		"load_mode":   {"type":"string","description":"Page load mode: normal/eager/none."},
		"storage_type":{"type":"string","description":"Storage type: local/session."},
		"storage_data":{"type":"object","description":"Key-value pairs for set_storage."},
		"auth_token":  {"type":"string","description":"Auth token for inject_auth_token."},
		"listen_targets":{"type":"array","description":"URL patterns to listen for.","items":{"type":"string"}},
		"listen_count":{"type":"integer","description":"Number of packets to wait for."},
		"alert_action":{"type":"string","description":"Dialog action: accept/dismiss."},
		"latitude":    {"type":"number","description":"Latitude for set_geolocation."},
		"longitude":   {"type":"number","description":"Longitude for set_geolocation."},
		"timezone":    {"type":"string","description":"IANA timezone for set_timezone."},
		"cf_challenge_timeout":{"type":"integer","description":"CF challenge timeout in ms (default 30000)."},
		"google_challenge_timeout":{"type":"integer","description":"Google challenge wait timeout in ms (default 60000)."},
		"google_auto_consent":{"type":"boolean","description":"Auto-handle Google consent page during navigate (default false)."},
		"navigate_retry":{"type":"integer","description":"Number of navigate retries on failure (default 0)."},
		"navigate_retry_interval":{"type":"integer","description":"Retry interval in ms (default 2000)."},
		"user_data_dir":{"type":"string","description":"Chrome user data dir for persistent profile (reuse logged-in sessions)."},
		"block_webrtc":{"type":"boolean","description":"Block WebRTC to prevent IP leaks (default false)."},
		"hide_canvas":{"type":"boolean","description":"Add canvas fingerprinting noise (default false)."},
		"disable_webgl":{"type":"boolean","description":"Disable WebGL to prevent GPU fingerprinting (default false)."},
		"proxies":{"type":"array","items":{"type":"string"},"description":"List of proxy URLs for rotation (http/socks5). First proxy used for browser."},
		"proxy_strategy":{"type":"string","description":"Proxy rotation strategy: cyclic (default) or random."},
		"disable_resources":{"type":"boolean","description":"Block fonts/images/media/stylesheets for speed (default false)."},
		"blocked_domains":{"type":"array","items":{"type":"string"},"description":"Custom domains to block (subdomains matched automatically)."},
		"block_ads":{"type":"boolean","description":"Block known ad/tracker domains (default false)."}
	},
	"required": ["action"]
}`)
}
