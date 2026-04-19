// Package agents — fork analytics.
//
// Task E05 · emit a structured event after every fork completes so hosts
// can track prompt-cache hit rate, chain depth, and usage. Mirrors
// src/tools/AgentTool/forkSubagent.ts::logForkAgentQueryEvent.
//
// The emitter is a pluggable function; hosts register their telemetry
// sink via RegisterForkAnalytics. The default is a no-op.
package agents

import (
	"sync/atomic"
	"time"
)

// ForkAnalyticsEvent captures the metrics a host usually wants after a
// fork completes. Every field is optional — the emitter must tolerate
// zero-valued fields when the runner couldn't supply them.
type ForkAnalyticsEvent struct {
	// Timestamp is when the event was emitted (UTC).
	Timestamp time.Time

	// ChainID + Depth come from QueryChainTracking so hosts can reconstruct
	// the parent→fork lineage.
	ChainID string
	Depth   int

	// AgentType is the agent definition driving the fork (e.g. "Explore").
	AgentType string

	// SessionID is the forked engine's session id (= fork task id).
	SessionID string

	// DurationMs is the wall-clock time the fork ran for.
	DurationMs int64

	// Usage is the aggregate token usage reported by the fork.
	Usage Usage

	// CacheHitRate is the ratio cache_read / (cache_read + cache_creation)
	// in the reported usage. Zero when neither is populated.
	CacheHitRate float64

	// MaxTurnsReached records whether the fork terminated because it hit
	// the turn limit (as opposed to a natural end-of-turn).
	MaxTurnsReached bool

	// Error, when non-empty, means the fork failed.
	Error string
}

// ForkAnalyticsEmitter is the host-supplied sink.
type ForkAnalyticsEmitter func(event ForkAnalyticsEvent)

var forkAnalyticsEmitter atomic.Value // stores *forkAnalyticsHolder

type forkAnalyticsHolder struct{ fn ForkAnalyticsEmitter }

// RegisterForkAnalytics installs or clears the fork analytics emitter.
// Returns the previously installed emitter so callers can restore it in
// defers.
func RegisterForkAnalytics(fn ForkAnalyticsEmitter) ForkAnalyticsEmitter {
	prev := loadForkAnalytics()
	if fn == nil {
		forkAnalyticsEmitter.Store((*forkAnalyticsHolder)(nil))
	} else {
		forkAnalyticsEmitter.Store(&forkAnalyticsHolder{fn: fn})
	}
	return prev
}

// HasForkAnalytics reports whether a non-default emitter is registered.
func HasForkAnalytics() bool { return loadForkAnalytics() != nil }

// LogForkAgentQueryEvent forwards event to the registered emitter. Fills
// Timestamp and CacheHitRate when the caller left them zero. Safe to
// call from any goroutine; a missing emitter is a no-op.
func LogForkAgentQueryEvent(event ForkAnalyticsEvent) {
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	}
	if event.CacheHitRate == 0 {
		event.CacheHitRate = computeCacheHitRate(event.Usage)
	}
	if fn := loadForkAnalytics(); fn != nil {
		fn(event)
	}
}

// computeCacheHitRate returns cache_read / (cache_read + cache_creation)
// (≈ 0 when the denominator is zero).
func computeCacheHitRate(u Usage) float64 {
	read := float64(u.CacheReadInputTokens)
	create := float64(u.CacheCreationInputTokens)
	total := read + create
	if total == 0 {
		return 0
	}
	return read / total
}

func loadForkAnalytics() ForkAnalyticsEmitter {
	v := forkAnalyticsEmitter.Load()
	if v == nil {
		return nil
	}
	h, _ := v.(*forkAnalyticsHolder)
	if h == nil {
		return nil
	}
	return h.fn
}
