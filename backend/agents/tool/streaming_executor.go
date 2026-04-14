package tool

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"

	"github.com/wall-ai/ubuilding/backend/agents"
)

// ---------------------------------------------------------------------------
// StreamingToolExecutor — executes tools as they stream in with concurrency control
// Maps to TypeScript StreamingToolExecutor in StreamingToolExecutor.ts
// ---------------------------------------------------------------------------

// ToolStatus tracks the lifecycle of a single tool execution.
type ToolStatus string

const (
	ToolStatusQueued    ToolStatus = "queued"
	ToolStatusExecuting ToolStatus = "executing"
	ToolStatusCompleted ToolStatus = "completed"
	ToolStatusYielded   ToolStatus = "yielded"
)

// TrackedTool holds the state of a tool as it progresses through execution.
type TrackedTool struct {
	ID               string
	Block            agents.ToolUseBlock
	AssistantMessage *agents.Message
	Status           ToolStatus
	IsConcurrencySafe bool
	Results          []agents.Message
	ContextModifier  func(*agents.ToolUseContext) *agents.ToolUseContext
}

// StreamingToolExecutor manages tool execution as tool_use blocks stream in.
// It ensures:
//   - Concurrent-safe tools can execute in parallel
//   - Non-concurrent tools execute alone (exclusive access)
//   - Results are buffered and emitted in order
//   - Sibling error causes abort of running tools
type StreamingToolExecutor struct {
	mu              sync.Mutex
	tools           []TrackedTool
	toolDefinitions Tools
	canUseTool      CanUseToolFn
	toolCtx         *agents.ToolUseContext
	hasErrored      bool
	discarded       bool
	logger          *slog.Logger

	// Sibling abort: child of toolCtx.Ctx, fires when a tool errors
	siblingCtx    context.Context
	siblingCancel context.CancelFunc

	// Signal waiters when a tool completes
	completedCond *sync.Cond
}

// NewStreamingToolExecutor creates a new streaming executor.
func NewStreamingToolExecutor(
	toolDefs Tools,
	canUseTool CanUseToolFn,
	toolCtx *agents.ToolUseContext,
	logger *slog.Logger,
) *StreamingToolExecutor {
	if logger == nil {
		logger = slog.Default()
	}
	siblingCtx, siblingCancel := context.WithCancel(toolCtx.Ctx)
	mu := &sync.Mutex{}
	return &StreamingToolExecutor{
		toolDefinitions: toolDefs,
		canUseTool:      canUseTool,
		toolCtx:         toolCtx,
		logger:          logger,
		siblingCtx:      siblingCtx,
		siblingCancel:   siblingCancel,
		completedCond:   sync.NewCond(mu),
	}
}

// Discard marks the executor as discarded (e.g., due to streaming fallback).
// Queued tools won't start, in-progress tools will produce error results.
func (e *StreamingToolExecutor) Discard() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.discarded = true
	e.siblingCancel()
}

// AddTool queues a new tool for execution. Starts immediately if conditions allow.
func (e *StreamingToolExecutor) AddTool(block agents.ToolUseBlock, assistantMsg *agents.Message) {
	e.mu.Lock()

	toolDef := e.toolDefinitions.FindByName(block.Name)
	if toolDef == nil {
		// Tool not found — complete immediately with error
		e.tools = append(e.tools, TrackedTool{
			ID:               block.ID,
			Block:            block,
			AssistantMessage: assistantMsg,
			Status:           ToolStatusCompleted,
			IsConcurrencySafe: true,
			Results: []agents.Message{
				createToolErrorMsg(block.ID, "Error: No such tool available: "+block.Name, assistantMsg.UUID),
			},
		})
		e.mu.Unlock()
		e.completedCond.Broadcast()
		return
	}

	isSafe := toolDef.IsConcurrencySafe(block.Input)
	e.tools = append(e.tools, TrackedTool{
		ID:               block.ID,
		Block:            block,
		AssistantMessage: assistantMsg,
		Status:           ToolStatusQueued,
		IsConcurrencySafe: isSafe,
	})
	e.mu.Unlock()

	go e.processQueue()
}

// GetCompletedResults returns results from tools that have completed, in order.
// Results are returned only once (status moves from Completed to Yielded).
func (e *StreamingToolExecutor) GetCompletedResults() []agents.Message {
	e.mu.Lock()
	defer e.mu.Unlock()

	var results []agents.Message
	for i := range e.tools {
		if e.tools[i].Status != ToolStatusCompleted {
			break // maintain order: stop at first non-completed
		}
		results = append(results, e.tools[i].Results...)
		e.tools[i].Status = ToolStatusYielded
	}
	return results
}

// GetAllResults blocks until all tools are completed and returns all results.
func (e *StreamingToolExecutor) GetAllResults() ([]agents.Message, []func(*agents.ToolUseContext) *agents.ToolUseContext) {
	e.completedCond.L.Lock()
	for !e.allDone() {
		e.completedCond.Wait()
	}
	e.completedCond.L.Unlock()

	e.mu.Lock()
	defer e.mu.Unlock()

	var messages []agents.Message
	var modifiers []func(*agents.ToolUseContext) *agents.ToolUseContext

	for i := range e.tools {
		messages = append(messages, e.tools[i].Results...)
		if e.tools[i].ContextModifier != nil {
			modifiers = append(modifiers, e.tools[i].ContextModifier)
		}
	}
	return messages, modifiers
}

// allDone checks if all tools have completed (must hold mu or completedCond.L).
func (e *StreamingToolExecutor) allDone() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, t := range e.tools {
		if t.Status == ToolStatusQueued || t.Status == ToolStatusExecuting {
			return false
		}
	}
	return true
}

// processQueue attempts to start queued tools based on concurrency rules.
func (e *StreamingToolExecutor) processQueue() {
	e.mu.Lock()
	defer e.mu.Unlock()

	for i := range e.tools {
		if e.tools[i].Status != ToolStatusQueued {
			continue
		}
		if e.discarded {
			e.tools[i].Status = ToolStatusCompleted
			e.tools[i].Results = []agents.Message{
				createToolErrorMsg(e.tools[i].Block.ID, "Streaming fallback - tool execution discarded", e.tools[i].AssistantMessage.UUID),
			}
			e.completedCond.Broadcast()
			continue
		}
		if e.canExecute(e.tools[i].IsConcurrencySafe) {
			e.tools[i].Status = ToolStatusExecuting
			go e.executeTool(i)
		} else if !e.tools[i].IsConcurrencySafe {
			break // maintain order for non-concurrent tools
		}
	}
}

// canExecute checks if a tool can start based on current state.
func (e *StreamingToolExecutor) canExecute(isConcurrencySafe bool) bool {
	for _, t := range e.tools {
		if t.Status == ToolStatusExecuting {
			if !isConcurrencySafe || !t.IsConcurrencySafe {
				return false
			}
		}
	}
	return true
}

// executeTool runs a single tool and updates its state.
func (e *StreamingToolExecutor) executeTool(idx int) {
	e.mu.Lock()
	tracked := &e.tools[idx]
	block := tracked.Block
	assistantMsg := tracked.AssistantMessage
	e.mu.Unlock()

	// Find tool definition
	toolDef := e.toolDefinitions.FindByName(block.Name)
	if toolDef == nil {
		e.completeWithError(idx, "Error: Tool not found: "+block.Name)
		return
	}

	// Validate input
	validation := toolDef.ValidateInput(block.Input, e.toolCtx)
	if validation != nil && !validation.Valid {
		e.completeWithError(idx, "Validation error: "+validation.Message)
		return
	}

	// Check permissions
	if e.canUseTool != nil {
		permResult := e.canUseTool(block.Name, block.Input, e.toolCtx)
		if permResult != nil && permResult.Behavior == PermissionDeny {
			e.completeWithError(idx, "Permission denied: "+permResult.Message)
			return
		}
		if permResult != nil && len(permResult.UpdatedInput) > 0 {
			block.Input = permResult.UpdatedInput
		}
	}

	// Execute tool
	toolResult, err := toolDef.Call(e.siblingCtx, block.Input, e.toolCtx)
	if err != nil {
		e.logger.Error("streaming tool error", "tool", block.Name, "error", err)
		e.mu.Lock()
		e.hasErrored = true
		e.siblingCancel() // abort sibling tools
		e.mu.Unlock()
		e.completeWithError(idx, "Error: "+err.Error())
		return
	}

	// Build result
	var resultMsgs []agents.Message
	var ctxModifier func(*agents.ToolUseContext) *agents.ToolUseContext

	if toolResult != nil {
		resultBlock := toolDef.MapToolResultToParam(toolResult.Data, block.ID)
		if resultBlock != nil {
			resultMsgs = append(resultMsgs, agents.Message{
				Type:    agents.MessageTypeUser,
				Content: []agents.ContentBlock{*resultBlock},
				SourceToolAssistantUUID: assistantMsg.UUID,
			})
		}
		resultMsgs = append(resultMsgs, toolResult.NewMessages...)
		ctxModifier = toolResult.ContextModifier
	}

	e.mu.Lock()
	e.tools[idx].Status = ToolStatusCompleted
	e.tools[idx].Results = resultMsgs
	e.tools[idx].ContextModifier = ctxModifier
	e.mu.Unlock()

	e.completedCond.Broadcast()
	go e.processQueue() // check if queued tools can now start
}

// completeWithError marks a tool as completed with an error result.
func (e *StreamingToolExecutor) completeWithError(idx int, errorMsg string) {
	e.mu.Lock()
	tracked := &e.tools[idx]
	tracked.Status = ToolStatusCompleted
	tracked.Results = []agents.Message{
		createToolErrorMsg(tracked.Block.ID, errorMsg, tracked.AssistantMessage.UUID),
	}
	e.mu.Unlock()
	e.completedCond.Broadcast()
	go e.processQueue()
}

// suppress unused import warning for json
var _ = json.RawMessage{}
