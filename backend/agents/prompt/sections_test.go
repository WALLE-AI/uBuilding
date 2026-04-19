package prompt

import (
	"errors"
	"testing"
)

func TestCrossRef_NilResolverFallsBack(t *testing.T) {
	if got := CrossRef(nil, "Bash"); got != "Bash" {
		t.Errorf("CrossRef(nil, Bash) = %q, want Bash", got)
	}
}

func TestCrossRef_EmptyResultFallsBack(t *testing.T) {
	resolve := func(string) string { return "" }
	if got := CrossRef(resolve, "Bash"); got != "Bash" {
		t.Errorf("CrossRef(empty, Bash) = %q, want Bash", got)
	}
}

func TestCrossRef_ResolvedNameWins(t *testing.T) {
	resolve := func(p string) string {
		if p == "Bash" {
			return "ShellTool"
		}
		return ""
	}
	if got := CrossRef(resolve, "Bash"); got != "ShellTool" {
		t.Errorf("CrossRef resolved wrong: %q, want ShellTool", got)
	}
	// Unknown primary still falls back.
	if got := CrossRef(resolve, "Read"); got != "Read" {
		t.Errorf("CrossRef fallback wrong for unknown primary: %q", got)
	}
}

func TestToolListResolveFn_Membership(t *testing.T) {
	resolve := ToolListResolveFn([]string{"Bash", "Read", "Edit"})
	cases := map[string]string{
		"Bash":  "Bash",
		"Read":  "Read",
		"Grep":  "",
		"":      "",
	}
	for in, want := range cases {
		if got := resolve(in); got != want {
			t.Errorf("resolve(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestToolListResolveFn_EmptyList(t *testing.T) {
	resolve := ToolListResolveFn(nil)
	if resolve == nil {
		t.Fatal("resolve must not be nil")
	}
	if got := resolve("Bash"); got != "" {
		t.Errorf("empty resolver returned %q, want empty string", got)
	}
}

// Sanity: NewSystemPromptSection / SectionCache still behave as expected.
func TestSectionCache_GetSetClear(t *testing.T) {
	c := NewSectionCache()
	if _, ok := c.Get("x"); ok {
		t.Error("empty cache should miss")
	}
	c.Set("x", "v1")
	if v, ok := c.Get("x"); !ok || v != "v1" {
		t.Errorf("Get after Set = (%q, %v); want (v1, true)", v, ok)
	}
	c.Clear()
	if _, ok := c.Get("x"); ok {
		t.Error("Clear should drop entries")
	}
}

func TestResolveSystemPromptSections_UsesCache(t *testing.T) {
	cache := NewSectionCache()
	calls := 0
	compute := func() (string, error) { calls++; return "v", nil }

	sections := []SystemPromptSectionDef{
		NewSystemPromptSection("s1", compute),
	}
	for i := 0; i < 3; i++ {
		got, err := ResolveSystemPromptSections(sections, cache)
		if err != nil {
			t.Fatalf("resolve err: %v", err)
		}
		if len(got) != 1 || got[0] != "v" {
			t.Errorf("iter %d: got %v", i, got)
		}
	}
	if calls != 1 {
		t.Errorf("compute called %d times; want 1 (cached thereafter)", calls)
	}
}

func TestResolveSystemPromptSections_DangerousUncachedAlwaysRuns(t *testing.T) {
	cache := NewSectionCache()
	calls := 0
	compute := func() (string, error) { calls++; return "v", nil }

	sections := []SystemPromptSectionDef{
		NewDangerousUncachedSection("s1", compute, "test"),
	}
	for i := 0; i < 3; i++ {
		if _, err := ResolveSystemPromptSections(sections, cache); err != nil {
			t.Fatalf("iter %d: %v", i, err)
		}
	}
	if calls != 3 {
		t.Errorf("dangerous section recomputed %d times; want 3", calls)
	}
}

func TestResolveSystemPromptSections_PropagatesErr(t *testing.T) {
	cache := NewSectionCache()
	boom := errors.New("boom")
	sections := []SystemPromptSectionDef{
		NewSystemPromptSection("s1", func() (string, error) { return "", boom }),
	}
	if _, err := ResolveSystemPromptSections(sections, cache); !errors.Is(err, boom) {
		t.Errorf("err = %v; want wraps boom", err)
	}
}
