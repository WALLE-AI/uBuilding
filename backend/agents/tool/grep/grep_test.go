package grep

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
		os.MkdirAll(filepath.Dir(full), 0o755)
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestGrep_GoFallback_Content(t *testing.T) {
	dir := mktree(t, map[string]string{
		"a.go":     "package a\n\nfunc Foo() {}\n",
		"b.go":     "package b\n\nfunc Bar() {}\n",
		"c.md":     "no match here\n",
	})
	g := New().WithLocator(NoopLocator{})
	raw, _ := json.Marshal(Input{Pattern: "^func ", Path: dir, ShowLineNumbers: true, Glob: "**/*.go"})
	res, err := g.Call(context.Background(), raw, nil)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	out := res.Data.(Output)
	if out.Total != 2 {
		t.Fatalf("total=%d want 2", out.Total)
	}
	for _, m := range out.Matches {
		if m.Line == 0 {
			t.Fatalf("expected line number, got %+v", m)
		}
	}
}

func TestGrep_CaseInsensitive(t *testing.T) {
	dir := mktree(t, map[string]string{"a.txt": "Hello\nWORLD\n"})
	g := New().WithLocator(NoopLocator{})
	raw, _ := json.Marshal(Input{Pattern: "world", Path: dir, CaseInsensitive: true})
	res, _ := g.Call(context.Background(), raw, nil)
	if res.Data.(Output).Total != 1 {
		t.Fatal("case-insensitive match missing")
	}
}

func TestGrep_FilesWithMatches(t *testing.T) {
	dir := mktree(t, map[string]string{
		"a.txt": "foo\n",
		"b.txt": "bar\n",
		"c.txt": "foo bar\n",
	})
	g := New().WithLocator(NoopLocator{})
	raw, _ := json.Marshal(Input{Pattern: "foo", Path: dir, OutputMode: OutputFilesWithMatches})
	res, _ := g.Call(context.Background(), raw, nil)
	out := res.Data.(Output)
	if len(out.Files) != 2 {
		t.Fatalf("files=%v", out.Files)
	}
}

func TestGrep_Count(t *testing.T) {
	dir := mktree(t, map[string]string{
		"a.txt": "foo\nfoo\nbar\n",
	})
	g := New().WithLocator(NoopLocator{})
	raw, _ := json.Marshal(Input{Pattern: "foo", Path: dir, OutputMode: OutputCount})
	res, _ := g.Call(context.Background(), raw, nil)
	out := res.Data.(Output)
	if out.Total != 2 {
		t.Fatalf("total=%d", out.Total)
	}
}

func TestGrep_Context(t *testing.T) {
	dir := mktree(t, map[string]string{"a.txt": "1\n2\n3 TARGET\n4\n5\n"})
	g := New().WithLocator(NoopLocator{})
	raw, _ := json.Marshal(Input{Pattern: "TARGET", Path: dir, Context: 1})
	res, _ := g.Call(context.Background(), raw, nil)
	out := res.Data.(Output)
	// expect 3 lines: 2, 3 TARGET, 4
	if out.Total != 3 {
		t.Fatalf("total=%d lines=%+v", out.Total, out.Matches)
	}
}

func TestGrep_InvalidRegex(t *testing.T) {
	g := New()
	raw, _ := json.Marshal(Input{Pattern: "("})
	v := g.ValidateInput(raw, nil)
	if v.Valid {
		t.Fatal("should reject invalid regex")
	}
}
