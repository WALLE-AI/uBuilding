// Package todo implements the TodoWrite tool plus the shared in-memory Store
// that the engine attaches to each ToolUseContext. The store is the only
// source of truth for the session's todo list.
package todo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/wall-ai/ubuilding/backend/agents"
	"github.com/wall-ai/ubuilding/backend/agents/tool"
)

// Name is the tool name exposed to the model.
const Name = "TodoWrite"

// Status values mirror claude-code's TodoWrite.
const (
	StatusPending    = "pending"
	StatusInProgress = "in_progress"
	StatusCompleted  = "completed"
)

// Priority values.
const (
	PriorityHigh   = "high"
	PriorityMedium = "medium"
	PriorityLow    = "low"
)

// Item is a single todo entry.
type Item struct {
	ID       string `json:"id"`
	Content  string `json:"content"`
	Status   string `json:"status"`
	Priority string `json:"priority"`
}

// Store is the session-scoped, concurrency-safe todo list.
type Store struct {
	mu    sync.RWMutex
	items []Item
}

// NewStore returns an empty store.
func NewStore() *Store { return &Store{} }

// Snapshot returns a copy of the current list.
func (s *Store) Snapshot() []Item {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Item, len(s.items))
	copy(out, s.items)
	return out
}

// Replace swaps the list atomically.
func (s *Store) Replace(items []Item) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.items = append(s.items[:0:0], items...)
}

// Input matches claude-code's TodoWrite input.
type Input struct {
	Todos []Item `json:"todos"`
}

// Output is the structured result.
type Output struct {
	Todos []Item `json:"todos"`
}

// Tool implements tool.Tool for TodoWrite.
type Tool struct {
	tool.ToolDefaults
}

// New returns a TodoWrite tool.
func New() *Tool { return &Tool{} }

func (t *Tool) Name() string                            { return Name }
func (t *Tool) IsReadOnly(_ json.RawMessage) bool       { return false }
func (t *Tool) IsConcurrencySafe(_ json.RawMessage) bool { return false }

func (t *Tool) InputSchema() *tool.JSONSchema {
	return &tool.JSONSchema{
		Type: "object",
		Properties: map[string]*tool.SchemaProperty{
			"todos": {Type: "array", Description: "Full todo list for the current task. Replaces any prior list."},
		},
		Required: []string{"todos"},
	}
}

func (t *Tool) Description(_ json.RawMessage) string { return "Manage session todo list" }

func (t *Tool) Prompt(_ tool.PromptOptions) string {
	return `Creates or replaces the session's todo list.

Each todo is an object with:
- id: unique identifier
- content: task description
- status: pending | in_progress | completed
- priority: high | medium | low

Usage:
- Call this at the start of non-trivial tasks to outline the plan.
- Mark the task currently being worked on as "in_progress" (only one at a time).
- Update as tasks complete. The tool replaces the entire list on each call, so always send the full updated list.`
}

func (t *Tool) ValidateInput(input json.RawMessage, _ *agents.ToolUseContext) *tool.ValidationResult {
	var in Input
	if err := json.Unmarshal(input, &in); err != nil {
		return &tool.ValidationResult{Valid: false, Message: fmt.Sprintf("invalid input: %v", err)}
	}
	if err := validateList(in.Todos); err != nil {
		return &tool.ValidationResult{Valid: false, Message: err.Error()}
	}
	return &tool.ValidationResult{Valid: true}
}

func validateList(items []Item) error {
	ids := map[string]bool{}
	inProgress := 0
	for i, it := range items {
		if strings.TrimSpace(it.ID) == "" {
			return fmt.Errorf("todos[%d]: id must not be empty", i)
		}
		if ids[it.ID] {
			return fmt.Errorf("todos[%d]: duplicate id %q", i, it.ID)
		}
		ids[it.ID] = true
		if strings.TrimSpace(it.Content) == "" {
			return fmt.Errorf("todos[%d]: content must not be empty", i)
		}
		switch it.Status {
		case StatusPending, StatusInProgress, StatusCompleted:
		default:
			return fmt.Errorf("todos[%d]: status must be pending|in_progress|completed", i)
		}
		switch it.Priority {
		case PriorityHigh, PriorityMedium, PriorityLow:
		default:
			return fmt.Errorf("todos[%d]: priority must be high|medium|low", i)
		}
		if it.Status == StatusInProgress {
			inProgress++
		}
	}
	if inProgress > 1 {
		return errors.New("at most one todo may be in_progress at a time")
	}
	return nil
}

func (t *Tool) CheckPermissions(input json.RawMessage, _ *agents.ToolUseContext) (*tool.PermissionResult, error) {
	return &tool.PermissionResult{Behavior: tool.PermissionAllow, UpdatedInput: input, DecisionReason: "todo-default-allow"}, nil
}

func (t *Tool) Call(ctx context.Context, input json.RawMessage, toolCtx *agents.ToolUseContext) (*tool.ToolResult, error) {
	var in Input
	if err := json.Unmarshal(input, &in); err != nil {
		return nil, err
	}
	store := storeFromCtx(toolCtx)
	if store == nil {
		return nil, errors.New("TodoWrite: no TodoStore attached to context")
	}
	store.Replace(in.Todos)
	return &tool.ToolResult{Data: Output{Todos: store.Snapshot()}}, nil
}

func (t *Tool) MapToolResultToParam(content interface{}, toolUseID string) *agents.ContentBlock {
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
	if len(out.Todos) == 0 {
		return "Todo list cleared."
	}
	items := append([]Item(nil), out.Todos...)
	sort.SliceStable(items, func(i, j int) bool {
		return statusRank(items[i].Status) < statusRank(items[j].Status)
	})
	var sb strings.Builder
	sb.WriteString("Todos:\n")
	for _, it := range items {
		fmt.Fprintf(&sb, "- [%s] (%s) %s\n", it.Status, it.Priority, it.Content)
	}
	return sb.String()
}

func statusRank(s string) int {
	switch s {
	case StatusInProgress:
		return 0
	case StatusPending:
		return 1
	case StatusCompleted:
		return 2
	}
	return 3
}

// storeFromCtx extracts a *Store from toolCtx.TodoStore.
func storeFromCtx(toolCtx *agents.ToolUseContext) *Store {
	if toolCtx == nil {
		return nil
	}
	if s, ok := toolCtx.TodoStore.(*Store); ok {
		return s
	}
	return nil
}

var _ tool.Tool = (*Tool)(nil)
