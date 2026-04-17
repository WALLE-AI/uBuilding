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
type OutputInput struct {
	// BashID is the id returned by a previous Bash/PowerShell call that was
	// invoked with run_in_background=true.
	BashID string `json:"bash_id"`
	// Incremental, when true (default), returns only the output emitted since
	// the last TaskOutput call for this id. Set false to re-read everything.
	Incremental *bool `json:"incremental,omitempty"`
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

func (t *OutputTool) Name() string                            { return OutputName }
func (t *OutputTool) IsReadOnly(_ json.RawMessage) bool       { return true }
func (t *OutputTool) IsConcurrencySafe(_ json.RawMessage) bool { return true }

func (t *OutputTool) InputSchema() *tool.JSONSchema {
	return &tool.JSONSchema{
		Type: "object",
		Properties: map[string]*tool.SchemaProperty{
			"bash_id":     {Type: "string", Description: "ID returned by Bash / PowerShell with run_in_background=true."},
			"incremental": {Type: "boolean", Description: "Return only newly emitted output since the last call (default true)."},
		},
		Required: []string{"bash_id"},
	}
}

func (t *OutputTool) Description(_ json.RawMessage) string { return "Read output from a background shell job" }

func (t *OutputTool) Prompt(_ tool.PromptOptions) string {
	return `Reads captured output from a background shell job started via Bash / PowerShell (run_in_background=true).

Params:
- bash_id (required): the id returned by the background Bash call.
- incremental: default true — returns only bytes since the previous TaskOutput; false returns the full buffer.

Returns status (running | succeeded | failed | cancelled), exit_code, output slice, and output_cursor for pagination.`
}

func (t *OutputTool) ValidateInput(input json.RawMessage, _ *agents.ToolUseContext) *tool.ValidationResult {
	var in OutputInput
	if err := json.Unmarshal(input, &in); err != nil {
		return &tool.ValidationResult{Valid: false, Message: fmt.Sprintf("invalid input: %v", err)}
	}
	if strings.TrimSpace(in.BashID) == "" {
		return &tool.ValidationResult{Valid: false, Message: "bash_id required"}
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
	mgr := managerFromCtx(toolCtx)
	if mgr == nil {
		return nil, errors.New("TaskOutput: no bg.Manager attached to context")
	}
	advance := true
	if in.Incremental != nil {
		advance = *in.Incremental
	}
	job, slice, trunc, err := mgr.ReadOutput(in.BashID, advance)
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

func (t *StopTool) Name() string                            { return StopName }
func (t *StopTool) IsReadOnly(_ json.RawMessage) bool       { return false }
func (t *StopTool) IsConcurrencySafe(_ json.RawMessage) bool { return true }
func (t *StopTool) IsDestructive(_ json.RawMessage) bool    { return true }

func (t *StopTool) InputSchema() *tool.JSONSchema {
	return &tool.JSONSchema{
		Type: "object",
		Properties: map[string]*tool.SchemaProperty{
			"id": {Type: "string", Description: "Background-shell bash_id or task-graph node id to cancel."},
		},
		Required: []string{"id"},
	}
}

func (t *StopTool) Description(_ json.RawMessage) string { return "Cancel a background job or task-graph node" }

func (t *StopTool) Prompt(_ tool.PromptOptions) string {
	return `Cancels an in-progress background shell job OR a task-graph node. The tool tries the bg-shell manager first (ids start with "bash_") and falls back to the task-graph store.`
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
