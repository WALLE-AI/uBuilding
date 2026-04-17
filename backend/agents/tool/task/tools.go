// DEPRECATED. Legacy input shapes kept so older tests still compile.
// These are no longer wired to any Tool implementation.
package task

// StartInput mirrors the old TaskStart input shape. The backing tool has been
// removed; use bash.Input{RunInBackground: true} instead.
//
// Deprecated: use bash.Input / powershell.Input with RunInBackground.
type StartInput struct {
Command   string `json:"command"`
TimeoutMs int    `json:"timeout_ms,omitempty"`
Cwd       string `json:"cwd,omitempty"`
}

// StatusInput mirrors the old TaskStatus input shape.
//
// Deprecated: use bg.OutputInput (TaskOutput tool) instead.
type StatusInput struct {
TaskID string `json:"task_id"`
}

// KillInput mirrors the old TaskKill input shape.
//
// Deprecated: use bg.StopInput (TaskStop tool) instead.
type KillInput struct {
TaskID string `json:"task_id"`
}
