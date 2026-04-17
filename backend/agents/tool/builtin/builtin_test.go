package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wall-ai/ubuilding/backend/agents"
	"github.com/wall-ai/ubuilding/backend/agents/tool"
	"github.com/wall-ai/ubuilding/backend/agents/tool/webfetch"
)

func TestTools_ReturnsTwo(t *testing.T) {
	ts := Tools()
	if len(ts) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(ts))
	}
	names := ts.Names()
	if !containsString(names, "WebSearch") || !containsString(names, "WebFetch") {
		t.Errorf("expected WebSearch + WebFetch, got %v", names)
	}
}

func TestRegister_AndLookup(t *testing.T) {
	r := tool.NewRegistry()
	Register(r)
	if r.FindByName("WebSearch") == nil {
		t.Error("WebSearch not found after Register")
	}
	if r.FindByName("WebFetch") == nil {
		t.Error("WebFetch not found after Register")
	}
	if !r.IsBuiltin("WebSearch") || !r.IsBuiltin("WebFetch") {
		t.Error("Register must flag tools as builtin")
	}
}

func TestAssemble_BuiltinsArePrefix(t *testing.T) {
	r := tool.NewRegistry()
	Register(r)
	pool := tool.AssembleToolPool(r, agents.NewEmptyToolPermissionContext(), nil)
	names := pool.Names()
	if len(names) != 2 {
		t.Fatalf("expected 2 tools in pool, got %v", names)
	}
	// WebFetch < WebSearch alphabetically.
	if names[0] != "WebFetch" || names[1] != "WebSearch" {
		t.Errorf("expected [WebFetch, WebSearch], got %v", names)
	}
}

func TestWebFetch_EndToEndViaHTTPTest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<html><body><h1>Greetings</h1><p>From httptest.</p></body></html>`)
	}))
	defer srv.Close()

	r := tool.NewRegistry()
	Register(r, Options{
		WebFetchOptions: []webfetch.Option{webfetch.WithAllowLoopback()},
	})
	wf := r.FindByName("WebFetch")
	if wf == nil {
		t.Fatal("WebFetch not found")
	}
	raw, _ := json.Marshal(map[string]string{"url": srv.URL})
	res, err := wf.Call(context.Background(), raw, nil)
	if err != nil {
		t.Fatalf("Call error: %v", err)
	}
	out, ok := res.Data.(webfetch.Output)
	if !ok {
		t.Fatalf("expected webfetch.Output, got %T", res.Data)
	}
	if out.Code != 200 {
		t.Errorf("expected HTTP 200, got %d", out.Code)
	}
	if !strings.Contains(out.Result, "Greetings") {
		t.Errorf("expected 'Greetings' in result, got %q", out.Result)
	}
}

func containsString(ss []string, target string) bool {
	for _, s := range ss {
		if s == target {
			return true
		}
	}
	return false
}
