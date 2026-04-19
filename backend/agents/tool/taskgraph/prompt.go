package taskgraph

import "github.com/wall-ai/ubuilding/backend/agents/tool"

// ---------------------------------------------------------------------------
// TaskCreate prompt — mirrors opensource/claude-code-main TaskCreateTool/prompt.ts
// ---------------------------------------------------------------------------

func buildCreatePrompt(opts tool.PromptOptions) string {
	teammateContext := ""
	teammateTips := ""
	if opts.AgentSwarmsEnabled {
		teammateContext = " and potentially assigned to teammates"
		teammateTips = "- Include enough detail in the description for another agent to understand and complete the task\n" +
			"- New tasks are created with status 'pending' and no owner - use TaskUpdate with the `owner` parameter to assign them\n"
	}
	return `Use this tool to create a structured task list for your current coding session. This helps you track progress, organize complex tasks, and demonstrate thoroughness to the user.
It also helps the user understand the progress of the task and overall progress of their requests.

## When to Use This Tool

Use this tool proactively in these scenarios:

- Complex multi-step tasks - When a task requires 3 or more distinct steps or actions
- Non-trivial and complex tasks - Tasks that require careful planning or multiple operations` + teammateContext + `
- Plan mode - When using plan mode, create a task list to track the work
- User explicitly requests todo list - When the user directly asks you to use the todo list
- User provides multiple tasks - When users provide a list of things to be done (numbered or comma-separated)
- After receiving new instructions - Immediately capture user requirements as tasks
- When you start working on a task - Mark it as in_progress BEFORE beginning work
- After completing a task - Mark it as completed and add any new follow-up tasks discovered during implementation

## When NOT to Use This Tool

Skip using this tool when:
- There is only a single, straightforward task
- The task is trivial and tracking it provides no organizational benefit
- The task can be completed in less than 3 trivial steps
- The task is purely conversational or informational

NOTE that you should not use this tool if there is only one trivial task to do. In this case you are better off just doing the task directly.

## Task Fields

- **title** (` + "`subject`" + ` upstream): A brief, actionable title in imperative form (e.g., "Fix authentication bug in login flow")
- **description**: What needs to be done
- **activeForm** (optional): Present continuous form shown in the spinner when the task is in_progress (e.g., "Fixing authentication bug"). If omitted, the spinner shows the title instead.
- **depends_on** (` + "`blockedBy`" + ` upstream): Tasks that must complete before this one can start
- **payload** (` + "`metadata`" + ` upstream): Free-form string→string metadata

All tasks are created with status ` + "`pending`" + `.

## Tips

- Create tasks with clear, specific titles that describe the outcome
- After creating tasks, use TaskUpdate to set up dependencies via ` + "`depends_on`" + ` (blocks/blockedBy) if needed
` + teammateTips + `- Check TaskList first to avoid creating duplicate tasks
`
}

// ---------------------------------------------------------------------------
// TaskGet prompt — mirrors TaskGetTool/prompt.ts
// ---------------------------------------------------------------------------

const getPromptText = `Use this tool to retrieve a task by its ID from the task list.

## When to Use This Tool

- When you need the full description and context before starting work on a task
- To understand task dependencies (what it blocks, what blocks it)
- After being assigned a task, to get complete requirements

## Output

Returns full task details:
- **title** (upstream "subject"): Task title
- **description**: Detailed requirements and context
- **status**: 'pending', 'in_progress', 'blocked', 'completed', 'cancelled', or 'failed'
- **depends_on** (upstream "blockedBy"): Tasks that must complete before this one can start

## Tips

- After fetching a task, verify its depends_on list is empty (or all resolved) before beginning work.
- Use TaskList to see all tasks in summary form.
`

// ---------------------------------------------------------------------------
// TaskUpdate prompt — mirrors TaskUpdateTool/prompt.ts
// ---------------------------------------------------------------------------

const updatePromptText = `Use this tool to update a task in the task list.

## When to Use This Tool

**Mark tasks as resolved:**
- When you have completed the work described in a task
- When a task is no longer needed or has been superseded
- IMPORTANT: Always mark your assigned tasks as resolved when you finish them
- After resolving, call TaskList to find your next task

- ONLY mark a task as completed when you have FULLY accomplished it
- If you encounter errors, blockers, or cannot finish, keep the task as in_progress
- When blocked, create a new task describing what needs to be resolved
- Never mark a task as completed if:
  - Tests are failing
  - Implementation is partial
  - You encountered unresolved errors
  - You couldn't find necessary files or dependencies

**Cancel or fail tasks:**
- When a task is no longer relevant, set ` + "`status`" + ` to ` + "`cancelled`" + `
- When the work could not be completed, set ` + "`status`" + ` to ` + "`failed`" + `

**Update task details:**
- When requirements change or become clearer
- When establishing dependencies between tasks

## Fields You Can Update

- **status**: The task status (see Status Workflow below)
- **title**: Change the task title (imperative form, e.g., "Run tests")
- **description**: Change the task description
- **activeForm**: Present continuous form shown in the spinner when in_progress (e.g., "Running tests")
- **owner**: Change the task owner (agent name)
- **payload**: Replacement free-form metadata map
- **depends_on**: Replacement dependency list (tasks that must complete before this one can start)
- **parent_id**: Reparent the task (empty string detaches)

## Status Workflow

Status progresses: ` + "`pending`" + ` → ` + "`in_progress`" + ` → ` + "`completed`" + `

Use ` + "`cancelled`" + ` when a task is no longer relevant; use ` + "`failed`" + ` when work could not be completed.

## Staleness

Make sure to read a task's latest state using ` + "`TaskGet`" + ` before updating it.

## Examples

Mark task as in progress when starting work:
` + "```json" + `
{"id": "1", "status": "in_progress"}
` + "```" + `

Mark task as completed after finishing work:
` + "```json" + `
{"id": "1", "status": "completed"}
` + "```" + `

Cancel a task that is no longer relevant:
` + "```json" + `
{"id": "1", "status": "cancelled"}
` + "```" + `

Claim a task by setting owner:
` + "```json" + `
{"id": "1", "owner": "my-name"}
` + "```" + `

Set up task dependencies (task 2 is blocked by task 1):
` + "```json" + `
{"id": "2", "depends_on": ["1"]}
` + "```" + `
`

// ---------------------------------------------------------------------------
// TaskList prompt — mirrors TaskListTool/prompt.ts
// ---------------------------------------------------------------------------

func buildListPrompt(opts tool.PromptOptions) string {
	teammateUseCase := ""
	teammateWorkflow := ""
	if opts.AgentSwarmsEnabled {
		teammateUseCase = "- Before assigning tasks to teammates, to see what's available\n"
		teammateWorkflow = `
## Teammate Workflow

When working as a teammate:
1. After completing your current task, call TaskList to find available work
2. Look for tasks with status 'pending', no owner, and empty depends_on
3. **Prefer tasks in ID order** (lowest ID first) when multiple tasks are available, as earlier tasks often set up context for later ones
4. Claim an available task using TaskUpdate (set ` + "`owner`" + ` to your name), or wait for leader assignment
5. If blocked, focus on unblocking tasks or notify the team lead
`
	}
	return `Use this tool to list all tasks in the task list.

## When to Use This Tool

- To see what tasks are available to work on (status: 'pending', no owner, not blocked)
- To check overall progress on the project
- To find tasks that are blocked and need dependencies resolved
` + teammateUseCase + `- After completing a task, to check for newly unblocked work or claim the next available task
- **Prefer working on tasks in ID order** (lowest ID first) when multiple tasks are available, as earlier tasks often set up context for later ones

## Output

Returns a summary of each task:
- **id**: Task identifier (use with TaskGet, TaskUpdate)
- **title** (upstream "subject"): Brief description of the task
- **status**: 'pending', 'in_progress', 'blocked', 'completed', 'cancelled', or 'failed'
- **owner**: Agent ID if assigned, empty if available
- **depends_on** (upstream "blockedBy"): List of open task IDs that must be resolved first (tasks with non-empty depends_on cannot be claimed until dependencies resolve)

Use TaskGet with a specific task ID to view full details including description and payload.
` + teammateWorkflow
}
