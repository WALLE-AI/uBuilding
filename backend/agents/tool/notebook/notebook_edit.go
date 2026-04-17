// Package notebook implements the NotebookEdit tool. It targets Jupyter .ipynb
// files (nbformat v4) and supports cell replace/insert. Deletion is not
// supported, mirroring claude-code's behaviour.
package notebook

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/wall-ai/ubuilding/backend/agents"
	"github.com/wall-ai/ubuilding/backend/agents/tool"
	"github.com/wall-ai/ubuilding/backend/agents/tool/fileio"
)

// Name is the tool name exposed to the model.
const Name = "NotebookEdit"

const (
	EditModeReplace = "replace"
	EditModeInsert  = "insert"

	CellTypeCode     = "code"
	CellTypeMarkdown = "markdown"
)

// Input matches claude-code's NotebookEdit input.
type Input struct {
	NotebookPath string `json:"notebook_path"`
	CellID       string `json:"cell_id,omitempty"`
	CellNumber   *int   `json:"cell_number,omitempty"`
	EditMode     string `json:"edit_mode,omitempty"` // replace (default) | insert
	CellType     string `json:"cell_type,omitempty"` // code | markdown (insert only)
	NewSource    string `json:"new_source"`
}

// Output is the structured result.
type Output struct {
	NotebookPath string `json:"notebook_path"`
	EditMode     string `json:"edit_mode"`
	CellIndex    int    `json:"cell_index"`
	CellID       string `json:"cell_id,omitempty"`
}

// Notebook is the minimal nbformat v4 shape we care about.
type Notebook struct {
	Cells        []json.RawMessage      `json:"cells"`
	Metadata     map[string]interface{} `json:"metadata"`
	Nbformat     int                    `json:"nbformat"`
	NbformatMinor int                   `json:"nbformat_minor"`
}

type cellHeader struct {
	ID       string `json:"id,omitempty"`
	CellType string `json:"cell_type"`
}

// NotebookEditTool implements tool.Tool.
type NotebookEditTool struct {
	tool.ToolDefaults
	workspaceRoots []string
}

// New returns a NotebookEditTool.
func New(workspaceRoots ...string) *NotebookEditTool {
	return &NotebookEditTool{workspaceRoots: workspaceRoots}
}

func (n *NotebookEditTool) Name() string                          { return Name }
func (n *NotebookEditTool) IsReadOnly(_ json.RawMessage) bool     { return false }
func (n *NotebookEditTool) IsConcurrencySafe(_ json.RawMessage) bool { return false }
func (n *NotebookEditTool) IsDestructive(_ json.RawMessage) bool  { return true }
func (n *NotebookEditTool) MaxResultSizeChars() int               { return fileio.MaxResultChars }

func (n *NotebookEditTool) InputSchema() *tool.JSONSchema {
	return &tool.JSONSchema{
		Type: "object",
		Properties: map[string]*tool.SchemaProperty{
			"notebook_path": {Type: "string", Description: "Absolute path to the .ipynb file."},
			"cell_id":       {Type: "string", Description: "Target cell id (preferred over cell_number when set)."},
			"cell_number":   {Type: "integer", Description: "0-indexed cell position when cell_id is not supplied."},
			"edit_mode":     {Type: "string", Description: "replace (default) or insert.", Enum: []string{"replace", "insert"}},
			"cell_type":     {Type: "string", Description: "Cell type for insert mode.", Enum: []string{"code", "markdown"}},
			"new_source":    {Type: "string", Description: "New cell source."},
		},
		Required: []string{"notebook_path", "new_source"},
	}
}

func (n *NotebookEditTool) Description(input json.RawMessage) string {
	var in Input
	_ = json.Unmarshal(input, &in)
	if in.NotebookPath == "" {
		return "Edit a notebook"
	}
	return "NotebookEdit " + in.NotebookPath
}

func (n *NotebookEditTool) Prompt(_ tool.PromptOptions) string {
	return `Replaces or inserts a cell in a Jupyter notebook (.ipynb).

Rules:
- notebook_path MUST be absolute.
- Target cell via cell_id (preferred) or cell_number (0-indexed).
- edit_mode: "replace" (default) overwrites the target cell's source; "insert" adds a new cell at cell_number.
- Inserting into an empty notebook requires cell_number=0.
- cell_type (code|markdown) is required for insert mode.
- Deletion is NOT supported. To "remove" a cell, edit its source to indicate it should be deleted manually.`
}

func (n *NotebookEditTool) ValidateInput(input json.RawMessage, _ *agents.ToolUseContext) *tool.ValidationResult {
	var in Input
	if err := json.Unmarshal(input, &in); err != nil {
		return &tool.ValidationResult{Valid: false, Message: fmt.Sprintf("invalid input: %v", err)}
	}
	if err := fileio.EnsureAbsolute(in.NotebookPath); err != nil {
		return &tool.ValidationResult{Valid: false, Message: err.Error()}
	}
	if !strings.HasSuffix(strings.ToLower(in.NotebookPath), ".ipynb") {
		return &tool.ValidationResult{Valid: false, Message: "notebook_path must end with .ipynb"}
	}
	mode := in.EditMode
	if mode == "" {
		mode = EditModeReplace
	}
	if mode != EditModeReplace && mode != EditModeInsert {
		return &tool.ValidationResult{Valid: false, Message: "edit_mode must be replace or insert"}
	}
	if mode == EditModeInsert {
		if in.CellNumber == nil {
			return &tool.ValidationResult{Valid: false, Message: "insert requires cell_number"}
		}
		if in.CellType != CellTypeCode && in.CellType != CellTypeMarkdown {
			return &tool.ValidationResult{Valid: false, Message: "insert requires cell_type=code|markdown"}
		}
	}
	return &tool.ValidationResult{Valid: true}
}

func (n *NotebookEditTool) CheckPermissions(input json.RawMessage, _ *agents.ToolUseContext) (*tool.PermissionResult, error) {
	return &tool.PermissionResult{Behavior: tool.PermissionAllow, UpdatedInput: input, DecisionReason: "notebook-default-allow"}, nil
}

func (n *NotebookEditTool) Call(ctx context.Context, input json.RawMessage, toolCtx *agents.ToolUseContext) (*tool.ToolResult, error) {
	var in Input
	if err := json.Unmarshal(input, &in); err != nil {
		return nil, fmt.Errorf("invalid input: %w", err)
	}
	path, err := fileio.Resolve(in.NotebookPath)
	if err != nil {
		return nil, err
	}
	if err := fileio.EnsureInWorkspace(path, n.workspaceRoots); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var nb Notebook
	if err := json.Unmarshal(data, &nb); err != nil {
		return nil, fmt.Errorf("parse notebook: %w", err)
	}
	mode := in.EditMode
	if mode == "" {
		mode = EditModeReplace
	}

	switch mode {
	case EditModeReplace:
		idx, err := locateCell(nb.Cells, in.CellID, in.CellNumber)
		if err != nil {
			return nil, err
		}
		updated, err := replaceCellSource(nb.Cells[idx], in.NewSource, in.CellType)
		if err != nil {
			return nil, err
		}
		nb.Cells[idx] = updated
		if err := writeNotebook(path, &nb); err != nil {
			return nil, err
		}
		id, _ := cellIDOf(nb.Cells[idx])
		return &tool.ToolResult{Data: Output{NotebookPath: path, EditMode: mode, CellIndex: idx, CellID: id}}, nil

	case EditModeInsert:
		idx := *in.CellNumber
		if len(nb.Cells) == 0 && idx != 0 {
			return nil, errors.New("inserting into an empty notebook requires cell_number=0")
		}
		if idx < 0 || idx > len(nb.Cells) {
			return nil, fmt.Errorf("cell_number %d out of range [0,%d]", idx, len(nb.Cells))
		}
		cell := buildCell(in.CellType, in.NewSource)
		nb.Cells = append(nb.Cells, nil)
		copy(nb.Cells[idx+1:], nb.Cells[idx:])
		nb.Cells[idx] = cell
		if err := writeNotebook(path, &nb); err != nil {
			return nil, err
		}
		return &tool.ToolResult{Data: Output{NotebookPath: path, EditMode: mode, CellIndex: idx}}, nil
	}
	return nil, fmt.Errorf("unsupported edit_mode %q", mode)
}

func (n *NotebookEditTool) MapToolResultToParam(content interface{}, toolUseID string) *agents.ContentBlock {
	return &agents.ContentBlock{
		Type:      agents.ContentBlockToolResult,
		ToolUseID: toolUseID,
		Content:   renderOutput(content),
	}
}

func renderOutput(content interface{}) string {
	var out Output
	switch v := content.(type) {
	case Output:
		out = v
	case *Output:
		if v != nil {
			out = *v
		}
	case string:
		return v
	default:
		b, _ := json.Marshal(content)
		return string(b)
	}
	return fmt.Sprintf("%s %s cell[%d] in %s", strings.Title(out.EditMode), "notebook", out.CellIndex, out.NotebookPath)
}

// ── helpers ────────────────────────────────────────────────────────────────

func locateCell(cells []json.RawMessage, cellID string, cellNumber *int) (int, error) {
	if cellID != "" {
		for i, raw := range cells {
			if id, _ := cellIDOf(raw); id == cellID {
				return i, nil
			}
		}
		return 0, fmt.Errorf("cell_id %q not found", cellID)
	}
	if cellNumber == nil {
		return 0, errors.New("either cell_id or cell_number must be provided")
	}
	if *cellNumber < 0 || *cellNumber >= len(cells) {
		return 0, fmt.Errorf("cell_number %d out of range [0,%d)", *cellNumber, len(cells))
	}
	return *cellNumber, nil
}

func cellIDOf(raw json.RawMessage) (string, error) {
	var h cellHeader
	if err := json.Unmarshal(raw, &h); err != nil {
		return "", err
	}
	return h.ID, nil
}

func replaceCellSource(raw json.RawMessage, newSource, newType string) (json.RawMessage, error) {
	var cell map[string]interface{}
	if err := json.Unmarshal(raw, &cell); err != nil {
		return nil, err
	}
	cell["source"] = splitSourceLines(newSource)
	if newType == CellTypeCode || newType == CellTypeMarkdown {
		cell["cell_type"] = newType
		if newType == CellTypeCode {
			if _, ok := cell["outputs"]; !ok {
				cell["outputs"] = []interface{}{}
			}
			if _, ok := cell["execution_count"]; !ok {
				cell["execution_count"] = nil
			}
		} else {
			delete(cell, "outputs")
			delete(cell, "execution_count")
		}
	}
	return json.Marshal(cell)
}

func buildCell(cellType, source string) json.RawMessage {
	cell := map[string]interface{}{
		"cell_type": cellType,
		"metadata":  map[string]interface{}{},
		"source":    splitSourceLines(source),
	}
	if cellType == CellTypeCode {
		cell["outputs"] = []interface{}{}
		cell["execution_count"] = nil
	}
	b, _ := json.Marshal(cell)
	return b
}

// splitSourceLines matches the nbformat v4 convention: source is a list of
// strings, each ending with "\n" except the last.
func splitSourceLines(s string) []string {
	if s == "" {
		return []string{}
	}
	parts := strings.SplitAfter(s, "\n")
	return parts
}

func writeNotebook(path string, nb *Notebook) error {
	if nb.Metadata == nil {
		nb.Metadata = map[string]interface{}{}
	}
	if nb.Nbformat == 0 {
		nb.Nbformat = 4
	}
	if nb.NbformatMinor == 0 {
		nb.NbformatMinor = 5
	}
	data, err := json.MarshalIndent(nb, "", " ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

var _ tool.Tool = (*NotebookEditTool)(nil)
