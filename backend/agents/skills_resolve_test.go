package agents

import (
	"context"
	"testing"
)

func TestSkillRegistry_ResolveStrategies(t *testing.T) {
	reg := NewSkillRegistry()
	reg.Register(Skill{Name: "verify", Body: "V"})
	reg.Register(Skill{Name: "verify", Plugin: "my-plugin", Body: "V-PLUGIN"})
	reg.Register(Skill{Name: "build", Plugin: "other", Body: "B"})

	// 1. Exact unqualified
	s, ok := reg.ResolveSkillName("verify")
	if !ok || s.Body != "V" {
		t.Fatalf("exact unqualified: ok=%v body=%q", ok, s.Body)
	}
	// 2. Exact qualified
	s, ok = reg.ResolveSkillName("my-plugin:verify")
	if !ok || s.Body != "V-PLUGIN" {
		t.Fatalf("exact qualified: ok=%v body=%q", ok, s.Body)
	}
	// 3. Suffix fallback — "build" matches "other:build" (no bare "build"
	// registered).
	s, ok = reg.ResolveSkillName("build")
	if !ok || s.Body != "B" {
		t.Fatalf("suffix fallback: ok=%v body=%q", ok, s.Body)
	}
	// 4. Unknown
	if _, ok := reg.ResolveSkillName("missing"); ok {
		t.Fatal("unknown should miss")
	}
	// 5. Empty name
	if _, ok := reg.ResolveSkillName(""); ok {
		t.Fatal("empty name should miss")
	}
	// 6. Unknown qualified form
	if _, ok := reg.ResolveSkillName("ghost:verify"); ok {
		t.Fatal("unknown plugin prefix should miss (only exact match)")
	}
}

func TestSkillRegistry_AllSorted(t *testing.T) {
	reg := NewSkillRegistry()
	reg.Register(Skill{Name: "b"})
	reg.Register(Skill{Name: "a"})
	reg.Register(Skill{Name: "z", Plugin: "p"})

	all := reg.All()
	if len(all) != 3 {
		t.Fatalf("got %d skills", len(all))
	}
	want := []string{"a", "b", "p:z"}
	for i, s := range all {
		if s.QualifiedName() != want[i] {
			t.Fatalf("all[%d] = %q; want %q", i, s.QualifiedName(), want[i])
		}
	}
}

func TestSkillRegistry_NilSafe(t *testing.T) {
	var reg *SkillRegistry
	if s, ok := reg.ResolveSkillName("x"); ok {
		t.Fatalf("nil registry must miss: %+v", s)
	}
	reg.Register(Skill{Name: "x"}) // must not panic
	if reg.All() != nil {
		t.Fatal("nil registry should have no skills")
	}
}

func TestSkillInvocationLog_RecordAndClear(t *testing.T) {
	log := NewSkillInvocationLog()
	log.RecordInvocation("agt-1", Skill{Name: "verify"})
	log.RecordInvocation("agt-1", Skill{Name: "build"})
	log.RecordInvocation("agt-2", Skill{Name: "verify"})

	if log.TotalInvocations() != 3 {
		t.Fatalf("total = %d", log.TotalInvocations())
	}
	if got := log.InvokedFor("agt-1"); len(got) != 2 {
		t.Fatalf("agt-1 = %+v", got)
	}
	log.ClearInvokedSkillsForAgent("agt-1")
	if log.InvokedFor("agt-1") != nil {
		t.Fatal("agt-1 still recorded after clear")
	}
	if log.InvokedFor("agt-2") == nil {
		t.Fatal("agt-2 must survive independent clear")
	}
	// Clearing unknown agent is a no-op.
	log.ClearInvokedSkillsForAgent("ghost")
}

func TestMakeSkillResolver_AdapterShape(t *testing.T) {
	reg := NewSkillRegistry()
	reg.Register(Skill{Name: "verify", Body: "verify body"})
	log := NewSkillInvocationLog()

	resolve := MakeSkillResolver(reg, log)
	blocks, err := resolve(context.Background(), "agt", "verify")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(blocks) != 1 || blocks[0].Text != "verify body" {
		t.Fatalf("blocks = %+v", blocks)
	}
	// Unknown → (nil, nil)
	blocks, err = resolve(context.Background(), "agt", "ghost")
	if err != nil || blocks != nil {
		t.Fatalf("unknown resolve: blocks=%v err=%v", blocks, err)
	}
	// Invocation was recorded for agent "agt".
	if log.TotalInvocations() != 1 {
		t.Fatalf("invocations = %d", log.TotalInvocations())
	}
}

func TestMakeSkillResolver_NilRegistry(t *testing.T) {
	resolve := MakeSkillResolver(nil, nil)
	blocks, err := resolve(context.Background(), "agt", "x")
	if err != nil || blocks != nil {
		t.Fatalf("nil registry: blocks=%v err=%v", blocks, err)
	}
}

func TestMakeSkillResolver_EmptyBodySkipped(t *testing.T) {
	reg := NewSkillRegistry()
	reg.Register(Skill{Name: "empty"})
	resolve := MakeSkillResolver(reg, nil)
	blocks, _ := resolve(context.Background(), "agt", "empty")
	if blocks != nil {
		t.Fatal("empty-body skill should yield nil blocks")
	}
}
