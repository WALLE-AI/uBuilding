package askuser

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/wall-ai/ubuilding/backend/agents"
	"github.com/wall-ai/ubuilding/backend/agents/tool"
)

func TestAskUser_PromptKeywords(t *testing.T) {
	p := New().Prompt(tool.PromptOptions{})
	for _, want := range []string{
		"Gather user preferences",
		"Clarify ambiguous instructions",
		`select "Other" to provide custom text input`,
		"multiSelect: true",
		"(Recommended)",
		"Plan mode note",
		"ExitPlanMode",
		"Preview feature",
		"ASCII mockups",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("AskUser.Prompt() missing %q\n----\n%s", want, p)
		}
	}
}

func TestAskUser_PromptHTMLPreviewBranch(t *testing.T) {
	md := New().Prompt(tool.PromptOptions{PreviewFormat: "markdown"})
	html := New().Prompt(tool.PromptOptions{PreviewFormat: "html"})
	if !strings.Contains(md, "ASCII mockups") {
		t.Error("markdown branch must mention ASCII mockups")
	}
	if !strings.Contains(html, "HTML mockups") {
		t.Error("html branch must mention HTML mockups")
	}
	if strings.Contains(html, "ASCII mockups") {
		t.Error("html branch must not mention markdown-only ASCII mockups")
	}
	if strings.Contains(md, "self-contained HTML fragment") {
		t.Error("markdown branch must not include the html-only instruction")
	}
}

func TestAskUser_Validation(t *testing.T) {
	tool := New()
	cases := []struct {
		in    Input
		valid bool
	}{
		{Input{Question: "pick", Options: []agents.AskUserOption{{Label: "A"}, {Label: "B"}}}, true},
		{Input{Question: "", Options: []agents.AskUserOption{{Label: "A"}}}, false},
		{Input{Question: "q", Options: []agents.AskUserOption{{Label: "A"}, {Label: "B"}, {Label: "C"}, {Label: "D"}, {Label: "E"}}}, false},
		{Input{Question: "q", Options: []agents.AskUserOption{{Label: "other"}}}, false},
		{Input{Question: "q", Options: []agents.AskUserOption{{Label: ""}}}, false},
	}
	for i, c := range cases {
		raw, _ := json.Marshal(c.in)
		v := tool.ValidateInput(raw, nil)
		if v.Valid != c.valid {
			t.Errorf("case %d valid=%v want %v (%s)", i, v.Valid, c.valid, v.Message)
		}
	}
}

func TestAskUser_Call(t *testing.T) {
	var seenPayload agents.AskUserPayload
	var seenEvent bool
	tc := &agents.ToolUseContext{
		Ctx: context.Background(),
		AskUser: func(_ context.Context, p agents.AskUserPayload) (agents.AskUserResponse, error) {
			seenPayload = p
			return agents.AskUserResponse{Selected: []string{"A"}}, nil
		},
		EmitEvent: func(e agents.StreamEvent) {
			if e.Type == agents.EventAskUser {
				seenEvent = true
			}
		},
	}
	tool := New()
	raw, _ := json.Marshal(Input{Question: "q?", Options: []agents.AskUserOption{{Label: "A"}, {Label: "B"}}})
	res, err := tool.Call(context.Background(), raw, tc)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if seenPayload.Question != "q?" {
		t.Fatalf("payload: %+v", seenPayload)
	}
	if !seenEvent {
		t.Fatalf("expected EventAskUser emitted")
	}
	out := res.Data.(Output)
	if len(out.Selected) != 1 || out.Selected[0] != "A" {
		t.Fatalf("selected=%v", out.Selected)
	}
}

func TestAskUser_NoHandler(t *testing.T) {
	tool := New()
	raw, _ := json.Marshal(Input{Question: "q?", Options: []agents.AskUserOption{{Label: "A"}}})
	_, err := tool.Call(context.Background(), raw, &agents.ToolUseContext{Ctx: context.Background()})
	if err == nil {
		t.Fatal("expected error with no AskUser handler")
	}
}
