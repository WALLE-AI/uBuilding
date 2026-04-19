// Package agenttool implements the Task (subagent) tool. Invoking it
// dispatches a sub-query to the engine via ToolUseContext.SpawnSubAgent,
// returning the subagent's final textual answer.
package agenttool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/wall-ai/ubuilding/backend/agents"
	"github.com/wall-ai/ubuilding/backend/agents/tool"
)

// Name is the tool name exposed to the model. Aliased to "Task" to match
// claude-code's public surface; callers can override via WithName.
const Name = "Task"

// Input matches claude-code's Task tool input (subset — A15 adds the
// remaining multi-agent / isolation fields incrementally; unknown fields
// unmarshalled from the model are ignored).
type Input struct {
	Description  string `json:"description,omitempty"`
	Prompt       string `json:"prompt"`
	SubagentType string `json:"subagent_type,omitempty"`
	MaxTurns     int    `json:"max_turns,omitempty"`

	// A15 · optional multi-agent fields. All are accepted at parse time but
	// only `Model` / `RunInBackground` / `Mode` affect Phase A behaviour —
	// the rest are wired in Phase B+D.
	Model           string `json:"model,omitempty"`
	RunInBackground bool   `json:"run_in_background,omitempty"`
	Name            string `json:"name,omitempty"`
	TeamName        string `json:"team_name,omitempty"`
	Mode            string `json:"mode,omitempty"`
	Isolation       string `json:"isolation,omitempty"`
	Cwd             string `json:"cwd,omitempty"`
}

// Output is the structured result. A15 turns this into a small discriminated
// shape: Status = "completed" (synchronous) or "async_launched" (Phase D);
// Phase A always emits Status=completed.
type Output struct {
	Status       string `json:"status"`
	Description  string `json:"description,omitempty"`
	SubagentType string `json:"subagent_type,omitempty"`
	Result       string `json:"result,omitempty"`

	// Phase D async preview fields — empty for sync invocations.
	AgentID    string `json:"agent_id,omitempty"`
	OutputFile string `json:"output_file,omitempty"`
}

// Status constants.
const (
	StatusCompleted     = "completed"
	StatusAsyncLaunched = "async_launched"
)

// AgentCatalogFn returns the active agent definitions available to the Task
// tool. Used for A10 prompt rendering and A11 allow-list defaults.
type AgentCatalogFn func() []*agents.AgentDefinition

// Option configures an AgentTool.
type Option func(*AgentTool)

// WithName overrides the public tool name (default: "Task").
func WithName(name string) Option { return func(a *AgentTool) { a.name = name } }

// WithAllowedSubagentTypes restricts which subagent types the model may spawn.
// Empty list == unrestricted (but SubagentType is still validated non-empty
// against the engine's AgentDefinitions when supplied).
func WithAllowedSubagentTypes(types ...string) Option {
	return func(a *AgentTool) { a.allowedTypes = append(a.allowedTypes, types...) }
}

// WithAgentCatalog installs a callback that resolves the active agent list
// when the tool renders its Prompt() or defaults its allow list (A10, A11).
// The callback is invoked on every render to stay in sync with live
// registry mutations (/reload-plugins, policy changes).
func WithAgentCatalog(fn AgentCatalogFn) Option {
	return func(a *AgentTool) { a.catalog = fn }
}

// AgentCatalogFilterFn post-processes the catalog (e.g. through
// permission.FilterDeniedAgentsByType). Used by hosts to apply
// Task(name) deny rules before the prompt renders (B12).
type AgentCatalogFilterFn func([]*agents.AgentDefinition) []*agents.AgentDefinition

// WithAgentCatalogFilter installs a post-filter that runs on every catalog
// render. Use WithAgentCatalogFilter alongside WithAgentCatalog to hide
// agents denied by permission rules.
func WithAgentCatalogFilter(fn AgentCatalogFilterFn) Option {
	return func(a *AgentTool) { a.catalogFilter = fn }
}

// AgentTool implements tool.Tool.
type AgentTool struct {
	tool.ToolDefaults
	name          string
	allowedTypes  []string
	catalog       AgentCatalogFn
	catalogFilter AgentCatalogFilterFn
}

// New returns an AgentTool.
func New(opts ...Option) *AgentTool {
	a := &AgentTool{name: Name}
	for _, o := range opts {
		o(a)
	}
	return a
}

func (a *AgentTool) Name() string                             { return a.name }
func (a *AgentTool) IsReadOnly(_ json.RawMessage) bool        { return false }
func (a *AgentTool) IsConcurrencySafe(_ json.RawMessage) bool { return false }

func (a *AgentTool) InputSchema() *tool.JSONSchema {
	return &tool.JSONSchema{
		Type: "object",
		Properties: map[string]*tool.SchemaProperty{
			"description":       {Type: "string", Description: "A short (3-5 word) description of the task."},
			"prompt":            {Type: "string", Description: "The task for the agent to perform."},
			"subagent_type":     {Type: "string", Description: "The type of specialized agent to use for this task (optional; defaults to general-purpose)."},
			"max_turns":         {Type: "integer", Description: "Cap on model turns for the sub-query (optional)."},
			"model":             {Type: "string", Description: "Optional model override for this agent (alias or full id). Takes precedence over the agent definition's model."},
			"run_in_background": {Type: "boolean", Description: "Set to true to run this agent in the background; you will be notified when it completes. Phase D wiring — currently treated as foreground."},
			"name":              {Type: "string", Description: "Name for the spawned agent, used for SendMessage routing (Phase D)."},
			"team_name":         {Type: "string", Description: "Team context for spawning (Phase D)."},
			"mode":              {Type: "string", Description: "Permission mode override for the spawned agent (e.g., \"plan\")."},
			"isolation":         {Type: "string", Description: "Isolation mode (\"worktree\") — Phase D."},
			"cwd":               {Type: "string", Description: "Absolute working directory for the agent (overrides CWD)."},
		},
		Required: []string{"prompt"},
	}
}

func (a *AgentTool) Description(input json.RawMessage) string {
	var in Input
	_ = json.Unmarshal(input, &in)
	if in.Description != "" {
		return "Subagent: " + in.Description
	}
	return "Dispatch a subagent"
}

func (a *AgentTool) Prompt(_ tool.PromptOptions) string {
	// A10 · render the list of active agents the model can dispatch to.
	// Format mirrors prompt.ts::formatAgentLine: "- <type>: <whenToUse> (Tools: ...)".
	agentLines := a.renderAgentCatalog()

	base := `Launch a new agent to handle complex, multi-step tasks autonomously.

The Task tool launches specialized agents (subprocesses) that autonomously handle complex tasks. Each agent type has specific capabilities and tools available to it.`

	if agentLines != "" {
		base += "\n\nAvailable agent types and the tools they have access to:\n" + agentLines
	}
	base += `

When using the Task tool, specify a subagent_type parameter to select which agent type to use. If omitted, the general-purpose agent is used.

Usage notes:
- Always include a short description (3-5 words) summarizing what the agent will do.
- When the agent is done, it will return a single message back to you. Relay the result to the user as a concise summary; the raw tool result is not shown to them.
- Launch multiple agents concurrently by emitting several Task tool calls in one message.
- The agent runs with its own message history; only its final textual result is returned here.`

	return base
}

// renderAgentCatalog formats the active agent registry for the Task prompt.
// Returns "" when no catalog callback is installed (e.g. legacy tests).
// B12 · honours the FilterDeniedAgents hook when `catalogFilter` is set
// so agents denied via `Task(name)` permission rules disappear from the
// listing. Callers that don't wire a filter get the unfiltered catalog.
func (a *AgentTool) renderAgentCatalog() string {
	if a.catalog == nil {
		return ""
	}
	defs := a.catalog()
	if a.catalogFilter != nil {
		defs = a.catalogFilter(defs)
	}
	if len(defs) == 0 {
		return ""
	}
	var b strings.Builder
	for _, d := range defs {
		if d == nil {
			continue
		}
		toolsDesc := toolsDescription(d)
		fmt.Fprintf(&b, "- %s: %s (Tools: %s)\n", d.AgentType, d.WhenToUse, toolsDesc)
	}
	return strings.TrimRight(b.String(), "\n")
}

// toolsDescription mirrors prompt.ts::getToolsDescription. Handles the 4
// shapes: both allow+deny, allow only, deny only, no restrictions.
func toolsDescription(d *agents.AgentDefinition) string {
	hasAllow := len(d.Tools) > 0
	hasDeny := len(d.DisallowedTools) > 0
	switch {
	case hasAllow && hasDeny:
		deny := make(map[string]struct{}, len(d.DisallowedTools))
		for _, t := range d.DisallowedTools {
			deny[t] = struct{}{}
		}
		effective := make([]string, 0, len(d.Tools))
		for _, t := range d.Tools {
			if _, blocked := deny[t]; !blocked {
				effective = append(effective, t)
			}
		}
		if len(effective) == 0 {
			return "None"
		}
		return strings.Join(effective, ", ")
	case hasAllow:
		return strings.Join(d.Tools, ", ")
	case hasDeny:
		return "All tools except " + strings.Join(d.DisallowedTools, ", ")
	default:
		return "All tools"
	}
}

func (a *AgentTool) ValidateInput(input json.RawMessage, toolCtx *agents.ToolUseContext) *tool.ValidationResult {
	var in Input
	if err := json.Unmarshal(input, &in); err != nil {
		return &tool.ValidationResult{Valid: false, Message: fmt.Sprintf("invalid input: %v", err)}
	}
	if strings.TrimSpace(in.Prompt) == "" {
		return &tool.ValidationResult{Valid: false, Message: "prompt required"}
	}
	if in.MaxTurns < 0 {
		return &tool.ValidationResult{Valid: false, Message: "max_turns must be non-negative"}
	}
	// A11 · the effective allow list is the explicit one set via the option
	// (takes precedence) OR, when unset, the AllowedAgentTypes attached to
	// the engine's AgentDefinitions — which mirrors how TS's
	// filterDeniedAgents result flows through Agent(name) permission rules.
	allowList := a.effectiveAllowList(toolCtx)
	if in.SubagentType != "" && len(allowList) > 0 {
		allowed := false
		for _, t := range allowList {
			if t == in.SubagentType {
				allowed = true
				break
			}
		}
		if !allowed {
			return &tool.ValidationResult{Valid: false, Message: fmt.Sprintf("subagent_type %q not in allow list", in.SubagentType)}
		}
	}
	return &tool.ValidationResult{Valid: true}
}

// effectiveAllowList blends the tool-level explicit list (WithAllowedSubagentTypes)
// with the engine-supplied AgentDefinitions.AllowedAgentTypes (A11).
func (a *AgentTool) effectiveAllowList(toolCtx *agents.ToolUseContext) []string {
	if len(a.allowedTypes) > 0 {
		return a.allowedTypes
	}
	if toolCtx != nil && toolCtx.Options.AgentDefinitions != nil {
		return toolCtx.Options.AgentDefinitions.AllowedAgentTypes
	}
	return nil
}

func (a *AgentTool) CheckPermissions(input json.RawMessage, _ *agents.ToolUseContext) (*tool.PermissionResult, error) {
	return &tool.PermissionResult{Behavior: tool.PermissionAllow, UpdatedInput: input, DecisionReason: "subagent-default-allow"}, nil
}

func (a *AgentTool) Call(ctx context.Context, input json.RawMessage, toolCtx *agents.ToolUseContext) (*tool.ToolResult, error) {
	var in Input
	if err := json.Unmarshal(input, &in); err != nil {
		return nil, err
	}
	if toolCtx == nil || toolCtx.SpawnSubAgent == nil {
		return nil, errors.New("AgentTool: no SpawnSubAgent handler attached to context")
	}

	// D03 · reject fork-inside-fork. Hosts in fork mode stamp the
	// parent's messages with the fork boilerplate; we refuse new fork
	// spawns to avoid infinite recursion.
	if agents.IsInForkChild(toolCtx.Messages) {
		return nil, errors.New("AgentTool: fork-inside-fork rejected")
	}

	// D04 · if the fork feature is enabled and the caller did not supply
	// a subagent_type, route to the synthetic fork agent. Otherwise fall
	// through to the existing direct spawn path.
	if in.SubagentType == "" && agents.ForkSubagentEnabled() {
		in.SubagentType = agents.ForkAgentType
	}

	// A15 · pass-through model override. Background/name/team/isolation/cwd
	// are accepted but not yet plumbed to SpawnSubAgent — Phase D wires
	// them through SubAgentParams expansions.
	result, err := toolCtx.SpawnSubAgent(ctx, agents.SubAgentParams{
		Description:  in.Description,
		Prompt:       in.Prompt,
		SubagentType: in.SubagentType,
		MaxTurns:     in.MaxTurns,
		Model:        in.Model,
	})
	if err != nil {
		return nil, err
	}
	return &tool.ToolResult{Data: Output{
		Status:       StatusCompleted,
		Description:  in.Description,
		SubagentType: in.SubagentType,
		Result:       result,
	}}, nil
}

func (a *AgentTool) MapToolResultToParam(content interface{}, toolUseID string) *agents.ContentBlock {
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
	return out.Result
}

var _ tool.Tool = (*AgentTool)(nil)
