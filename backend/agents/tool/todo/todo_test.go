package todo

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/wall-ai/ubuilding/backend/agents"
	"github.com/wall-ai/ubuilding/backend/agents/tool"
)

func TestTodoWrite_PromptKeywords(t *testing.T) {
	p := New().Prompt(tool.PromptOptions{})
	for _, want := range []string{
		"structured task list for your current coding session",
		"## When to Use This Tool",
		"## When NOT to Use This Tool",
		"## Examples of When to Use the Todo List",
		"## Examples of When NOT to Use the Todo List",
		"## Task States and Management",
		"activeForm",
		"present continuous",
		"Exactly ONE task must be in_progress at any time",
		"content: \"Fix authentication bug\"",
		"activeForm: \"Fixing authentication bug\"",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("TodoWrite prompt missing %q", want)
		}
	}
}

func TestTodoWrite_DescriptionMentionsActiveForm(t *testing.T) {
	d := New().Description(nil)
	if !strings.Contains(d, "activeForm") {
		t.Errorf("Description missing activeForm mention: %q", d)
	}
}

func TestTodoWrite_RenderUsesActiveFormForInProgress(t *testing.T) {
	out := Output{Todos: []Item{
		{ID: "1", Content: "Build feature X", ActiveForm: "Building feature X", Status: StatusInProgress, Priority: PriorityHigh},
		{ID: "2", Content: "Write tests", ActiveForm: "Writing tests", Status: StatusPending, Priority: PriorityMedium},
	}}
	rendered := renderOutput(out)
	if !strings.Contains(rendered, "Building feature X") {
		t.Errorf("in-progress item must render activeForm; got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "Write tests") {
		t.Errorf("pending item must render content; got:\n%s", rendered)
	}
	if strings.Contains(rendered, "Build feature X") {
		t.Errorf("in-progress item must NOT render content form; got:\n%s", rendered)
	}
}

func ctxWithStore() (*agents.ToolUseContext, *Store) {
	s := NewStore()
	return &agents.ToolUseContext{Ctx: context.Background(), TodoStore: s}, s
}

func TestTodoWrite_Replace(t *testing.T) {
	tc, store := ctxWithStore()
	tool := New()
	in := Input{Todos: []Item{
		{ID: "1", Content: "do thing", ActiveForm: "doing thing", Status: StatusInProgress, Priority: PriorityHigh},
		{ID: "2", Content: "later", ActiveForm: "doing later", Status: StatusPending, Priority: PriorityLow},
	}}
	raw, _ := json.Marshal(in)
	if v := tool.ValidateInput(raw, tc); !v.Valid {
		t.Fatalf("validate: %s", v.Message)
	}
	res, err := tool.Call(context.Background(), raw, tc)
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	out := res.Data.(Output)
	if len(out.Todos) != 2 {
		t.Fatalf("len=%d", len(out.Todos))
	}
	if len(store.Snapshot()) != 2 {
		t.Fatal("store not updated")
	}
}

func TestTodoWrite_Validation(t *testing.T) {
	tool := New()
	cases := []struct {
		in    Input
		valid bool
	}{
		{Input{Todos: []Item{{ID: "1", Content: "a", ActiveForm: "doing a", Status: "pending", Priority: "high"}}}, true},
		{Input{Todos: []Item{{ID: "", Content: "a", ActiveForm: "doing a", Status: "pending", Priority: "high"}}}, false},
		{Input{Todos: []Item{{ID: "1", Content: "", ActiveForm: "doing a", Status: "pending", Priority: "high"}}}, false},
		// activeForm must not be empty
		{Input{Todos: []Item{{ID: "1", Content: "a", ActiveForm: "", Status: "pending", Priority: "high"}}}, false},
		{Input{Todos: []Item{{ID: "1", Content: "a", ActiveForm: "doing a", Status: "bogus", Priority: "high"}}}, false},
		{Input{Todos: []Item{{ID: "1", Content: "a", ActiveForm: "doing a", Status: "pending", Priority: "x"}}}, false},
		{Input{Todos: []Item{
			{ID: "1", Content: "a", ActiveForm: "doing a", Status: StatusInProgress, Priority: "high"},
			{ID: "2", Content: "b", ActiveForm: "doing b", Status: StatusInProgress, Priority: "high"},
		}}, false},
		{Input{Todos: []Item{
			{ID: "1", Content: "a", ActiveForm: "doing a", Status: "pending", Priority: "high"},
			{ID: "1", Content: "b", ActiveForm: "doing b", Status: "pending", Priority: "high"},
		}}, false},
	}
	for i, c := range cases {
		raw, _ := json.Marshal(c.in)
		v := tool.ValidateInput(raw, nil)
		if v.Valid != c.valid {
			t.Errorf("case %d: valid=%v want %v (msg=%s)", i, v.Valid, c.valid, v.Message)
		}
	}
}

func TestTodoWrite_NoStore(t *testing.T) {
	tool := New()
	raw, _ := json.Marshal(Input{Todos: []Item{{ID: "1", Content: "a", ActiveForm: "doing a", Status: "pending", Priority: "high"}}})
	_, err := tool.Call(context.Background(), raw, &agents.ToolUseContext{Ctx: context.Background()})
	if err == nil {
		t.Fatal("expected error when no TodoStore attached")
	}
}
