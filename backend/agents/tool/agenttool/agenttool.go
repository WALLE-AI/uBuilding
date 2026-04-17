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

// Input matches claude-code's Task tool input.
type Input struct {
	Description  string `json:"description,omitempty"`
	Prompt       string `json:"prompt"`
	SubagentType string `json:"subagent_type,omitempty"`
	MaxTurns     int    `json:"max_turns,omitempty"`
}

// Output is the structured result.
type Output struct {
	Description  string `json:"description,omitempty"`
	SubagentType string `json:"subagent_type,omitempty"`
	Result       string `json:"result"`
}

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

// AgentTool implements tool.Tool.
type AgentTool struct {
	tool.ToolDefaults
	name         string
	allowedTypes []string
}

// New returns an AgentTool.
func New(opts ...Option) *AgentTool {
	a := &AgentTool{name: Name}
	for _, o := range opts {
		o(a)
	}
	return a
}

func (a *AgentTool) Name() string                          { return a.name }
func (a *AgentTool) IsReadOnly(_ json.RawMessage) bool     { return false }
func (a *AgentTool) IsConcurrencySafe(_ json.RawMessage) bool { return false }

func (a *AgentTool) InputSchema() *tool.JSONSchema {
	return &tool.JSONSchema{
		Type: "object",
		Properties: map[string]*tool.SchemaProperty{
			"description":   {Type: "string", Description: "Short label for the sub-task."},
			"prompt":        {Type: "string", Description: "The full prompt to hand to the subagent."},
			"subagent_type": {Type: "string", Description: "Named subagent definition to use (optional)."},
			"max_turns":     {Type: "integer", Description: "Maximum model turns for the sub-query."},
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
	return `Dispatches a sub-query to a focused subagent. Use for exploratory searches, heavy-context research, or when you want an isolated agent to solve a sub-problem and return only its final answer.

Params:
- prompt (required): the full instruction handed to the subagent.
- description: short label for this sub-task.
- subagent_type: named definition (e.g. "code-review").
- max_turns: cap on the sub-query's loop length.

The subagent runs with its own message history; only its final textual result is returned here.`
}

func (a *AgentTool) ValidateInput(input json.RawMessage, _ *agents.ToolUseContext) *tool.ValidationResult {
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
	if in.SubagentType != "" && len(a.allowedTypes) > 0 {
		allowed := false
		for _, t := range a.allowedTypes {
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
	result, err := toolCtx.SpawnSubAgent(ctx, agents.SubAgentParams{
		Description:  in.Description,
		Prompt:       in.Prompt,
		SubagentType: in.SubagentType,
		MaxTurns:     in.MaxTurns,
	})
	if err != nil {
		return nil, err
	}
	return &tool.ToolResult{Data: Output{
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
