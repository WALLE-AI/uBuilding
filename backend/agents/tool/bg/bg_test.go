package bg

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wall-ai/ubuilding/backend/agents"
)

func TestManager_StartSucceeds(t *testing.T) {
	m := NewManager()
	id, err := m.Start(context.Background(), "noop", func(ctx context.Context, write func(string)) (int, error) {
		write("hello")
		return 0, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(id, IDPrefix) {
		t.Fatalf("id missing prefix: %s", id)
	}
	if _, err := m.WaitForTerminal(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	j, _ := m.Get(id)
	if j.Status != StatusSucceeded {
		t.Fatalf("status=%s", j.Status)
	}
	if !strings.Contains(j.Output, "hello") {
		t.Fatalf("output=%q", j.Output)
	}
}

func TestManager_StopCancels(t *testing.T) {
	m := NewManager()
	id, _ := m.Start(context.Background(), "loop", func(ctx context.Context, _ func(string)) (int, error) {
		<-ctx.Done()
		return -1, ctx.Err()
	})
	time.Sleep(20 * time.Millisecond)
	j, err := m.Stop(id, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if j.Status != StatusCancelled {
		t.Fatalf("status=%s", j.Status)
	}
}

func TestManager_ReadOutputIncremental(t *testing.T) {
	m := NewManager()
	ch := make(chan string, 3)
	done := make(chan struct{})
	id, _ := m.Start(context.Background(), "stream", func(ctx context.Context, write func(string)) (int, error) {
		for chunk := range ch {
			write(chunk)
		}
		close(done)
		return 0, nil
	})

	ch <- "alpha"
	time.Sleep(20 * time.Millisecond)
	_, s1, _, err := m.ReadOutput(id, true)
	if err != nil {
		t.Fatal(err)
	}
	if s1 != "alpha" {
		t.Fatalf("slice1=%q", s1)
	}

	ch <- "beta"
	time.Sleep(20 * time.Millisecond)
	_, s2, _, _ := m.ReadOutput(id, true)
	if s2 != "beta" {
		t.Fatalf("slice2=%q", s2)
	}

	// Re-read without advancing should return empty (cursor already at end).
	_, s3, _, _ := m.ReadOutput(id, false)
	if s3 != "" {
		t.Fatalf("slice3=%q (should be empty; cursor already at end)", s3)
	}

	// Full read ignoring cursor.
	_, s4, _, _ := m.ReadOutput(id, true) // now cursor==len; full slice empty
	if s4 != "" {
		t.Fatalf("slice4=%q", s4)
	}

	close(ch)
	<-done
}

func TestManager_ReadOutput_FullReread(t *testing.T) {
	m := NewManager()
	id, _ := m.Start(context.Background(), "one", func(_ context.Context, write func(string)) (int, error) {
		write("abc")
		return 0, nil
	})
	m.WaitForTerminal(context.Background(), id)
	// First incremental read pulls everything.
	_, slice, _, _ := m.ReadOutput(id, true)
	if slice != "abc" {
		t.Fatalf("slice=%q", slice)
	}
	// Second incremental read → empty.
	_, slice, _, _ = m.ReadOutput(id, true)
	if slice != "" {
		t.Fatalf("slice=%q", slice)
	}
}

func TestManager_ListSortedByStart(t *testing.T) {
	m := NewManager()
	id1, _ := m.Start(context.Background(), "a", func(context.Context, func(string)) (int, error) { return 0, nil })
	time.Sleep(5 * time.Millisecond)
	id2, _ := m.Start(context.Background(), "b", func(context.Context, func(string)) (int, error) { return 0, nil })
	m.WaitForTerminal(context.Background(), id1)
	m.WaitForTerminal(context.Background(), id2)
	list := m.List()
	if len(list) != 2 {
		t.Fatalf("list=%d", len(list))
	}
	if list[0].ID != id2 {
		t.Fatalf("newest should be first: %s vs %s", list[0].ID, id2)
	}
}

// Concurrency smoke: writing from many goroutines while readers pull output.
func TestManager_ConcurrentReadsWrites(t *testing.T) {
	m := NewManager()
	done := make(chan struct{})
	id, _ := m.Start(context.Background(), "stream", func(ctx context.Context, write func(string)) (int, error) {
		var wg sync.WaitGroup
		for i := 0; i < 10; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for j := 0; j < 50; j++ {
					write("x")
				}
			}()
		}
		wg.Wait()
		close(done)
		return 0, nil
	})
	// Readers race with writers.
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				m.ReadOutput(id, true)
				time.Sleep(time.Millisecond)
			}
		}()
	}
	<-done
	wg.Wait()
	j, _ := m.Get(id)
	if j.Status != StatusSucceeded {
		t.Fatalf("status=%s", j.Status)
	}
}

// ── Tool-level tests ──────────────────────────────────────────────────────

func newCtxWithMgr() (*agents.ToolUseContext, *Manager) {
	m := NewManager()
	return &agents.ToolUseContext{Ctx: context.Background(), TaskManager: m}, m
}

func TestOutputTool_ReadsIncremental(t *testing.T) {
	tc, mgr := newCtxWithMgr()
	id, _ := mgr.Start(context.Background(), "x", func(_ context.Context, write func(string)) (int, error) {
		write("first")
		return 0, nil
	})
	mgr.WaitForTerminal(context.Background(), id)

	out := NewOutputTool()
	raw, _ := json.Marshal(OutputInput{BashID: id})
	res, err := out.Call(context.Background(), raw, tc)
	if err != nil {
		t.Fatal(err)
	}
	r := res.Data.(OutputResult)
	if r.Status != StatusSucceeded || r.Output != "first" {
		t.Fatalf("result=%+v", r)
	}
}

func TestOutputTool_NotFound(t *testing.T) {
	tc, _ := newCtxWithMgr()
	out := NewOutputTool()
	raw, _ := json.Marshal(OutputInput{BashID: "bash_missing"})
	if _, err := out.Call(context.Background(), raw, tc); err == nil {
		t.Fatal("expected not-found error")
	}
}

func TestStopTool_BgPath(t *testing.T) {
	tc, mgr := newCtxWithMgr()
	id, _ := mgr.Start(context.Background(), "loop", func(ctx context.Context, _ func(string)) (int, error) {
		<-ctx.Done()
		return -1, ctx.Err()
	})
	stop := NewStopTool()
	raw, _ := json.Marshal(StopInput{ID: id})
	res, err := stop.Call(context.Background(), raw, tc)
	if err != nil {
		t.Fatal(err)
	}
	r := res.Data.(StopResult)
	if r.Kind != "bg" || r.Status != StatusCancelled {
		t.Fatalf("result=%+v", r)
	}
}

type fakeGraph struct{ status string }

func (f *fakeGraph) Stop(id string) (string, bool, error) {
	if id == "missing" {
		return "", false, nil
	}
	if id == "boom" {
		return "", false, errors.New("boom")
	}
	return f.status, true, nil
}

func TestStopTool_GraphPath(t *testing.T) {
	tc := &agents.ToolUseContext{Ctx: context.Background(), TaskGraph: &fakeGraph{status: "cancelled"}}
	stop := NewStopTool()
	raw, _ := json.Marshal(StopInput{ID: "node-42"})
	res, err := stop.Call(context.Background(), raw, tc)
	if err != nil {
		t.Fatal(err)
	}
	r := res.Data.(StopResult)
	if r.Kind != "graph" {
		t.Fatalf("kind=%s", r.Kind)
	}
}

func TestStopTool_NotFound(t *testing.T) {
	tc := &agents.ToolUseContext{Ctx: context.Background(), TaskGraph: &fakeGraph{}}
	stop := NewStopTool()
	raw, _ := json.Marshal(StopInput{ID: "missing"})
	if _, err := stop.Call(context.Background(), raw, tc); err == nil {
		t.Fatal("expected not-found error")
	}
}

func TestStopTool_Validation(t *testing.T) {
	stop := NewStopTool()
	raw, _ := json.Marshal(StopInput{})
	v := stop.ValidateInput(raw, nil)
	if v.Valid {
		t.Fatal("empty id should be invalid")
	}
}
