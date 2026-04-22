package memory

import (
	"context"
	"sync"
	"time"

	"github.com/wall-ai/ubuilding/backend/agents"
)

// ---------------------------------------------------------------------------
// M14.I5 · DreamTask — background task state and factory for auto-dream.
//
// Ports `src/tasks/DreamTask/` state management and integrates with
// the BackgroundTaskManager via a factory function.
// ---------------------------------------------------------------------------

// DreamPhase represents the current phase of a dream task.
type DreamPhase string

const (
	DreamPhaseStarting DreamPhase = "starting"
	DreamPhaseUpdating DreamPhase = "updating"
	DreamPhaseComplete DreamPhase = "complete"
	DreamPhaseFailed   DreamPhase = "failed"
	DreamPhaseKilled   DreamPhase = "killed"
)

// DreamTurn records a single assistant turn from the forked dream agent.
type DreamTurn struct {
	Text         string
	ToolUseCount int
}

// DreamTaskState holds the mutable state of an in-progress dream task.
type DreamTaskState struct {
	mu                sync.Mutex
	Phase             DreamPhase
	SessionsReviewing int
	FilesTouched      []string
	Turns             []DreamTurn
	PriorMtime        *time.Time
	StartTime         time.Time
	Error             string
}

// NewDreamTaskState creates a new dream task state.
func NewDreamTaskState(sessionsReviewing int, priorMtime *time.Time) *DreamTaskState {
	return &DreamTaskState{
		Phase:             DreamPhaseStarting,
		SessionsReviewing: sessionsReviewing,
		FilesTouched:      make([]string, 0),
		Turns:             make([]DreamTurn, 0),
		PriorMtime:        priorMtime,
		StartTime:         time.Now(),
	}
}

// AddTurn records a dream agent turn and transitions to "updating"
// phase on the first tool use.
func (s *DreamTaskState) AddTurn(turn DreamTurn, touchedPaths []string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.Turns = append(s.Turns, turn)

	// Deduplicate touched paths.
	seen := make(map[string]bool, len(s.FilesTouched))
	for _, f := range s.FilesTouched {
		seen[f] = true
	}
	for _, p := range touchedPaths {
		if !seen[p] {
			s.FilesTouched = append(s.FilesTouched, p)
			seen[p] = true
		}
	}

	// Transition to updating phase on first tool use.
	if s.Phase == DreamPhaseStarting && turn.ToolUseCount > 0 {
		s.Phase = DreamPhaseUpdating
	}
}

// Complete marks the task as successfully completed.
func (s *DreamTaskState) Complete() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Phase = DreamPhaseComplete
}

// Fail marks the task as failed with an error message.
func (s *DreamTaskState) Fail(errMsg string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Phase = DreamPhaseFailed
	s.Error = errMsg
}

// Kill marks the task as killed by the user.
func (s *DreamTaskState) Kill() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Phase = DreamPhaseKilled
}

// GetPhase returns the current phase (thread-safe).
func (s *DreamTaskState) GetPhase() DreamPhase {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Phase
}

// GetFilesTouched returns a copy of the files-touched list (thread-safe).
func (s *DreamTaskState) GetFilesTouched() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := make([]string, len(s.FilesTouched))
	copy(cp, s.FilesTouched)
	return cp
}

// IsDreamTask checks if a background task result is a *DreamTaskState.
func IsDreamTask(result interface{}) bool {
	_, ok := result.(*DreamTaskState)
	return ok
}

// AutoDreamTaskFactory creates a BackgroundTaskFunc that wraps the
// AutoDreamService.ExecuteAutoDream call and returns a *DreamTaskState
// as the task result.
//
// Usage:
//
//	svc := memory.NewAutoDreamService(cwd, sessionID, cfg, settings, logger)
//	mgr.RegisterFactory(agents.TaskTypeAutoDream, memory.AutoDreamTaskFactory(svc))
func AutoDreamTaskFactory(svc *AutoDreamService) agents.BackgroundTaskFunc {
	return func(ctx context.Context, taskCtx agents.BackgroundTaskContext) (interface{}, error) {
		result, err := svc.ExecuteAutoDream(ctx, taskCtx.Messages)
		if err != nil {
			return nil, err
		}

		state := NewDreamTaskState(result.SessionCount, nil)
		if result.Skipped {
			state.Phase = DreamPhaseComplete
			return state, nil
		}

		// Populate touched files from result.
		state.mu.Lock()
		state.FilesTouched = result.FilesTouched
		state.Phase = DreamPhaseComplete
		state.mu.Unlock()

		return state, nil
	}
}
