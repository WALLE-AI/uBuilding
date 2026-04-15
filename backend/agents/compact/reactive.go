package compact

import (
	"context"

	"github.com/wall-ai/ubuilding/backend/agents"
)

// ---------------------------------------------------------------------------
// ReactiveCompactor — emergency compaction on prompt-too-long errors
// Maps to TypeScript tryReactiveCompact() in query.ts
// ---------------------------------------------------------------------------

// ReactiveCompactor performs emergency compaction when the API returns a
// prompt_too_long error. It forces an auto-compact pass regardless of
// threshold to recover from the error and allow the query to continue.
type ReactiveCompactor struct {
	auto *AutoCompactor
}

// NewReactiveCompactor creates a new ReactiveCompactor backed by an AutoCompactor.
func NewReactiveCompactor(auto *AutoCompactor) *ReactiveCompactor {
	return &ReactiveCompactor{auto: auto}
}

// TryReactiveCompact attempts emergency compaction. The hasAttempted guard
// prevents infinite retry loops — each query iteration may only attempt
// reactive compaction once.
//
// Returns the compacted result if successful, or nil if compaction was not
// applied (e.g., too few messages, circuit breaker, or LLM failure).
func (r *ReactiveCompactor) TryReactiveCompact(
	ctx context.Context,
	messages []agents.Message,
	systemPrompt string,
	querySource string,
	tracking *AutoCompactTrackingState,
	hasAttempted bool,
) *agents.AutocompactResult {
	// Guard: only attempt once per iteration to prevent infinite loops
	if hasAttempted {
		return nil
	}

	if len(messages) < 3 {
		return nil
	}

	// Force compaction regardless of threshold
	result := r.auto.CompactWithTracking(ctx, messages, systemPrompt, querySource, tracking, true)
	if result == nil || !result.Applied {
		// Fallback: try snip compaction as a last resort
		return r.trySnipFallback(messages, systemPrompt)
	}

	// Tag the metadata as reactive-triggered
	if result.Metadata != nil {
		result.Metadata.Trigger = "reactive"
	}

	return result
}

// trySnipFallback applies snip compaction as a last resort when LLM-based
// compaction fails during reactive compact. Snip compaction is purely local
// and doesn't require an API call.
func (r *ReactiveCompactor) trySnipFallback(
	messages []agents.Message,
	systemPrompt string,
) *agents.AutocompactResult {
	snip := &SnipCompactor{
		PreserveCount:    6, // keep fewer messages in emergency
		MaxHistoryTokens: 1, // force snip to always trigger
	}

	snipResult := snip.SnipIfNeeded(messages)
	if snipResult == nil || snipResult.TokensFreed == 0 {
		return nil
	}

	return &agents.AutocompactResult{
		Messages:    snipResult.Messages,
		TokensSaved: snipResult.TokensFreed,
		Applied:     true,
		Summary:     "[Reactive snip compaction — oldest messages removed to fit context window]",
		Metadata: &agents.CompactMetadata{
			Trigger: "reactive_snip",
		},
	}
}

// TryPartialReactiveCompact attempts partial compaction — summarizing only
// the oldest portion of the conversation while keeping recent messages intact.
// This preserves more context than full compaction.
func (r *ReactiveCompactor) TryPartialReactiveCompact(
	ctx context.Context,
	messages []agents.Message,
	systemPrompt string,
	querySource string,
	tracking *AutoCompactTrackingState,
	keepRecentGroups int,
) *agents.AutocompactResult {
	if len(messages) < 6 {
		return nil
	}

	groups := GroupMessagesByAPIRound(messages)
	if len(groups) <= keepRecentGroups {
		return nil
	}

	// Split: summarize older groups, keep recent ones
	prefix, suffix := SplitAtGroup(messages, len(groups)-keepRecentGroups)
	if len(prefix) < 3 {
		return nil
	}

	// Compact only the prefix
	result := r.auto.CompactWithTracking(ctx, prefix, systemPrompt, querySource, tracking, true)
	if result == nil || !result.Applied {
		return nil
	}

	// Recombine: compacted prefix + preserved suffix
	combined := make([]agents.Message, 0, len(result.Messages)+len(suffix))
	combined = append(combined, result.Messages...)
	combined = append(combined, suffix...)

	result.Messages = combined
	if result.Metadata != nil {
		result.Metadata.Trigger = "reactive_partial"
	}

	return result
}
