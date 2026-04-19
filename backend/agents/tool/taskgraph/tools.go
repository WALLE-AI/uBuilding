package taskgraph

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/wall-ai/ubuilding/backend/agents"
	"github.com/wall-ai/ubuilding/backend/agents/tool"
)

// Tool names (align with claude-code TodoV2).
const (
	CreateName = "TaskCreate"
	GetName    = "TaskGet"
	UpdateName = "TaskUpdate"
	ListName   = "TaskList"
	// NOTE: TaskStop is registered by the `bg` package; the task-graph Store
	// implements bg.GraphStopper so TaskStop can cancel nodes via the graph
	// side without needing a separate tool here.
)

// ──────────────────────────────────────────────────────────────────────────
// TaskCreate
// ──────────────────────────────────────────────────────────────────────────

// CreateInput is the TaskCreate input. Mirrors claude-code-main's TaskCreate
// schema field names (title/description/activeForm/owner/metadata) with the
// Go store's additional parent/dependency affordances.
type CreateInput struct {
	Title       string            `json:"title"`
	Description string            `json:"description,omitempty"`
	ActiveForm  string            `json:"activeForm,omitempty"`
	Status      string            `json:"status,omitempty"`
	Owner       string            `json:"owner,omitempty"`
	ParentID    string            `json:"parent_id,omitempty"`
	DependsOn   []string          `json:"depends_on,omitempty"`
	Payload     map[string]string `json:"payload,omitempty"`
}

// CreateTool implements tool.Tool for TaskCreate.
type CreateTool struct{ tool.ToolDefaults }

// NewCreateTool returns a TaskCreate tool.
func NewCreateTool() *CreateTool { return &CreateTool{} }

func (t *CreateTool) Name() string                             { return CreateName }
func (t *CreateTool) IsReadOnly(_ json.RawMessage) bool        { return false }
func (t *CreateTool) IsConcurrencySafe(_ json.RawMessage) bool { return false }

func (t *CreateTool) InputSchema() *tool.JSONSchema {
	return &tool.JSONSchema{
		Type: "object",
		Properties: map[string]*tool.SchemaProperty{
			"title":       {Type: "string", Description: "Imperative task title (upstream \"subject\"), e.g. \"Fix authentication bug\"."},
			"description": {Type: "string", Description: "Long-form description of what needs to be done."},
			"activeForm":  {Type: "string", Description: "Present-continuous label shown in the spinner while in_progress, e.g. \"Fixing authentication bug\". If omitted, the title is reused."},
			"status":      {Type: "string", Description: "Initial status (default: pending)."},
			"owner":       {Type: "string", Description: "Optional agent id to claim the task at creation time."},
			"parent_id":   {Type: "string", Description: "Parent task id."},
			"depends_on":  {Type: "array", Description: "Task ids this task depends on (upstream \"blockedBy\")."},
			"payload":     {Type: "object", Description: "Arbitrary string metadata (upstream \"metadata\")."},
		},
		Required: []string{"title"},
	}
}

func (t *CreateTool) Description(_ json.RawMessage) string {
	return "Create a new task in the task list"
}

func (t *CreateTool) Prompt(opts tool.PromptOptions) string { return buildCreatePrompt(opts) }

func (t *CreateTool) ValidateInput(input json.RawMessage, _ *agents.ToolUseContext) *tool.ValidationResult {
	var in CreateInput
	if err := json.Unmarshal(input, &in); err != nil {
		return &tool.ValidationResult{Valid: false, Message: fmt.Sprintf("invalid input: %v", err)}
	}
	if strings.TrimSpace(in.Title) == "" {
		return &tool.ValidationResult{Valid: false, Message: "title required"}
	}
	if in.Status != "" {
		if _, ok := ValidStatuses[in.Status]; !ok {
			return &tool.ValidationResult{Valid: false, Message: "invalid status"}
		}
	}
	return &tool.ValidationResult{Valid: true}
}

func (t *CreateTool) CheckPermissions(input json.RawMessage, _ *agents.ToolUseContext) (*tool.PermissionResult, error) {
	return &tool.PermissionResult{Behavior: tool.PermissionAllow, UpdatedInput: input, DecisionReason: "taskcreate-session-scope"}, nil
}

func (t *CreateTool) Call(_ context.Context, input json.RawMessage, toolCtx *agents.ToolUseContext) (*tool.ToolResult, error) {
	var in CreateInput
	if err := json.Unmarshal(input, &in); err != nil {
		return nil, err
	}
	store := storeFromCtx(toolCtx)
	if store == nil {
		return nil, errors.New("TaskCreate: no TaskGraph store attached to context")
	}
	node, err := store.Add(Node{
		Title: in.Title, Description: in.Description, ActiveForm: in.ActiveForm,
		Status: in.Status, Owner: in.Owner,
		ParentID: in.ParentID, DependsOn: in.DependsOn, Payload: in.Payload,
	})
	if err != nil {
		return nil, err
	}
	return &tool.ToolResult{Data: node}, nil
}

func (t *CreateTool) MapToolResultToParam(content interface{}, toolUseID string) *agents.ContentBlock {
	return &agents.ContentBlock{Type: agents.ContentBlockToolResult, ToolUseID: toolUseID, Content: renderNode(content)}
}

// ──────────────────────────────────────────────────────────────────────────
// TaskGet
// ──────────────────────────────────────────────────────────────────────────

// GetInput is the TaskGet input.
type GetInput struct {
	ID string `json:"id"`
}

// GetTool implements tool.Tool for TaskGet.
type GetTool struct{ tool.ToolDefaults }

// NewGetTool returns a TaskGet tool.
func NewGetTool() *GetTool { return &GetTool{} }

func (t *GetTool) Name() string                             { return GetName }
func (t *GetTool) IsReadOnly(_ json.RawMessage) bool        { return true }
func (t *GetTool) IsConcurrencySafe(_ json.RawMessage) bool { return true }

func (t *GetTool) InputSchema() *tool.JSONSchema {
	return &tool.JSONSchema{
		Type:       "object",
		Properties: map[string]*tool.SchemaProperty{"id": {Type: "string", Description: "Task id."}},
		Required:   []string{"id"},
	}
}

func (t *GetTool) Description(_ json.RawMessage) string {
	return "Get a task by ID from the task list"
}
func (t *GetTool) Prompt(_ tool.PromptOptions) string { return getPromptText }

func (t *GetTool) ValidateInput(input json.RawMessage, _ *agents.ToolUseContext) *tool.ValidationResult {
	var in GetInput
	if err := json.Unmarshal(input, &in); err != nil {
		return &tool.ValidationResult{Valid: false, Message: err.Error()}
	}
	if in.ID == "" {
		return &tool.ValidationResult{Valid: false, Message: "id required"}
	}
	return &tool.ValidationResult{Valid: true}
}

func (t *GetTool) CheckPermissions(input json.RawMessage, _ *agents.ToolUseContext) (*tool.PermissionResult, error) {
	return &tool.PermissionResult{Behavior: tool.PermissionAllow, UpdatedInput: input, DecisionReason: "taskget-read-only"}, nil
}

func (t *GetTool) Call(_ context.Context, input json.RawMessage, toolCtx *agents.ToolUseContext) (*tool.ToolResult, error) {
	var in GetInput
	if err := json.Unmarshal(input, &in); err != nil {
		return nil, err
	}
	store := storeFromCtx(toolCtx)
	if store == nil {
		return nil, errors.New("TaskGet: no TaskGraph store attached to context")
	}
	n, ok := store.Get(in.ID)
	if !ok {
		return nil, fmt.Errorf("task %s not found", in.ID)
	}
	return &tool.ToolResult{Data: n}, nil
}

func (t *GetTool) MapToolResultToParam(content interface{}, toolUseID string) *agents.ContentBlock {
	return &agents.ContentBlock{Type: agents.ContentBlockToolResult, ToolUseID: toolUseID, Content: renderNode(content)}
}

// ──────────────────────────────────────────────────────────────────────────
// TaskUpdate
// ──────────────────────────────────────────────────────────────────────────

// UpdateInput is the TaskUpdate input. Null/omitted fields leave the node
// unchanged. Mirrors claude-code-main TaskUpdate (subject→title,
// metadata→payload, blockedBy→depends_on).
type UpdateInput struct {
	ID          string             `json:"id"`
	Title       *string            `json:"title,omitempty"`
	Description *string            `json:"description,omitempty"`
	ActiveForm  *string            `json:"activeForm,omitempty"`
	Status      *string            `json:"status,omitempty"`
	Owner       *string            `json:"owner,omitempty"`
	ParentID    *string            `json:"parent_id,omitempty"`
	DependsOn   *[]string          `json:"depends_on,omitempty"`
	Payload     *map[string]string `json:"payload,omitempty"`
}

// UpdateTool implements tool.Tool for TaskUpdate.
type UpdateTool struct{ tool.ToolDefaults }

// NewUpdateTool returns a TaskUpdate tool.
func NewUpdateTool() *UpdateTool { return &UpdateTool{} }

func (t *UpdateTool) Name() string                             { return UpdateName }
func (t *UpdateTool) IsReadOnly(_ json.RawMessage) bool        { return false }
func (t *UpdateTool) IsConcurrencySafe(_ json.RawMessage) bool { return false }

func (t *UpdateTool) InputSchema() *tool.JSONSchema {
	return &tool.JSONSchema{
		Type: "object",
		Properties: map[string]*tool.SchemaProperty{
			"id":          {Type: "string", Description: "Task id."},
			"title":       {Type: "string", Description: "New imperative title (upstream \"subject\")."},
			"description": {Type: "string", Description: "Replacement description."},
			"activeForm":  {Type: "string", Description: "Replacement present-continuous label shown in the spinner."},
			"status":      {Type: "string", Description: "New status (pending|in_progress|blocked|completed|cancelled|failed)."},
			"owner":       {Type: "string", Description: "New owner (empty string = unassign)."},
			"parent_id":   {Type: "string", Description: "New parent (empty string = detach)."},
			"depends_on":  {Type: "array", Description: "Replacement dependency list (upstream \"blockedBy\")."},
			"payload":     {Type: "object", Description: "Replacement payload map (upstream \"metadata\")."},
		},
		Required: []string{"id"},
	}
}

func (t *UpdateTool) Description(_ json.RawMessage) string {
	return "Update a task in the task list"
}

func (t *UpdateTool) Prompt(_ tool.PromptOptions) string { return updatePromptText }

func (t *UpdateTool) ValidateInput(input json.RawMessage, _ *agents.ToolUseContext) *tool.ValidationResult {
	var in UpdateInput
	if err := json.Unmarshal(input, &in); err != nil {
		return &tool.ValidationResult{Valid: false, Message: err.Error()}
	}
	if in.ID == "" {
		return &tool.ValidationResult{Valid: false, Message: "id required"}
	}
	if in.Status != nil {
		if _, ok := ValidStatuses[*in.Status]; !ok {
			return &tool.ValidationResult{Valid: false, Message: "invalid status"}
		}
	}
	return &tool.ValidationResult{Valid: true}
}

func (t *UpdateTool) CheckPermissions(input json.RawMessage, _ *agents.ToolUseContext) (*tool.PermissionResult, error) {
	return &tool.PermissionResult{Behavior: tool.PermissionAllow, UpdatedInput: input, DecisionReason: "taskupdate-session-scope"}, nil
}

func (t *UpdateTool) Call(_ context.Context, input json.RawMessage, toolCtx *agents.ToolUseContext) (*tool.ToolResult, error) {
	var in UpdateInput
	if err := json.Unmarshal(input, &in); err != nil {
		return nil, err
	}
	store := storeFromCtx(toolCtx)
	if store == nil {
		return nil, errors.New("TaskUpdate: no TaskGraph store attached to context")
	}
	n, err := store.Update(in.ID, UpdateFields{
		Title: in.Title, Description: in.Description, ActiveForm: in.ActiveForm,
		Status: in.Status, Owner: in.Owner,
		ParentID: in.ParentID, DependsOn: in.DependsOn, Payload: in.Payload,
	})
	if err != nil {
		return nil, err
	}
	return &tool.ToolResult{Data: n}, nil
}

func (t *UpdateTool) MapToolResultToParam(content interface{}, toolUseID string) *agents.ContentBlock {
	return &agents.ContentBlock{Type: agents.ContentBlockToolResult, ToolUseID: toolUseID, Content: renderNode(content)}
}

// ──────────────────────────────────────────────────────────────────────────
// TaskList
// ──────────────────────────────────────────────────────────────────────────

// ListInput is the TaskList input.
type ListInput struct {
	Status string `json:"status,omitempty"`
}

// ListTool implements tool.Tool for TaskList.
type ListTool struct{ tool.ToolDefaults }

// NewListTool returns a TaskList tool.
func NewListTool() *ListTool { return &ListTool{} }

func (t *ListTool) Name() string                             { return ListName }
func (t *ListTool) IsReadOnly(_ json.RawMessage) bool        { return true }
func (t *ListTool) IsConcurrencySafe(_ json.RawMessage) bool { return true }

func (t *ListTool) InputSchema() *tool.JSONSchema {
	return &tool.JSONSchema{
		Type: "object",
		Properties: map[string]*tool.SchemaProperty{
			"status": {Type: "string", Description: "Filter by status (empty = all)."},
		},
	}
}

func (t *ListTool) Description(_ json.RawMessage) string {
	return "List all tasks in the task list"
}
func (t *ListTool) Prompt(opts tool.PromptOptions) string { return buildListPrompt(opts) }

func (t *ListTool) ValidateInput(input json.RawMessage, _ *agents.ToolUseContext) *tool.ValidationResult {
	var in ListInput
	if err := json.Unmarshal(input, &in); err != nil {
		return &tool.ValidationResult{Valid: false, Message: err.Error()}
	}
	if in.Status != "" {
		if _, ok := ValidStatuses[in.Status]; !ok {
			return &tool.ValidationResult{Valid: false, Message: "invalid status"}
		}
	}
	return &tool.ValidationResult{Valid: true}
}

func (t *ListTool) CheckPermissions(input json.RawMessage, _ *agents.ToolUseContext) (*tool.PermissionResult, error) {
	return &tool.PermissionResult{Behavior: tool.PermissionAllow, UpdatedInput: input, DecisionReason: "tasklist-read-only"}, nil
}

func (t *ListTool) Call(_ context.Context, input json.RawMessage, toolCtx *agents.ToolUseContext) (*tool.ToolResult, error) {
	var in ListInput
	_ = json.Unmarshal(input, &in)
	store := storeFromCtx(toolCtx)
	if store == nil {
		return nil, errors.New("TaskList: no TaskGraph store attached to context")
	}
	return &tool.ToolResult{Data: store.List(in.Status)}, nil
}

func (t *ListTool) MapToolResultToParam(content interface{}, toolUseID string) *agents.ContentBlock {
	return &agents.ContentBlock{Type: agents.ContentBlockToolResult, ToolUseID: toolUseID, Content: renderList(content)}
}

// ──────────────────────────────────────────────────────────────────────────
// helpers
// ──────────────────────────────────────────────────────────────────────────

func storeFromCtx(toolCtx *agents.ToolUseContext) *Store {
	if toolCtx == nil {
		return nil
	}
	if s, ok := toolCtx.TaskGraph.(*Store); ok {
		return s
	}
	return nil
}

func renderNode(content interface{}) string {
	var n Node
	switch v := content.(type) {
	case Node:
		n = v
	case *Node:
		if v != nil {
			n = *v
		}
	case string:
		return v
	default:
		b, _ := json.Marshal(content)
		return string(b)
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "task %s [%s] %s", n.ID, n.Status, n.Title)
	if n.Owner != "" {
		fmt.Fprintf(&sb, " owner=%s", n.Owner)
	}
	if n.ParentID != "" {
		fmt.Fprintf(&sb, " (parent=%s)", n.ParentID)
	}
	if len(n.DependsOn) > 0 {
		fmt.Fprintf(&sb, " deps=%v", n.DependsOn)
	}
	return sb.String()
}

func renderList(content interface{}) string {
	nodes, ok := content.([]Node)
	if !ok {
		b, _ := json.Marshal(content)
		return string(b)
	}
	if len(nodes) == 0 {
		return "No tasks."
	}
	var sb strings.Builder
	for _, n := range nodes {
		fmt.Fprintf(&sb, "- %s [%s] %s\n", n.ID, n.Status, n.Title)
	}
	return sb.String()
}

// Compile-time assertions.
var (
	_ tool.Tool = (*CreateTool)(nil)
	_ tool.Tool = (*GetTool)(nil)
	_ tool.Tool = (*UpdateTool)(nil)
	_ tool.Tool = (*ListTool)(nil)
)
