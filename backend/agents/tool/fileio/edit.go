package fileio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/wall-ai/ubuilding/backend/agents"
	"github.com/wall-ai/ubuilding/backend/agents/tool"
)

// EditName is the tool name exposed to the model.
const EditName = "Edit"

// EditInput matches claude-code's Edit input.
type EditInput struct {
	FilePath   string `json:"file_path"`
	OldString  string `json:"old_string"`
	NewString  string `json:"new_string"`
	ReplaceAll bool   `json:"replace_all,omitempty"`
}

// EditOutput captures the structured result.
type EditOutput struct {
	FilePath string `json:"file_path"`
	Replaced int    `json:"replaced"`
	Created  bool   `json:"created,omitempty"`
}

// EditTool implements tool.Tool for in-place string replacement.
type EditTool struct {
	tool.ToolDefaults
	workspaceRoots []string
}

// NewEditTool returns an Edit tool.
func NewEditTool(workspaceRoots ...string) *EditTool {
	return &EditTool{workspaceRoots: workspaceRoots}
}

func (e *EditTool) Name() string                             { return EditName }
func (e *EditTool) Aliases() []string                        { return []string{"FileEdit"} }
func (e *EditTool) IsReadOnly(_ json.RawMessage) bool        { return false }
func (e *EditTool) IsConcurrencySafe(_ json.RawMessage) bool { return false }
func (e *EditTool) IsDestructive(_ json.RawMessage) bool     { return true }
func (e *EditTool) MaxResultSizeChars() int                  { return MaxResultChars }

func (e *EditTool) InputSchema() *tool.JSONSchema {
	return &tool.JSONSchema{
		Type: "object",
		Properties: map[string]*tool.SchemaProperty{
			"file_path":   {Type: "string", Description: "Absolute path to the file to edit."},
			"old_string":  {Type: "string", Description: "Exact text to replace. Empty string + nonexistent file = create."},
			"new_string":  {Type: "string", Description: "Replacement text. Must differ from old_string."},
			"replace_all": {Type: "boolean", Description: "Replace every occurrence (default: only one)."},
		},
		Required: []string{"file_path", "old_string", "new_string"},
	}
}

func (e *EditTool) Description(input json.RawMessage) string {
	var in EditInput
	_ = json.Unmarshal(input, &in)
	if in.FilePath == "" {
		return "Edit a file"
	}
	return "Edit " + in.FilePath
}

func (e *EditTool) Prompt(opts tool.PromptOptions) string {
	readRef := resolvePeer(opts, "Read")
	minimalUniquenessHint := ""
	if opts.UserType == "ant" {
		minimalUniquenessHint = "\n- Use the smallest old_string that's clearly unique — usually 2-4 adjacent lines is sufficient. Avoid including 10+ lines of context when less uniquely identifies the target."
	}
	return `Performs exact string replacements in files.

Usage:
- You must use your ` + "`" + readRef + "`" + ` tool at least once in the conversation before editing. This tool will error if you attempt an edit without reading the file. 
- When editing text from Read tool output, ensure you preserve the exact indentation (tabs/spaces) as it appears AFTER the line number prefix. The line number prefix format is: spaces + line number + tab. Everything after that tab is the actual file content to match. Never include any part of the line number prefix in the old_string or new_string.
- ALWAYS prefer editing existing files in the codebase. NEVER write new files unless explicitly required.
- Only use emojis if the user explicitly requests it. Avoid adding emojis to files unless asked.
- The edit will FAIL if ` + "`old_string`" + ` is not unique in the file. Either provide a larger string with more surrounding context to make it unique or use ` + "`replace_all`" + ` to change every instance of ` + "`old_string`" + `.` + minimalUniquenessHint + `
- Use ` + "`replace_all`" + ` for replacing and renaming strings across the file. This parameter is useful if you want to rename a variable for instance.
- To create a new file, pass an empty old_string and the full new file contents as new_string; parent directories are created automatically.
- The edit will FAIL if ` + "`old_string`" + ` and ` + "`new_string`" + ` are identical (no-op edits are rejected).`
}

func (e *EditTool) ValidateInput(input json.RawMessage, _ *agents.ToolUseContext) *tool.ValidationResult {
	var in EditInput
	if err := json.Unmarshal(input, &in); err != nil {
		return &tool.ValidationResult{Valid: false, Message: fmt.Sprintf("invalid input: %v", err)}
	}
	if err := EnsureAbsolute(in.FilePath); err != nil {
		return &tool.ValidationResult{Valid: false, Message: err.Error()}
	}
	if in.OldString == in.NewString {
		return &tool.ValidationResult{Valid: false, Message: "old_string and new_string must differ"}
	}
	return &tool.ValidationResult{Valid: true}
}

func (e *EditTool) CheckPermissions(input json.RawMessage, _ *agents.ToolUseContext) (*tool.PermissionResult, error) {
	return &tool.PermissionResult{Behavior: tool.PermissionAllow, UpdatedInput: input, DecisionReason: "edit-default-allow"}, nil
}

func (e *EditTool) Call(ctx context.Context, input json.RawMessage, toolCtx *agents.ToolUseContext) (*tool.ToolResult, error) {
	var in EditInput
	if err := json.Unmarshal(input, &in); err != nil {
		return nil, fmt.Errorf("invalid input: %w", err)
	}
	path, err := Resolve(in.FilePath)
	if err != nil {
		return nil, err
	}
	if err := EnsureInWorkspace(path, e.workspaceRoots); err != nil {
		return nil, err
	}

	// Creation path: empty old_string + missing file.
	if in.OldString == "" {
		if _, err := os.Stat(path); err == nil {
			return nil, errors.New("cannot create: file already exists (pass the old contents as old_string to edit it)")
		} else if !os.IsNotExist(err) {
			return nil, err
		}
		if err := os.MkdirAll(dirOf(path), 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(path, []byte(in.NewString), 0o644); err != nil {
			return nil, err
		}
		_, _ = RecordReadState(toolCtx, path)
		return &tool.ToolResult{Data: EditOutput{FilePath: path, Replaced: 1, Created: true}}, nil
	}

	// Edit existing file: require fresh Read.
	fresh, err := HasFreshRead(toolCtx, path)
	if err != nil {
		return nil, err
	}
	if !fresh {
		return nil, fmt.Errorf("refuse to edit: read %s first (or re-read if it changed on disk)", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	text := string(data)
	count := strings.Count(text, in.OldString)
	if count == 0 {
		return nil, fmt.Errorf("old_string not found in %s", path)
	}
	if count > 1 && !in.ReplaceAll {
		return nil, fmt.Errorf("old_string matches %d locations in %s; pass replace_all=true or add surrounding context", count, path)
	}
	var updated string
	if in.ReplaceAll {
		updated = strings.ReplaceAll(text, in.OldString, in.NewString)
	} else {
		updated = strings.Replace(text, in.OldString, in.NewString, 1)
	}
	if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
		return nil, err
	}
	replaced := count
	if !in.ReplaceAll {
		replaced = 1
	}
	_, _ = RecordReadState(toolCtx, path)
	return &tool.ToolResult{Data: EditOutput{FilePath: path, Replaced: replaced}}, nil
}

func (e *EditTool) MapToolResultToParam(content interface{}, toolUseID string) *agents.ContentBlock {
	return &agents.ContentBlock{
		Type:      agents.ContentBlockToolResult,
		ToolUseID: toolUseID,
		Content:   renderEditOutput(content),
	}
}

func renderEditOutput(content interface{}) string {
	var out EditOutput
	switch v := content.(type) {
	case EditOutput:
		out = v
	case *EditOutput:
		if v != nil {
			out = *v
		}
	case string:
		return v
	default:
		b, _ := json.Marshal(content)
		return string(b)
	}
	if out.Created {
		return fmt.Sprintf("Created %s", out.FilePath)
	}
	return fmt.Sprintf("Edited %s (%d replacement(s))", out.FilePath, out.Replaced)
}

func dirOf(p string) string {
	if i := strings.LastIndexAny(p, "/\\"); i >= 0 {
		return p[:i]
	}
	return "."
}

var _ tool.Tool = (*EditTool)(nil)
