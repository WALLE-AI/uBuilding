package tool

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"

	"golang.org/x/sync/errgroup"

	"github.com/wall-ai/ubuilding/backend/agents"
)

// ---------------------------------------------------------------------------
// Orchestrator — manages concurrent/serial tool execution
// Maps to TypeScript toolOrchestration.ts (runTools + partitionToolCalls)
// ---------------------------------------------------------------------------

// Orchestrator coordinates tool execution with concurrency control.
type Orchestrator struct {
	tools  Tools
	logger *slog.Logger
}

// NewOrchestrator creates a new Orchestrator with the given tools.
func NewOrchestrator(tools Tools, logger *slog.Logger) *Orchestrator {
	if logger == nil {
		logger = slog.Default()
	}
	return &Orchestrator{tools: tools, logger: logger}
}

// RunToolsResult holds the aggregated result of running tool calls.
type RunToolsResult struct {
	Messages         []agents.Message
	ContextModifiers []func(*agents.ToolUseContext) *agents.ToolUseContext
}

// RunTools executes a batch of tool calls with concurrency control.
// Concurrent-safe tools run in parallel; non-concurrent-safe tools run serially.
// This maps to TypeScript's runTools in toolOrchestration.ts.
func (o *Orchestrator) RunTools(
	ctx context.Context,
	calls []agents.ToolUseBlock,
	assistantMsg *agents.Message,
	toolCtx *agents.ToolUseContext,
	canUseTool CanUseToolFn,
) *RunToolsResult {
	// Partition tool calls into concurrency groups
	groups := o.partitionToolCalls(calls)

	result := &RunToolsResult{}
	var resultsMu sync.Mutex

	for _, group := range groups {
		select {
		case <-ctx.Done():
			// Create error results for remaining tools
			for _, call := range group.Calls {
				msg := createToolCancelledMessage(call.ID, assistantMsg.UUID)
				resultsMu.Lock()
				result.Messages = append(result.Messages, msg)
				resultsMu.Unlock()
			}
			return result
		default:
		}

		if group.Concurrent && len(group.Calls) > 1 {
			// Execute concurrent-safe tools in parallel
			o.runConcurrentGroup(ctx, group.Calls, assistantMsg, toolCtx, canUseTool, result, &resultsMu)
		} else {
			// Execute serially
			for _, call := range group.Calls {
				select {
				case <-ctx.Done():
					msg := createToolCancelledMessage(call.ID, assistantMsg.UUID)
					resultsMu.Lock()
					result.Messages = append(result.Messages, msg)
					resultsMu.Unlock()
					continue
				default:
				}

				execResult := o.executeSingleTool(ctx, call, assistantMsg, toolCtx, canUseTool)
				resultsMu.Lock()
				result.Messages = append(result.Messages, execResult.Messages...)
				if execResult.ContextModifier != nil {
					result.ContextModifiers = append(result.ContextModifiers, execResult.ContextModifier)
				}
				resultsMu.Unlock()
			}
		}
	}

	return result
}

// ---------------------------------------------------------------------------
// Tool call grouping
// ---------------------------------------------------------------------------

// toolGroup represents a batch of tool calls that can be executed together.
type toolGroup struct {
	Calls      []agents.ToolUseBlock
	Concurrent bool
}

// partitionToolCalls groups tool calls by concurrency safety.
// Maps to TypeScript's partitionToolCalls in toolOrchestration.ts.
//
// Algorithm: walk the calls in order, accumulating a concurrent batch.
// When a non-concurrent call is found, flush the concurrent batch first,
// then put the non-concurrent call in its own serial batch.
func (o *Orchestrator) partitionToolCalls(calls []agents.ToolUseBlock) []toolGroup {
	var groups []toolGroup
	var currentConcurrent []agents.ToolUseBlock

	for _, call := range calls {
		toolDef := o.tools.FindByName(call.Name)
		isSafe := false
		if toolDef != nil {
			isSafe = toolDef.IsConcurrencySafe(call.Input)
		}

		if isSafe {
			currentConcurrent = append(currentConcurrent, call)
		} else {
			// Flush any accumulated concurrent calls first
			if len(currentConcurrent) > 0 {
				groups = append(groups, toolGroup{
					Calls:      currentConcurrent,
					Concurrent: true,
				})
				currentConcurrent = nil
			}
			// Non-concurrent call gets its own serial group
			groups = append(groups, toolGroup{
				Calls:      []agents.ToolUseBlock{call},
				Concurrent: false,
			})
		}
	}

	// Flush remaining concurrent calls
	if len(currentConcurrent) > 0 {
		groups = append(groups, toolGroup{
			Calls:      currentConcurrent,
			Concurrent: true,
		})
	}

	return groups
}

// ---------------------------------------------------------------------------
// Concurrent execution
// ---------------------------------------------------------------------------

func (o *Orchestrator) runConcurrentGroup(
	ctx context.Context,
	calls []agents.ToolUseBlock,
	assistantMsg *agents.Message,
	toolCtx *agents.ToolUseContext,
	canUseTool CanUseToolFn,
	result *RunToolsResult,
	resultsMu *sync.Mutex,
) {
	g, gCtx := errgroup.WithContext(ctx)

	type indexedResult struct {
		Index           int
		ExecResult      *SingleToolResult
	}

	indexedResults := make([]indexedResult, len(calls))

	for i, call := range calls {
		i, call := i, call // capture loop vars
		g.Go(func() error {
			execResult := o.executeSingleTool(gCtx, call, assistantMsg, toolCtx, canUseTool)
			indexedResults[i] = indexedResult{Index: i, ExecResult: execResult}
			return nil // we don't short-circuit on tool errors
		})
	}

	_ = g.Wait() // errors are captured in results, not returned

	// Collect results in original order
	resultsMu.Lock()
	for _, ir := range indexedResults {
		if ir.ExecResult != nil {
			result.Messages = append(result.Messages, ir.ExecResult.Messages...)
			if ir.ExecResult.ContextModifier != nil {
				result.ContextModifiers = append(result.ContextModifiers, ir.ExecResult.ContextModifier)
			}
		}
	}
	resultsMu.Unlock()
}

// ---------------------------------------------------------------------------
// Single tool execution
// ---------------------------------------------------------------------------

// SingleToolResult holds the result of executing a single tool call.
type SingleToolResult struct {
	Messages        []agents.Message
	ContextModifier func(*agents.ToolUseContext) *agents.ToolUseContext
}

// CanUseToolFn is the permission check function type.
// Maps to TypeScript's CanUseToolFn from useCanUseTool.tsx.
type CanUseToolFn func(toolName string, input json.RawMessage, toolCtx *agents.ToolUseContext) *PermissionResult

// executeSingleTool runs a single tool call through the full pipeline:
// find tool → validate input → check permissions → call → collect result.
// Maps to TypeScript's runToolUse in toolExecution.ts.
func (o *Orchestrator) executeSingleTool(
	ctx context.Context,
	call agents.ToolUseBlock,
	assistantMsg *agents.Message,
	toolCtx *agents.ToolUseContext,
	canUseTool CanUseToolFn,
) *SingleToolResult {
	result := &SingleToolResult{}

	// 1. Find tool definition
	toolDef := o.tools.FindByName(call.Name)
	if toolDef == nil {
		result.Messages = append(result.Messages, createToolErrorMsg(
			call.ID, "Error: No such tool available: "+call.Name, assistantMsg.UUID,
		))
		return result
	}

	// 2. Validate input
	validation := toolDef.ValidateInput(call.Input, toolCtx)
	if validation != nil && !validation.Valid {
		result.Messages = append(result.Messages, createToolErrorMsg(
			call.ID, "Validation error: "+validation.Message, assistantMsg.UUID,
		))
		return result
	}

	// 3. Check permissions
	if canUseTool != nil {
		permResult := canUseTool(call.Name, call.Input, toolCtx)
		if permResult != nil && permResult.Behavior == PermissionDeny {
			result.Messages = append(result.Messages, createToolErrorMsg(
				call.ID, "Permission denied: "+permResult.Message, assistantMsg.UUID,
			))
			return result
		}
		// If permission returns updated input, use that instead
		if permResult != nil && len(permResult.UpdatedInput) > 0 {
			call.Input = permResult.UpdatedInput
		}
	}

	// 4. Execute tool
	toolResult, err := toolDef.Call(ctx, call.Input, toolCtx)
	if err != nil {
		o.logger.Error("tool execution error", "tool", call.Name, "error", err)
		result.Messages = append(result.Messages, createToolErrorMsg(
			call.ID, "Error: "+err.Error(), assistantMsg.UUID,
		))
		return result
	}

	// 5. Build result message
	if toolResult != nil {
		// Map tool result to content block
		resultBlock := toolDef.MapToolResultToParam(toolResult.Data, call.ID)
		if resultBlock != nil {
			resultMsg := agents.Message{
				Type: agents.MessageTypeUser,
				Content: []agents.ContentBlock{*resultBlock},
				SourceToolAssistantUUID: assistantMsg.UUID,
			}
			result.Messages = append(result.Messages, resultMsg)
		}

		// Collect new messages from tool
		result.Messages = append(result.Messages, toolResult.NewMessages...)

		// Collect context modifier
		if toolResult.ContextModifier != nil {
			result.ContextModifier = toolResult.ContextModifier
		}
	}

	return result
}

// ---------------------------------------------------------------------------
// Helper functions
// ---------------------------------------------------------------------------

func createToolErrorMsg(toolUseID, errorMsg, assistantUUID string) agents.Message {
	return agents.Message{
		Type: agents.MessageTypeUser,
		Content: []agents.ContentBlock{
			{
				Type:      agents.ContentBlockToolResult,
				ToolUseID: toolUseID,
				Content:   "<tool_use_error>" + errorMsg + "</tool_use_error>",
				IsError:   true,
			},
		},
		ToolUseResult:           errorMsg,
		SourceToolAssistantUUID: assistantUUID,
	}
}

func createToolCancelledMessage(toolUseID, assistantUUID string) agents.Message {
	return createToolErrorMsg(toolUseID, "Cancelled by user", assistantUUID)
}
