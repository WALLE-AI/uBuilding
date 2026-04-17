package todo

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/wall-ai/ubuilding/backend/agents"
)

func ctxWithStore() (*agents.ToolUseContext, *Store) {
	s := NewStore()
	return &agents.ToolUseContext{Ctx: context.Background(), TodoStore: s}, s
}

func TestTodoWrite_Replace(t *testing.T) {
	tc, store := ctxWithStore()
	tool := New()
	in := Input{Todos: []Item{
		{ID: "1", Content: "do thing", Status: StatusInProgress, Priority: PriorityHigh},
		{ID: "2", Content: "later", Status: StatusPending, Priority: PriorityLow},
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
		{Input{Todos: []Item{{ID: "1", Content: "a", Status: "pending", Priority: "high"}}}, true},
		{Input{Todos: []Item{{ID: "", Content: "a", Status: "pending", Priority: "high"}}}, false},
		{Input{Todos: []Item{{ID: "1", Content: "", Status: "pending", Priority: "high"}}}, false},
		{Input{Todos: []Item{{ID: "1", Content: "a", Status: "bogus", Priority: "high"}}}, false},
		{Input{Todos: []Item{{ID: "1", Content: "a", Status: "pending", Priority: "x"}}}, false},
		{Input{Todos: []Item{
			{ID: "1", Content: "a", Status: StatusInProgress, Priority: "high"},
			{ID: "2", Content: "b", Status: StatusInProgress, Priority: "high"},
		}}, false},
		{Input{Todos: []Item{
			{ID: "1", Content: "a", Status: "pending", Priority: "high"},
			{ID: "1", Content: "b", Status: "pending", Priority: "high"},
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
	raw, _ := json.Marshal(Input{Todos: []Item{{ID: "1", Content: "a", Status: "pending", Priority: "high"}}})
	_, err := tool.Call(context.Background(), raw, &agents.ToolUseContext{Ctx: context.Background()})
	if err == nil {
		t.Fatal("expected error when no TodoStore attached")
	}
}
