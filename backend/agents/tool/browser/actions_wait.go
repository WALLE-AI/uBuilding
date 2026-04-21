package browser

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/go-rod/rod/lib/proto"
)

func (t *BrowserTool) doWaitForElement(in *Input) string {
	_, page, err := t.getSessionAndPage(in)
	if err != nil {
		return errStr(err)
	}
	if in.Locator == "" {
		return "Error: locator is required"
	}
	state := in.WaitState
	if state == "" {
		state = "visible"
	}
	timeout := 30 * time.Second
	if in.Timeout > 0 {
		timeout = time.Duration(in.Timeout) * time.Millisecond
	}

	el, err := t.waitForElementState(page, in.Locator, state, timeout)
	if err != nil {
		return fmt.Sprintf("wait_for_element timed out: %v\nLocator: %s\nState: %s", err, in.Locator, state)
	}
	if el == nil {
		return fmt.Sprintf("Element is now %s. Locator: %s", state, in.Locator)
	}
	return fmt.Sprintf("Element reached state %q.\n%s", state, briefElement(el))
}

func (t *BrowserTool) doWaitForURL(in *Input) string {
	_, page, err := t.getSessionAndPage(in)
	if err != nil {
		return errStr(err)
	}
	pattern := in.WaitPattern
	if pattern == "" {
		pattern = in.URL
	}
	if pattern == "" {
		return "Error: wait_pattern or url is required"
	}
	timeout := 30 * time.Second
	if in.Timeout > 0 {
		timeout = time.Duration(in.Timeout) * time.Millisecond
	}

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		currentURL := safeInfo(page).URL
		matches := strings.Contains(currentURL, pattern)
		if in.WaitExclude {
			matches = !matches
		}
		if matches {
			return fmt.Sprintf("URL condition met.\n  Current URL: %s\n  Pattern: %s", currentURL, pattern)
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Sprintf("wait_for_url timed out.\n  Pattern: %s\n  Current URL: %s", pattern, safeInfo(page).URL)
}

func (t *BrowserTool) doWaitForTitle(in *Input) string {
	_, page, err := t.getSessionAndPage(in)
	if err != nil {
		return errStr(err)
	}
	pattern := in.WaitPattern
	if pattern == "" {
		return "Error: wait_pattern is required"
	}
	timeout := 30 * time.Second
	if in.Timeout > 0 {
		timeout = time.Duration(in.Timeout) * time.Millisecond
	}

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		title := safeInfo(page).Title
		matches := strings.Contains(title, pattern)
		if in.WaitExclude {
			matches = !matches
		}
		if matches {
			return fmt.Sprintf("Title condition met.\n  Title: %s\n  Pattern: %s", title, pattern)
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Sprintf("wait_for_title timed out. Pattern: %s", pattern)
}

func (t *BrowserTool) doWaitForNetworkIdle(in *Input) string {
	_, page, err := t.getSessionAndPage(in)
	if err != nil {
		return errStr(err)
	}
	timeout := 30 * time.Second
	if in.Timeout > 0 {
		timeout = time.Duration(in.Timeout) * time.Millisecond
	}

	// Use DOM stability as a proxy for network idle.
	// WaitDOMStable waits until the DOM tree has no mutations for the given interval.
	err = page.Timeout(timeout).WaitDOMStable(500*time.Millisecond, 0.1)
	if err != nil {
		return fmt.Sprintf("wait_for_network_idle timed out: %v", err)
	}

	return "Network is idle (DOM stable)."
}

func (t *BrowserTool) doWaitForAlert(in *Input) string {
	s, page, err := t.getSessionAndPage(in)
	if err != nil {
		return errStr(err)
	}
	timeout := 30 * time.Second
	if in.Timeout > 0 {
		timeout = time.Duration(in.Timeout) * time.Millisecond
	}

	// Wait for dialog event
	wait := page.EachEvent(func(e *proto.PageJavascriptDialogOpening) bool {
		s.mu.Lock()
		s.alertText = e.Message
		s.alertType = string(e.Type)
		s.mu.Unlock()
		return true
	})

	done := make(chan struct{})
	go func() {
		wait()
		close(done)
	}()

	select {
	case <-done:
		s.mu.RLock()
		text := s.alertText
		typ := s.alertType
		s.mu.RUnlock()
		return fmt.Sprintf("Alert detected.\n  Type: %s\n  Text: %s", typ, text)
	case <-time.After(timeout):
		return "wait_for_alert timed out. No dialog appeared."
	}
}

func (t *BrowserTool) doWaitForAnyElement(in *Input) string {
	_, page, err := t.getSessionAndPage(in)
	if err != nil {
		return errStr(err)
	}
	if len(in.WaitMultiple) == 0 {
		return "Error: wait_multiple is required (list of locators)"
	}
	timeout := 30 * time.Second
	if in.Timeout > 0 {
		timeout = time.Duration(in.Timeout) * time.Millisecond
	}

	type result struct {
		index   int
		locator string
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	ch := make(chan result, len(in.WaitMultiple))
	for i, loc := range in.WaitMultiple {
		go func(idx int, l string) {
			resolved := Resolve(l)
			p := page.Context(ctx)
			var findErr error
			switch resolved.Strategy {
			case StrategyXPath:
				_, findErr = p.ElementX(resolved.Value)
			default:
				_, findErr = p.Element(resolved.Value)
			}
			if findErr == nil {
				select {
				case ch <- result{index: idx, locator: l}:
				case <-ctx.Done():
				}
			}
		}(i, loc)
	}

	select {
	case r := <-ch:
		return fmt.Sprintf("Element found (index %d).\n  Locator: %s", r.index, r.locator)
	case <-ctx.Done():
		return "wait_for_any_element timed out. None of the locators matched."
	}
}
