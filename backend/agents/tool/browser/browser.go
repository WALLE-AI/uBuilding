package browser

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"

	"github.com/wall-ai/ubuilding/backend/agents"
	"github.com/wall-ai/ubuilding/backend/agents/tool"
)

// BrowserTool implements tool.Tool for DrissionPage-style browser automation.
type BrowserTool struct {
	tool.ToolDefaults
	manager *SessionManager
}

// Compile-time assertion.
var _ tool.Tool = (*BrowserTool)(nil)

// New creates a new BrowserTool ready to register.
func New() *BrowserTool {
	return &BrowserTool{
		manager: getManager(),
	}
}

func (t *BrowserTool) Name() string      { return "Browser" }
func (t *BrowserTool) Aliases() []string { return []string{"BrowserDrission"} }

func (t *BrowserTool) Description(input json.RawMessage) string {
	var in Input
	if err := json.Unmarshal(input, &in); err != nil || in.Action == "" {
		return "Browser automation"
	}
	switch in.Action {
	case ActionNavigate:
		u := in.URL
		if len(u) > 60 {
			u = u[:60] + "\u2026"
		}
		return "Navigating to " + u
	case ActionScreenshot:
		return "Taking screenshot"
	case ActionSmartClick:
		return "Clicking " + in.Locator
	default:
		return "Browser: " + in.Action
	}
}

func (t *BrowserTool) InputSchema() *tool.JSONSchema {
	return &tool.JSONSchema{
		Type: "object",
		Properties: map[string]*tool.SchemaProperty{
			"action":                   {Type: "string", Description: "The browser action to perform."},
			"session_id":               {Type: "string", Description: "Session ID. Auto-selected if omitted."},
			"url":                      {Type: "string", Description: "URL for navigate/http actions."},
			"locator":                  {Type: "string", Description: "Element locator. Supports CSS (#id,.class), XPath (//tag), text=, text:, @attr=, @@multi, tag:div@attr=val."},
			"text":                     {Type: "string", Description: "Text for input/select actions."},
			"script":                   {Type: "string", Description: "JavaScript code to execute."},
			"key":                      {Type: "string", Description: "Key name for key_press (e.g. Enter, Tab, Escape)."},
			"timeout":                  {Type: "integer", Description: "Timeout in milliseconds (default 30000)."},
			"wait_state":               {Type: "string", Description: "Element state to wait for: visible/hidden/present/absent/enabled/clickable."},
			"headless":                 {Type: "boolean", Description: "Run browser in headless mode (default true)."},
			"cdp_url":                  {Type: "string", Description: "CDP WebSocket URL to connect to existing Chrome."},
			"headers":                  {Type: "object", Description: "HTTP headers to inject."},
			"cookies":                  {Type: "array", Description: "Cookies to set."},
			"cookie_string":            {Type: "string", Description: "Cookie string to inject (name=value; name2=value2)."},
			"cdp_method":               {Type: "string", Description: "CDP method name for cdp_send."},
			"cdp_params":               {Type: "object", Description: "CDP method parameters."},
			"x":                        {Type: "number", Description: "X coordinate for mouse actions."},
			"y":                        {Type: "number", Description: "Y coordinate for mouse actions."},
			"full_page":                {Type: "boolean", Description: "Full page screenshot."},
			"scroll_direction":         {Type: "string", Description: "Scroll direction: up/down/left/right/top/bottom/into_view."},
			"scroll_amount":            {Type: "integer", Description: "Scroll pixels (default 300)."},
			"tab_id":                   {Type: "string", Description: "Tab ID for switch_tab/close_tab."},
			"proxy":                    {Type: "string", Description: "Proxy URL (http/socks5)."},
			"format":                   {Type: "string", Description: "Screenshot format: png/jpeg."},
			"quality":                  {Type: "integer", Description: "JPEG quality 0-100."},
			"load_mode":                {Type: "string", Description: "Page load mode: normal/eager/none."},
			"storage_type":             {Type: "string", Description: "Storage type: local/session."},
			"storage_data":             {Type: "object", Description: "Key-value pairs for set_storage."},
			"auth_token":               {Type: "string", Description: "Auth token for inject_auth_token."},
			"listen_targets":           {Type: "array", Description: "URL patterns to listen for."},
			"listen_count":             {Type: "integer", Description: "Number of packets to wait for."},
			"alert_action":             {Type: "string", Description: "Dialog action: accept/dismiss."},
			"latitude":                 {Type: "number", Description: "Latitude for set_geolocation."},
			"longitude":                {Type: "number", Description: "Longitude for set_geolocation."},
			"timezone":                 {Type: "string", Description: "IANA timezone for set_timezone."},
			"cf_challenge_timeout":     {Type: "integer", Description: "CF challenge timeout in ms (default 30000)."},
			"google_challenge_timeout": {Type: "integer", Description: "Google challenge wait timeout in ms (default 60000)."},
			"google_auto_consent":      {Type: "boolean", Description: "Auto-handle Google consent page during navigate (default false)."},
			"navigate_retry":           {Type: "integer", Description: "Number of navigate retries on failure (default 0)."},
			"navigate_retry_interval":  {Type: "integer", Description: "Retry interval in ms (default 2000)."},
			"user_data_dir":            {Type: "string", Description: "Chrome user data dir for persistent profile."},
			"block_webrtc":             {Type: "boolean", Description: "Block WebRTC to prevent IP leaks (default false)."},
			"hide_canvas":              {Type: "boolean", Description: "Add canvas fingerprinting noise (default false)."},
			"disable_webgl":            {Type: "boolean", Description: "Disable WebGL to prevent GPU fingerprinting (default false)."},
			"proxies":                  {Type: "array", Description: "List of proxy URLs for rotation (http/socks5)."},
			"proxy_strategy":           {Type: "string", Description: "Proxy rotation strategy: cyclic (default) or random."},
			"disable_resources":        {Type: "boolean", Description: "Block fonts/images/media/stylesheets for speed (default false)."},
			"blocked_domains":          {Type: "array", Description: "Custom domains to block."},
			"block_ads":                {Type: "boolean", Description: "Block known ad/tracker domains (default false)."},
		},
		Required: []string{"action"},
	}
}

func (t *BrowserTool) IsEnabled() bool                          { return true }
func (t *BrowserTool) IsReadOnly(_ json.RawMessage) bool        { return false }
func (t *BrowserTool) IsConcurrencySafe(_ json.RawMessage) bool { return false }
func (t *BrowserTool) IsDestructive(_ json.RawMessage) bool     { return false }
func (t *BrowserTool) MaxResultSizeChars() int                  { return 200_000 }

func (t *BrowserTool) Prompt(_ tool.PromptOptions) string {
	return `Browser automation tool with DrissionPage-style locators.

Use "action" to specify what to do. Common workflow:
  1. create_session — launch browser (headless by default)
  2. navigate — go to URL
  3. find_element / smart_click / input — interact with page
  4. screenshot / get_html / snapshot — extract data
  5. close_session — release resources

Locator syntax (set via "locator" field):
  CSS:       #id  .class  css=div>span
  XPath:     //div[@id='x']  xpath=//a
  Text:      text=Login  text:Search  text^Start  text$End
  Attribute: @href=/api  @@class=btn@@type=submit
  Tag+attr:  tag:button@type=submit

Key actions:
  Session:   create_session, close_session, list_sessions
  Navigate:  navigate, back, forward, reload
  Elements:  find_element, find_elements, smart_click, hover, input
  Wait:      wait_for_element, wait_for_url, wait_for_network_idle
  Network:   network_listen_start, network_listen_wait, network_listen_get
  Data:      get_cookies, set_cookies, screenshot, pdf, get_html, snapshot
  Auth:      set_extra_headers, inject_cookies_string, inject_auth_token
  CDP:       cdp_send, set_geolocation, set_timezone
  CF bypass: wait_cloudflare_challenge, extract_cf_clearance
  Google:    detect_google_captcha, wait_google_challenge, handle_google_consent, inject_google_cookies

Google Search anti-block workflow (recommended):
  1. create_session(headless=false, user_data_dir="~/.chrome-profile")
  2. navigate(url="https://www.google.com/search?q=...", google_auto_consent=true)
  3. detect_google_captcha → check if blocked
  4. wait_google_challenge(google_challenge_timeout=60000) → wait/handle if blocked
  5. find_elements / get_html / snapshot → extract results`
}

func (t *BrowserTool) ValidateInput(input json.RawMessage, _ *agents.ToolUseContext) *tool.ValidationResult {
	var in Input
	if err := json.Unmarshal(input, &in); err != nil {
		return &tool.ValidationResult{Valid: false, Message: "invalid input JSON: " + err.Error()}
	}
	if in.Action == "" {
		return &tool.ValidationResult{Valid: false, Message: "action is required"}
	}
	return &tool.ValidationResult{Valid: true}
}

func (t *BrowserTool) CheckPermissions(input json.RawMessage, _ *agents.ToolUseContext) (*tool.PermissionResult, error) {
	return &tool.PermissionResult{
		Behavior:       tool.PermissionAllow,
		UpdatedInput:   input,
		DecisionReason: "default-allow",
	}, nil
}

// Call dispatches the browser action and returns the result synchronously.
func (t *BrowserTool) Call(ctx context.Context, input json.RawMessage, _ *agents.ToolUseContext) (*tool.ToolResult, error) {
	var in Input
	if err := json.Unmarshal(input, &in); err != nil {
		return nil, fmt.Errorf("invalid input: %w", err)
	}
	result := t.dispatch(ctx, &in)
	return &tool.ToolResult{Data: result}, nil
}

// MapToolResultToParam converts the tool result to an API-compatible content block.
func (t *BrowserTool) MapToolResultToParam(content interface{}, toolUseID string) *agents.ContentBlock {
	var text string
	switch v := content.(type) {
	case string:
		text = v
	default:
		b, _ := json.Marshal(content)
		text = string(b)
	}
	return &agents.ContentBlock{
		Type:      agents.ContentBlockToolResult,
		ToolUseID: toolUseID,
		Content:   text,
	}
}

// dispatch routes the action to the appropriate handler.
func (t *BrowserTool) dispatch(ctx context.Context, in *Input) string {
	switch in.Action {
	// --- Session management ---
	case ActionCreateSession:
		return t.doCreateSession(ctx, in)
	case ActionCloseSession:
		return t.doCloseSession(in)
	case ActionListSessions:
		return t.doListSessions()

	// --- Navigation ---
	case ActionNavigate:
		return t.doNavigate(in)
	case ActionBack:
		return t.doNavDirection(in, "back")
	case ActionForward:
		return t.doNavDirection(in, "forward")
	case ActionReload:
		return t.doNavDirection(in, "reload")
	case ActionWaitForLoad:
		return t.doWaitForLoad(in)

	// --- Element locating ---
	case ActionFindElement:
		return t.doFindElement(in)
	case ActionFindElements:
		return t.doFindElements(in)
	case ActionGetElementInfo:
		return t.doGetElementInfo(in)
	case ActionGetElementState:
		return t.doGetElementState(in)

	// --- Smart interaction ---
	case ActionSmartClick:
		return t.doSmartClick(in)
	case ActionHover:
		return t.doHover(in)
	case ActionDoubleClick:
		return t.doDoubleClick(in)
	case ActionRightClick:
		return t.doRightClick(in)
	case ActionDragDrop:
		return t.doDragDrop(in)
	case ActionSelectOpt:
		return t.doSelectOption(in)
	case ActionUploadFile:
		return t.doUploadFile(in)
	case ActionExecuteJS:
		return t.doExecuteJS(in)
	case ActionKeyPress:
		return t.doKeyPress(in)
	case ActionClearInput:
		return t.doClearInput(in)
	case ActionInput:
		return t.doInput(in)

	// --- Wait ---
	case ActionWaitForElement:
		return t.doWaitForElement(in)
	case ActionWaitForURL:
		return t.doWaitForURL(in)
	case ActionWaitForTitle:
		return t.doWaitForTitle(in)
	case ActionWaitForNetworkIdle:
		return t.doWaitForNetworkIdle(in)
	case ActionWaitForAlert:
		return t.doWaitForAlert(in)
	case ActionWaitForAnyElement:
		return t.doWaitForAnyElement(in)

	// --- Network listening ---
	case ActionNetworkListenStart:
		return t.doNetworkListenStart(in)
	case ActionNetworkListenWait:
		return t.doNetworkListenWait(in)
	case ActionNetworkListenGet:
		return t.doNetworkListenGet(in)
	case ActionNetworkListenStop:
		return t.doNetworkListenStop(in)
	case ActionNetworkListenClear:
		return t.doNetworkListenClear(in)
	case ActionNetworkListenSteps:
		return t.doNetworkListenSteps(in)

	// --- Dialog ---
	case ActionHandleAlert:
		return t.doHandleAlert(in)
	case ActionGetAlertText:
		return t.doGetAlertText(in)
	case ActionSetAutoAlert:
		return t.doSetAutoAlert(in)

	// --- IFrame ---
	case ActionListIframes:
		return t.doListIframes(in)
	case ActionEnterIframe:
		return t.doEnterIframe(in)
	case ActionExitIframe:
		return t.doExitIframe(in)

	// --- Tabs ---
	case ActionNewTab:
		return t.doNewTab(in)
	case ActionListTabs:
		return t.doListTabs(in)
	case ActionSwitchTab:
		return t.doSwitchTab(in)
	case ActionCloseTab:
		return t.doCloseTab(in)
	case ActionClickForNewTab:
		return t.doClickForNewTab(in)
	case ActionClickForURLChange:
		return t.doClickForURLChange(in)

	// --- Data extraction ---
	case ActionGetCookies:
		return t.doGetCookies(in)
	case ActionSetCookies:
		return t.doSetCookies(in)
	case ActionGetStorage:
		return t.doGetStorage(in)
	case ActionGetConsoleLogs:
		return t.doGetConsoleLogs(in)

	// --- Screenshot / PDF ---
	case ActionScreenshot:
		return t.doScreenshot(in)
	case ActionScreenshotElement:
		return t.doScreenshotElement(in)
	case ActionPDF:
		return t.doPDF(in)

	// --- Download ---
	case ActionSetupDownload:
		return t.doSetupDownload(in)
	case ActionListDownloads:
		return t.doListDownloads(in)

	// --- Snapshot ---
	case ActionSnapshot:
		return t.doSnapshot(in)

	// --- Scroll / HTML ---
	case ActionScroll:
		return t.doScroll(in)
	case ActionGetHTML:
		return t.doGetHTML(in)
	case ActionFindChild:
		return t.doFindChild(in)

	// --- V2: Auth/Headers ---
	case ActionSetExtraHeaders:
		return t.doSetExtraHeaders(in)
	case ActionClearExtraHeaders:
		return t.doClearExtraHeaders(in)
	case ActionSetUserAgent:
		return t.doSetUserAgent(in)
	case ActionSetHTTPAuth:
		return t.doSetHTTPAuth(in)
	case ActionInjectCookieString:
		return t.doInjectCookieString(in)
	case ActionInjectAuthToken:
		return t.doInjectAuthToken(in)

	// --- V2: Route/Block ---
	case ActionRouteAdd:
		return t.doRouteAdd(in)
	case ActionRouteRemove:
		return t.doRouteRemove(in)
	case ActionRouteList:
		return t.doRouteList(in)
	case ActionSetBlockedURLs:
		return t.doSetBlockedURLs(in)

	// --- V2: Load mode / MHTML / Blob ---
	case ActionSetLoadMode:
		return t.doSetLoadMode(in)
	case ActionSaveMHTML:
		return t.doSaveMHTML(in)
	case ActionGetBlobURL:
		return t.doGetBlobURL(in)

	// --- V3: Storage / CDP ---
	case ActionSetStorage:
		return t.doSetStorage(in)
	case ActionClearStorage:
		return t.doClearStorage(in)
	case ActionCDPSend:
		return t.doCDPSend(in)
	case ActionClearCookies:
		return t.doClearCookies(in)
	case ActionSetGeolocation:
		return t.doSetGeolocation(in)
	case ActionSetTimezone:
		return t.doSetTimezone(in)

	// --- V3: Fetch intercept ---
	case ActionFetchInterceptStart:
		return t.doFetchInterceptStart(in)
	case ActionFetchInterceptStop:
		return t.doFetchInterceptStop(in)
	case ActionNavigateWithHeaders:
		return t.doNavigateWithHeaders(in)
	case ActionExtractAuthNetwork:
		return t.doExtractAuthFromNetwork(in)

	// --- V4: HTTP dual mode ---
	case ActionCookiesToHTTP:
		return t.doCookiesToHTTP(in)
	case ActionHTTPGet:
		return t.doHTTPGet(in)
	case ActionHTTPPost:
		return t.doHTTPPost(in)
	case ActionHTTPClose:
		return t.doHTTPClose(in)
	case ActionHTTPToBrowserCookie:
		return t.doHTTPToBrowserCookies(in)
	case ActionFindElementShadow:
		return t.doFindElementShadow(in)
	case ActionClearCache:
		return t.doClearCache(in)
	case ActionGetNavHistory:
		return t.doGetNavHistory(in)

	// --- V5: CDP capabilities ---
	case ActionGetPerfMetrics:
		return t.doGetPerfMetrics(in)
	case ActionGetResponseBody:
		return t.doGetResponseBody(in)
	case ActionSetDeviceMetrics:
		return t.doSetDeviceMetrics(in)
	case ActionGetFullAXTree:
		return t.doGetFullAXTree(in)
	case ActionEnableBrowserLog:
		return t.doEnableBrowserLog(in)
	case ActionGetBrowserLogs:
		return t.doGetBrowserLogs(in)

	// --- V6: Cloudflare bypass ---
	case ActionWaitCFChallenge:
		return t.doWaitCFChallenge(in)
	case ActionExtractCFClear:
		return t.doExtractCFClearance(in)
	case ActionVerifyCFClear:
		return t.doVerifyCFClearance(in)

	// --- V7: Google bypass ---
	case ActionDetectGoogleCaptcha:
		return t.doDetectGoogleCaptcha(in)
	case ActionWaitGoogleChallenge:
		return t.doWaitGoogleChallenge(in)
	case ActionHandleGoogleConsent:
		return t.doHandleGoogleConsent(in)
	case ActionInjectGoogleCookies:
		return t.doInjectGoogleCookies(in)

	// --- Actions API ---
	case ActionMoveToAction:
		return t.doActionMoveTo(in)
	case ActionClickAtAction:
		return t.doActionClickAt(in)
	case ActionTypeAction:
		return t.doActionType(in)
	case ActionKeyDown:
		return t.doActionKeyDown(in)
	case ActionKeyUp:
		return t.doActionKeyUp(in)
	case ActionScrollAt:
		return t.doActionScrollAt(in)

	default:
		return fmt.Sprintf("Unknown action: %q. Use list_sessions, create_session, navigate, find_element, smart_click, screenshot, etc.", in.Action)
	}
}

// getSession is a helper that resolves the session from input.
func (t *BrowserTool) getSession(in *Input) (*BrowserSession, error) {
	return t.manager.GetSession(in.SessionID)
}

// getSessionAndPage resolves both session and active page.
func (t *BrowserTool) getSessionAndPage(in *Input) (*BrowserSession, *rod.Page, error) {
	s, err := t.getSession(in)
	if err != nil {
		return nil, nil, err
	}
	p := s.activePage()
	if p == nil {
		return nil, nil, fmt.Errorf("no active page in session %s", s.ID)
	}
	return s, p, nil
}

// errStr formats an error as tool output.
func errStr(err error) string {
	return fmt.Sprintf("Error: %v", err)
}

// safeInfo returns page info without panicking. Returns nil on error.
func safeInfo(page *rod.Page) *proto.TargetTargetInfo {
	info, err := page.Info()
	if err != nil {
		return &proto.TargetTargetInfo{}
	}
	return info
}
