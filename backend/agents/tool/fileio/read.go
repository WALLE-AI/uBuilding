package fileio

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/wall-ai/ubuilding/backend/agents"
	"github.com/wall-ai/ubuilding/backend/agents/tool"
)

const (
	// ReadName is the tool name exposed to the model.
	ReadName = "Read"

	// DefaultReadLimit caps the number of lines returned when the caller
	// does not specify a limit.
	DefaultReadLimit = 2000

	// MaxLineLength caps individual line length in the returned view.
	MaxLineLength = 2000

	// MaxResultChars bounds the total serialized output. 100k mirrors TS.
	MaxResultChars = 100_000
)

// ReadInput matches claude-code's Read input.
type ReadInput struct {
	FilePath string `json:"file_path"`
	Offset   int    `json:"offset,omitempty"`
	Limit    int    `json:"limit,omitempty"`
}

// ReadOutput is the structured result surfaced to downstream rendering.
type ReadOutput struct {
	FilePath   string `json:"file_path"`
	Content    string `json:"content"`
	Binary     bool   `json:"binary,omitempty"`
	TotalLines int    `json:"total_lines"`
	OffsetUsed int    `json:"offset_used"`
	LimitUsed  int    `json:"limit_used"`
	Truncated  bool   `json:"truncated,omitempty"`
}

// ReadTool implements tool.Tool for reading file contents.
type ReadTool struct {
	tool.ToolDefaults
	workspaceRoots []string
}

// NewReadTool returns a Read tool.
func NewReadTool(workspaceRoots ...string) *ReadTool {
	return &ReadTool{workspaceRoots: workspaceRoots}
}

func (r *ReadTool) Name() string                            { return ReadName }
func (r *ReadTool) Aliases() []string                       { return []string{"FileRead"} }
func (r *ReadTool) IsReadOnly(_ json.RawMessage) bool       { return true }
func (r *ReadTool) IsConcurrencySafe(_ json.RawMessage) bool { return true }
func (r *ReadTool) MaxResultSizeChars() int                 { return MaxResultChars }

func (r *ReadTool) InputSchema() *tool.JSONSchema {
	return &tool.JSONSchema{
		Type: "object",
		Properties: map[string]*tool.SchemaProperty{
			"file_path": {Type: "string", Description: "Absolute path to the file to read."},
			"offset":    {Type: "integer", Description: "1-indexed line number to start reading from."},
			"limit":     {Type: "integer", Description: "Maximum number of lines to read."},
		},
		Required: []string{"file_path"},
	}
}

func (r *ReadTool) Description(input json.RawMessage) string {
	var in ReadInput
	_ = json.Unmarshal(input, &in)
	if in.FilePath == "" {
		return "Read a file"
	}
	return "Read " + in.FilePath
}

func (r *ReadTool) Prompt(_ tool.PromptOptions) string {
	return `Reads a file from the local filesystem.

Usage:
- file_path MUST be an absolute path.
- Optional offset (1-indexed line number) and limit (max lines) let you page through large files.
- Output is returned with 1-indexed line numbers in "cat -n" style.
- Lines longer than ` + fmt.Sprintf("%d", MaxLineLength) + ` chars are truncated.
- Binary files are detected and a placeholder is returned instead of raw bytes.
- Before editing a file with Edit/Write, you SHOULD Read it first so the edit gate accepts the change.`
}

func (r *ReadTool) ValidateInput(input json.RawMessage, _ *agents.ToolUseContext) *tool.ValidationResult {
	var in ReadInput
	if err := json.Unmarshal(input, &in); err != nil {
		return &tool.ValidationResult{Valid: false, Message: fmt.Sprintf("invalid input: %v", err)}
	}
	if err := EnsureAbsolute(in.FilePath); err != nil {
		return &tool.ValidationResult{Valid: false, Message: err.Error()}
	}
	if in.Offset < 0 || in.Limit < 0 {
		return &tool.ValidationResult{Valid: false, Message: "offset/limit must be non-negative"}
	}
	return &tool.ValidationResult{Valid: true}
}

func (r *ReadTool) CheckPermissions(input json.RawMessage, _ *agents.ToolUseContext) (*tool.PermissionResult, error) {
	return &tool.PermissionResult{Behavior: tool.PermissionAllow, UpdatedInput: input, DecisionReason: "read-is-safe"}, nil
}

func (r *ReadTool) Call(ctx context.Context, input json.RawMessage, toolCtx *agents.ToolUseContext) (*tool.ToolResult, error) {
	var in ReadInput
	if err := json.Unmarshal(input, &in); err != nil {
		return nil, fmt.Errorf("invalid input: %w", err)
	}
	path, err := Resolve(in.FilePath)
	if err != nil {
		return nil, err
	}
	if err := EnsureInWorkspace(path, r.workspaceRoots); err != nil {
		return nil, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		return nil, fmt.Errorf("path is a directory: %s", path)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if isBinary(data) {
		_, _ = RecordReadState(toolCtx, path)
		return &tool.ToolResult{Data: ReadOutput{
			FilePath: path,
			Content:  fmt.Sprintf("<binary file, %d bytes>", len(data)),
			Binary:   true,
		}}, nil
	}

	offset := in.Offset
	if offset <= 0 {
		offset = 1
	}
	limit := in.Limit
	if limit <= 0 {
		limit = DefaultReadLimit
	}

	content, totalLines, truncated := renderWithLineNumbers(data, offset, limit)
	_, _ = RecordReadState(toolCtx, path)

	return &tool.ToolResult{Data: ReadOutput{
		FilePath:   path,
		Content:    content,
		TotalLines: totalLines,
		OffsetUsed: offset,
		LimitUsed:  limit,
		Truncated:  truncated,
	}}, nil
}

func (r *ReadTool) MapToolResultToParam(content interface{}, toolUseID string) *agents.ContentBlock {
	return &agents.ContentBlock{
		Type:      agents.ContentBlockToolResult,
		ToolUseID: toolUseID,
		Content:   renderReadOutput(content),
	}
}

func renderReadOutput(content interface{}) string {
	var out ReadOutput
	switch v := content.(type) {
	case ReadOutput:
		out = v
	case *ReadOutput:
		if v != nil {
			out = *v
		}
	case string:
		return v
	default:
		b, _ := json.Marshal(content)
		return string(b)
	}
	if out.Binary {
		return out.Content
	}
	if out.Truncated {
		return out.Content + fmt.Sprintf("\n… (truncated at line %d; total %d lines)\n", out.OffsetUsed+out.LimitUsed-1, out.TotalLines)
	}
	return out.Content
}

// isBinary returns true when the first 8k bytes contain a NUL byte or a high
// proportion of non-printable characters.
func isBinary(data []byte) bool {
	n := len(data)
	if n > 8192 {
		n = 8192
	}
	head := data[:n]
	if bytes.IndexByte(head, 0) >= 0 {
		return true
	}
	var nonPrint int
	for _, b := range head {
		if b == '\t' || b == '\n' || b == '\r' {
			continue
		}
		if b < 0x20 || b == 0x7f {
			nonPrint++
		}
	}
	return n > 0 && nonPrint*10 > n // > 10% non-printable
}

// renderWithLineNumbers produces "cat -n"-style output for lines [offset, offset+limit).
func renderWithLineNumbers(data []byte, offset, limit int) (content string, totalLines int, truncated bool) {
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	var sb strings.Builder
	lineNum := 0
	emitted := 0
	for scanner.Scan() {
		lineNum++
		if lineNum < offset {
			continue
		}
		if emitted >= limit {
			totalLines = countRemaining(scanner, lineNum)
			truncated = true
			break
		}
		line := scanner.Text()
		if len(line) > MaxLineLength {
			line = line[:MaxLineLength] + "…"
		}
		fmt.Fprintf(&sb, "%6d\t%s\n", lineNum, line)
		emitted++
	}
	if err := scanner.Err(); err == nil && !truncated {
		totalLines = lineNum
	}
	return sb.String(), totalLines, truncated
}

func countRemaining(scanner *bufio.Scanner, lastSeen int) int {
	count := lastSeen
	for scanner.Scan() {
		count++
	}
	return count
}

var _ tool.Tool = (*ReadTool)(nil)
