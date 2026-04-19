package brief

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wall-ai/ubuilding/backend/agents"
	"github.com/wall-ai/ubuilding/backend/agents/tool"
)

type stubTool struct {
	tool.ToolDefaults
	name    string
	aliases []string
}

func (s *stubTool) Name() string                         { return s.name }
func (s *stubTool) Aliases() []string                    { return s.aliases }
func (s *stubTool) InputSchema() *tool.JSONSchema        { return &tool.JSONSchema{Type: "object"} }
func (s *stubTool) Description(_ json.RawMessage) string { return "" }
func (s *stubTool) Prompt(_ tool.PromptOptions) string   { return "" }
func (s *stubTool) Call(_ context.Context, _ json.RawMessage, _ *agents.ToolUseContext) (*tool.ToolResult, error) {
	return nil, nil
}
func (s *stubTool) MapToolResultToParam(_ interface{}, _ string) *agents.ContentBlock {
	return nil
}

func TestBrief_PromptKeywords(t *testing.T) {
	p := New().Prompt(tool.PromptOptions{})
	for _, want := range []string{
		"Send a message the user will read",
		"`message` supports markdown",
		"`attachments` takes file paths",
		"'normal'",
		"'proactive'",
		"downstream routing uses it",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("Brief.Prompt missing %q", want)
		}
	}
}

func TestBrief_ProactiveSectionKeywords(t *testing.T) {
	sec := ProactiveSection(tool.PromptOptions{})
	for _, want := range []string{
		"## Talking to the user",
		Name,
		"Even for \"hi\"",
		"ack → work → result",
		"Second person always",
	} {
		if !strings.Contains(sec, want) {
			t.Errorf("ProactiveSection missing %q", want)
		}
	}
}

func TestBrief_ProactiveSectionUsesAliasedName(t *testing.T) {
	custom := &stubTool{name: "CustomBrief", aliases: []string{Name}}
	sec := ProactiveSection(tool.PromptOptions{Tools: []tool.Tool{custom}})
	if !strings.Contains(sec, "CustomBrief is where your replies go") {
		t.Errorf("ProactiveSection did not honour aliased tool name; got:\n%s", sec)
	}
}

// fakeResolver returns a canned attachment list.
type fakeResolver struct{ atts []agents.BriefAttachment }

func (f fakeResolver) Resolve(_ context.Context, _ []string) ([]agents.BriefAttachment, error) {
	return f.atts, nil
}

func TestBrief_Validation(t *testing.T) {
	b := New()
	cases := []struct {
		name  string
		in    Input
		valid bool
	}{
		{"empty-message", Input{Status: "normal"}, false},
		{"bad-status", Input{Message: "hi", Status: "weird"}, false},
		{"good-normal", Input{Message: "hi", Status: "normal"}, true},
		{"good-proactive", Input{Message: "done", Status: "proactive"}, true},
		{"relative-attachment", Input{Message: "m", Status: "normal", Attachments: []string{"rel.png"}}, false},
	}
	for _, c := range cases {
		raw, _ := json.Marshal(c.in)
		v := b.ValidateInput(raw, nil)
		if v.Valid != c.valid {
			t.Errorf("%s: valid=%v want=%v (msg=%s)", c.name, v.Valid, c.valid, v.Message)
		}
	}
}

func TestBrief_EmitsEvent(t *testing.T) {
	b := New()
	var got *agents.StreamEvent
	tc := &agents.ToolUseContext{
		Ctx:       context.Background(),
		EmitEvent: func(e agents.StreamEvent) { ev := e; got = &ev },
	}
	raw, _ := json.Marshal(Input{Message: "hi", Status: "normal"})
	res, err := b.Call(context.Background(), raw, tc)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.Type != agents.EventBrief {
		t.Fatalf("event missing: %+v", got)
	}
	p := res.Data.(agents.BriefPayload)
	if p.Message != "hi" || p.Status != "normal" || p.SentAt == "" {
		t.Fatalf("payload=%+v", p)
	}
}

func TestBrief_AttachmentResolver(t *testing.T) {
	resolved := []agents.BriefAttachment{{Path: "/a.png", Size: 10, IsImage: true}}
	b := New(WithAttachmentResolver(fakeResolver{atts: resolved}))
	raw, _ := json.Marshal(Input{Message: "x", Status: "normal", Attachments: []string{filepath.FromSlash("/a.png")}})
	res, err := b.Call(context.Background(), raw, &agents.ToolUseContext{Ctx: context.Background()})
	if err != nil {
		t.Fatal(err)
	}
	p := res.Data.(agents.BriefPayload)
	if len(p.Attachments) != 1 || p.Attachments[0].Path != "/a.png" {
		t.Fatalf("atts=%+v", p.Attachments)
	}
}

func TestBrief_DefaultResolverStatsFiles(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "hi.png")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	b := New()
	raw, _ := json.Marshal(Input{Message: "x", Status: "normal", Attachments: []string{f}})
	res, err := b.Call(context.Background(), raw, &agents.ToolUseContext{Ctx: context.Background()})
	if err != nil {
		t.Fatal(err)
	}
	p := res.Data.(agents.BriefPayload)
	if len(p.Attachments) != 1 || !p.Attachments[0].IsImage || p.Attachments[0].Size != 1 {
		t.Fatalf("attachment=%+v", p.Attachments)
	}
}

func TestBrief_DefaultResolverRejectsMissing(t *testing.T) {
	b := New()
	raw, _ := json.Marshal(Input{Message: "x", Status: "normal", Attachments: []string{filepath.Join(t.TempDir(), "nope")}})
	_, err := b.Call(context.Background(), raw, &agents.ToolUseContext{Ctx: context.Background()})
	if err == nil {
		t.Fatal("expected missing-file error")
	}
}

func TestBrief_RenderWithAttachmentsCount(t *testing.T) {
	b := New()
	cb := b.MapToolResultToParam(agents.BriefPayload{
		Attachments: []agents.BriefAttachment{{Path: "/a"}, {Path: "/b"}},
	}, "id1")
	s, _ := cb.Content.(string)
	if !strings.Contains(s, "2 attachments") {
		t.Fatalf("content=%q", s)
	}
}

func TestBrief_RenderNoAttachments(t *testing.T) {
	b := New()
	cb := b.MapToolResultToParam(agents.BriefPayload{}, "id1")
	s, _ := cb.Content.(string)
	if !strings.Contains(s, "Message delivered") || strings.Contains(s, "attachment") {
		t.Fatalf("content=%q", s)
	}
}
