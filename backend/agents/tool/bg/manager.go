// Package bg implements background shell-job management and the two tools
// (TaskOutput, TaskStop) that front it. Models spawn a background job by
// calling Bash / PowerShell with `run_in_background: true`; the tool returns
// a `bash_id` that can be polled via TaskOutput and cancelled via TaskStop.
//
// This mirrors claude-code-main's BashTool background flow. It is distinct
// from the TodoV2 "task graph" surface (see agents/tool/taskgraph).
package bg

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
)

// Status values.
const (
	StatusRunning   = "running"
	StatusSucceeded = "succeeded"
	StatusFailed    = "failed"
	StatusCancelled = "cancelled"
)

// Job is a snapshot of a background shell job.
type Job struct {
	ID         string    `json:"id"`
	Command    string    `json:"command"`
	Status     string    `json:"status"`
	ExitCode   int       `json:"exit_code"`
	StartedAt  time.Time `json:"started_at"`
	EndedAt    time.Time `json:"ended_at,omitempty"`
	Output     string    `json:"output"`
	Truncated  bool      `json:"truncated,omitempty"`
	Error      string    `json:"error,omitempty"`
	// OutputCursor is the byte offset into the captured output that callers
	// have already seen. TaskOutput uses it to emit incremental slices.
	OutputCursor int `json:"output_cursor,omitempty"`
}

// Runner executes a job body and reports its outcome.
type Runner func(ctx context.Context, write func(chunk string)) (exitCode int, err error)

// Manager tracks background-job lifecycles for a single session.
type Manager struct {
	mu             sync.RWMutex
	jobs           map[string]*jobState
	maxOutputBytes int
}

type jobState struct {
	job          Job
	cancel       context.CancelFunc
	output       chunkedBuffer
	done         chan struct{}
	outputCursor int // bytes already handed to callers
}

type chunkedBuffer struct {
	mu        sync.Mutex
	buf       []byte
	max       int
	truncated atomic.Bool
}

func (b *chunkedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.max > 0 {
		remaining := b.max - len(b.buf)
		if remaining <= 0 {
			b.truncated.Store(true)
			return len(p), nil
		}
		if len(p) > remaining {
			b.buf = append(b.buf, p[:remaining]...)
			b.truncated.Store(true)
			return len(p), nil
		}
	}
	b.buf = append(b.buf, p...)
	return len(p), nil
}

func (b *chunkedBuffer) Snapshot() (string, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.buf), b.truncated.Load()
}

// DefaultMaxOutputBytes is the per-job output cap.
const DefaultMaxOutputBytes = 1 << 20

// IDPrefix is prepended to every job id so callers (and the shared TaskStop
// tool) can distinguish background-shell ids from task-graph ids at a glance.
const IDPrefix = "bash_"

// NewManager returns a new job manager.
func NewManager() *Manager {
	return &Manager{jobs: map[string]*jobState{}, maxOutputBytes: DefaultMaxOutputBytes}
}

// Start launches a new background job and returns its id.
func (m *Manager) Start(parent context.Context, command string, runner Runner) (string, error) {
	if runner == nil {
		return "", errors.New("bg: runner required")
	}
	id := IDPrefix + uuid.NewString()
	ctx, cancel := context.WithCancel(parent)
	st := &jobState{
		cancel: cancel,
		done:   make(chan struct{}),
	}
	st.output.max = m.maxOutputBytes
	st.job = Job{
		ID:        id,
		Command:   command,
		Status:    StatusRunning,
		StartedAt: time.Now(),
	}
	m.mu.Lock()
	m.jobs[id] = st
	m.mu.Unlock()
	go func() {
		defer close(st.done)
		exit, err := runner(ctx, func(chunk string) { st.output.Write([]byte(chunk)) })
		m.mu.Lock()
		defer m.mu.Unlock()
		st.job.EndedAt = time.Now()
		st.job.ExitCode = exit
		switch {
		case err != nil && ctx.Err() == context.Canceled:
			st.job.Status = StatusCancelled
			st.job.Error = err.Error()
		case err != nil:
			st.job.Status = StatusFailed
			st.job.Error = err.Error()
		default:
			st.job.Status = StatusSucceeded
		}
	}()
	return id, nil
}

// Get returns a snapshot of the job including its full output buffer.
func (m *Manager) Get(id string) (Job, bool) {
	m.mu.RLock()
	st, ok := m.jobs[id]
	m.mu.RUnlock()
	if !ok {
		return Job{}, false
	}
	return m.snapshot(st), true
}

// Owns reports whether the given id is tracked by this manager (cheap check).
func (m *Manager) Owns(id string) bool {
	m.mu.RLock()
	_, ok := m.jobs[id]
	m.mu.RUnlock()
	return ok
}

func (m *Manager) snapshot(st *jobState) Job {
	out, trunc := st.output.Snapshot()
	m.mu.RLock()
	j := st.job
	cursor := st.outputCursor
	m.mu.RUnlock()
	j.Output = out
	j.Truncated = trunc
	j.OutputCursor = cursor
	return j
}

// ReadOutput returns the incremental output since the last call, along with
// the latest status. `advance` controls whether the cursor is advanced so the
// next call only returns newly emitted bytes.
func (m *Manager) ReadOutput(id string, advance bool) (Job, string, bool, error) {
	m.mu.RLock()
	st, ok := m.jobs[id]
	m.mu.RUnlock()
	if !ok {
		return Job{}, "", false, fmt.Errorf("bg: job %s not found", id)
	}
	full, trunc := st.output.Snapshot()
	m.mu.Lock()
	cursor := st.outputCursor
	if cursor > len(full) {
		cursor = len(full)
	}
	slice := full[cursor:]
	if advance {
		st.outputCursor = len(full)
	}
	job := st.job
	job.Output = full
	job.Truncated = trunc
	job.OutputCursor = st.outputCursor
	m.mu.Unlock()
	return job, slice, trunc, nil
}

// List returns all known jobs sorted by StartedAt (newest first).
func (m *Manager) List() []Job {
	m.mu.RLock()
	ids := make([]string, 0, len(m.jobs))
	for id := range m.jobs {
		ids = append(ids, id)
	}
	m.mu.RUnlock()
	out := make([]Job, 0, len(ids))
	for _, id := range ids {
		if j, ok := m.Get(id); ok {
			out = append(out, j)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt.After(out[j].StartedAt) })
	return out
}

// Stop cancels a job and waits up to timeout for it to finish.
func (m *Manager) Stop(id string, timeout time.Duration) (Job, error) {
	m.mu.RLock()
	st, ok := m.jobs[id]
	m.mu.RUnlock()
	if !ok {
		return Job{}, fmt.Errorf("bg: job %s not found", id)
	}
	st.cancel()
	select {
	case <-st.done:
	case <-time.After(timeout):
	}
	j, _ := m.Get(id)
	return j, nil
}

// Remove deletes a terminated job from the registry. No-op if still running.
func (m *Manager) Remove(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	st, ok := m.jobs[id]
	if !ok {
		return false
	}
	if st.job.Status == StatusRunning {
		return false
	}
	delete(m.jobs, id)
	return true
}

// WaitForTerminal blocks until the job terminates or ctx cancels.
func (m *Manager) WaitForTerminal(ctx context.Context, id string) (Job, error) {
	m.mu.RLock()
	st, ok := m.jobs[id]
	m.mu.RUnlock()
	if !ok {
		return Job{}, fmt.Errorf("bg: job %s not found", id)
	}
	select {
	case <-st.done:
		j, _ := m.Get(id)
		return j, nil
	case <-ctx.Done():
		j, _ := m.Get(id)
		return j, ctx.Err()
	}
}
