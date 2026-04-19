package agents

import (
	"testing"
	"time"
)

func TestLogForkAgentQueryEvent_NoEmitter_Noop(t *testing.T) {
	RegisterForkAnalytics(nil) // ensure clean state
	if HasForkAnalytics() {
		t.Fatal("no emitter should be registered")
	}
	// Must not panic when no emitter is installed.
	LogForkAgentQueryEvent(ForkAnalyticsEvent{})
}

func TestLogForkAgentQueryEvent_FillsDefaults(t *testing.T) {
	var got ForkAnalyticsEvent
	prev := RegisterForkAnalytics(func(e ForkAnalyticsEvent) { got = e })
	t.Cleanup(func() { RegisterForkAnalytics(prev) })

	LogForkAgentQueryEvent(ForkAnalyticsEvent{
		ChainID:   "chain-1",
		Depth:     2,
		AgentType: "Plan",
		Usage:     Usage{CacheReadInputTokens: 80, CacheCreationInputTokens: 20},
	})

	if got.Timestamp.IsZero() {
		t.Fatal("Timestamp should be auto-populated")
	}
	if got.ChainID != "chain-1" || got.Depth != 2 || got.AgentType != "Plan" {
		t.Fatalf("fields lost: %+v", got)
	}
	// 80 / (80 + 20) = 0.8
	if got.CacheHitRate < 0.79 || got.CacheHitRate > 0.81 {
		t.Fatalf("CacheHitRate = %v; want ~0.8", got.CacheHitRate)
	}
}

func TestLogForkAgentQueryEvent_ZeroUsageHitRateZero(t *testing.T) {
	var got ForkAnalyticsEvent
	prev := RegisterForkAnalytics(func(e ForkAnalyticsEvent) { got = e })
	t.Cleanup(func() { RegisterForkAnalytics(prev) })

	LogForkAgentQueryEvent(ForkAnalyticsEvent{})
	if got.CacheHitRate != 0 {
		t.Fatalf("CacheHitRate with zero usage = %v", got.CacheHitRate)
	}
}

func TestLogForkAgentQueryEvent_PreserveCallerTimestamp(t *testing.T) {
	var got ForkAnalyticsEvent
	prev := RegisterForkAnalytics(func(e ForkAnalyticsEvent) { got = e })
	t.Cleanup(func() { RegisterForkAnalytics(prev) })

	ts := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	LogForkAgentQueryEvent(ForkAnalyticsEvent{Timestamp: ts})
	if !got.Timestamp.Equal(ts) {
		t.Fatalf("caller timestamp overwritten: %v vs %v", got.Timestamp, ts)
	}
}

func TestRegisterForkAnalytics_ReturnsPrevious(t *testing.T) {
	RegisterForkAnalytics(nil)
	first := ForkAnalyticsEmitter(func(ForkAnalyticsEvent) {})
	RegisterForkAnalytics(first)
	prev := RegisterForkAnalytics(nil)
	if prev == nil {
		t.Fatal("prev should be non-nil after registering first")
	}
	if HasForkAnalytics() {
		t.Fatal("should be cleared")
	}
	RegisterForkAnalytics(prev)
	if !HasForkAnalytics() {
		t.Fatal("restore failed")
	}
	RegisterForkAnalytics(nil)
}
