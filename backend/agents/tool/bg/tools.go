package bg

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/wall-ai/ubuilding/backend/agents"
	"github.com/wall-ai/ubuilding/backend/agents/tool"
)

// Tool names.
const (
	OutputName = "TaskOutput"
	StopName   = "TaskStop"
)

// OutputInput is the TaskOutput input shape.
//
// Upstream claude-code-main renamed AgentOutputTool/BashOutputTool to
// TaskOutput and standardised on `task_id`; it still maps the legacy
// `bash_id` / `agentId` parameter names onto `task_id` at the API layer
// (see utils/api.ts around TASK_OUTPUT_TOOL_NAME). We accept all three here.
type OutputInput struct {
	// BashID is the id returned by a previous Bash/PowerShell call that was
	// invoked with run_in_background=true. Kept for backwards compatibility
	// with the BashOutputTool surface.
	BashID string `json:"bash_id,omitempty"`
	// TaskID is the upstream canonical name for the same value.
	TaskID string `json:"task_id,omitempty"`
	// AgentID is the legacy AgentOutputTool alias.
	AgentID string `json:"agentId,omitempty"`
	// Incremental, when true (default), returns only the output emitted since
	// the last TaskOutput call for this id. Set false to re-read everything.
	Incremental *bool `json:"incremental,omitempty"`
}

// resolveID returns the id the caller supplied via any of the three aliases.
func (in OutputInput) resolveID() string {
	switch {
	case strings.TrimSpace(in.BashID) != "":
		return strings.TrimSpace(in.BashID)
	case strings.TrimSpace(in.TaskID) != "":
		return strings.TrimSpace(in.TaskID)
	case strings.TrimSpace(in.AgentID) != "":
		return strings.TrimSpace(in.AgentID)
	}
	return ""
}

// OutputResult is the TaskOutput result surfaced to the model.
type OutputResult struct {
	BashID       string `json:"bash_id"`
	Status       string `json:"status"`
	ExitCode     int    `json:"exit_code"`
	Output       string `json:"output"`
	Truncated    bool   `json:"truncated,omitempty"`
	Incremental  bool   `json:"incremental"`
	OutputCursor int    `json:"output_cursor"`
}

// StopInput is the TaskStop input shape.
type StopInput struct {
	// ID may be either a background-shell bash_id or a task-graph node id;
	// the tool probes both sources.
	ID string `json:"id"`
}

// StopResult is the TaskStop result.
type StopResult struct {
	ID     string `json:"id"`
	Kind   string `json:"kind"` // "bg" | "graph"
	Status string `json:"status"`
}

// GraphStopper is the hook TaskStopTool consults to cancel a task-graph node.
// Callers wire this to taskgraph.Store.Stop (or equivalent) via
// ToolUseContext.TaskGraph. Kept here as a small interface to avoid an import
// cycle between `bg` and `taskgraph`.
type GraphStopper interface {
	Stop(id string) (status string, ok bool, err error)
}

// ──────────────────────────────────────────────────────────────────────────
// TaskOutputTool
// ──────────────────────────────────────────────────────────────────────────

// OutputTool implements tool.Tool for TaskOutput.
type OutputTool struct {
	tool.ToolDefaults
}

// NewOutputTool returns a TaskOutput tool.
func NewOutputTool() *OutputTool { return &OutputTool{} }

func (t *OutputTool) Name() string                             { return OutputName }
func (t *OutputTool) IsReadOnly(_ json.RawMessage) bool        { return true }
func (t *OutputTool) IsConcurrencySafe(_ json.RawMessage) bool { return true }

// Aliases mirrors upstream's backwards-compatible aliases
// (AgentOutputTool / BashOutputTool) so permission rules and rename
// migrations keep working.
func (t *OutputTool) Aliases() []string { return []string{"AgentOutputTool", "BashOutputTool"} }

func (t *OutputTool) InputSchema() *tool.JSONSchema {
	return &tool.JSONSchema{
		Type: "object",
		Properties: map[string]*tool.SchemaProperty{
			"task_id":     {Type: "string", Description: "ID returned by a Bash/PowerShell call with run_in_background=true. Upstream canonical name."},
			"bash_id":     {Type: "string", Description: "Legacy alias for task_id (BashOutputTool surface)."},
			"agentId":     {Type: "string", Description: "Legacy alias for task_id (AgentOutputTool surface)."},
			"incremental": {Type: "boolean", Description: "Return only newly emitted output since the last call (default true)."},
		},
	}
}

func (t *OutputTool) Description(_ json.RawMessage) string {
	return "[Deprecated] — prefer Read on the task output file path"
}

func (t *OutputTool) Prompt(_ tool.PromptOptions) string {
	return `DEPRECATED: Prefer using the Read tool on the task's output file path instead. Background tasks return their output file path in the tool result, and you receive a <task-notification> with the same path when the task completes — Read that file directly.

- Retrieves output from a running or completed task (background shell, agent, or remote session)
- Takes a ` + "`task_id`" + ` parameter identifying the task (legacy aliases ` + "`bash_id`" + ` / ` + "`agentId`" + ` are accepted for backwards compatibility)
- Returns the task output along with status information
- Use ` + "`incremental=true`" + ` (default) to receive only bytes emitted since the previous call; ` + "`incremental=false`" + ` returns the full captured buffer
- Works with all task types: background shells, async agents, and remote sessions`
}

func (t *OutputTool) ValidateInput(input json.RawMessage, _ *agents.ToolUseContext) *tool.ValidationResult {
	var in OutputInput
	if err := json.Unmarshal(input, &in); err != nil {
		return &tool.ValidationResult{Valid: false, Message: fmt.Sprintf("invalid input: %v", err)}
	}
	if in.resolveID() == "" {
		return &tool.ValidationResult{Valid: false, Message: "task_id required (aliases: bash_id, agentId)"}
	}
	return &tool.ValidationResult{Valid: true}
}

func (t *OutputTool) CheckPermissions(input json.RawMessage, _ *agents.ToolUseContext) (*tool.PermissionResult, error) {
	return &tool.PermissionResult{Behavior: tool.PermissionAllow, UpdatedInput: input, DecisionReason: "taskoutput-read-only"}, nil
}

func (t *OutputTool) Call(ctx context.Context, input json.RawMessage, toolCtx *agents.ToolUseContext) (*tool.ToolResult, error) {
	var in OutputInput
	if err := json.Unmarshal(input, &in); err != nil {
		return nil, err
	}
	id := in.resolveID()
	if id == "" {
		return nil, errors.New("TaskOutput: task_id required")
	}
	mgr := managerFromCtx(toolCtx)
	if mgr == nil {
		return nil, errors.New("TaskOutput: no bg.Manager attached to context")
	}
	advance := true
	if in.Incremental != nil {
		advance = *in.Incremental
	}
	job, slice, trunc, err := mgr.ReadOutput(id, advance)
	if err != nil {
		return nil, err
	}
	return &tool.ToolResult{Data: OutputResult{
		BashID:       job.ID,
		Status:       job.Status,
		ExitCode:     job.ExitCode,
		Output:       slice,
		Truncated:    trunc,
		Incremental:  advance,
		OutputCursor: job.OutputCursor,
	}}, nil
}

func (t *OutputTool) MapToolResultToParam(content interface{}, toolUseID string) *agents.ContentBlock {
	return &agents.ContentBlock{
		Type:      agents.ContentBlockToolResult,
		ToolUseID: toolUseID,
		Content:   renderOutput(content),
	}
}

func renderOutput(content interface{}) string {
	var r OutputResult
	switch v := content.(type) {
	case OutputResult:
		r = v
	case *OutputResult:
		if v != nil {
			r = *v
		}
	case string:
		return v
	default:
		b, _ := json.Marshal(content)
		return string(b)
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "job %s [%s] exit=%d cursor=%d\n", r.BashID, r.Status, r.ExitCode, r.OutputCursor)
	if r.Output != "" {
		sb.WriteString(r.Output)
		if !strings.HasSuffix(r.Output, "\n") {
			sb.WriteString("\n")
		}
	}
	if r.Truncated {
		sb.WriteString("(buffer truncated)\n")
	}
	return sb.String()
}

// ──────────────────────────────────────────────────────────────────────────
// TaskStopTool (dual: bg-shell + task-graph)
// ──────────────────────────────────────────────────────────────────────────

// StopTool implements tool.Tool for TaskStop.
type StopTool struct {
	tool.ToolDefaults
}

// NewStopTool returns a TaskStop tool.
func NewStopTool() *StopTool { return &StopTool{} }

func (t *StopTool) Name() string                             { return StopName }
func (t *StopTool) IsReadOnly(_ json.RawMessage) bool        { return false }
func (t *StopTool) IsConcurrencySafe(_ json.RawMessage) bool { return true }
func (t *StopTool) IsDestructive(_ json.RawMessage) bool     { return true }

// Aliases mirrors upstream's KillShell → TaskStop rename.
func (t *StopTool) Aliases() []string { return []string{"KillShell"} }

func (t *StopTool) InputSchema() *tool.JSONSchema {
	return &tool.JSONSchema{
		Type: "object",
		Properties: map[string]*tool.SchemaProperty{
			"id": {Type: "string", Description: "Background-shell bash_id or task-graph node id to cancel."},
		},
		Required: []string{"id"},
	}
}

func (t *StopTool) Description(_ json.RawMessage) string {
	return `
- Stops a running background task by its ID
- Takes a task_id parameter identifying the task to stop
- Returns a success or failure status
- Use this tool when you need to terminate a long-running task
`
}

func (t *StopTool) Prompt(_ tool.PromptOptions) string {
	return `Cancels an in-progress background task by its ID.

- Takes a ` + "`id`" + ` parameter that may be either a bg-shell id (from Bash / PowerShell with run_in_background=true) or a task-graph node id (from TaskCreate).
- The tool tries the bg-shell manager first (ids starting with "bash_" or owned by the manager) and falls back to the task-graph store for graph nodes.
- Returns the resulting status (e.g. cancelled) and which side handled the stop ("bg" or "graph").
- Use this tool when you need to terminate a long-running task.
`
}

func (t *StopTool) ValidateInput(input json.RawMessage, _ *agents.ToolUseContext) *tool.ValidationResult {
	var in StopInput
	if err := json.Unmarshal(input, &in); err != nil {
		return &tool.ValidationResult{Valid: false, Message: err.Error()}
	}
	if strings.TrimSpace(in.ID) == "" {
		return &tool.ValidationResult{Valid: false, Message: "id required"}
	}
	return &tool.ValidationResult{Valid: true}
}

func (t *StopTool) CheckPermissions(input json.RawMessage, _ *agents.ToolUseContext) (*tool.PermissionResult, error) {
	return &tool.PermissionResult{Behavior: tool.PermissionAllow, UpdatedInput: input, DecisionReason: "taskstop-session-scope"}, nil
}

func (t *StopTool) Call(ctx context.Context, input json.RawMessage, toolCtx *agents.ToolUseContext) (*tool.ToolResult, error) {
	var in StopInput
	if err := json.Unmarshal(input, &in); err != nil {
		return nil, err
	}
	if mgr := managerFromCtx(toolCtx); mgr != nil && (strings.HasPrefix(in.ID, IDPrefix) || mgr.Owns(in.ID)) {
		j, err := mgr.Stop(in.ID, 5*time.Second)
		if err != nil {
			return nil, err
		}
		return &tool.ToolResult{Data: StopResult{ID: in.ID, Kind: "bg", Status: j.Status}}, nil
	}
	if gs := graphStopperFromCtx(toolCtx); gs != nil {
		if status, ok, err := gs.Stop(in.ID); err != nil {
			return nil, err
		} else if ok {
			return &tool.ToolResult{Data: StopResult{ID: in.ID, Kind: "graph", Status: status}}, nil
		}
	}
	return nil, fmt.Errorf("TaskStop: id %s not found in bg manager or task graph", in.ID)
}

func (t *StopTool) MapToolResultToParam(content interface{}, toolUseID string) *agents.ContentBlock {
	return &agents.ContentBlock{
		Type:      agents.ContentBlockToolResult,
		ToolUseID: toolUseID,
		Content:   renderStop(content),
	}
}

func renderStop(content interface{}) string {
	var r StopResult
	switch v := content.(type) {
	case StopResult:
		r = v
	case *StopResult:
		if v != nil {
			r = *v
		}
	case string:
		return v
	default:
		b, _ := json.Marshal(content)
		return string(b)
	}
	return fmt.Sprintf("Stopped %s (%s) → %s", r.ID, r.Kind, r.Status)
}

// ──────────────────────────────────────────────────────────────────────────
// helpers
// ──────────────────────────────────────────────────────────────────────────

func managerFromCtx(toolCtx *agents.ToolUseContext) *Manager {
	if toolCtx == nil {
		return nil
	}
	if m, ok := toolCtx.TaskManager.(*Manager); ok {
		return m
	}
	return nil
}

func graphStopperFromCtx(toolCtx *agents.ToolUseContext) GraphStopper {
	if toolCtx == nil {
		return nil
	}
	if gs, ok := toolCtx.TaskGraph.(GraphStopper); ok {
		return gs
	}
	return nil
}

// Compile-time assertions.
var (
	_ tool.Tool = (*OutputTool)(nil)
	_ tool.Tool = (*StopTool)(nil)
)
