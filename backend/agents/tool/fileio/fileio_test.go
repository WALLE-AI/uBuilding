package fileio

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

func newToolCtx() *agents.ToolUseContext {
	return &agents.ToolUseContext{Ctx: context.Background(), ReadFileState: agents.NewFileStateCache()}
}

func TestRead_Basic(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.txt")
	os.WriteFile(p, []byte("alpha\nbeta\ngamma\n"), 0o644)

	r := NewReadTool()
	raw, _ := json.Marshal(ReadInput{FilePath: p})
	res, err := r.Call(context.Background(), raw, newToolCtx())
	if err != nil {
		t.Fatalf("Call err: %v", err)
	}
	out := res.Data.(ReadOutput)
	if !strings.Contains(out.Content, "alpha") || !strings.Contains(out.Content, "gamma") {
		t.Fatalf("unexpected content: %q", out.Content)
	}
	if out.TotalLines != 3 {
		t.Fatalf("TotalLines=%d want 3", out.TotalLines)
	}
}

func TestRead_OffsetLimit(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.txt")
	os.WriteFile(p, []byte("1\n2\n3\n4\n5\n"), 0o644)

	r := NewReadTool()
	raw, _ := json.Marshal(ReadInput{FilePath: p, Offset: 2, Limit: 2})
	res, _ := r.Call(context.Background(), raw, newToolCtx())
	out := res.Data.(ReadOutput)
	if !strings.Contains(out.Content, "2") || !strings.Contains(out.Content, "3") {
		t.Fatalf("missing 2/3: %q", out.Content)
	}
	if strings.Contains(out.Content, "\t4") {
		t.Fatalf("limit not honored: %q", out.Content)
	}
	if !out.Truncated {
		t.Fatalf("expected truncated=true")
	}
}

func TestRead_Binary(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "b.bin")
	os.WriteFile(p, []byte{0, 1, 2, 0, 0, 3}, 0o644)
	r := NewReadTool()
	raw, _ := json.Marshal(ReadInput{FilePath: p})
	res, _ := r.Call(context.Background(), raw, newToolCtx())
	out := res.Data.(ReadOutput)
	if !out.Binary {
		t.Fatalf("expected Binary=true")
	}
}

func TestRead_NotAbsolute(t *testing.T) {
	r := NewReadTool()
	raw, _ := json.Marshal(ReadInput{FilePath: "relative.txt"})
	v := r.ValidateInput(raw, nil)
	if v.Valid {
		t.Fatalf("expected invalid for relative path")
	}
}

func TestEdit_RequiresRead(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.txt")
	os.WriteFile(p, []byte("hello world"), 0o644)

	e := NewEditTool()
	raw, _ := json.Marshal(EditInput{FilePath: p, OldString: "hello", NewString: "hi"})
	_, err := e.Call(context.Background(), raw, newToolCtx())
	if err == nil || !strings.Contains(err.Error(), "read") {
		t.Fatalf("expected read-required error, got %v", err)
	}
}

func TestEdit_AfterRead(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.txt")
	os.WriteFile(p, []byte("hello world"), 0o644)
	tc := newToolCtx()

	// Read first.
	r := NewReadTool()
	rraw, _ := json.Marshal(ReadInput{FilePath: p})
	if _, err := r.Call(context.Background(), rraw, tc); err != nil {
		t.Fatalf("read err: %v", err)
	}

	e := NewEditTool()
	raw, _ := json.Marshal(EditInput{FilePath: p, OldString: "hello", NewString: "hi"})
	res, err := e.Call(context.Background(), raw, tc)
	if err != nil {
		t.Fatalf("edit err: %v", err)
	}
	out := res.Data.(EditOutput)
	if out.Replaced != 1 {
		t.Fatalf("replaced=%d want 1", out.Replaced)
	}
	data, _ := os.ReadFile(p)
	if string(data) != "hi world" {
		t.Fatalf("unexpected file: %q", data)
	}
}

func TestEdit_MultipleMatchesRequiresReplaceAll(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.txt")
	os.WriteFile(p, []byte("aa aa aa"), 0o644)
	tc := newToolCtx()
	r := NewReadTool()
	rraw, _ := json.Marshal(ReadInput{FilePath: p})
	r.Call(context.Background(), rraw, tc)

	e := NewEditTool()
	raw, _ := json.Marshal(EditInput{FilePath: p, OldString: "aa", NewString: "bb"})
	if _, err := e.Call(context.Background(), raw, tc); err == nil {
		t.Fatalf("expected multi-match error")
	}
	raw, _ = json.Marshal(EditInput{FilePath: p, OldString: "aa", NewString: "bb", ReplaceAll: true})
	res, err := e.Call(context.Background(), raw, tc)
	if err != nil {
		t.Fatalf("replace_all err: %v", err)
	}
	out := res.Data.(EditOutput)
	if out.Replaced != 3 {
		t.Fatalf("replaced=%d want 3", out.Replaced)
	}
}

func TestEdit_SameStringsRejected(t *testing.T) {
	e := NewEditTool()
	raw, _ := json.Marshal(EditInput{FilePath: "/tmp/a", OldString: "x", NewString: "x"})
	v := e.ValidateInput(raw, nil)
	if v.Valid {
		t.Fatalf("expected invalid when old==new")
	}
}

func TestEdit_Create(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "nested", "new.txt")
	e := NewEditTool()
	raw, _ := json.Marshal(EditInput{FilePath: p, OldString: "", NewString: "created-content"})
	res, err := e.Call(context.Background(), raw, newToolCtx())
	if err != nil {
		t.Fatalf("create err: %v", err)
	}
	out := res.Data.(EditOutput)
	if !out.Created {
		t.Fatalf("Created=false")
	}
	data, _ := os.ReadFile(p)
	if string(data) != "created-content" {
		t.Fatalf("bad contents: %q", data)
	}
}

func TestWrite_NewFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "sub", "w.txt")
	w := NewWriteTool()
	raw, _ := json.Marshal(WriteInput{FilePath: p, Content: "hello"})
	res, err := w.Call(context.Background(), raw, newToolCtx())
	if err != nil {
		t.Fatalf("write err: %v", err)
	}
	out := res.Data.(WriteOutput)
	if !out.Created || out.Bytes != 5 {
		t.Fatalf("unexpected: %+v", out)
	}
}

func TestWrite_OverwriteRequiresRead(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "w.txt")
	os.WriteFile(p, []byte("old"), 0o644)
	w := NewWriteTool()
	raw, _ := json.Marshal(WriteInput{FilePath: p, Content: "new"})
	_, err := w.Call(context.Background(), raw, newToolCtx())
	if err == nil || !strings.Contains(err.Error(), "read") {
		t.Fatalf("expected read-required error, got %v", err)
	}

	tc := newToolCtx()
	r := NewReadTool()
	rraw, _ := json.Marshal(ReadInput{FilePath: p})
	r.Call(context.Background(), rraw, tc)
	if _, err := w.Call(context.Background(), raw, tc); err != nil {
		t.Fatalf("after read, write err: %v", err)
	}
	data, _ := os.ReadFile(p)
	if string(data) != "new" {
		t.Fatalf("bad contents: %q", data)
	}
}

// Sprint-2 Prompt() keyword coverage. These are deliberately coarse so
// they fail only when a required bullet is dropped (not for minor
// rewording). Add one keyword per upstream prompt.ts bullet.
func TestRead_PromptKeywords(t *testing.T) {
	p := NewReadTool().Prompt(tool.PromptOptions{})
	for _, want := range []string{
		"absolute path",
		"cat -n",
		"images",
		"Jupyter notebooks",
		"ls command via the Bash tool",
		"binary",
		"empty contents",
		"Read it first",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("Read.Prompt() missing %q\n----\n%s", want, p)
		}
	}
}

func TestEdit_PromptKeywords(t *testing.T) {
	p := NewEditTool().Prompt(tool.PromptOptions{})
	for _, want := range []string{
		"exact string replacements",
		"Read",
		"line number prefix",
		"spaces + line number + tab",
		"replace_all",
		"prefer editing existing files",
		"Only use emojis",
		"identical",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("Edit.Prompt() missing %q\n----\n%s", want, p)
		}
	}
}

func TestEdit_PromptAntBranchAddsUniquenessHint(t *testing.T) {
	extern := NewEditTool().Prompt(tool.PromptOptions{})
	ant := NewEditTool().Prompt(tool.PromptOptions{UserType: "ant"})
	if strings.Contains(extern, "smallest old_string") {
		t.Fatal("external prompt leaked the ant-only hint")
	}
	if !strings.Contains(ant, "smallest old_string") {
		t.Fatal("ant prompt missing minimal-uniqueness hint")
	}
}

func TestWrite_PromptKeywords(t *testing.T) {
	p := NewWriteTool().Prompt(tool.PromptOptions{})
	for _, want := range []string{
		"overwrite the existing file",
		"MUST use the `Read` tool first",
		"Prefer the Edit tool",
		"NEVER create documentation files",
		"Only use emojis",
		"absolute path",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("Write.Prompt() missing %q\n----\n%s", want, p)
		}
	}
}

func TestEnsureInWorkspace(t *testing.T) {
	dir := t.TempDir()
	inside := filepath.Join(dir, "a.txt")
	outside := filepath.Join(os.TempDir(), "..", "escape.txt")
	if err := EnsureInWorkspace(inside, []string{dir}); err != nil {
		t.Fatalf("inside rejected: %v", err)
	}
	if err := EnsureInWorkspace(outside, []string{dir}); err == nil {
		t.Fatalf("outside accepted")
	}
}
