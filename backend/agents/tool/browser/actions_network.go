package browser

import (
	"fmt"
	"strings"
	"time"

	"github.com/go-rod/rod/lib/proto"
)

func (t *BrowserTool) doNetworkListenStart(in *Input) string {
	s, page, err := t.getSessionAndPage(in)
	if err != nil {
		return errStr(err)
	}
	if s.networkListener != nil && s.networkListener.IsActive() {
		return "Network listener already active. Stop it first or use network_listen_get."
	}
	nl := NewNetworkListener(page)
	nl.Start(in.ListenTargets, in.ListenIsRegex, in.ListenMethods, in.ListenTypes)
	s.mu.Lock()
	s.networkListener = nl
	s.mu.Unlock()

	desc := "all traffic"
	if len(in.ListenTargets) > 0 {
		desc = fmt.Sprintf("targets=%v", in.ListenTargets)
	}
	return fmt.Sprintf("Network listener started. Capturing %s.", desc)
}

func (t *BrowserTool) doNetworkListenWait(in *Input) string {
	s, _, err := t.getSessionAndPage(in)
	if err != nil {
		return errStr(err)
	}
	s.mu.RLock()
	nl := s.networkListener
	s.mu.RUnlock()
	if nl == nil || !nl.IsActive() {
		return "Error: no active network listener. Call network_listen_start first."
	}

	count := in.ListenCount
	if count <= 0 {
		count = 1
	}
	timeout := 30 * time.Second
	if in.Timeout > 0 {
		timeout = time.Duration(in.Timeout) * time.Millisecond
	}

	packets := nl.WaitForCount(count, timeout)
	return formatPackets(packets)
}

func (t *BrowserTool) doNetworkListenGet(in *Input) string {
	s, _, err := t.getSessionAndPage(in)
	if err != nil {
		return errStr(err)
	}
	s.mu.RLock()
	nl := s.networkListener
	s.mu.RUnlock()
	if nl == nil {
		return "Error: no network listener. Call network_listen_start first."
	}

	max := in.MaxResults
	if max <= 0 {
		max = 0
	}
	packets := nl.GetAll(max)
	if len(packets) == 0 {
		return "No packets captured yet."
	}
	return formatPackets(packets)
}

func (t *BrowserTool) doNetworkListenStop(in *Input) string {
	s, _, err := t.getSessionAndPage(in)
	if err != nil {
		return errStr(err)
	}
	s.mu.Lock()
	nl := s.networkListener
	if nl != nil {
		nl.Stop()
	}
	s.mu.Unlock()

	count := 0
	if nl != nil {
		count = nl.Count()
	}
	return fmt.Sprintf("Network listener stopped. %d packet(s) captured.", count)
}

func (t *BrowserTool) doNetworkListenClear(in *Input) string {
	s, _, err := t.getSessionAndPage(in)
	if err != nil {
		return errStr(err)
	}
	s.mu.RLock()
	nl := s.networkListener
	s.mu.RUnlock()
	if nl != nil {
		nl.Clear()
	}
	return "Network listener buffer cleared."
}

func (t *BrowserTool) doNetworkListenSteps(in *Input) string {
	s, page, err := t.getSessionAndPage(in)
	if err != nil {
		return errStr(err)
	}

	// Start listener if not active
	nl := s.networkListener
	if nl == nil || !nl.IsActive() {
		nl = NewNetworkListener(page)
		nl.Start(in.ListenTargets, in.ListenIsRegex, in.ListenMethods, in.ListenTypes)
		s.mu.Lock()
		s.networkListener = nl
		s.mu.Unlock()
	}

	count := in.ListenCount
	if count <= 0 {
		count = 1
	}
	timeout := 30 * time.Second
	if in.Timeout > 0 {
		timeout = time.Duration(in.Timeout) * time.Millisecond
	}

	packets := nl.WaitForCount(count, timeout)
	nl.Stop()

	return formatPackets(packets)
}

// --- Dialog actions ---

func (t *BrowserTool) doHandleAlert(in *Input) string {
	s, page, err := t.getSessionAndPage(in)
	if err != nil {
		return errStr(err)
	}
	accept := in.AlertAction != "dismiss"
	promptText := in.AlertText

	s.mu.RLock()
	alertType := s.alertType
	alertMsg := s.alertText
	s.mu.RUnlock()

	err = proto.PageHandleJavaScriptDialog{
		Accept:     accept,
		PromptText: promptText,
	}.Call(page)
	if err != nil {
		return fmt.Sprintf("handle_alert failed: %v (no pending dialog?)", err)
	}

	action := "accepted"
	if !accept {
		action = "dismissed"
	}
	return fmt.Sprintf("Dialog %s.\n  Type: %s\n  Message: %s", action, alertType, alertMsg)
}

func (t *BrowserTool) doGetAlertText(in *Input) string {
	s, _, err := t.getSessionAndPage(in)
	if err != nil {
		return errStr(err)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.alertText == "" {
		return "No alert text captured."
	}
	return fmt.Sprintf("Alert type: %s\nText: %s", s.alertType, s.alertText)
}

func (t *BrowserTool) doSetAutoAlert(in *Input) string {
	s, _, err := t.getSessionAndPage(in)
	if err != nil {
		return errStr(err)
	}
	val := true
	if in.AutoAlert != nil {
		val = *in.AutoAlert
	}
	s.mu.Lock()
	s.autoAlert = val
	s.mu.Unlock()
	return fmt.Sprintf("Auto-alert set to %v.", val)
}

// --- IFrame actions ---

func (t *BrowserTool) doListIframes(in *Input) string {
	_, page, err := t.getSessionAndPage(in)
	if err != nil {
		return errStr(err)
	}

	frames, elemErr := page.Elements("iframe")
	if elemErr != nil {
		return fmt.Sprintf("list_iframes failed: %v", elemErr)
	}
	if len(frames) == 0 {
		return "No iframes found on page."
	}

	var lines []string
	lines = append(lines, fmt.Sprintf("Found %d iframe(s):", len(frames)))
	for i, f := range frames {
		src, _ := f.Attribute("src")
		name, _ := f.Attribute("name")
		id, _ := f.Attribute("id")
		srcStr := ""
		if src != nil {
			srcStr = *src
		}
		nameStr := ""
		if name != nil {
			nameStr = *name
		}
		idStr := ""
		if id != nil {
			idStr = *id
		}
		lines = append(lines, fmt.Sprintf("  [%d] id=%q name=%q src=%q", i, idStr, nameStr, truncStr(srcStr, 80)))
	}
	return strings.Join(lines, "\n")
}

func (t *BrowserTool) doEnterIframe(in *Input) string {
	s, page, err := t.getSessionAndPage(in)
	if err != nil {
		return errStr(err)
	}
	selector := in.IframeSelector
	if selector == "" {
		selector = "iframe"
	}

	el, err := page.Element(selector)
	if err != nil {
		return fmt.Sprintf("iframe not found: %v", err)
	}
	frame, err := el.Frame()
	if err != nil {
		return fmt.Sprintf("enter_iframe failed: %v", err)
	}

	s.mu.Lock()
	s.iframeCtx = frame
	s.mu.Unlock()

	return fmt.Sprintf("Entered iframe: %s", selector)
}

func (t *BrowserTool) doExitIframe(in *Input) string {
	s, _, err := t.getSessionAndPage(in)
	if err != nil {
		return errStr(err)
	}
	s.mu.Lock()
	s.iframeCtx = nil
	s.mu.Unlock()
	return "Exited iframe. Now targeting main page."
}

// formatPackets formats captured network packets for output.
func formatPackets(packets []*DataPacket) string {
	if len(packets) == 0 {
		return "No packets captured."
	}
	var lines []string
	lines = append(lines, fmt.Sprintf("Captured %d packet(s):", len(packets)))
	for i, p := range packets {
		if i >= 20 {
			lines = append(lines, fmt.Sprintf("  ... and %d more", len(packets)-20))
			break
		}
		lines = append(lines, fmt.Sprintf("  [%d] %s %s → %d (%s)",
			i, p.Method, truncStr(p.URL, 80), p.Status, p.ResourceType))
		if p.ResponseBody != "" {
			body := p.ResponseBody
			if len(body) > 200 {
				body = body[:200] + "..."
			}
			lines = append(lines, fmt.Sprintf("       body: %s", body))
		}
	}
	return strings.Join(lines, "\n")
}
