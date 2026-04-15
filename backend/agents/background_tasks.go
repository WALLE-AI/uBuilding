package agents

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

// ---------------------------------------------------------------------------
// Background Task Framework
// Maps to TypeScript's background tasks fired in stopHooks.ts:
//   - executePromptSuggestion
//   - executeMemoryExtraction
//   - executeAutoDream
//
// In Go, these are goroutines managed by a BackgroundTaskManager that
// provides lifecycle tracking, cancellation, and result collection.
// ---------------------------------------------------------------------------

// BackgroundTaskType identifies the kind of background task.
type BackgroundTaskType string

const (
	TaskTypePromptSuggestion BackgroundTaskType = "prompt_suggestion"
	TaskTypeMemoryExtraction BackgroundTaskType = "memory_extraction"
	TaskTypeAutoDream        BackgroundTaskType = "auto_dream"
	TaskTypeSessionMemory    BackgroundTaskType = "session_memory"
)

// BackgroundTaskStatus represents the lifecycle state of a task.
type BackgroundTaskStatus string

const (
	TaskStatusPending   BackgroundTaskStatus = "pending"
	TaskStatusRunning   BackgroundTaskStatus = "running"
	TaskStatusCompleted BackgroundTaskStatus = "completed"
	TaskStatusFailed    BackgroundTaskStatus = "failed"
	TaskStatusCancelled BackgroundTaskStatus = "cancelled"
)

// BackgroundTask is a unit of async work.
type BackgroundTask struct {
	ID        string
	Type      BackgroundTaskType
	Status    BackgroundTaskStatus
	StartTime time.Time
	EndTime   time.Time
	Error     error
	Result    interface{} // task-specific result
}

// BackgroundTaskFunc is the function signature for a background task.
// It receives a context and the current stop hook context, and returns
// an optional result.
type BackgroundTaskFunc func(ctx context.Context, taskCtx BackgroundTaskContext) (interface{}, error)

// BackgroundTaskContext provides context for background task execution.
type BackgroundTaskContext struct {
	SessionID  string
	Messages   []Message
	ToolCtx    *ToolUseContext
	StopReason string
	TurnCount  int
	Cwd        string
	Logger     *slog.Logger
}

// ---------------------------------------------------------------------------
// BackgroundTaskManager
// ---------------------------------------------------------------------------

// taskCounter provides globally unique task IDs.
var taskCounter atomic.Int64

// BackgroundTaskManager manages fire-and-forget background tasks.
// Tasks are started after a turn completes and run concurrently.
type BackgroundTaskManager struct {
	mu     sync.Mutex
	tasks  map[string]*BackgroundTask
	wg     sync.WaitGroup
	logger *slog.Logger

	// Cancel all background tasks
	cancelFunc context.CancelFunc
	ctx        context.Context

	// Registered task factories
	factories map[BackgroundTaskType]BackgroundTaskFunc
}

// NewBackgroundTaskManager creates a new task manager.
func NewBackgroundTaskManager(logger *slog.Logger) *BackgroundTaskManager {
	ctx, cancel := context.WithCancel(context.Background())
	if logger == nil {
		logger = slog.Default()
	}
	return &BackgroundTaskManager{
		tasks:      make(map[string]*BackgroundTask),
		logger:     logger,
		ctx:        ctx,
		cancelFunc: cancel,
		factories:  make(map[BackgroundTaskType]BackgroundTaskFunc),
	}
}

// RegisterFactory registers a task implementation for a given type.
func (m *BackgroundTaskManager) RegisterFactory(taskType BackgroundTaskType, fn BackgroundTaskFunc) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.factories[taskType] = fn
}

// Start launches a background task of the given type.
// Returns the task ID. If no factory is registered, returns empty string.
func (m *BackgroundTaskManager) Start(taskType BackgroundTaskType, taskCtx BackgroundTaskContext) string {
	m.mu.Lock()
	fn, ok := m.factories[taskType]
	if !ok {
		m.mu.Unlock()
		return ""
	}

	taskID := fmt.Sprintf("%s_%d", taskType, taskCounter.Add(1))
	task := &BackgroundTask{
		ID:        taskID,
		Type:      taskType,
		Status:    TaskStatusRunning,
		StartTime: time.Now(),
	}
	m.tasks[taskID] = task
	m.mu.Unlock()

	m.wg.Add(1)
	go func() {
		defer m.wg.Done()

		result, err := fn(m.ctx, taskCtx)

		m.mu.Lock()
		defer m.mu.Unlock()

		task.EndTime = time.Now()
		task.Result = result
		task.Error = err

		if m.ctx.Err() != nil {
			task.Status = TaskStatusCancelled
		} else if err != nil {
			task.Status = TaskStatusFailed
			m.logger.Error("background task failed",
				"task_id", taskID,
				"type", taskType,
				"error", err,
				"duration_ms", task.EndTime.Sub(task.StartTime).Milliseconds(),
			)
		} else {
			task.Status = TaskStatusCompleted
			m.logger.Debug("background task completed",
				"task_id", taskID,
				"type", taskType,
				"duration_ms", task.EndTime.Sub(task.StartTime).Milliseconds(),
			)
		}
	}()

	return taskID
}

// GetTask returns a task by ID, or nil if not found.
func (m *BackgroundTaskManager) GetTask(taskID string) *BackgroundTask {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.tasks[taskID]
}

// GetTasksByType returns all tasks of a given type.
func (m *BackgroundTaskManager) GetTasksByType(taskType BackgroundTaskType) []*BackgroundTask {
	m.mu.Lock()
	defer m.mu.Unlock()

	var result []*BackgroundTask
	for _, t := range m.tasks {
		if t.Type == taskType {
			result = append(result, t)
		}
	}
	return result
}

// ActiveCount returns the number of currently running tasks.
func (m *BackgroundTaskManager) ActiveCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()

	count := 0
	for _, t := range m.tasks {
		if t.Status == TaskStatusRunning {
			count++
		}
	}
	return count
}

// CancelAll cancels all running background tasks.
func (m *BackgroundTaskManager) CancelAll() {
	m.cancelFunc()
}

// Wait blocks until all background tasks have completed or been cancelled.
func (m *BackgroundTaskManager) Wait() {
	m.wg.Wait()
}

// WaitWithTimeout blocks until all tasks complete or the timeout elapses.
// Returns true if all tasks completed, false on timeout.
func (m *BackgroundTaskManager) WaitWithTimeout(timeout time.Duration) bool {
	done := make(chan struct{})
	go func() {
		m.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return true
	case <-time.After(timeout):
		return false
	}
}

// Shutdown cancels all tasks and waits for them to finish.
func (m *BackgroundTaskManager) Shutdown(timeout time.Duration) {
	m.CancelAll()
	m.WaitWithTimeout(timeout)
}

// ---------------------------------------------------------------------------
// End-of-turn task runner
// ---------------------------------------------------------------------------

// RunEndOfTurnTasks fires the standard set of background tasks after a turn.
// Maps to the post-turn logic in TS stopHooks.ts.
func (m *BackgroundTaskManager) RunEndOfTurnTasks(taskCtx BackgroundTaskContext) {
	// Fire all registered tasks that should run at end of turn.
	// The order doesn't matter since they run concurrently.
	for taskType := range m.factories {
		m.Start(taskType, taskCtx)
	}
}
