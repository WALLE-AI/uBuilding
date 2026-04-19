package notebook

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wall-ai/ubuilding/backend/agents/tool"
)

const sampleNotebook = `{
 "cells": [
  {"cell_type": "code", "id": "c1", "metadata": {}, "source": ["print('hi')\n"], "outputs": [], "execution_count": null},
  {"cell_type": "markdown", "id": "c2", "metadata": {}, "source": ["# Title\n"]}
 ],
 "metadata": {},
 "nbformat": 4,
 "nbformat_minor": 5
}`

func writeTemp(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "nb.ipynb")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestReplace_ByID(t *testing.T) {
	p := writeTemp(t, sampleNotebook)
	n := New()
	raw, _ := json.Marshal(Input{NotebookPath: p, CellID: "c1", NewSource: "print('updated')"})
	res, err := n.Call(context.Background(), raw, nil)
	if err != nil {
		t.Fatalf("Call err: %v", err)
	}
	out := res.Data.(Output)
	if out.CellIndex != 0 || out.CellID != "c1" {
		t.Fatalf("unexpected: %+v", out)
	}
	data, _ := os.ReadFile(p)
	if !strings.Contains(string(data), "print('updated')") {
		t.Fatalf("source not updated: %s", string(data))
	}
}

func TestReplace_ByNumber(t *testing.T) {
	p := writeTemp(t, sampleNotebook)
	n := New()
	idx := 1
	raw, _ := json.Marshal(Input{NotebookPath: p, CellNumber: &idx, NewSource: "# New"})
	_, err := n.Call(context.Background(), raw, nil)
	if err != nil {
		t.Fatalf("Call err: %v", err)
	}
	data, _ := os.ReadFile(p)
	if !strings.Contains(string(data), "# New") {
		t.Fatalf("did not update: %s", string(data))
	}
}

func TestInsert(t *testing.T) {
	p := writeTemp(t, sampleNotebook)
	n := New()
	idx := 1
	raw, _ := json.Marshal(Input{NotebookPath: p, CellNumber: &idx, EditMode: EditModeInsert, CellType: CellTypeCode, NewSource: "print('between')"})
	res, err := n.Call(context.Background(), raw, nil)
	if err != nil {
		t.Fatalf("insert err: %v", err)
	}
	out := res.Data.(Output)
	if out.CellIndex != 1 {
		t.Fatalf("index=%d", out.CellIndex)
	}
	var nb Notebook
	data, _ := os.ReadFile(p)
	json.Unmarshal(data, &nb)
	if len(nb.Cells) != 3 {
		t.Fatalf("cells=%d want 3", len(nb.Cells))
	}
	if !strings.Contains(string(nb.Cells[1]), "print('between')") {
		t.Fatalf("inserted cell missing source: %s", string(nb.Cells[1]))
	}
}

func TestInsert_EmptyNotebookRequiresZero(t *testing.T) {
	p := writeTemp(t, `{"cells": [], "metadata": {}, "nbformat": 4, "nbformat_minor": 5}`)
	n := New()
	idx := 1
	raw, _ := json.Marshal(Input{NotebookPath: p, CellNumber: &idx, EditMode: EditModeInsert, CellType: CellTypeCode, NewSource: "print('x')"})
	if _, err := n.Call(context.Background(), raw, nil); err == nil {
		t.Fatalf("expected error when inserting at non-zero into empty")
	}
	idx = 0
	raw, _ = json.Marshal(Input{NotebookPath: p, CellNumber: &idx, EditMode: EditModeInsert, CellType: CellTypeCode, NewSource: "print('x')"})
	if _, err := n.Call(context.Background(), raw, nil); err != nil {
		t.Fatalf("insert-at-0 into empty: %v", err)
	}
}

// S2-T11 · delete mode coverage.

func TestDelete_ByID(t *testing.T) {
	p := writeTemp(t, sampleNotebook)
	n := New()
	raw, _ := json.Marshal(Input{NotebookPath: p, CellID: "c1", EditMode: EditModeDelete})
	res, err := n.Call(context.Background(), raw, nil)
	if err != nil {
		t.Fatalf("delete err: %v", err)
	}
	out := res.Data.(Output)
	if out.EditMode != EditModeDelete || out.CellIndex != 0 || out.CellID != "c1" {
		t.Fatalf("unexpected: %+v", out)
	}
	var nb Notebook
	data, _ := os.ReadFile(p)
	if err := json.Unmarshal(data, &nb); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(nb.Cells) != 1 {
		t.Fatalf("cells=%d want 1", len(nb.Cells))
	}
	if id, _ := cellIDOf(nb.Cells[0]); id != "c2" {
		t.Fatalf("survivor id=%q want c2", id)
	}
}

func TestDelete_ByNumber_Middle(t *testing.T) {
	extended := `{
 "cells": [
  {"cell_type": "code", "id": "c1", "metadata": {}, "source": ["a\n"], "outputs": [], "execution_count": null},
  {"cell_type": "code", "id": "c2", "metadata": {}, "source": ["b\n"], "outputs": [], "execution_count": null},
  {"cell_type": "markdown", "id": "c3", "metadata": {}, "source": ["# z\n"]}
 ],
 "metadata": {}, "nbformat": 4, "nbformat_minor": 5
}`
	p := writeTemp(t, extended)
	n := New()
	idx := 1
	raw, _ := json.Marshal(Input{NotebookPath: p, CellNumber: &idx, EditMode: EditModeDelete})
	if _, err := n.Call(context.Background(), raw, nil); err != nil {
		t.Fatalf("delete middle: %v", err)
	}
	var nb Notebook
	data, _ := os.ReadFile(p)
	json.Unmarshal(data, &nb)
	if len(nb.Cells) != 2 {
		t.Fatalf("cells=%d want 2", len(nb.Cells))
	}
	// Remaining order: c1, c3.
	id0, _ := cellIDOf(nb.Cells[0])
	id1, _ := cellIDOf(nb.Cells[1])
	if id0 != "c1" || id1 != "c3" {
		t.Fatalf("order wrong: %q %q", id0, id1)
	}
}

func TestDelete_EmptyNotebookRejected(t *testing.T) {
	p := writeTemp(t, `{"cells": [], "metadata": {}, "nbformat": 4, "nbformat_minor": 5}`)
	n := New()
	idx := 0
	raw, _ := json.Marshal(Input{NotebookPath: p, CellNumber: &idx, EditMode: EditModeDelete})
	if _, err := n.Call(context.Background(), raw, nil); err == nil {
		t.Fatal("expected error when deleting from empty notebook")
	}
}

func TestDelete_UnknownCellIDRejected(t *testing.T) {
	p := writeTemp(t, sampleNotebook)
	n := New()
	raw, _ := json.Marshal(Input{NotebookPath: p, CellID: "does-not-exist", EditMode: EditModeDelete})
	if _, err := n.Call(context.Background(), raw, nil); err == nil {
		t.Fatal("expected error for unknown cell_id")
	}
}

func TestValidate_DeleteRequiresTarget(t *testing.T) {
	n := New()
	raw, _ := json.Marshal(Input{NotebookPath: "/tmp/a.ipynb", EditMode: EditModeDelete})
	if v := n.ValidateInput(raw, nil); v.Valid {
		t.Fatal("delete without cell_id/cell_number should be invalid")
	}
}

func TestPrompt_MentionsDelete(t *testing.T) {
	n := New()
	p := n.Prompt(tool.PromptOptions{})
	if !strings.Contains(p, "delete") {
		t.Fatalf("prompt missing delete mention: %s", p)
	}
}

func TestValidate_RequiresIpynb(t *testing.T) {
	n := New()
	raw, _ := json.Marshal(Input{NotebookPath: "/tmp/a.txt", NewSource: "x"})
	v := n.ValidateInput(raw, nil)
	if v.Valid {
		t.Fatal("non-ipynb extension should be rejected")
	}
}

func TestValidate_InsertNeedsCellType(t *testing.T) {
	n := New()
	idx := 0
	raw, _ := json.Marshal(Input{NotebookPath: "/tmp/a.ipynb", CellNumber: &idx, EditMode: EditModeInsert, NewSource: "x"})
	v := n.ValidateInput(raw, nil)
	if v.Valid {
		t.Fatal("insert should require cell_type")
	}
}
