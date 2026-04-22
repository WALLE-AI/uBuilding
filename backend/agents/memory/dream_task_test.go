package memory

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// ---------------------------------------------------------------------------
// M14.T5 · DreamTask state tests.
// ---------------------------------------------------------------------------

func TestDreamTaskState_NewDefaults(t *testing.T) {
	prior := time.Now().Add(-1 * time.Hour)
	state := NewDreamTaskState(5, &prior)

	assert.Equal(t, DreamPhaseStarting, state.GetPhase())
	assert.Equal(t, 5, state.SessionsReviewing)
	assert.Empty(t, state.FilesTouched)
	assert.Empty(t, state.Turns)
	assert.NotNil(t, state.PriorMtime)
}

func TestDreamTaskState_AddTurn_TransitionToUpdating(t *testing.T) {
	state := NewDreamTaskState(3, nil)

	// Add a turn with no tool use — stays in starting.
	state.AddTurn(DreamTurn{Text: "Reading memory files...", ToolUseCount: 0}, nil)
	assert.Equal(t, DreamPhaseStarting, state.GetPhase())

	// Add a turn with tool use — transitions to updating.
	state.AddTurn(DreamTurn{Text: "Updating preferences...", ToolUseCount: 2}, []string{"prefs.md"})
	assert.Equal(t, DreamPhaseUpdating, state.GetPhase())
	assert.Contains(t, state.GetFilesTouched(), "prefs.md")
}

func TestDreamTaskState_AddTurn_DeduplicatesFiles(t *testing.T) {
	state := NewDreamTaskState(1, nil)

	state.AddTurn(DreamTurn{ToolUseCount: 1}, []string{"a.md", "b.md"})
	state.AddTurn(DreamTurn{ToolUseCount: 1}, []string{"b.md", "c.md"})

	files := state.GetFilesTouched()
	assert.Equal(t, 3, len(files))
	assert.Contains(t, files, "a.md")
	assert.Contains(t, files, "b.md")
	assert.Contains(t, files, "c.md")
}

func TestDreamTaskState_Complete(t *testing.T) {
	state := NewDreamTaskState(1, nil)
	state.Complete()
	assert.Equal(t, DreamPhaseComplete, state.GetPhase())
}

func TestDreamTaskState_Fail(t *testing.T) {
	state := NewDreamTaskState(1, nil)
	state.Fail("side query timeout")
	assert.Equal(t, DreamPhaseFailed, state.GetPhase())
	assert.Equal(t, "side query timeout", state.Error)
}

func TestDreamTaskState_Kill(t *testing.T) {
	state := NewDreamTaskState(1, nil)
	state.Kill()
	assert.Equal(t, DreamPhaseKilled, state.GetPhase())
}

func TestIsDreamTask(t *testing.T) {
	state := NewDreamTaskState(1, nil)
	assert.True(t, IsDreamTask(state))
	assert.False(t, IsDreamTask("not a dream task"))
	assert.False(t, IsDreamTask(nil))
}
