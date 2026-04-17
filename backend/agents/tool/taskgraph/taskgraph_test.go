package taskgraph

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/wall-ai/ubuilding/backend/agents"
)

func strp(s string) *string         { return &s }
func slicep(s []string) *[]string   { return &s }

// ── Store ─────────────────────────────────────────────────────────────────

func TestStore_AddGet(t *testing.T) {
	s := NewStore()
	n, err := s.Add(Node{Title: "root"})
	if err != nil {
		t.Fatal(err)
	}
	if n.ID == "" {
		t.Fatal("expected auto-generated id")
	}
	if n.Status != StatusPending {
		t.Fatalf("default status = %s", n.Status)
	}
	got, ok := s.Get(n.ID)
	if !ok || got.Title != "root" {
		t.Fatalf("get = %+v, ok=%v", got, ok)
	}
}

func TestStore_AddMissingParentRejected(t *testing.T) {
	s := NewStore()
	_, err := s.Add(Node{Title: "x", ParentID: "missing"})
	if err == nil {
		t.Fatal("expected missing-parent error")
	}
}

func TestStore_AddMissingDependencyRejected(t *testing.T) {
	s := NewStore()
	_, err := s.Add(Node{Title: "x", DependsOn: []string{"missing"}})
	if err == nil {
		t.Fatal("expected missing-dep error")
	}
}

func TestStore_UpdateStatusTransition(t *testing.T) {
	s := NewStore()
	a, _ := s.Add(Node{Title: "a"})
	if _, err := s.Update(a.ID, UpdateFields{Status: strp(StatusInProgress)}); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(a.ID)
	if got.Status != StatusInProgress {
		t.Fatalf("status=%s", got.Status)
	}
}

func TestStore_UpdateRejectsSelfParent(t *testing.T) {
	s := NewStore()
	a, _ := s.Add(Node{Title: "a"})
	if _, err := s.Update(a.ID, UpdateFields{ParentID: strp(a.ID)}); err == nil {
		t.Fatal("expected self-parent error")
	}
}

func TestStore_UpdateRejectsSelfDependency(t *testing.T) {
	s := NewStore()
	a, _ := s.Add(Node{Title: "a"})
	if _, err := s.Update(a.ID, UpdateFields{DependsOn: slicep([]string{a.ID})}); err == nil {
		t.Fatal("expected self-dep error")
	}
}

func TestStore_UpdateInvalidStatusRejected(t *testing.T) {
	s := NewStore()
	a, _ := s.Add(Node{Title: "a"})
	if _, err := s.Update(a.ID, UpdateFields{Status: strp("bogus")}); err == nil {
		t.Fatal("expected invalid-status error")
	}
}

func TestStore_CycleDetection_ParentChain(t *testing.T) {
	s := NewStore()
	a, _ := s.Add(Node{Title: "a"})
	b, _ := s.Add(Node{Title: "b", ParentID: a.ID})
	c, _ := s.Add(Node{Title: "c", ParentID: b.ID})
	// Now try to make a's parent = c → forms a cycle a→c→b→a
	if _, err := s.Update(a.ID, UpdateFields{ParentID: strp(c.ID)}); err == nil {
		t.Fatal("expected cycle error")
	}
}

func TestStore_CycleDetection_Dependency(t *testing.T) {
	s := NewStore()
	a, _ := s.Add(Node{Title: "a"})
	b, _ := s.Add(Node{Title: "b", DependsOn: []string{a.ID}})
	if _, err := s.Update(a.ID, UpdateFields{DependsOn: slicep([]string{b.ID})}); err == nil {
		t.Fatal("expected dependency-cycle error")
	}
}

func TestStore_ListFilter(t *testing.T) {
	s := NewStore()
	s.Add(Node{Title: "p1", Status: StatusPending})
	s.Add(Node{Title: "p2", Status: StatusPending})
	s.Add(Node{Title: "done", Status: StatusCompleted})
	if got := s.List(StatusPending); len(got) != 2 {
		t.Fatalf("pending filter = %d", len(got))
	}
	if got := s.List(""); len(got) != 3 {
		t.Fatalf("unfiltered = %d", len(got))
	}
}

func TestStore_Children(t *testing.T) {
	s := NewStore()
	p, _ := s.Add(Node{Title: "p"})
	s.Add(Node{Title: "c1", ParentID: p.ID})
	s.Add(Node{Title: "c2", ParentID: p.ID})
	if got := s.Children(p.ID); len(got) != 2 {
		t.Fatalf("children=%d", len(got))
	}
}

func TestStore_StopTransitionsCancelled(t *testing.T) {
	s := NewStore()
	a, _ := s.Add(Node{Title: "a", Status: StatusInProgress})
	status, ok, err := s.Stop(a.ID)
	if err != nil || !ok || status != StatusCancelled {
		t.Fatalf("stop a → (%q,%v,%v)", status, ok, err)
	}
}

func TestStore_StopUnknownReportsNotFound(t *testing.T) {
	s := NewStore()
	_, ok, err := s.Stop("missing")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("expected not found")
	}
}

func TestStore_ConcurrentAddUpdate(t *testing.T) {
	s := NewStore()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			n, err := s.Add(Node{Title: "x"})
			if err != nil {
				return
			}
			s.Update(n.ID, UpdateFields{Status: strp(StatusInProgress)})
		}()
	}
	wg.Wait()
	if got := s.List(""); len(got) != 50 {
		t.Fatalf("expected 50, got %d", len(got))
	}
}

// ── Tools ─────────────────────────────────────────────────────────────────

func ctxWithStore() (*agents.ToolUseContext, *Store) {
	s := NewStore()
	return &agents.ToolUseContext{Ctx: context.Background(), TaskGraph: s}, s
}

func TestCreateTool_Basic(t *testing.T) {
	tc, store := ctxWithStore()
	c := NewCreateTool()
	raw, _ := json.Marshal(CreateInput{Title: "plan"})
	res, err := c.Call(context.Background(), raw, tc)
	if err != nil {
		t.Fatal(err)
	}
	n := res.Data.(Node)
	if n.ID == "" || n.Title != "plan" {
		t.Fatalf("bad node: %+v", n)
	}
	if len(store.List("")) != 1 {
		t.Fatal("store not updated")
	}
}

func TestCreateTool_Validation(t *testing.T) {
	c := NewCreateTool()
	raw, _ := json.Marshal(CreateInput{Title: ""})
	if v := c.ValidateInput(raw, nil); v.Valid {
		t.Fatal("empty title must be invalid")
	}
	raw, _ = json.Marshal(CreateInput{Title: "ok", Status: "bogus"})
	if v := c.ValidateInput(raw, nil); v.Valid {
		t.Fatal("bad status must be invalid")
	}
}

func TestGetTool_Roundtrip(t *testing.T) {
	tc, store := ctxWithStore()
	n, _ := store.Add(Node{Title: "t"})
	g := NewGetTool()
	raw, _ := json.Marshal(GetInput{ID: n.ID})
	res, err := g.Call(context.Background(), raw, tc)
	if err != nil {
		t.Fatal(err)
	}
	if got := res.Data.(Node); got.ID != n.ID {
		t.Fatalf("got=%+v", got)
	}
}

func TestUpdateTool_StatusChange(t *testing.T) {
	tc, store := ctxWithStore()
	n, _ := store.Add(Node{Title: "t"})
	u := NewUpdateTool()
	raw, _ := json.Marshal(UpdateInput{ID: n.ID, Status: strp(StatusCompleted)})
	res, err := u.Call(context.Background(), raw, tc)
	if err != nil {
		t.Fatal(err)
	}
	if res.Data.(Node).Status != StatusCompleted {
		t.Fatalf("status not applied: %+v", res.Data)
	}
}

func TestListTool_Filter(t *testing.T) {
	tc, store := ctxWithStore()
	store.Add(Node{Title: "a", Status: StatusPending})
	store.Add(Node{Title: "b", Status: StatusCompleted})
	lt := NewListTool()
	raw, _ := json.Marshal(ListInput{Status: StatusCompleted})
	res, err := lt.Call(context.Background(), raw, tc)
	if err != nil {
		t.Fatal(err)
	}
	got := res.Data.([]Node)
	if len(got) != 1 || got[0].Status != StatusCompleted {
		t.Fatalf("unexpected: %+v", got)
	}
}

func TestTool_NoContextStore(t *testing.T) {
	tc := &agents.ToolUseContext{Ctx: context.Background()}
	c := NewCreateTool()
	raw, _ := json.Marshal(CreateInput{Title: "x"})
	if _, err := c.Call(context.Background(), raw, tc); err == nil {
		t.Fatal("expected error when store missing")
	}
}

// rendering smoke
func TestRenderNode_Formats(t *testing.T) {
	n := Node{ID: "task_1", Title: "hi", Status: "pending", ParentID: "p", DependsOn: []string{"d1"}}
	out := renderNode(n)
	if !strings.Contains(out, "task_1") || !strings.Contains(out, "parent=p") || !strings.Contains(out, "deps=[d1]") {
		t.Fatalf("render = %q", out)
	}
}
