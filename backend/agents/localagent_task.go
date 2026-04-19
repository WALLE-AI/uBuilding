// Package agents — async sub-agent lifecycle management.
//
// Tasks D09 · D16 · D17 · port the observable surface of
// src/tasks/LocalAgentTask/LocalAgentTask.ts. Responsibilities:
//
//   - D16 · LocalAgentTask state machine (foreground/async/completed/
//     failed/killed) with a tiny registry that hosts can drive themselves.
//   - D09 · SpawnAsyncSubAgent spawns the sub-agent on a goroutine, returns
//     immediately with a task id + output-file placeholder, and enqueues
//     a `<task-notification>` once the child finishes.
//   - D17 · EncodeTaskNotification formats the XML blob the coordinator
//     parent reads via a user-role message.
//
// The module is intentionally self-contained: no IO (beyond the existing
// sidechain paths) and no external deps. Hosts wire the notification
// callback to their own surface (WebSocket, CLI UI, SDK transport…).
package agents

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"time"
)

// AgentTaskState names the lifecycle states an async agent can occupy.
type AgentTaskState string

const (
	TaskStatePending   AgentTaskState = "pending"
	TaskStateRunning   AgentTaskState = "running"
	TaskStateCompleted AgentTaskState = "completed"
	TaskStateFailed    AgentTaskState = "failed"
	TaskStateKilled    AgentTaskState = "killed"
)

// LocalAgentTask is the in-memory record of an async sub-agent launch.
type LocalAgentTask struct {
	ID          string
	AgentType   string
	Description string
	Prompt      string
	State       AgentTaskState
	StartedAt   time.Time
	CompletedAt time.Time
	FinalText   string
	Error       string
	OutputFile  string

	cancel context.CancelFunc
	mu     sync.Mutex
}

// LocalAgentTaskManager tracks every spawned async sub-agent. Safe for
// concurrent use.
type LocalAgentTaskManager struct {
	mu    sync.Mutex
	tasks map[string]*LocalAgentTask

	// OnNotification, when non-nil, is invoked whenever a task transitions
	// to a terminal state (completed/failed/killed). Kept for the simple
	// single-subscriber case; UI hosts that need fan-out should use
	// AddObserver instead.
	OnNotification func(note TaskNotification)

	// observers is the multi-subscriber list (Wave 3 UI wiring). Access is
	// guarded by obsMu so observers can be added / removed concurrently
	// with task transitions.
	obsMu     sync.RWMutex
	observers map[int]TaskObserver
	nextObsID int
}

// TaskObserver is the callback signature hosts register via AddObserver.
// The callback is invoked synchronously inside the goroutine that drove
// the state transition — observers MUST be non-blocking (a blocking
// observer stalls sub-agent completion reporting).
type TaskObserver func(note TaskNotification)

// NewLocalAgentTaskManager constructs an empty manager.
func NewLocalAgentTaskManager() *LocalAgentTaskManager {
	return &LocalAgentTaskManager{
		tasks:     map[string]*LocalAgentTask{},
		observers: map[int]TaskObserver{},
	}
}

// AddObserver registers an observer and returns a cancellation function
// that removes it. Safe to call from any goroutine, including from inside
// another observer (the add is queued against obsMu).
func (m *LocalAgentTaskManager) AddObserver(obs TaskObserver) func() {
	if obs == nil {
		return func() {}
	}
	m.obsMu.Lock()
	if m.observers == nil {
		m.observers = map[int]TaskObserver{}
	}
	id := m.nextObsID
	m.nextObsID++
	m.observers[id] = obs
	m.obsMu.Unlock()
	return func() {
		m.obsMu.Lock()
		delete(m.observers, id)
		m.obsMu.Unlock()
	}
}

// ObserverCount returns the number of currently registered observers.
// Useful for tests and readiness probes.
func (m *LocalAgentTaskManager) ObserverCount() int {
	m.obsMu.RLock()
	defer m.obsMu.RUnlock()
	return len(m.observers)
}

// Register adds a task in the pending state and returns its pointer.
func (m *LocalAgentTaskManager) Register(task *LocalAgentTask) *LocalAgentTask {
	if task.ID == "" {
		task.ID = "agt-" + randAgentID()
	}
	if task.StartedAt.IsZero() {
		task.StartedAt = time.Now()
	}
	if task.State == "" {
		task.State = TaskStatePending
	}
	m.mu.Lock()
	m.tasks[task.ID] = task
	m.mu.Unlock()
	return task
}

// Get returns the task with the given id, or nil.
func (m *LocalAgentTaskManager) Get(id string) *LocalAgentTask {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.tasks[id]
}

// List returns a snapshot of every tracked task.
func (m *LocalAgentTaskManager) List() []*LocalAgentTask {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*LocalAgentTask, 0, len(m.tasks))
	for _, t := range m.tasks {
		out = append(out, t)
	}
	return out
}

// Kill marks the task killed and cancels its context if still running.
// No-op when the task is already in a terminal state.
func (m *LocalAgentTaskManager) Kill(id string) error {
	m.mu.Lock()
	task := m.tasks[id]
	m.mu.Unlock()
	if task == nil {
		return fmt.Errorf("LocalAgentTask: unknown id %q", id)
	}
	task.mu.Lock()
	defer task.mu.Unlock()
	if task.terminal() {
		return nil
	}
	task.State = TaskStateKilled
	task.CompletedAt = time.Now()
	if task.cancel != nil {
		task.cancel()
	}
	m.notify(task)
	return nil
}

// complete transitions task to completed with the supplied final text.
func (m *LocalAgentTaskManager) complete(task *LocalAgentTask, finalText string) {
	task.mu.Lock()
	defer task.mu.Unlock()
	if task.terminal() {
		return
	}
	task.State = TaskStateCompleted
	task.FinalText = finalText
	task.CompletedAt = time.Now()
	m.notify(task)
}

// fail transitions task to failed with an error string.
func (m *LocalAgentTaskManager) fail(task *LocalAgentTask, err error) {
	task.mu.Lock()
	defer task.mu.Unlock()
	if task.terminal() {
		return
	}
	task.State = TaskStateFailed
	task.Error = err.Error()
	task.CompletedAt = time.Now()
	m.notify(task)
}

func (m *LocalAgentTaskManager) notify(task *LocalAgentTask) {
	note := TaskNotification{
		TaskID:    task.ID,
		AgentType: task.AgentType,
		Status:    task.State,
		Summary:   task.Description,
		Result:    task.FinalText,
		Error:     task.Error,
		Usage: TaskUsageSummary{
			DurationMs: task.CompletedAt.Sub(task.StartedAt).Milliseconds(),
		},
	}
	if m.OnNotification != nil {
		m.OnNotification(note)
	}
	// Snapshot observers under the read lock so a panicking / slow observer
	// doesn't block other registrations for the duration of the callback.
	m.obsMu.RLock()
	snapshot := make([]TaskObserver, 0, len(m.observers))
	for _, obs := range m.observers {
		snapshot = append(snapshot, obs)
	}
	m.obsMu.RUnlock()
	for _, obs := range snapshot {
		obs(note)
	}
}

func (t *LocalAgentTask) terminal() bool {
	switch t.State {
	case TaskStateCompleted, TaskStateFailed, TaskStateKilled:
		return true
	}
	return false
}

// ---------------------------------------------------------------------------
// D17 · <task-notification> XML
// ---------------------------------------------------------------------------

// TaskNotification mirrors the XML blob coordinator parents consume.
type TaskNotification struct {
	XMLName   xml.Name         `xml:"task-notification"`
	TaskID    string           `xml:"task-id"`
	AgentType string           `xml:"agent-type,omitempty"`
	Status    AgentTaskState   `xml:"status"`
	Summary   string           `xml:"summary,omitempty"`
	Result    string           `xml:"result,omitempty"`
	Error     string           `xml:"error,omitempty"`
	Usage     TaskUsageSummary `xml:"usage"`
}

// TaskUsageSummary is the condensed usage reported back to the parent.
// Token / tool counts stay optional: callers can set them when known.
type TaskUsageSummary struct {
	TotalTokens int   `xml:"total_tokens,omitempty"`
	ToolUses    int   `xml:"tool_uses,omitempty"`
	DurationMs  int64 `xml:"duration_ms,omitempty"`
}

// EncodeTaskNotification marshals a TaskNotification into the XML blob the
// coordinator-mode parent agent expects as a user-role message.
func EncodeTaskNotification(note TaskNotification) (string, error) {
	var buf bytes.Buffer
	enc := xml.NewEncoder(&buf)
	enc.Indent("", "  ")
	if err := enc.Encode(note); err != nil {
		return "", err
	}
	if err := enc.Flush(); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// ---------------------------------------------------------------------------
// D09 · SpawnAsyncSubAgent
// ---------------------------------------------------------------------------

// AsyncAgentHandle is returned immediately from SpawnAsyncSubAgent so the
// parent agent can continue while the child runs in the background.
type AsyncAgentHandle struct {
	TaskID     string
	AgentType  string
	OutputFile string
}

// SpawnAsyncSubAgent launches the sub-agent on a goroutine, registers it
// with manager, and returns immediately. The goroutine updates task state
// via the manager; callers subscribe to completion via
// LocalAgentTaskManager.OnNotification or poll the task state directly.
func (e *QueryEngine) SpawnAsyncSubAgent(
	ctx context.Context,
	manager *LocalAgentTaskManager,
	params SubAgentParams,
) (*AsyncAgentHandle, error) {
	if manager == nil {
		return nil, errors.New("SpawnAsyncSubAgent: manager required")
	}
	// Resolve agent type up-front so we can populate the handle + task.
	agents := e.Agents()
	agentType := params.SubagentType
	if agentType == "" {
		agentType = GeneralPurposeAgent.AgentType
	}
	def := agents.FindActive(agentType)
	if def == nil && agentType == GeneralPurposeAgent.AgentType {
		gp := GeneralPurposeAgent
		def = &gp
	}
	if def == nil {
		return nil, fmt.Errorf("%w: %s", ErrUnknownSubagentType, agentType)
	}

	task := manager.Register(&LocalAgentTask{
		AgentType:   def.AgentType,
		Description: params.Description,
		Prompt:      params.Prompt,
		State:       TaskStateRunning,
		OutputFile:  asyncOutputFile(def.AgentType),
	})
	childCtx, cancel := context.WithCancel(ctx)
	task.cancel = cancel

	go func() {
		defer cancel()
		final, err := e.SpawnSubAgent(childCtx, params)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				// The coordinator killed us — don't overwrite the state
				// transition Kill() already performed.
				return
			}
			manager.fail(task, err)
			return
		}
		manager.complete(task, final)
	}()

	return &AsyncAgentHandle{
		TaskID:     task.ID,
		AgentType:  def.AgentType,
		OutputFile: task.OutputFile,
	}, nil
}

// asyncOutputFile is the placeholder path used when no sidechain writer is
// wired. Hosts typically override it to point at the sidechain transcript.
func asyncOutputFile(agentType string) string {
	return filepath.Join(".claude", "subagents", agentType+".jsonl")
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

var asyncIDCounter struct {
	mu sync.Mutex
	n  int
}

func randAgentID() string {
	asyncIDCounter.mu.Lock()
	defer asyncIDCounter.mu.Unlock()
	asyncIDCounter.n++
	return fmt.Sprintf("%d-%d", time.Now().UnixNano(), asyncIDCounter.n)
}
