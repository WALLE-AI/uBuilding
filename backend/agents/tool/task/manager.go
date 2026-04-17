// Package task is DEPRECATED. The background-shell job manager has moved to
// `agents/tool/bg` and the TodoV2 task-graph CRUD lives in
// `agents/tool/taskgraph`. This file keeps type aliases and thin wrappers so
// legacy callers (integration tests) still compile during the transition.
//
// New code MUST import the concrete packages directly.
package task

import (
	"github.com/wall-ai/ubuilding/backend/agents/tool/bg"
)

// Manager is an alias for bg.Manager.
//
// Deprecated: use bg.Manager directly.
type Manager = bg.Manager

// Task is an alias for bg.Job, preserving the historical name.
//
// Deprecated: use bg.Job directly.
type Task = bg.Job

// Runner is an alias for bg.Runner.
//
// Deprecated: use bg.Runner directly.
type Runner = bg.Runner

// NewManager returns a new background-job manager.
//
// Deprecated: use bg.NewManager directly.
func NewManager() *Manager { return bg.NewManager() }

// Status constants (re-exported for legacy callers).
const (
	StatusRunning   = bg.StatusRunning
	StatusSucceeded = bg.StatusSucceeded
	StatusFailed    = bg.StatusFailed
	StatusCancelled = bg.StatusCancelled
)
