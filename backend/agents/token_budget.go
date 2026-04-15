package agents

import (
	"fmt"
	"time"
)

// ---------------------------------------------------------------------------
// BudgetTracker — token budget tracking and continuation decisions
// Maps to TypeScript BudgetTracker in query/tokenBudget.ts
// ---------------------------------------------------------------------------

const (
	budgetCompletionThreshold  = 0.9
	budgetDiminishingThreshold = 500
)

// BudgetTracker monitors token consumption across iterations and decides
// whether the query loop should continue or stop.
type BudgetTracker struct {
	// ContinuationCount tracks how many times we've continued past end_turn.
	ContinuationCount int

	// LastDeltaTokens is the output token delta from the previous check.
	LastDeltaTokens int

	// LastGlobalTurnTokens is the global turn output tokens at last check.
	LastGlobalTurnTokens int

	// StartedAt records when this tracker was created.
	StartedAt time.Time

	// History of output token counts per iteration (for legacy CheckBudget).
	outputHistory []int

	// Total input tokens accumulated.
	totalInputTokens int

	// Total output tokens accumulated.
	totalOutputTokens int

	// Maximum allowed total tokens (from TaskBudget).
	maxTotalTokens int
}

// NewBudgetTracker creates a new BudgetTracker with optional budget limit.
func NewBudgetTracker(budget *TaskBudget) *BudgetTracker {
	bt := &BudgetTracker{
		StartedAt: time.Now(),
	}
	if budget != nil {
		bt.maxTotalTokens = budget.Total
	}
	return bt
}

// ---------------------------------------------------------------------------
// TokenBudgetDecision — matches TypeScript ContinueDecision | StopDecision
// ---------------------------------------------------------------------------

// BudgetAction is "continue" or "stop".
type BudgetAction string

const (
	BudgetActionContinue BudgetAction = "continue"
	BudgetActionStop     BudgetAction = "stop"
)

// TokenBudgetDecision is the result of CheckTokenBudget.
type TokenBudgetDecision struct {
	Action            BudgetAction
	NudgeMessage      string // only set when Action == "continue"
	ContinuationCount int
	Pct               int
	TurnTokens        int
	Budget            int
	CompletionEvent   *BudgetCompletionEvent // only set when Action == "stop" and meaningful
}

// BudgetCompletionEvent is emitted when the budget cycle finishes.
type BudgetCompletionEvent struct {
	ContinuationCount  int   `json:"continuation_count"`
	Pct                int   `json:"pct"`
	TurnTokens         int   `json:"turn_tokens"`
	Budget             int   `json:"budget"`
	DiminishingReturns bool  `json:"diminishing_returns"`
	DurationMs         int64 `json:"duration_ms"`
}

// CheckTokenBudget evaluates whether the query loop should continue
// or stop based on token budget. Matches TypeScript checkTokenBudget().
func CheckTokenBudget(
	tracker *BudgetTracker,
	agentID string,
	budget int,
	globalTurnTokens int,
) TokenBudgetDecision {
	// Subagents or no/zero budget → always stop (no continuation)
	if agentID != "" || budget <= 0 {
		return TokenBudgetDecision{Action: BudgetActionStop}
	}

	turnTokens := globalTurnTokens
	pct := 0
	if budget > 0 {
		pct = int(float64(turnTokens) / float64(budget) * 100)
	}
	deltaSinceLastCheck := globalTurnTokens - tracker.LastGlobalTurnTokens

	isDiminishing := tracker.ContinuationCount >= 3 &&
		deltaSinceLastCheck < budgetDiminishingThreshold &&
		tracker.LastDeltaTokens < budgetDiminishingThreshold

	// Continue if not diminishing and below completion threshold
	if !isDiminishing && float64(turnTokens) < float64(budget)*budgetCompletionThreshold {
		tracker.ContinuationCount++
		tracker.LastDeltaTokens = deltaSinceLastCheck
		tracker.LastGlobalTurnTokens = globalTurnTokens
		return TokenBudgetDecision{
			Action:            BudgetActionContinue,
			NudgeMessage:      GetBudgetContinuationMessage(pct, turnTokens, budget),
			ContinuationCount: tracker.ContinuationCount,
			Pct:               pct,
			TurnTokens:        turnTokens,
			Budget:            budget,
		}
	}

	// Stop with completion event if we had any continuations or diminishing returns
	if isDiminishing || tracker.ContinuationCount > 0 {
		return TokenBudgetDecision{
			Action: BudgetActionStop,
			CompletionEvent: &BudgetCompletionEvent{
				ContinuationCount:  tracker.ContinuationCount,
				Pct:                pct,
				TurnTokens:         turnTokens,
				Budget:             budget,
				DiminishingReturns: isDiminishing,
				DurationMs:         time.Since(tracker.StartedAt).Milliseconds(),
			},
		}
	}

	// No continuations happened, silent stop
	return TokenBudgetDecision{Action: BudgetActionStop}
}

// GetBudgetContinuationMessage generates the nudge message for the model.
// Matches TypeScript getBudgetContinuationMessage().
func GetBudgetContinuationMessage(pct, turnTokens, budget int) string {
	return fmt.Sprintf(
		"Stopped at %d%% of token target (%s / %s). Keep working \u2014 do not summarize.",
		pct, formatNumber(turnTokens), formatNumber(budget),
	)
}

// formatNumber formats an integer with comma separators (e.g., 1,234,567).
func formatNumber(n int) string {
	if n < 0 {
		return "-" + formatNumber(-n)
	}
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		return s
	}
	var result []byte
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			result = append(result, ',')
		}
		result = append(result, byte(c))
	}
	return string(result)
}

// ---------------------------------------------------------------------------
// Legacy API (kept for backward compatibility with existing callers)
// ---------------------------------------------------------------------------

// BudgetDecision represents whether to continue or stop (legacy).
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

// CheckBudget evaluates whether the query should continue (legacy API).
func (bt *BudgetTracker) CheckBudget() BudgetDecision {
	if bt.maxTotalTokens > 0 {
		total := bt.totalInputTokens + bt.totalOutputTokens
		if total >= bt.maxTotalTokens {
			return BudgetStop
		}
	}
	if bt.isDiminishingReturns() {
		return BudgetStop
	}
	return BudgetContinue
}

func (bt *BudgetTracker) isDiminishingReturns() bool {
	const (
		minConsecutive  = 3
		minOutputTokens = 500
	)
	if len(bt.outputHistory) < minConsecutive {
		return false
	}
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
