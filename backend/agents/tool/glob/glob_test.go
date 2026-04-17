package glob

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func mktree(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for p, body := range files {
		full := filepath.Join(dir, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestGlob_DoubleStar(t *testing.T) {
	dir := mktree(t, map[string]string{
		"a.go":            "",
		"src/b.go":        "",
		"src/inner/c.go":  "",
		"README.md":       "",
		"vendor/d.go":     "",
	})
	g := New()
	raw, _ := json.Marshal(Input{Pattern: "**/*.go", Path: dir})
	res, err := g.Call(context.Background(), raw, nil)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	out := res.Data.(Output)
	if out.Count != 4 {
		t.Fatalf("count=%d want 4: %+v", out.Count, out.Matches)
	}
}

func TestGlob_SingleStar(t *testing.T) {
	dir := mktree(t, map[string]string{
		"src/a.ts": "",
		"src/b.ts": "",
		"src/sub/c.ts": "",
	})
	g := New()
	raw, _ := json.Marshal(Input{Pattern: "src/*.ts", Path: dir})
	res, _ := g.Call(context.Background(), raw, nil)
	out := res.Data.(Output)
	if out.Count != 2 {
		t.Fatalf("count=%d want 2", out.Count)
	}
}

func TestGlob_NoMatches(t *testing.T) {
	dir := mktree(t, map[string]string{"a.go": ""})
	g := New()
	raw, _ := json.Marshal(Input{Pattern: "*.py", Path: dir})
	res, _ := g.Call(context.Background(), raw, nil)
	out := res.Data.(Output)
	if out.Count != 0 {
		t.Fatalf("expected 0, got %d", out.Count)
	}
}

func TestGlob_ValidateEmpty(t *testing.T) {
	g := New()
	raw, _ := json.Marshal(Input{Pattern: ""})
	v := g.ValidateInput(raw, nil)
	if v.Valid {
		t.Fatal("empty pattern should be invalid")
	}
}
