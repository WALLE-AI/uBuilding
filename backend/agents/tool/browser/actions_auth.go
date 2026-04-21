package browser

import (
	"encoding/base64"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
	"github.com/google/uuid"
	"github.com/ysmood/gson"
)

func (t *BrowserTool) doSetExtraHeaders(in *Input) string {
	s, page, err := t.getSessionAndPage(in)
	if err != nil {
		return errStr(err)
	}
	if len(in.Headers) == 0 {
		return "Error: headers map is required"
	}

	s.mu.Lock()
	for k, v := range in.Headers {
		s.extraHeaders[k] = v
	}
	allHeaders := make(map[string]string)
	for k, v := range s.extraHeaders {
		allHeaders[k] = v
	}
	s.mu.Unlock()

	headers := make(proto.NetworkHeaders)
	for k, v := range allHeaders {
		headers[k] = gson.New(v)
	}
	err = proto.NetworkSetExtraHTTPHeaders{Headers: headers}.Call(page)
	if err != nil {
		return fmt.Sprintf("set_extra_headers failed: %v", err)
	}
	return fmt.Sprintf("Set %d extra header(s).", len(in.Headers))
}

func (t *BrowserTool) doClearExtraHeaders(in *Input) string {
	s, page, err := t.getSessionAndPage(in)
	if err != nil {
		return errStr(err)
	}

	s.mu.Lock()
	if len(in.HeaderKeys) > 0 {
		for _, k := range in.HeaderKeys {
			delete(s.extraHeaders, k)
		}
	} else {
		s.extraHeaders = make(map[string]string)
	}
	remaining := make(map[string]string)
	for k, v := range s.extraHeaders {
		remaining[k] = v
	}
	s.mu.Unlock()

	headers := make(proto.NetworkHeaders)
	for k, v := range remaining {
		headers[k] = gson.New(v)
	}
	err = proto.NetworkSetExtraHTTPHeaders{Headers: headers}.Call(page)
	if err != nil {
		return fmt.Sprintf("clear_extra_headers failed: %v", err)
	}
	return fmt.Sprintf("Extra headers cleared. %d remaining.", len(remaining))
}

func (t *BrowserTool) doSetUserAgent(in *Input) string {
	_, page, err := t.getSessionAndPage(in)
	if err != nil {
		return errStr(err)
	}
	ua := in.UserAgent
	if ua == "" {
		return "Error: user_agent is required"
	}
	err = proto.NetworkSetUserAgentOverride{UserAgent: ua}.Call(page)
	if err != nil {
		return fmt.Sprintf("set_user_agent failed: %v", err)
	}
	// Also inject via JS
	js := fmt.Sprintf(`Object.defineProperty(navigator,'userAgent',{get:()=>%q})`, ua)
	_, _ = page.EvalOnNewDocument(js)
	return fmt.Sprintf("User-Agent set: %s", truncStr(ua, 80))
}

func (t *BrowserTool) doSetHTTPAuth(in *Input) string {
	_, page, err := t.getSessionAndPage(in)
	if err != nil {
		return errStr(err)
	}
	if in.AuthUsername == "" {
		return "Error: auth_username is required"
	}

	// Set Authorization header via extra headers
	cred := base64.StdEncoding.EncodeToString([]byte(in.AuthUsername + ":" + in.AuthPassword))
	headers := make(proto.NetworkHeaders)
	headers["Authorization"] = gson.New("Basic " + cred)
	err = proto.NetworkSetExtraHTTPHeaders{Headers: headers}.Call(page)
	if err != nil {
		return fmt.Sprintf("set_http_auth failed: %v", err)
	}
	return fmt.Sprintf("HTTP Basic Auth set for user: %s", in.AuthUsername)
}

func (t *BrowserTool) doInjectCookieString(in *Input) string {
	_, page, err := t.getSessionAndPage(in)
	if err != nil {
		return errStr(err)
	}
	if in.CookieString == "" {
		return "Error: cookie_string is required"
	}
	domain := in.CookieDomain
	if domain == "" {
		// Extract from current URL
		info := safeInfo(page)
		if info.URL != "" {
			parts := strings.Split(info.URL, "/")
			if len(parts) >= 3 {
				domain = strings.Split(parts[2], ":")[0]
			}
		}
	}
	if domain == "" {
		return "Error: could not determine domain. Provide cookie_domain."
	}

	pairs := strings.Split(in.CookieString, ";")
	var cookieParams []*proto.NetworkCookieParam
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
		cookieParams = append(cookieParams, &proto.NetworkCookieParam{
			Name:   name,
			Value:  value,
			Domain: domain,
			Path:   "/",
		})
		// Also set for .domain (subdomain coverage)
		if !strings.HasPrefix(domain, ".") {
			cookieParams = append(cookieParams, &proto.NetworkCookieParam{
				Name:   name,
				Value:  value,
				Domain: "." + domain,
				Path:   "/",
			})
		}
	}
	if len(cookieParams) == 0 {
		return "No valid cookies parsed from string."
	}

	err = page.SetCookies(cookieParams)
	if err != nil {
		return fmt.Sprintf("inject_cookies_string failed: %v", err)
	}
	return fmt.Sprintf("Injected %d cookie(s) for domain %s.", len(cookieParams)/2, domain)
}

func (t *BrowserTool) doInjectAuthToken(in *Input) string {
	s, page, err := t.getSessionAndPage(in)
	if err != nil {
		return errStr(err)
	}
	token := in.AuthToken
	if token == "" {
		return "Error: auth_token is required"
	}
	tokenType := in.AuthTokenType
	if tokenType == "" {
		tokenType = "Bearer"
	}

	s.mu.Lock()
	s.extraHeaders["Authorization"] = tokenType + " " + token
	allHeaders := make(map[string]string)
	for k, v := range s.extraHeaders {
		allHeaders[k] = v
	}
	s.mu.Unlock()

	headers := make(proto.NetworkHeaders)
	for k, v := range allHeaders {
		headers[k] = gson.New(v)
	}
	err = proto.NetworkSetExtraHTTPHeaders{Headers: headers}.Call(page)
	if err != nil {
		return fmt.Sprintf("inject_auth_token failed: %v", err)
	}
	return fmt.Sprintf("Auth token injected: %s %s...", tokenType, truncStr(token, 20))
}

// --- Route management ---

func (t *BrowserTool) doRouteAdd(in *Input) string {
	s, page, err := t.getSessionAndPage(in)
	if err != nil {
		return errStr(err)
	}
	if in.RoutePattern == "" {
		return "Error: route_pattern is required"
	}
	action := in.RouteAction
	if action == "" {
		action = "continue"
	}

	routeID := uuid.New().String()[:8]
	route := &RouteEntry{
		ID:          routeID,
		Pattern:     in.RoutePattern,
		Action:      action,
		Headers:     in.MockHeaders,
		MockStatus:  in.MockStatus,
		MockBody:    in.MockBody,
		MockHeaders: in.MockHeaders,
	}

	s.mu.Lock()
	s.routes[routeID] = route
	needStart := !s.routeActive
	s.mu.Unlock()

	// Start interception if not already active
	if needStart {
		startRouteInterception(s, page)
	}

	return fmt.Sprintf("Route added.\n  id: %s\n  pattern: %s\n  action: %s", routeID, in.RoutePattern, action)
}

// startRouteInterception enables Fetch domain and starts an event loop that
// matches paused requests against session routes.
func startRouteInterception(s *BrowserSession, page *rod.Page) {
	// Enable Fetch domain to intercept requests
	err := proto.FetchEnable{
		Patterns: []*proto.FetchRequestPattern{
			{URLPattern: "*", RequestStage: proto.FetchRequestStageRequest},
		},
	}.Call(page)
	if err != nil {
		slog.Warn("browser: FetchEnable failed", slog.Any("err", err))
		return
	}

	s.mu.Lock()
	s.routeActive = true
	s.routeStopCh = make(chan struct{})
	stopCh := s.routeStopCh
	s.mu.Unlock()

	wait := page.EachEvent(func(e *proto.FetchRequestPaused) {
		handleRoutedRequest(s, page, e)
	})

	go func() {
		done := make(chan struct{})
		go func() {
			wait()
			close(done)
		}()
		select {
		case <-stopCh:
			_ = proto.FetchDisable{}.Call(page)
			return
		case <-done:
			return
		}
	}()
}

// handleRoutedRequest applies route rules to a paused request.
func handleRoutedRequest(s *BrowserSession, page *rod.Page, e *proto.FetchRequestPaused) {
	s.mu.RLock()
	var matched *RouteEntry
	for _, r := range s.routes {
		if matchRoutePattern(r.Pattern, e.Request.URL) {
			matched = r
			break
		}
	}
	s.mu.RUnlock()

	if matched == nil {
		// No route matched — continue normally
		_ = proto.FetchContinueRequest{RequestID: e.RequestID}.Call(page)
		return
	}

	switch matched.Action {
	case "abort":
		_ = proto.FetchFailRequest{
			RequestID:   e.RequestID,
			ErrorReason: proto.NetworkErrorReasonBlockedByClient,
		}.Call(page)
	case "mock_response":
		status := matched.MockStatus
		if status == 0 {
			status = 200
		}
		var headers []*proto.FetchHeaderEntry
		for k, v := range matched.MockHeaders {
			headers = append(headers, &proto.FetchHeaderEntry{Name: k, Value: v})
		}
		_ = proto.FetchFulfillRequest{
			RequestID:       e.RequestID,
			ResponseCode:    status,
			ResponseHeaders: headers,
			Body:            []byte(base64.StdEncoding.EncodeToString([]byte(matched.MockBody))),
		}.Call(page)
	case "add_headers":
		var headers []*proto.FetchHeaderEntry
		// Copy existing headers from the NetworkHeaders map
		for k, v := range e.Request.Headers {
			headers = append(headers, &proto.FetchHeaderEntry{
				Name:  k,
				Value: v.Str(),
			})
		}
		// Add extra headers from route
		for k, v := range matched.Headers {
			headers = append(headers, &proto.FetchHeaderEntry{Name: k, Value: v})
		}
		_ = proto.FetchContinueRequest{
			RequestID: e.RequestID,
			Headers:   headers,
		}.Call(page)
	default:
		// "continue" or unknown — pass through
		_ = proto.FetchContinueRequest{RequestID: e.RequestID}.Call(page)
	}
}

// matchRoutePattern performs simple wildcard matching (* as glob).
func matchRoutePattern(pattern, url string) bool {
	if pattern == "*" {
		return true
	}
	// Simple wildcard: *text* style
	if strings.HasPrefix(pattern, "*") && strings.HasSuffix(pattern, "*") {
		return strings.Contains(url, pattern[1:len(pattern)-1])
	}
	if strings.HasPrefix(pattern, "*") {
		return strings.HasSuffix(url, pattern[1:])
	}
	if strings.HasSuffix(pattern, "*") {
		return strings.HasPrefix(url, pattern[:len(pattern)-1])
	}
	return strings.Contains(url, pattern)
}

func (t *BrowserTool) doRouteRemove(in *Input) string {
	s, page, err := t.getSessionAndPage(in)
	if err != nil {
		return errStr(err)
	}
	if in.RouteID == "" {
		return "Error: route_id is required"
	}
	s.mu.Lock()
	delete(s.routes, in.RouteID)
	remaining := len(s.routes)
	s.mu.Unlock()

	// Disable interception when no routes remain
	if remaining == 0 {
		stopRouteInterception(s, page)
	}

	return fmt.Sprintf("Route %s removed.", in.RouteID)
}

// stopRouteInterception disables Fetch domain and stops the event loop.
func stopRouteInterception(s *BrowserSession, page *rod.Page) {
	s.mu.Lock()
	if s.routeActive && s.routeStopCh != nil {
		close(s.routeStopCh)
		s.routeStopCh = nil
		s.routeActive = false
	}
	s.mu.Unlock()
	_ = proto.FetchDisable{}.Call(page)
}

func (t *BrowserTool) doRouteList(in *Input) string {
	s, _, err := t.getSessionAndPage(in)
	if err != nil {
		return errStr(err)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.routes) == 0 {
		return "No active routes."
	}
	return resultJSON(s.routes)
}

func (t *BrowserTool) doSetBlockedURLs(in *Input) string {
	_, page, err := t.getSessionAndPage(in)
	if err != nil {
		return errStr(err)
	}

	urls := in.BlockedURLs
	// Handle presets
	if in.BlockPreset != "" {
		switch in.BlockPreset {
		case "ads":
			urls = append(urls, "*doubleclick.net*", "*googlesyndication*", "*adservice*", "*analytics*", "*tracking*")
		case "media":
			urls = append(urls, "*.png", "*.jpg", "*.jpeg", "*.gif", "*.svg", "*.mp4", "*.webm", "*.mp3")
		case "fonts":
			urls = append(urls, "*.woff", "*.woff2", "*.ttf", "*.eot")
		}
	}

	if len(urls) == 0 {
		return "Error: blocked_urls or block_preset is required"
	}

	err = proto.NetworkSetBlockedURLs{Urls: urls}.Call(page)
	if err != nil {
		return fmt.Sprintf("set_blocked_urls failed: %v", err)
	}
	return fmt.Sprintf("Blocked %d URL pattern(s).", len(urls))
}

func (t *BrowserTool) doSetLoadMode(in *Input) string {
	s, _, err := t.getSessionAndPage(in)
	if err != nil {
		return errStr(err)
	}
	mode := in.LoadMode
	if mode == "" {
		mode = "normal"
	}
	if mode != "normal" && mode != "eager" && mode != "none" {
		return fmt.Sprintf("Invalid load_mode: %q (use normal/eager/none)", mode)
	}
	s.mu.Lock()
	s.loadMode = mode
	s.mu.Unlock()
	return fmt.Sprintf("Load mode set to: %s", mode)
}

func (t *BrowserTool) doSaveMHTML(in *Input) string {
	_, page, err := t.getSessionAndPage(in)
	if err != nil {
		return errStr(err)
	}
	res, err := proto.PageCaptureSnapshot{}.Call(page)
	if err != nil {
		return fmt.Sprintf("save_mhtml failed: %v", err)
	}
	savePath := in.SavePath
	if savePath == "" {
		return fmt.Sprintf("MHTML captured (%d bytes). Provide save_path to write.", len(res.Data))
	}
	err = writeFile(savePath, []byte(res.Data))
	if err != nil {
		return fmt.Sprintf("MHTML captured but save failed: %v", err)
	}
	return fmt.Sprintf("MHTML saved: %s (%d bytes)", savePath, len(res.Data))
}

func (t *BrowserTool) doGetBlobURL(in *Input) string {
	_, page, err := t.getSessionAndPage(in)
	if err != nil {
		return errStr(err)
	}
	blobURL := in.BlobURL
	if blobURL == "" {
		return "Error: blob_url is required"
	}
	res, err := page.Eval(fmt.Sprintf(`() => {
		return fetch(%q).then(r => r.text());
	}`, blobURL))
	if err != nil {
		return fmt.Sprintf("get_blob_url failed: %v", err)
	}
	content := res.Value.Str()
	if len(content) > 5000 {
		content = content[:5000] + "... (truncated)"
	}
	return content
}

func writeFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	_ = os.MkdirAll(dir, 0o755)
	return os.WriteFile(path, data, 0o644)
}
