package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/wall-ai/ubuilding/backend/agents"
)

// fakeRegistry is a table-driven McpResourceRegistry for tests.
type fakeRegistry struct {
	servers   []string
	resources map[string][]agents.McpResource
	contents  map[string][]agents.McpResourceContent // keyed by "server|uri"
	readErr   error
}

func (f *fakeRegistry) ListServers() []string { return f.servers }

func (f *fakeRegistry) ListResources(_ context.Context, server string) ([]agents.McpResource, error) {
	if server != "" {
		return f.resources[server], nil
	}
	var all []agents.McpResource
	for _, s := range f.servers {
		all = append(all, f.resources[s]...)
	}
	return all, nil
}

func (f *fakeRegistry) ReadResource(_ context.Context, server, uri string) ([]agents.McpResourceContent, error) {
	if f.readErr != nil {
		return nil, f.readErr
	}
	return f.contents[server+"|"+uri], nil
}

func newFake() *fakeRegistry {
	return &fakeRegistry{
		servers: []string{"alpha", "beta"},
		resources: map[string][]agents.McpResource{
			"alpha": {{URI: "file://a1", Name: "a1", Server: "alpha"}},
			"beta":  {{URI: "file://b1", Name: "b1", Server: "beta"}},
		},
		contents: map[string][]agents.McpResourceContent{
			"alpha|file://a1": {{URI: "file://a1", Text: "hello"}},
		},
	}
}

func ctxWith(reg agents.McpResourceRegistry) *agents.ToolUseContext {
	return &agents.ToolUseContext{Ctx: context.Background(), McpResources: reg}
}

// ── ListMcpResources ──────────────────────────────────────────────────────

func TestList_AllServers(t *testing.T) {
	lt := NewListTool()
	raw, _ := json.Marshal(ListInput{})
	res, err := lt.Call(context.Background(), raw, ctxWith(newFake()))
	if err != nil {
		t.Fatal(err)
	}
	got := res.Data.([]agents.McpResource)
	if len(got) != 2 {
		t.Fatalf("len=%d, want 2", len(got))
	}
}

func TestList_FilterServer(t *testing.T) {
	lt := NewListTool()
	raw, _ := json.Marshal(ListInput{Server: "alpha"})
	res, err := lt.Call(context.Background(), raw, ctxWith(newFake()))
	if err != nil {
		t.Fatal(err)
	}
	got := res.Data.([]agents.McpResource)
	if len(got) != 1 || got[0].Server != "alpha" {
		t.Fatalf("got=%+v", got)
	}
}

func TestList_UnknownServerErrors(t *testing.T) {
	lt := NewListTool()
	raw, _ := json.Marshal(ListInput{Server: "zeta"})
	_, err := lt.Call(context.Background(), raw, ctxWith(newFake()))
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("want not-found error, got %v", err)
	}
}

func TestList_NoRegistry(t *testing.T) {
	lt := NewListTool()
	raw, _ := json.Marshal(ListInput{})
	_, err := lt.Call(context.Background(), raw, &agents.ToolUseContext{Ctx: context.Background()})
	if err == nil {
		t.Fatal("expected registry-missing error")
	}
}

func TestList_RenderEmpty(t *testing.T) {
	lt := NewListTool()
	cb := lt.MapToolResultToParam([]agents.McpResource{}, "id1")
	if s, _ := cb.Content.(string); !strings.Contains(s, "No resources found") {
		t.Fatalf("content=%v", cb.Content)
	}
}

func TestList_RenderJSON(t *testing.T) {
	lt := NewListTool()
	cb := lt.MapToolResultToParam([]agents.McpResource{{URI: "u", Server: "s"}}, "id1")
	if s, _ := cb.Content.(string); !strings.Contains(s, `"uri":"u"`) {
		t.Fatalf("content=%v", cb.Content)
	}
}

// ── ReadMcpResource ───────────────────────────────────────────────────────

func TestRead_Happy(t *testing.T) {
	rt := NewReadTool()
	raw, _ := json.Marshal(ReadInput{Server: "alpha", URI: "file://a1"})
	res, err := rt.Call(context.Background(), raw, ctxWith(newFake()))
	if err != nil {
		t.Fatal(err)
	}
	got := res.Data.([]agents.McpResourceContent)
	if len(got) != 1 || got[0].Text != "hello" {
		t.Fatalf("got=%+v", got)
	}
}

func TestRead_Validation(t *testing.T) {
	rt := NewReadTool()
	if v := rt.ValidateInput(json.RawMessage(`{"server":"","uri":"u"}`), nil); v.Valid {
		t.Fatal("empty server must be invalid")
	}
	if v := rt.ValidateInput(json.RawMessage(`{"server":"s","uri":""}`), nil); v.Valid {
		t.Fatal("empty uri must be invalid")
	}
}

func TestRead_UnknownServerErrors(t *testing.T) {
	rt := NewReadTool()
	raw, _ := json.Marshal(ReadInput{Server: "zeta", URI: "u"})
	_, err := rt.Call(context.Background(), raw, ctxWith(newFake()))
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("want not-found, got %v", err)
	}
}

func TestRead_PropagatesRegistryError(t *testing.T) {
	reg := newFake()
	reg.readErr = errors.New("boom")
	rt := NewReadTool()
	raw, _ := json.Marshal(ReadInput{Server: "alpha", URI: "file://a1"})
	_, err := rt.Call(context.Background(), raw, ctxWith(reg))
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("want boom, got %v", err)
	}
}

func TestRead_NoRegistry(t *testing.T) {
	rt := NewReadTool()
	raw, _ := json.Marshal(ReadInput{Server: "x", URI: "y"})
	_, err := rt.Call(context.Background(), raw, &agents.ToolUseContext{Ctx: context.Background()})
	if err == nil {
		t.Fatal("expected registry-missing error")
	}
}
