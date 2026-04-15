package tool

import (
	"context"
	"encoding/json"
	"fmt"
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

// AbortReason classifies why a tool should be cancelled.
// Maps to TS getAbortReason() return values.
type AbortReason string

const (
	AbortReasonNone              AbortReason = ""
	AbortReasonSiblingError      AbortReason = "sibling_error"
	AbortReasonUserInterrupted   AbortReason = "user_interrupted"
	AbortReasonStreamingFallback AbortReason = "streaming_fallback"
)

// TrackedTool holds the state of a tool as it progresses through execution.
type TrackedTool struct {
	ID                string
	Block             agents.ToolUseBlock
	AssistantMessage  *agents.Message
	Status            ToolStatus
	IsConcurrencySafe bool
	Results           []agents.Message
	PendingProgress   []agents.Message
	ContextModifiers  []func(*agents.ToolUseContext) *agents.ToolUseContext
}

// StreamingToolExecutor manages tool execution as tool_use blocks stream in.
// It ensures:
//   - Concurrent-safe tools can execute in parallel
//   - Non-concurrent tools execute alone (exclusive access)
//   - Results are buffered and emitted in order
//   - Sibling Bash error causes abort of running tools
//   - Progress messages are yielded immediately (out of order)
type StreamingToolExecutor struct {
	mu              sync.Mutex
	tools           []TrackedTool
	toolDefinitions Tools
	canUseTool      CanUseToolFn
	toolCtx         *agents.ToolUseContext
	hasErrored      bool
	discarded       bool
	logger          *slog.Logger

	// Description of the tool that caused a sibling error (for error messages).
	erroredToolDescription string

	// Sibling abort: child of toolCtx.Ctx, fires when a Bash tool errors
	// so sibling subprocesses die immediately instead of running to completion.
	siblingCtx    context.Context
	siblingCancel context.CancelFunc

	// Signal waiters when a tool completes or progress is available
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
			ID:                block.ID,
			Block:             block,
			AssistantMessage:  assistantMsg,
			Status:            ToolStatusCompleted,
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
		ID:                block.ID,
		Block:             block,
		AssistantMessage:  assistantMsg,
		Status:            ToolStatusQueued,
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
		modifiers = append(modifiers, e.tools[i].ContextModifiers...)
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
// Maps to TS executeTool() with abort reason checks and Bash-only sibling cancel.
func (e *StreamingToolExecutor) executeTool(idx int) {
	e.mu.Lock()
	tracked := &e.tools[idx]
	block := tracked.Block
	assistantMsg := tracked.AssistantMessage
	e.mu.Unlock()

	// Check if already aborted before starting
	if reason := e.getAbortReason(); reason != AbortReasonNone {
		e.completeWithSyntheticError(idx, reason)
		return
	}

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

		// Only Bash errors cancel siblings. Bash commands often have implicit
		// dependency chains; Read/WebFetch/etc are independent.
		if block.Name == "Bash" || block.Name == "PowerShell" {
			e.mu.Lock()
			e.hasErrored = true
			e.erroredToolDescription = e.getToolDescription(block)
			e.siblingCancel()
			e.mu.Unlock()
		}

		e.completeWithError(idx, "Error: "+err.Error())
		return
	}

	// Build result
	var resultMsgs []agents.Message
	var ctxModifiers []func(*agents.ToolUseContext) *agents.ToolUseContext

	if toolResult != nil {
		resultBlock := toolDef.MapToolResultToParam(toolResult.Data, block.ID)
		if resultBlock != nil {
			resultMsgs = append(resultMsgs, agents.Message{
				Type:                    agents.MessageTypeUser,
				Content:                 []agents.ContentBlock{*resultBlock},
				SourceToolAssistantUUID: assistantMsg.UUID,
			})
		}
		resultMsgs = append(resultMsgs, toolResult.NewMessages...)
		if toolResult.ContextModifier != nil {
			ctxModifiers = append(ctxModifiers, toolResult.ContextModifier)
		}
	}

	// Check for error in result content (tool returned is_error=true)
	if hasToolError(resultMsgs) && (block.Name == "Bash" || block.Name == "PowerShell") {
		e.mu.Lock()
		e.hasErrored = true
		e.erroredToolDescription = e.getToolDescription(block)
		e.siblingCancel()
		e.mu.Unlock()
	}

	e.mu.Lock()
	e.tools[idx].Status = ToolStatusCompleted
	e.tools[idx].Results = resultMsgs
	e.tools[idx].ContextModifiers = ctxModifiers

	// Apply context modifiers for non-concurrent tools (TS L391-395)
	if !e.tools[idx].IsConcurrencySafe && len(ctxModifiers) > 0 {
		for _, mod := range ctxModifiers {
			e.toolCtx = mod(e.toolCtx)
		}
	}
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

// GetRemainingResults waits for all in-flight tools to complete and returns
// all remaining results that haven't been yielded via GetCompletedResults.
// This corresponds to the TS getRemainingResults() async generator.
// Used in two paths:
//  1. Post-streaming tool collection (Phase 6 in queryloop)
//  2. Abort cleanup (emit synthetic results for orphaned tools)
func (e *StreamingToolExecutor) GetRemainingResults() ([]agents.Message, []func(*agents.ToolUseContext) *agents.ToolUseContext) {
	if e.IsDiscarded() {
		return nil, nil
	}

	// Wait for all tools to finish
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
		if e.tools[i].Status == ToolStatusCompleted {
			messages = append(messages, e.tools[i].Results...)
			modifiers = append(modifiers, e.tools[i].ContextModifiers...)
			e.tools[i].Status = ToolStatusYielded
		}
	}
	return messages, modifiers
}

// HasPendingTools returns true if there are tools that haven't completed yet.
func (e *StreamingToolExecutor) HasPendingTools() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, t := range e.tools {
		if t.Status == ToolStatusQueued || t.Status == ToolStatusExecuting {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Abort reason & synthetic error helpers
// ---------------------------------------------------------------------------

// getAbortReason determines why pending tools should be cancelled.
// Maps to TS getAbortReason() in StreamingToolExecutor.ts L210-231.
func (e *StreamingToolExecutor) getAbortReason() AbortReason {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.discarded {
		return AbortReasonStreamingFallback
	}
	if e.hasErrored {
		return AbortReasonSiblingError
	}
	if e.toolCtx.Ctx.Err() != nil {
		return AbortReasonUserInterrupted
	}
	return AbortReasonNone
}

// createSyntheticErrorMessage builds a synthetic tool_result error for a cancelled tool.
// Maps to TS createSyntheticErrorMessage() in StreamingToolExecutor.ts L153-205.
func (e *StreamingToolExecutor) createSyntheticErrorMessage(toolUseID string, reason AbortReason, assistantUUID string) agents.Message {
	var errText string
	switch reason {
	case AbortReasonUserInterrupted:
		errText = "User rejected tool use"
	case AbortReasonStreamingFallback:
		errText = "Streaming fallback - tool execution discarded"
	case AbortReasonSiblingError:
		if e.erroredToolDescription != "" {
			errText = fmt.Sprintf("Cancelled: parallel tool call %s errored", e.erroredToolDescription)
		} else {
			errText = "Cancelled: parallel tool call errored"
		}
	default:
		errText = "Tool execution cancelled"
	}
	return createToolErrorMsg(toolUseID, errText, assistantUUID)
}

// completeWithSyntheticError marks a tool as completed with a synthetic abort error.
func (e *StreamingToolExecutor) completeWithSyntheticError(idx int, reason AbortReason) {
	e.mu.Lock()
	tracked := &e.tools[idx]
	tracked.Status = ToolStatusCompleted
	tracked.Results = []agents.Message{
		e.createSyntheticErrorMessage(tracked.Block.ID, reason, tracked.AssistantMessage.UUID),
	}
	e.mu.Unlock()
	e.completedCond.Broadcast()
	go e.processQueue()
}

// getToolDescription builds a human-readable description of a tool call.
// Maps to TS getToolDescription() in StreamingToolExecutor.ts L243-252.
func (e *StreamingToolExecutor) getToolDescription(block agents.ToolUseBlock) string {
	if len(block.Input) > 0 {
		var input map[string]interface{}
		if err := json.Unmarshal(block.Input, &input); err == nil {
			for _, key := range []string{"command", "file_path", "pattern"} {
				if v, ok := input[key].(string); ok && v != "" {
					if len(v) > 40 {
						v = v[:40] + "\u2026"
					}
					return fmt.Sprintf("%s(%s)", block.Name, v)
				}
			}
		}
	}
	return block.Name
}

// hasToolError checks if any message in the results contains a tool error.
func hasToolError(msgs []agents.Message) bool {
	for _, msg := range msgs {
		for _, block := range msg.Content {
			if block.Type == agents.ContentBlockToolResult && block.IsError {
				return true
			}
		}
	}
	return false
}

// IsDiscarded returns true if the executor has been discarded.
func (e *StreamingToolExecutor) IsDiscarded() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.discarded
}

// GetUpdatedContext returns the current tool use context (may have been modified by tools).
// Maps to TS getUpdatedContext() in StreamingToolExecutor.ts L516-518.
func (e *StreamingToolExecutor) GetUpdatedContext() *agents.ToolUseContext {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.toolCtx
}

// GetPendingProgress collects and drains pending progress messages from all tools.
// Progress messages are yielded immediately regardless of tool completion order.
func (e *StreamingToolExecutor) GetPendingProgress() []agents.Message {
	e.mu.Lock()
	defer e.mu.Unlock()
	var msgs []agents.Message
	for i := range e.tools {
		msgs = append(msgs, e.tools[i].PendingProgress...)
		e.tools[i].PendingProgress = nil
	}
	return msgs
}

// suppress unused import warning for json
var _ = json.RawMessage{}
