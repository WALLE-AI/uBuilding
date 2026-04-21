package browser

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/go-rod/rod/lib/input"
	"github.com/go-rod/rod/lib/proto"
	"github.com/ysmood/gson"
)

func (t *BrowserTool) doCDPSend(in *Input) string {
	_, page, err := t.getSessionAndPage(in)
	if err != nil {
		return errStr(err)
	}
	if in.CDPMethod == "" {
		return "Error: cdp_method is required"
	}

	params, _ := json.Marshal(in.CDPParams)
	res, err := page.Call(nil, "", in.CDPMethod, params)
	if err != nil {
		return fmt.Sprintf("cdp_send %s failed: %v", in.CDPMethod, err)
	}

	result := string(res)
	if len(result) > 5000 {
		result = result[:5000] + "... (truncated)"
	}
	return fmt.Sprintf("CDP %s result:\n%s", in.CDPMethod, result)
}

func (t *BrowserTool) doClearCookies(in *Input) string {
	_, page, err := t.getSessionAndPage(in)
	if err != nil {
		return errStr(err)
	}
	err = proto.NetworkClearBrowserCookies{}.Call(page)
	if err != nil {
		return fmt.Sprintf("clear_cookies failed: %v", err)
	}
	return "All cookies cleared."
}

func (t *BrowserTool) doSetGeolocation(in *Input) string {
	_, page, err := t.getSessionAndPage(in)
	if err != nil {
		return errStr(err)
	}
	accuracy := in.Accuracy
	if accuracy <= 0 {
		accuracy = 1.0
	}
	err = proto.EmulationSetGeolocationOverride{
		Latitude:  &in.Latitude,
		Longitude: &in.Longitude,
		Accuracy:  &accuracy,
	}.Call(page)
	if err != nil {
		return fmt.Sprintf("set_geolocation failed: %v", err)
	}
	return fmt.Sprintf("Geolocation set: lat=%.6f, lon=%.6f, acc=%.1f", in.Latitude, in.Longitude, accuracy)
}

func (t *BrowserTool) doSetTimezone(in *Input) string {
	_, page, err := t.getSessionAndPage(in)
	if err != nil {
		return errStr(err)
	}
	if in.Timezone == "" {
		return "Error: timezone is required (e.g. America/New_York)"
	}
	err = proto.EmulationSetTimezoneOverride{TimezoneID: in.Timezone}.Call(page)
	if err != nil {
		return fmt.Sprintf("set_timezone failed: %v", err)
	}
	return fmt.Sprintf("Timezone set: %s", in.Timezone)
}

func (t *BrowserTool) doFetchInterceptStart(in *Input) string {
	_, page, err := t.getSessionAndPage(in)
	if err != nil {
		return errStr(err)
	}

	var patterns []*proto.FetchRequestPattern
	if len(in.FetchPatterns) > 0 {
		for _, p := range in.FetchPatterns {
			patterns = append(patterns, &proto.FetchRequestPattern{URLPattern: p})
		}
	} else {
		patterns = append(patterns, &proto.FetchRequestPattern{URLPattern: "*"})
	}

	err = proto.FetchEnable{Patterns: patterns}.Call(page)
	if err != nil {
		return fmt.Sprintf("fetch_intercept_start failed: %v", err)
	}
	return fmt.Sprintf("Fetch interception started with %d pattern(s).", len(patterns))
}

func (t *BrowserTool) doFetchInterceptStop(in *Input) string {
	_, page, err := t.getSessionAndPage(in)
	if err != nil {
		return errStr(err)
	}
	err = proto.FetchDisable{}.Call(page)
	if err != nil {
		return fmt.Sprintf("fetch_intercept_stop failed: %v", err)
	}
	return "Fetch interception stopped."
}

func (t *BrowserTool) doNavigateWithHeaders(in *Input) string {
	s, page, err := t.getSessionAndPage(in)
	if err != nil {
		return errStr(err)
	}
	if in.URL == "" {
		return "Error: url is required"
	}
	if len(in.Headers) == 0 {
		return "Error: headers is required"
	}

	// Temporarily set extra headers for this navigation
	s.mu.Lock()
	origHeaders := make(map[string]string)
	for k, v := range s.extraHeaders {
		origHeaders[k] = v
	}
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
	_ = proto.NetworkSetExtraHTTPHeaders{Headers: headers}.Call(page)

	err = page.Navigate(in.URL)
	_ = page.WaitLoad()

	// Restore original headers
	s.mu.Lock()
	s.extraHeaders = origHeaders
	s.mu.Unlock()
	restoreHeaders := make(proto.NetworkHeaders)
	for k, v := range origHeaders {
		restoreHeaders[k] = gson.New(v)
	}
	_ = proto.NetworkSetExtraHTTPHeaders{Headers: restoreHeaders}.Call(page)

	if err != nil {
		return fmt.Sprintf("navigate_with_headers failed: %v", err)
	}
	info := safeInfo(page)
	return fmt.Sprintf("Navigated with custom headers.\n  URL: %s\n  Title: %s", info.URL, info.Title)
}

func (t *BrowserTool) doExtractAuthFromNetwork(in *Input) string {
	s, _, err := t.getSessionAndPage(in)
	if err != nil {
		return errStr(err)
	}
	s.mu.RLock()
	nl := s.networkListener
	s.mu.RUnlock()
	if nl == nil {
		return "Error: start network_listen first, then perform the login action."
	}

	packets := nl.GetAll(0)
	var authTokens []string
	for _, p := range packets {
		for k, v := range p.RequestHeaders {
			lk := strings.ToLower(k)
			if lk == "authorization" || lk == "x-auth-token" || lk == "x-access-token" {
				authTokens = append(authTokens, fmt.Sprintf("%s: %s (from %s %s)", k, truncStr(v, 50), p.Method, truncStr(p.URL, 60)))
			}
		}
	}
	if len(authTokens) == 0 {
		return "No auth tokens found in captured network packets."
	}
	return fmt.Sprintf("Auth tokens found (%d):\n  %s", len(authTokens), strings.Join(authTokens, "\n  "))
}

func (t *BrowserTool) doFindElementShadow(in *Input) string {
	_, page, err := t.getSessionAndPage(in)
	if err != nil {
		return errStr(err)
	}
	if in.ShadowSelector == "" && in.Locator == "" {
		return "Error: shadow_selector or locator is required"
	}
	selector := in.ShadowSelector
	if selector == "" {
		selector = in.Locator
	}

	// Use DOM.performSearch to find in shadow DOM
	res, err := page.Eval(fmt.Sprintf(`() => {
		function findInShadow(root, sel) {
			let el = root.querySelector(sel);
			if (el) return el;
			let all = root.querySelectorAll('*');
			for (let node of all) {
				if (node.shadowRoot) {
					el = findInShadow(node.shadowRoot, sel);
					if (el) return el;
				}
			}
			return null;
		}
		let el = findInShadow(document, %q);
		if (!el) return null;
		return {
			tag: el.tagName.toLowerCase(),
			text: (el.innerText || '').substring(0, 200),
			id: el.id || ''
		};
	}`, selector))
	if err != nil || res.Value.Nil() {
		return fmt.Sprintf("Shadow DOM element not found: %s", selector)
	}
	v := res.Value
	return fmt.Sprintf("Shadow DOM element found: <%s> id=%q text=%q",
		v.Get("tag").Str(), v.Get("id").Str(), truncStr(v.Get("text").Str(), 80))
}

func (t *BrowserTool) doClearCache(in *Input) string {
	_, page, err := t.getSessionAndPage(in)
	if err != nil {
		return errStr(err)
	}
	_ = proto.NetworkClearBrowserCache{}.Call(page)
	_ = proto.NetworkClearBrowserCookies{}.Call(page)
	_, _ = page.Eval(`() => {
		try { localStorage.clear(); } catch(e) {}
		try { sessionStorage.clear(); } catch(e) {}
	}`)
	return "Cache, cookies, and storage cleared."
}

func (t *BrowserTool) doGetNavHistory(in *Input) string {
	_, page, err := t.getSessionAndPage(in)
	if err != nil {
		return errStr(err)
	}
	res, err := proto.PageGetNavigationHistory{}.Call(page)
	if err != nil {
		return fmt.Sprintf("get_navigation_history failed: %v", err)
	}

	var lines []string
	lines = append(lines, fmt.Sprintf("Navigation history (%d entries, current=%d):", len(res.Entries), res.CurrentIndex))
	for i, e := range res.Entries {
		marker := "  "
		if i == res.CurrentIndex {
			marker = "→ "
		}
		lines = append(lines, fmt.Sprintf("  %s[%d] %s — %s", marker, i, truncStr(e.Title, 40), truncStr(e.URL, 60)))
	}
	return strings.Join(lines, "\n")
}

func (t *BrowserTool) doGetPerfMetrics(in *Input) string {
	_, page, err := t.getSessionAndPage(in)
	if err != nil {
		return errStr(err)
	}
	_ = proto.PerformanceEnable{}.Call(page)
	res, err := proto.PerformanceGetMetrics{}.Call(page)
	if err != nil {
		return fmt.Sprintf("get_performance_metrics failed: %v", err)
	}

	metrics := make(map[string]float64)
	for _, m := range res.Metrics {
		metrics[m.Name] = m.Value
	}
	return resultJSON(metrics)
}

func (t *BrowserTool) doGetResponseBody(in *Input) string {
	_, page, err := t.getSessionAndPage(in)
	if err != nil {
		return errStr(err)
	}
	if in.NetworkRequestID == "" {
		return "Error: network_request_id is required"
	}
	res, err := proto.NetworkGetResponseBody{
		RequestID: proto.NetworkRequestID(in.NetworkRequestID),
	}.Call(page)
	if err != nil {
		return fmt.Sprintf("get_response_body failed: %v", err)
	}
	body := res.Body
	if len(body) > 5000 {
		body = body[:5000] + "... (truncated)"
	}
	return body
}

func (t *BrowserTool) doSetDeviceMetrics(in *Input) string {
	_, page, err := t.getSessionAndPage(in)
	if err != nil {
		return errStr(err)
	}
	w := in.DeviceWidth
	if w <= 0 {
		w = 375
	}
	h := in.DeviceHeight
	if h <= 0 {
		h = 812
	}
	scale := in.DeviceScaleFactor
	if scale <= 0 {
		scale = 3.0
	}
	err = proto.EmulationSetDeviceMetricsOverride{
		Width:             w,
		Height:            h,
		DeviceScaleFactor: scale,
		Mobile:            in.DeviceMobile,
	}.Call(page)
	if err != nil {
		return fmt.Sprintf("set_device_metrics failed: %v", err)
	}
	return fmt.Sprintf("Device metrics set: %dx%d scale=%.1f mobile=%v", w, h, scale, in.DeviceMobile)
}

func (t *BrowserTool) doGetFullAXTree(in *Input) string {
	_, page, err := t.getSessionAndPage(in)
	if err != nil {
		return errStr(err)
	}
	res, err := proto.AccessibilityGetFullAXTree{}.Call(page)
	if err != nil {
		return fmt.Sprintf("get_full_ax_tree failed: %v", err)
	}

	var lines []string
	lines = append(lines, fmt.Sprintf("Accessibility tree (%d nodes):", len(res.Nodes)))
	for i, node := range res.Nodes {
		if i >= 50 {
			lines = append(lines, fmt.Sprintf("  ... and %d more nodes", len(res.Nodes)-50))
			break
		}
		name := ""
		role := ""
		if node.Name != nil {
			name = fmt.Sprintf("%v", node.Name.Value)
		}
		if node.Role != nil {
			role = fmt.Sprintf("%v", node.Role.Value)
		}
		lines = append(lines, fmt.Sprintf("  [%d] role=%s name=%q", i, role, truncStr(name, 50)))
	}
	return strings.Join(lines, "\n")
}

func (t *BrowserTool) doEnableBrowserLog(in *Input) string {
	s, page, err := t.getSessionAndPage(in)
	if err != nil {
		return errStr(err)
	}
	err = proto.LogEnable{}.Call(page)
	if err != nil {
		return fmt.Sprintf("enable_browser_log failed: %v", err)
	}
	s.mu.Lock()
	s.browserLogEnabled = true
	s.mu.Unlock()

	go page.EachEvent(func(e *proto.LogEntryAdded) {
		s.mu.Lock()
		s.browserLogs = append(s.browserLogs, map[string]interface{}{
			"level":  string(e.Entry.Level),
			"source": string(e.Entry.Source),
			"text":   e.Entry.Text,
			"url":    e.Entry.URL,
		})
		s.mu.Unlock()
	})

	return "Browser logging enabled."
}

func (t *BrowserTool) doGetBrowserLogs(in *Input) string {
	s, _, err := t.getSessionAndPage(in)
	if err != nil {
		return errStr(err)
	}
	s.mu.RLock()
	logs := s.browserLogs
	s.mu.RUnlock()

	if len(logs) == 0 {
		return "No browser logs. Call enable_browser_log first."
	}
	return resultJSON(logs)
}

// --- Actions API (low-level mouse/keyboard) ---

func (t *BrowserTool) doActionMoveTo(in *Input) string {
	s, page, err := t.getSessionAndPage(in)
	if err != nil {
		return errStr(err)
	}
	x, y := in.X, in.Y
	if in.Locator != "" {
		el, elErr := t.resolveElement(page, in)
		if elErr != nil {
			return fmt.Sprintf("Element not found: %v", elErr)
		}
		shape, _ := el.Shape()
		if shape != nil {
			box := shape.Box()
			x = box.X + box.Width/2
			y = box.Y + box.Height/2
		}
	}
	err = page.Mouse.MoveTo(proto.NewPoint(x, y))
	if err != nil {
		return fmt.Sprintf("action_move_to failed: %v", err)
	}
	s.mu.Lock()
	s.actionsState.X = x
	s.actionsState.Y = y
	s.mu.Unlock()
	return fmt.Sprintf("Mouse moved to (%.0f, %.0f).", x, y)
}

func (t *BrowserTool) doActionClickAt(in *Input) string {
	_, page, err := t.getSessionAndPage(in)
	if err != nil {
		return errStr(err)
	}
	btn := proto.InputMouseButtonLeft
	if in.Button == "right" {
		btn = proto.InputMouseButtonRight
	} else if in.Button == "middle" {
		btn = proto.InputMouseButtonMiddle
	}

	err = page.Mouse.MoveTo(proto.NewPoint(in.X, in.Y))
	if err != nil {
		return fmt.Sprintf("move failed: %v", err)
	}
	err = page.Mouse.Click(btn, 1)
	if err != nil {
		return fmt.Sprintf("action_click_at failed: %v", err)
	}
	return fmt.Sprintf("Clicked at (%.0f, %.0f) button=%s.", in.X, in.Y, in.Button)
}

func (t *BrowserTool) doActionType(in *Input) string {
	_, page, err := t.getSessionAndPage(in)
	if err != nil {
		return errStr(err)
	}
	text := in.TypeText
	if text == "" {
		text = in.Text
	}
	if text == "" {
		return "Error: type_text or text is required"
	}
	for _, ch := range text {
		err = page.Keyboard.Type(input.Key(ch))
		if err != nil {
			return fmt.Sprintf("action_type failed at char %q: %v", string(ch), err)
		}
	}
	return fmt.Sprintf("Typed %d character(s).", len(text))
}

func (t *BrowserTool) doActionKeyDown(in *Input) string {
	_, page, err := t.getSessionAndPage(in)
	if err != nil {
		return errStr(err)
	}
	if in.Key == "" {
		return "Error: key is required"
	}
	keyInfo := resolveKeyInfo(in.Key)
	err = proto.InputDispatchKeyEvent{
		Type:                  proto.InputDispatchKeyEventTypeKeyDown,
		Key:                   keyInfo.key,
		Code:                  keyInfo.code,
		WindowsVirtualKeyCode: keyInfo.keyCode,
		NativeVirtualKeyCode:  keyInfo.keyCode,
	}.Call(page)
	if err != nil {
		return fmt.Sprintf("action_key_down %q failed: %v", in.Key, err)
	}
	return fmt.Sprintf("Key down: %s", in.Key)
}

func (t *BrowserTool) doActionKeyUp(in *Input) string {
	_, page, err := t.getSessionAndPage(in)
	if err != nil {
		return errStr(err)
	}
	if in.Key == "" {
		return "Error: key is required"
	}
	keyInfo := resolveKeyInfo(in.Key)
	err = proto.InputDispatchKeyEvent{
		Type:                  proto.InputDispatchKeyEventTypeKeyUp,
		Key:                   keyInfo.key,
		Code:                  keyInfo.code,
		WindowsVirtualKeyCode: keyInfo.keyCode,
		NativeVirtualKeyCode:  keyInfo.keyCode,
	}.Call(page)
	if err != nil {
		return fmt.Sprintf("action_key_up %q failed: %v", in.Key, err)
	}
	return fmt.Sprintf("Key up: %s", in.Key)
}

// keyInfo holds CDP key event parameters.
type keyInfo struct {
	key     string
	code    string
	keyCode int
}

// resolveKeyInfo maps a human-readable key name to CDP key event fields.
func resolveKeyInfo(name string) keyInfo {
	switch strings.ToLower(name) {
	case "shift":
		return keyInfo{"Shift", "ShiftLeft", 16}
	case "control", "ctrl":
		return keyInfo{"Control", "ControlLeft", 17}
	case "alt":
		return keyInfo{"Alt", "AltLeft", 18}
	case "meta", "command", "cmd":
		return keyInfo{"Meta", "MetaLeft", 91}
	case "enter", "return":
		return keyInfo{"Enter", "Enter", 13}
	case "tab":
		return keyInfo{"Tab", "Tab", 9}
	case "escape", "esc":
		return keyInfo{"Escape", "Escape", 27}
	case "backspace":
		return keyInfo{"Backspace", "Backspace", 8}
	case "delete":
		return keyInfo{"Delete", "Delete", 46}
	case "arrowup", "up":
		return keyInfo{"ArrowUp", "ArrowUp", 38}
	case "arrowdown", "down":
		return keyInfo{"ArrowDown", "ArrowDown", 40}
	case "arrowleft", "left":
		return keyInfo{"ArrowLeft", "ArrowLeft", 37}
	case "arrowright", "right":
		return keyInfo{"ArrowRight", "ArrowRight", 39}
	case "space":
		return keyInfo{" ", "Space", 32}
	default:
		// Single character: use its char code
		if len(name) == 1 {
			ch := rune(name[0])
			code := int(ch)
			if ch >= 'a' && ch <= 'z' {
				code = int(ch - 32) // uppercase keyCode
			}
			return keyInfo{name, "Key" + strings.ToUpper(name), code}
		}
		return keyInfo{name, name, 0}
	}
}

func (t *BrowserTool) doActionScrollAt(in *Input) string {
	_, page, err := t.getSessionAndPage(in)
	if err != nil {
		return errStr(err)
	}
	dx := in.DeltaX
	dy := in.DeltaY
	if dy == 0 {
		dy = -300
	}
	err = page.Mouse.MoveTo(proto.NewPoint(in.X, in.Y))
	if err != nil {
		return fmt.Sprintf("move failed: %v", err)
	}
	err = page.Mouse.Scroll(dx, dy, 0)
	if err != nil {
		return fmt.Sprintf("action_scroll_at failed: %v", err)
	}
	return fmt.Sprintf("Scrolled at (%.0f, %.0f) delta=(%.0f, %.0f).", in.X, in.Y, dx, dy)
}
