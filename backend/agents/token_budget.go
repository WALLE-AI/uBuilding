package agents

// ---------------------------------------------------------------------------
// BudgetTracker — token budget tracking and continuation decisions
// Maps to TypeScript BudgetTracker in query/tokenBudget.ts
// ---------------------------------------------------------------------------

// BudgetTracker monitors token consumption across iterations and decides
// whether the query loop should continue or stop.
type BudgetTracker struct {
	// History of output token counts per iteration
	outputHistory []int

	// Total input tokens accumulated
	totalInputTokens int

	// Total output tokens accumulated
	totalOutputTokens int

	// Maximum allowed total tokens (from TaskBudget)
	maxTotalTokens int
}

// NewBudgetTracker creates a new BudgetTracker with optional budget limit.
func NewBudgetTracker(budget *TaskBudget) *BudgetTracker {
	bt := &BudgetTracker{}
	if budget != nil {
		bt.maxTotalTokens = budget.Total
	}
	return bt
}

// BudgetDecision represents whether to continue or stop.
type BudgetDecision int

const (
	BudgetContinue BudgetDecision = iota
	BudgetStop
)

// RecordIteration records token usage from a single loop iteration.
func (bt *BudgetTracker) RecordIteration(usage Usage) {
	bt.totalInputTokens += usage.InputTokens
	bt.totalOutputTokens += usage.OutputTokens
	bt.outputHistory = append(bt.outputHistory, usage.OutputTokens)
}

// CheckBudget evaluates whether the query should continue.
// Implements two stopping conditions:
//   1. Hard budget limit exceeded
//   2. Diminishing returns (3+ consecutive iterations with < 500 output tokens)
func (bt *BudgetTracker) CheckBudget() BudgetDecision {
	// Hard budget check
	if bt.maxTotalTokens > 0 {
		total := bt.totalInputTokens + bt.totalOutputTokens
		if total >= bt.maxTotalTokens {
			return BudgetStop
		}
	}

	// Diminishing returns check
	if bt.isDiminishingReturns() {
		return BudgetStop
	}

	return BudgetContinue
}

// isDiminishingReturns detects when the model is producing very little output
// per iteration, indicating it may be stuck in a loop.
// Triggers when 3+ consecutive iterations produce < 500 output tokens each.
func (bt *BudgetTracker) isDiminishingReturns() bool {
	const (
		minConsecutive   = 3
		minOutputTokens  = 500
	)

	if len(bt.outputHistory) < minConsecutive {
		return false
	}

	// Check last N iterations
	start := len(bt.outputHistory) - minConsecutive
	for i := start; i < len(bt.outputHistory); i++ {
		if bt.outputHistory[i] >= minOutputTokens {
			return false
		}
	}
	return true
}

// Remaining returns the remaining token budget, or -1 if unlimited.
func (bt *BudgetTracker) Remaining() int {
	if bt.maxTotalTokens <= 0 {
		return -1
	}
	remaining := bt.maxTotalTokens - (bt.totalInputTokens + bt.totalOutputTokens)
	if remaining < 0 {
		return 0
	}
	return remaining
}

// TotalUsed returns the total tokens consumed.
func (bt *BudgetTracker) TotalUsed() int {
	return bt.totalInputTokens + bt.totalOutputTokens
}
