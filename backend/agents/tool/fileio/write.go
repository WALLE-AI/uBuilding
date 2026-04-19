package fileio

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/wall-ai/ubuilding/backend/agents"
	"github.com/wall-ai/ubuilding/backend/agents/tool"
)

// WriteName is the tool name exposed to the model.
const WriteName = "Write"

// WriteInput matches claude-code's Write input.
type WriteInput struct {
	FilePath string `json:"file_path"`
	Content  string `json:"content"`
}

// WriteOutput is the structured result.
type WriteOutput struct {
	FilePath string `json:"file_path"`
	Bytes    int    `json:"bytes"`
	Created  bool   `json:"created,omitempty"`
}

// WriteTool implements tool.Tool for creating/overwriting files.
type WriteTool struct {
	tool.ToolDefaults
	workspaceRoots []string
}

// NewWriteTool returns a Write tool.
func NewWriteTool(workspaceRoots ...string) *WriteTool {
	return &WriteTool{workspaceRoots: workspaceRoots}
}

func (w *WriteTool) Name() string                             { return WriteName }
func (w *WriteTool) Aliases() []string                        { return []string{"FileWrite"} }
func (w *WriteTool) IsReadOnly(_ json.RawMessage) bool        { return false }
func (w *WriteTool) IsConcurrencySafe(_ json.RawMessage) bool { return false }
func (w *WriteTool) IsDestructive(_ json.RawMessage) bool     { return true }
func (w *WriteTool) MaxResultSizeChars() int                  { return MaxResultChars }

func (w *WriteTool) InputSchema() *tool.JSONSchema {
	return &tool.JSONSchema{
		Type: "object",
		Properties: map[string]*tool.SchemaProperty{
			"file_path": {Type: "string", Description: "Absolute path to the file to write."},
			"content":   {Type: "string", Description: "Full file contents."},
		},
		Required: []string{"file_path", "content"},
	}
}

func (w *WriteTool) Description(input json.RawMessage) string {
	var in WriteInput
	_ = json.Unmarshal(input, &in)
	if in.FilePath == "" {
		return "Write a file"
	}
	return "Write " + in.FilePath
}

func (w *WriteTool) Prompt(opts tool.PromptOptions) string {
	readRef := resolvePeer(opts, "Read")
	editRef := resolvePeer(opts, "Edit")
	return `Writes a file to the local filesystem.

Usage:
- This tool will overwrite the existing file if there is one at the provided path.
- If this is an existing file, you MUST use the ` + "`" + readRef + "`" + ` tool first to read the file's contents. This tool will fail if you did not read the file first.
- Prefer the ` + editRef + ` tool for modifying existing files — it only sends the diff. Only use this tool to create new files or for complete rewrites.
- NEVER create documentation files (*.md) or README files unless explicitly requested by the User.
- Only use emojis if the user explicitly requests it. Avoid writing emojis to files unless asked.
- The file_path parameter must be an absolute path, not a relative path. Parent directories are created automatically.`
}

func (w *WriteTool) ValidateInput(input json.RawMessage, _ *agents.ToolUseContext) *tool.ValidationResult {
	var in WriteInput
	if err := json.Unmarshal(input, &in); err != nil {
		return &tool.ValidationResult{Valid: false, Message: fmt.Sprintf("invalid input: %v", err)}
	}
	if err := EnsureAbsolute(in.FilePath); err != nil {
		return &tool.ValidationResult{Valid: false, Message: err.Error()}
	}
	return &tool.ValidationResult{Valid: true}
}

func (w *WriteTool) CheckPermissions(input json.RawMessage, _ *agents.ToolUseContext) (*tool.PermissionResult, error) {
	return &tool.PermissionResult{Behavior: tool.PermissionAllow, UpdatedInput: input, DecisionReason: "write-default-allow"}, nil
}

func (w *WriteTool) Call(ctx context.Context, input json.RawMessage, toolCtx *agents.ToolUseContext) (*tool.ToolResult, error) {
	var in WriteInput
	if err := json.Unmarshal(input, &in); err != nil {
		return nil, fmt.Errorf("invalid input: %w", err)
	}
	path, err := Resolve(in.FilePath)
	if err != nil {
		return nil, err
	}
	if err := EnsureInWorkspace(path, w.workspaceRoots); err != nil {
		return nil, err
	}
	created := false
	if _, err := os.Stat(path); err != nil {
		if !os.IsNotExist(err) {
			return nil, err
		}
		created = true
	} else {
		// Existing file: require fresh Read.
		fresh, herr := HasFreshRead(toolCtx, path)
		if herr != nil {
			return nil, herr
		}
		if !fresh {
			return nil, fmt.Errorf("refuse to overwrite: read %s first (or re-read if it changed on disk)", path)
		}
	}
	if err := os.MkdirAll(dirOf(path), 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, []byte(in.Content), 0o644); err != nil {
		return nil, err
	}
	_, _ = RecordReadState(toolCtx, path)
	return &tool.ToolResult{Data: WriteOutput{FilePath: path, Bytes: len(in.Content), Created: created}}, nil
}

func (w *WriteTool) MapToolResultToParam(content interface{}, toolUseID string) *agents.ContentBlock {
	return &agents.ContentBlock{
		Type:      agents.ContentBlockToolResult,
		ToolUseID: toolUseID,
		Content:   renderWriteOutput(content),
	}
}

func renderWriteOutput(content interface{}) string {
	var out WriteOutput
	switch v := content.(type) {
	case WriteOutput:
		out = v
	case *WriteOutput:
		if v != nil {
			out = *v
		}
	case string:
		return v
	default:
		b, _ := json.Marshal(content)
		return string(b)
	}
	verb := "Wrote"
	if out.Created {
		verb = "Created"
	}
	return fmt.Sprintf("%s %s (%d bytes)", verb, out.FilePath, out.Bytes)
}

var _ tool.Tool = (*WriteTool)(nil)
