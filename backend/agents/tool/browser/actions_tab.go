package browser

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/go-rod/rod/lib/proto"
	"github.com/go-rod/stealth"
	"github.com/google/uuid"
)

func (t *BrowserTool) doNewTab(in *Input) string {
	s, _, err := t.getSessionAndPage(in)
	if err != nil {
		return errStr(err)
	}

	page, err := stealth.Page(s.browser)
	if err != nil {
		return fmt.Sprintf("new_tab failed: %v", err)
	}

	// Inject scripts
	_, _ = page.EvalOnNewDocument(antiDetectScript)
	_, _ = page.EvalOnNewDocument(consoleInitScript)

	if in.URL != "" {
		err = page.Navigate(in.URL)
		if err != nil {
			return fmt.Sprintf("new_tab navigate failed: %v", err)
		}
		_ = page.WaitLoad()
	}

	tabID := uuid.New().String()[:8]
	s.mu.Lock()
	s.pages[tabID] = page
	s.activeTab = tabID
	s.iframeCtx = nil
	s.mu.Unlock()

	go s.handleDialogs(context.Background(), page)

	url := ""
	info := safeInfo(page)
	url = info.URL

	return fmt.Sprintf("New tab created.\n  tab_id: %s\n  url: %s", tabID, url)
}

func (t *BrowserTool) doListTabs(in *Input) string {
	s, _, err := t.getSessionAndPage(in)
	if err != nil {
		return errStr(err)
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	var lines []string
	lines = append(lines, fmt.Sprintf("Tabs (%d):", len(s.pages)))
	for id, p := range s.pages {
		active := ""
		if id == s.activeTab {
			active = " [ACTIVE]"
		}
		info := safeInfo(p)
		lines = append(lines, fmt.Sprintf("  %s%s: %s — %s", id, active, truncStr(info.Title, 40), truncStr(info.URL, 60)))
	}
	return strings.Join(lines, "\n")
}

func (t *BrowserTool) doSwitchTab(in *Input) string {
	s, _, err := t.getSessionAndPage(in)
	if err != nil {
		return errStr(err)
	}
	if in.TabID == "" {
		return "Error: tab_id is required"
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	p, ok := s.pages[in.TabID]
	if !ok {
		return fmt.Sprintf("Tab %q not found. Use list_tabs.", in.TabID)
	}
	s.activeTab = in.TabID
	s.iframeCtx = nil

	_, actErr := p.Activate()
	if actErr != nil {
		return fmt.Sprintf("switch_tab activate failed: %v", actErr)
	}

	info := safeInfo(p)
	return fmt.Sprintf("Switched to tab %s.\n  URL: %s\n  Title: %s", in.TabID, info.URL, info.Title)
}

func (t *BrowserTool) doCloseTab(in *Input) string {
	s, _, err := t.getSessionAndPage(in)
	if err != nil {
		return errStr(err)
	}
	if in.TabID == "" {
		return "Error: tab_id is required"
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	p, ok := s.pages[in.TabID]
	if !ok {
		return fmt.Sprintf("Tab %q not found.", in.TabID)
	}

	_ = p.Close()
	delete(s.pages, in.TabID)

	if s.activeTab == in.TabID {
		// Switch to another tab
		for id := range s.pages {
			s.activeTab = id
			break
		}
	}

	if len(s.pages) == 0 {
		return fmt.Sprintf("Tab %s closed. No tabs remaining — session may need closing.", in.TabID)
	}
	return fmt.Sprintf("Tab %s closed. Active tab: %s", in.TabID, s.activeTab)
}

func (t *BrowserTool) doClickForNewTab(in *Input) string {
	s, page, err := t.getSessionAndPage(in)
	if err != nil {
		return errStr(err)
	}
	el, err := t.resolveElement(page, in)
	if err != nil {
		return fmt.Sprintf("Element not found: %v", err)
	}

	// Get pages before click
	s.mu.RLock()
	beforeCount := len(s.pages)
	s.mu.RUnlock()

	// Click the element
	_ = el.ScrollIntoView()
	_ = el.Click(proto.InputMouseButtonLeft, 1)

	// Wait for new page
	timeout := 10 * time.Second
	if in.Timeout > 0 {
		timeout = time.Duration(in.Timeout) * time.Millisecond
	}

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		pages, _ := s.browser.Pages()
		if len(pages) > beforeCount {
			newPage := pages[len(pages)-1]
			_ = newPage.WaitLoad()

			tabID := uuid.New().String()[:8]
			s.mu.Lock()
			s.pages[tabID] = newPage
			s.activeTab = tabID
			s.iframeCtx = nil
			s.mu.Unlock()

			go s.handleDialogs(context.Background(), newPage)
			info := safeInfo(newPage)
			return fmt.Sprintf("New tab opened via click.\n  tab_id: %s\n  URL: %s", tabID, info.URL)
		}
		time.Sleep(200 * time.Millisecond)
	}
	return "Click executed but no new tab detected within timeout."
}

func (t *BrowserTool) doClickForURLChange(in *Input) string {
	_, page, err := t.getSessionAndPage(in)
	if err != nil {
		return errStr(err)
	}
	el, err := t.resolveElement(page, in)
	if err != nil {
		return fmt.Sprintf("Element not found: %v", err)
	}

	beforeURL := safeInfo(page).URL

	_ = el.ScrollIntoView()
	_ = el.Click(proto.InputMouseButtonLeft, 1)

	timeout := 10 * time.Second
	if in.Timeout > 0 {
		timeout = time.Duration(in.Timeout) * time.Millisecond
	}

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		currentURL := safeInfo(page).URL
		if currentURL != beforeURL {
			return fmt.Sprintf("URL changed after click.\n  Before: %s\n  After: %s", beforeURL, currentURL)
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Sprintf("Click executed but URL unchanged.\n  URL: %s", beforeURL)
}
