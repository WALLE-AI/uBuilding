package browser

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wall-ai/ubuilding/backend/agents/tool/cwd"
)

// TestBrowserOpenBaiduAndScreenshot is an integration test that:
//  1. Sets workspace via cwd.Set to a temp directory
//  2. Creates a browser session (headless)
//  3. Navigates to https://www.baidu.com
//  4. Waits for the page to load
//  5. Takes a full-page screenshot using a RELATIVE path — verifies it resolves into the workspace
//  6. Closes the session
//
// Run with:
//
//	go test ./agents/tool/browser/... -run TestBrowserOpenBaiduAndScreenshot -count=1 -v
//
// Skip in CI with: -short flag
func TestBrowserOpenBaiduAndScreenshot(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}

	// Set workspace to a temp directory so relative paths resolve there.
	workspaceDir := t.TempDir()
	oldCwd := cwd.Get()
	cwd.Set(workspaceDir)
	defer cwd.Set(oldCwd)

	bt := New()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Use a relative path — should resolve to workspaceDir/screenshots/baidu_screenshot.png
	relScreenshotPath := filepath.Join("screenshots", "baidu_screenshot.png")
	expectedAbsPath := filepath.Join(workspaceDir, relScreenshotPath)

	// --- Step 1: Create session (headless) ---
	createInput := map[string]interface{}{
		"action":   "create_session",
		"headless": true,
	}
	createJSON, _ := json.Marshal(createInput)

	result, err := bt.Call(ctx, createJSON, nil)
	if err != nil {
		t.Fatalf("create_session failed: %v", err)
	}
	resultText, _ := result.Data.(string)
	t.Logf("create_session: %s", resultText)

	// Extract session_id from result text (format: "session_id: <id>")
	var sessionID string
	for _, line := range strings.Split(resultText, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "session_id:") {
			sessionID = strings.TrimSpace(strings.TrimPrefix(line, "session_id:"))
			break
		}
	}
	if sessionID == "" {
		t.Fatal("could not extract session_id from create_session response")
	}
	t.Logf("extracted session_id: %s", sessionID)

	// --- Step 2: Navigate to Baidu ---
	navInput := map[string]interface{}{
		"action": "navigate",
		"url":    "https://www.baidu.com",
	}
	navJSON, _ := json.Marshal(navInput)

	result, err = bt.Call(ctx, navJSON, nil)
	if err != nil {
		t.Fatalf("navigate failed: %v", err)
	}
	resultText, _ = result.Data.(string)
	t.Logf("navigate: %s", resultText)

	// --- Step 3: Wait for page to load via wait_for_title ---
	waitInput := map[string]interface{}{
		"action":       "wait_for_title",
		"wait_pattern": "百度",
		"timeout":      15000,
	}
	waitJSON, _ := json.Marshal(waitInput)

	result, err = bt.Call(ctx, waitJSON, nil)
	if err != nil {
		t.Fatalf("wait_for_title failed: %v", err)
	}
	resultText, _ = result.Data.(string)
	t.Logf("wait_for_title: %s", resultText)

	// --- Step 4: Screenshot with RELATIVE path (workspace-aware) ---
	shotInput := map[string]interface{}{
		"action":          "screenshot",
		"full_page":       true,
		"format":          "png",
		"screenshot_path": relScreenshotPath,
	}
	shotJSON, _ := json.Marshal(shotInput)

	result, err = bt.Call(ctx, shotJSON, nil)
	if err != nil {
		t.Fatalf("screenshot failed: %v", err)
	}
	resultText, _ = result.Data.(string)
	t.Logf("screenshot: %s", resultText)

	// Verify the file exists at the workspace-resolved absolute path
	info, statErr := os.Stat(expectedAbsPath)
	if statErr != nil {
		t.Fatalf("screenshot file not found at %s: %v", expectedAbsPath, statErr)
	}
	if info.Size() == 0 {
		t.Fatal("screenshot file is empty")
	}
	t.Logf("screenshot saved: %s (%d bytes)", expectedAbsPath, info.Size())

	// --- Step 5: Close session ---
	closeInput := map[string]interface{}{
		"action": "list_sessions",
	}
	closeJSON, _ := json.Marshal(closeInput)

	result, err = bt.Call(ctx, closeJSON, nil)
	if err != nil {
		t.Fatalf("list_sessions failed: %v", err)
	}
	resultText, _ = result.Data.(string)
	t.Logf("list_sessions: %s", resultText)

	// Close the session by session_id
	closeAllInput := map[string]interface{}{
		"action":     "close_session",
		"session_id": sessionID,
	}
	closeAllJSON, _ := json.Marshal(closeAllInput)

	result, err = bt.Call(ctx, closeAllJSON, nil)
	if err != nil {
		t.Fatalf("close_session failed: %v", err)
	}
	resultText, _ = result.Data.(string)
	t.Logf("close_session: %s", resultText)

	t.Log("=== Browser integration test PASSED ===")
}
