package taskgraph

import (
	"strings"
	"testing"

	"github.com/wall-ai/ubuilding/backend/agents/tool"
)

func TestTaskCreate_PromptKeywords(t *testing.T) {
	p := NewCreateTool().Prompt(tool.PromptOptions{})
	for _, want := range []string{
		"Use this tool to create a structured task list",
		"## When to Use This Tool",
		"## When NOT to Use This Tool",
		"## Task Fields",
		"**activeForm**",
		"**depends_on**",
		"`pending`",
		"Check TaskList first to avoid creating duplicate tasks",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("TaskCreate prompt missing %q", want)
		}
	}
	// Teammate-only bits must be absent when swarms are off.
	if strings.Contains(p, "potentially assigned to teammates") {
		t.Error("non-swarm prompt leaked teammate wording")
	}
	if strings.Contains(p, "use TaskUpdate with the `owner` parameter to assign them") {
		t.Error("non-swarm prompt leaked teammate tip")
	}
}

func TestTaskCreate_PromptSwarms(t *testing.T) {
	p := NewCreateTool().Prompt(tool.PromptOptions{AgentSwarmsEnabled: true})
	if !strings.Contains(p, "potentially assigned to teammates") {
		t.Error("swarm branch missing teammate context")
	}
	if !strings.Contains(p, "use TaskUpdate with the `owner` parameter to assign them") {
		t.Error("swarm branch missing teammate tip")
	}
}

func TestTaskGet_PromptKeywords(t *testing.T) {
	p := NewGetTool().Prompt(tool.PromptOptions{})
	for _, want := range []string{
		"retrieve a task by its ID",
		"## When to Use This Tool",
		"## Output",
		"**depends_on**",
		"Use TaskList to see all tasks in summary form",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("TaskGet prompt missing %q", want)
		}
	}
}

func TestTaskUpdate_PromptKeywords(t *testing.T) {
	p := NewUpdateTool().Prompt(tool.PromptOptions{})
	for _, want := range []string{
		"Use this tool to update a task",
		"## Fields You Can Update",
		"**activeForm**",
		"## Status Workflow",
		"`pending` → `in_progress` → `completed`",
		"`cancelled`",
		"`failed`",
		"## Examples",
		`{"id": "1", "status": "in_progress"}`,
		`{"id": "2", "depends_on": ["1"]}`,
	} {
		if !strings.Contains(p, want) {
			t.Errorf("TaskUpdate prompt missing %q", want)
		}
	}
}

func TestTaskList_PromptKeywords(t *testing.T) {
	p := NewListTool().Prompt(tool.PromptOptions{})
	for _, want := range []string{
		"Use this tool to list all tasks",
		"## When to Use This Tool",
		"## Output",
		"- **id**:",
		"Prefer working on tasks in ID order",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("TaskList prompt missing %q", want)
		}
	}
	if strings.Contains(p, "## Teammate Workflow") {
		t.Error("non-swarm prompt leaked teammate workflow section")
	}
}

func TestTaskList_PromptSwarmsTeammateWorkflow(t *testing.T) {
	p := NewListTool().Prompt(tool.PromptOptions{AgentSwarmsEnabled: true})
	for _, want := range []string{
		"## Teammate Workflow",
		"Before assigning tasks to teammates",
		"Claim an available task using TaskUpdate",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("TaskList swarms prompt missing %q", want)
		}
	}
}

func TestTask_DescriptionsMirrorUpstream(t *testing.T) {
	got := map[string]string{
		"create": NewCreateTool().Description(nil),
		"get":    NewGetTool().Description(nil),
		"update": NewUpdateTool().Description(nil),
		"list":   NewListTool().Description(nil),
	}
	want := map[string]string{
		"create": "Create a new task in the task list",
		"get":    "Get a task by ID from the task list",
		"update": "Update a task in the task list",
		"list":   "List all tasks in the task list",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s description = %q; want %q", k, got[k], v)
		}
	}
}

func TestTaskUpdate_AcceptsActiveFormAndOwner(t *testing.T) {
	store := NewStore()
	n, err := store.Add(Node{Title: "do stuff"})
	if err != nil {
		t.Fatal(err)
	}
	owner := "alice"
	active := "Doing stuff"
	desc := "the details"
	got, err := store.Update(n.ID, UpdateFields{
		Owner:       &owner,
		ActiveForm:  &active,
		Description: &desc,
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if got.Owner != owner || got.ActiveForm != active || got.Description != desc {
		t.Fatalf("fields not applied: %+v", got)
	}
}
