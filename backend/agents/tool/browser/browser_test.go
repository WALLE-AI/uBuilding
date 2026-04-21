package browser

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/wall-ai/ubuilding/backend/agents/tool/cwd"
)

func TestNewBrowserTool(t *testing.T) {
	bt := New()
	if bt == nil {
		t.Fatal("New() returned nil")
	}
	if bt.Name() != "Browser" {
		t.Errorf("Name() = %q, want %q", bt.Name(), "Browser")
	}
	aliases := bt.Aliases()
	if len(aliases) == 0 || aliases[0] != "BrowserDrission" {
		t.Errorf("Aliases() = %v, want [BrowserDrission]", aliases)
	}
	if bt.Description(json.RawMessage(`{}`)) == "" {
		t.Error("Description() is empty")
	}
}

func TestInputSchema(t *testing.T) {
	bt := New()
	schema := bt.InputSchema()
	if schema == nil {
		t.Fatal("InputSchema() is nil")
	}
	if schema.Type != "object" {
		t.Errorf("schema.Type = %q, want \"object\"", schema.Type)
	}
	if _, ok := schema.Properties["action"]; !ok {
		t.Error("schema missing 'action' property")
	}
	if len(schema.Required) == 0 || schema.Required[0] != "action" {
		t.Errorf("schema.Required = %v, want [action]", schema.Required)
	}
}

func TestValidateInput(t *testing.T) {
	bt := New()

	tests := []struct {
		name      string
		input     string
		wantValid bool
	}{
		{"valid action", `{"action":"list_sessions"}`, true},
		{"empty action", `{"action":""}`, false},
		{"missing action", `{}`, false},
		{"invalid json", `{bad}`, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := bt.ValidateInput(json.RawMessage(tc.input), nil)
			if result.Valid != tc.wantValid {
				t.Errorf("ValidateInput(%s).Valid = %v, want %v", tc.input, result.Valid, tc.wantValid)
			}
		})
	}
}

func TestLocatorResolve(t *testing.T) {
	tests := []struct {
		input    string
		strategy LocatorStrategy
		contains string
	}{
		{"#myid", StrategyCSS, "#myid"},
		{".myclass", StrategyCSS, ".myclass"},
		{"css=div > span", StrategyCSS, "div > span"},
		{"c=.foo", StrategyCSS, ".foo"},
		{"//div[@id='x']", StrategyXPath, "//div[@id='x']"},
		{"xpath=//a", StrategyXPath, "//a"},
		{"x=//span", StrategyXPath, "//span"},
		{"text=Login", StrategyXPath, "text()"},
		{"text:Search", StrategyXPath, "contains"},
		{"text^Start", StrategyXPath, "starts-with"},
		{"text$End", StrategyXPath, "substring"},
		{"@href=/api", StrategyXPath, "@href"},
		{"@@class=btn@@type=submit", StrategyXPath, "and"},
		{"@|class=btn@@type=submit", StrategyXPath, "or"},
		{"@!disabled", StrategyXPath, "not"},
		{"tag:button@type=submit", StrategyXPath, "//button"},
		{"plain text", StrategyXPath, "contains"},
		{"", StrategyCSS, "*"},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			res := Resolve(tc.input)
			if res.Strategy != tc.strategy {
				t.Errorf("Resolve(%q).Strategy = %d, want %d", tc.input, res.Strategy, tc.strategy)
			}
			if tc.contains != "" && !containsStr(res.Value, tc.contains) {
				t.Errorf("Resolve(%q).Value = %q, want it to contain %q", tc.input, res.Value, tc.contains)
			}
		})
	}
}

func containsStr(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && containsSubstr(s, sub))
}

func containsSubstr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestXPathQuote(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"hello", "'hello'"},
		{"it's", `"it's"`},
		{`say "hi"`, `'say "hi"'`},
	}
	for _, tc := range tests {
		got := xpathQuote(tc.input)
		if got != tc.want {
			t.Errorf("xpathQuote(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestDispatchUnknownAction(t *testing.T) {
	bt := New()
	in := &Input{Action: "nonexistent_action_xyz"}
	result := bt.dispatch(context.Background(), in)
	if !containsSubstr(result, "Unknown action") {
		t.Errorf("dispatch unknown action returned %q, want 'Unknown action' prefix", result)
	}
}

func TestDescription(t *testing.T) {
	bt := New()
	tests := []struct {
		input json.RawMessage
		want  string
	}{
		{json.RawMessage(`{"action":"navigate","url":"https://example.com"}`), "Navigating to https://example.com"},
		{json.RawMessage(`{"action":"screenshot"}`), "Taking screenshot"},
		{json.RawMessage(`{"action":"smart_click","locator":"#btn"}`), "Clicking #btn"},
		{json.RawMessage(`{"action":"list_sessions"}`), "Browser: list_sessions"},
	}
	for _, tc := range tests {
		got := bt.Description(tc.input)
		if got != tc.want {
			t.Errorf("Description(%s) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestNetworkListenerInit(t *testing.T) {
	// Can't fully test without a real page, but ensure constructor works
	nl := NewNetworkListener(nil)
	if nl == nil {
		t.Fatal("NewNetworkListener returned nil")
	}
	if nl.maxPackets != 500 {
		t.Errorf("maxPackets = %d, want 500", nl.maxPackets)
	}
	if nl.IsActive() {
		t.Error("new listener should not be active")
	}
	if nl.Count() != 0 {
		t.Error("new listener should have 0 packets")
	}
}

func TestSessionManagerInit(t *testing.T) {
	// Verify the global manager is accessible
	mgr := getManager()
	if mgr == nil {
		t.Fatal("getManager() returned nil")
	}
	list := mgr.ListSessions()
	if len(list) != 0 {
		t.Errorf("fresh manager has %d sessions, want 0", len(list))
	}
}

// TestSchemaDispatchConsistency verifies every action in the old JSON Schema enum
// has a corresponding dispatch case (not returning "Unknown action").
// Skips create_session since it launches a real browser.
func TestSchemaDispatchConsistency(t *testing.T) {
	// Use the raw inputSchema() (still available for tests) to get the full enum.
	schema := inputSchema()
	var parsed struct {
		Properties struct {
			Action struct {
				Enum []string `json:"enum"`
			} `json:"action"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(schema, &parsed); err != nil {
		t.Fatalf("schema parse error: %v", err)
	}

	// Actions that launch real browsers or have side effects
	skip := map[string]bool{"create_session": true}

	bt := New()
	for _, action := range parsed.Properties.Action.Enum {
		if skip[action] {
			continue
		}
		in := &Input{Action: action}
		result := bt.dispatch(context.Background(), in)
		if containsSubstr(result, "Unknown action") {
			t.Errorf("action %q is in schema enum but has no dispatch case", action)
		}
	}
}

func TestMatchRoutePattern(t *testing.T) {
	tests := []struct {
		pattern string
		url     string
		want    bool
	}{
		{"*", "https://example.com/foo", true},
		{"*api*", "https://example.com/api/v1", true},
		{"*api*", "https://example.com/static/main.js", false},
		{"*.js", "https://cdn.com/bundle.js", true},
		{"*.js", "https://cdn.com/bundle.css", false},
		{"https://example.com/*", "https://example.com/path", true},
		{"https://example.com/*", "https://other.com/path", false},
		{"analytics", "https://example.com/analytics/track", true},
	}
	for _, tc := range tests {
		got := matchRoutePattern(tc.pattern, tc.url)
		if got != tc.want {
			t.Errorf("matchRoutePattern(%q, %q) = %v, want %v", tc.pattern, tc.url, got, tc.want)
		}
	}
}

func TestResolveKeyInfo(t *testing.T) {
	tests := []struct {
		name        string
		wantKey     string
		wantCode    string
		wantKeyCode int
	}{
		{"shift", "Shift", "ShiftLeft", 16},
		{"ctrl", "Control", "ControlLeft", 17},
		{"Enter", "Enter", "Enter", 13},
		{"a", "a", "KeyA", 65},
	}
	for _, tc := range tests {
		ki := resolveKeyInfo(tc.name)
		if ki.key != tc.wantKey {
			t.Errorf("resolveKeyInfo(%q).key = %q, want %q", tc.name, ki.key, tc.wantKey)
		}
		if ki.code != tc.wantCode {
			t.Errorf("resolveKeyInfo(%q).code = %q, want %q", tc.name, ki.code, tc.wantCode)
		}
		if ki.keyCode != tc.wantKeyCode {
			t.Errorf("resolveKeyInfo(%q).keyCode = %d, want %d", tc.name, ki.keyCode, tc.wantKeyCode)
		}
	}
}

func TestCleanupIdleEmpty(t *testing.T) {
	mgr := NewSessionManager(5, 30*60*1e9) // 30min
	// Should not panic on empty sessions
	mgr.cleanupIdle()
	if len(mgr.ListSessions()) != 0 {
		t.Error("expected 0 sessions after cleanupIdle on empty manager")
	}
}

func TestResolveWorkspacePath(t *testing.T) {
	// Save and restore global cwd state.
	oldCwd := cwd.Get()
	defer cwd.Set(oldCwd)

	var ws string
	if runtime.GOOS == "windows" {
		ws = `C:\Users\test\workspace`
	} else {
		ws = "/home/test/workspace"
	}

	tests := []struct {
		name string
		cwd  string
		path string
		want string
	}{
		{"empty path", ws, "", ""},
		{"absolute path unchanged", ws, filepath.Join(ws, "file.png"), filepath.Join(ws, "file.png")},
		{"relative resolved to workspace", ws, filepath.Join("screenshots", "shot.png"), filepath.Join(ws, "screenshots", "shot.png")},
		{"cwd empty returns relative as-is", "", filepath.Join("screenshots", "shot.png"), filepath.Join("screenshots", "shot.png")},
		{"bare filename resolved", ws, "file.png", filepath.Join(ws, "file.png")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cwd.Set(tt.cwd)
			got := resolveWorkspacePath(tt.path)
			if got != tt.want {
				t.Errorf("resolveWorkspacePath(%q) = %q, want %q (cwd=%q)", tt.path, got, tt.want, tt.cwd)
			}
		})
	}
}

func TestAutoScreenshotPath(t *testing.T) {
	oldCwd := cwd.Get()
	defer cwd.Set(oldCwd)

	t.Run("returns empty when workspace not set", func(t *testing.T) {
		cwd.Set("")
		got := autoScreenshotPath("png")
		if got != "" {
			t.Errorf("expected empty, got %q", got)
		}
	})

	t.Run("generates path inside workspace/screenshots", func(t *testing.T) {
		ws := t.TempDir()
		cwd.Set(ws)
		got := autoScreenshotPath("png")
		if got == "" {
			t.Fatal("expected non-empty path")
		}
		if !filepath.IsAbs(got) {
			t.Errorf("expected absolute path, got %q", got)
		}
		if filepath.Base(filepath.Dir(got)) != "screenshots" {
			t.Errorf("expected parent dir 'screenshots', got %q", filepath.Base(filepath.Dir(got)))
		}
		if filepath.Ext(got) != ".png" {
			t.Errorf("expected .png extension, got %q", filepath.Ext(got))
		}
	})

	t.Run("uses jpg extension", func(t *testing.T) {
		ws := t.TempDir()
		cwd.Set(ws)
		got := autoScreenshotPath("jpg")
		if filepath.Ext(got) != ".jpg" {
			t.Errorf("expected .jpg extension, got %q", filepath.Ext(got))
		}
	})
}

func TestSaveBase64Action(t *testing.T) {
	oldCwd := cwd.Get()
	defer cwd.Set(oldCwd)

	ws := t.TempDir()
	cwd.Set(ws)

	bt := New()
	ctx := context.Background()

	t.Run("saves decoded bytes to workspace-relative path", func(t *testing.T) {
		payload := []byte("hello browser test")
		b64 := base64.StdEncoding.EncodeToString(payload)

		input := map[string]interface{}{
			"action":    "save_base64",
			"data":      b64,
			"save_path": "out/test.bin",
		}
		raw, _ := json.Marshal(input)
		result, err := bt.Call(ctx, raw, nil)
		if err != nil {
			t.Fatalf("save_base64 call failed: %v", err)
		}
		text := result.Data.(string)
		t.Logf("save_base64 result: %s", text)

		dest := filepath.Join(ws, "out", "test.bin")
		got, readErr := os.ReadFile(dest)
		if readErr != nil {
			t.Fatalf("file not found at %s: %v", dest, readErr)
		}
		if string(got) != string(payload) {
			t.Errorf("content mismatch: got %q, want %q", got, payload)
		}
	})

	t.Run("returns error when data is empty", func(t *testing.T) {
		input := map[string]interface{}{
			"action":    "save_base64",
			"data":      "",
			"save_path": "out/x.bin",
		}
		raw, _ := json.Marshal(input)
		result, _ := bt.Call(ctx, raw, nil)
		text := result.Data.(string)
		if len(text) < 5 || text[:5] != "Error" {
			t.Errorf("expected Error response, got %q", text)
		}
	})

	t.Run("returns error when save_path missing", func(t *testing.T) {
		input := map[string]interface{}{
			"action": "save_base64",
			"data":   base64.StdEncoding.EncodeToString([]byte("x")),
		}
		raw, _ := json.Marshal(input)
		result, _ := bt.Call(ctx, raw, nil)
		text := result.Data.(string)
		if len(text) < 5 || text[:5] != "Error" {
			t.Errorf("expected Error response, got %q", text)
		}
	})
}
