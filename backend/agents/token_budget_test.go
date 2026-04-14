package agents_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/wall-ai/ubuilding/backend/agents"
)

func TestBudgetTracker_NoBudget(t *testing.T) {
	bt := agents.NewBudgetTracker(nil)
	assert.Equal(t, agents.BudgetContinue, bt.CheckBudget())
	assert.Equal(t, -1, bt.Remaining())
}

func TestBudgetTracker_ExceedsBudget(t *testing.T) {
	bt := agents.NewBudgetTracker(&agents.TaskBudget{Total: 100})
	bt.RecordIteration(agents.Usage{InputTokens: 60, OutputTokens: 50})
	assert.Equal(t, agents.BudgetStop, bt.CheckBudget())
	assert.Equal(t, 0, bt.Remaining())
}

func TestBudgetTracker_WithinBudget(t *testing.T) {
	bt := agents.NewBudgetTracker(&agents.TaskBudget{Total: 1000})
	bt.RecordIteration(agents.Usage{InputTokens: 100, OutputTokens: 50})
	assert.Equal(t, agents.BudgetContinue, bt.CheckBudget())
	assert.Equal(t, 850, bt.Remaining())
}

func TestBudgetTracker_DiminishingReturns(t *testing.T) {
	bt := agents.NewBudgetTracker(nil) // no hard budget
	// 3 consecutive low-output iterations
	bt.RecordIteration(agents.Usage{InputTokens: 1000, OutputTokens: 100})
	bt.RecordIteration(agents.Usage{InputTokens: 1000, OutputTokens: 200})
	bt.RecordIteration(agents.Usage{InputTokens: 1000, OutputTokens: 50})
	assert.Equal(t, agents.BudgetStop, bt.CheckBudget())
}

func TestBudgetTracker_NoDiminishingReturns(t *testing.T) {
	bt := agents.NewBudgetTracker(nil)
	bt.RecordIteration(agents.Usage{InputTokens: 1000, OutputTokens: 100})
	bt.RecordIteration(agents.Usage{InputTokens: 1000, OutputTokens: 600}) // above threshold
	bt.RecordIteration(agents.Usage{InputTokens: 1000, OutputTokens: 50})
	assert.Equal(t, agents.BudgetContinue, bt.CheckBudget())
}
