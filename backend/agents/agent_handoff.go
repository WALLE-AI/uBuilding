// Package agents — handoff classification stub.
//
// Task E06 · interface slot for the claude-code handoff classifier. The
// real TypeScript implementation runs an LLM pass to decide whether a
// sub-agent's outbound message should be:
//
//   - ""                 — forwarded verbatim
//   - "warning"          — forwarded with a banner warning the user
//   - "security_block"   — blocked outright
//
// Porting the classifier is beyond Wave 3's scope (it depends on a model
// + prompt template we haven't brought over). This file supplies the
// stable Go API so host code can start calling it and we can drop a real
// implementation in later without churning callers.
package agents

import (
	"context"
	"sync/atomic"
)

// HandoffDecision enumerates the three possible outcomes of
// ClassifyHandoffIfNeeded. Defined as a string so host code can log it
// and so JSON encoding stays stable.
type HandoffDecision string

const (
	// HandoffDecisionPass means the message is safe to forward unmodified.
	HandoffDecisionPass HandoffDecision = ""

	// HandoffDecisionWarning means the message should be forwarded with a
	// user-visible warning banner.
	HandoffDecisionWarning HandoffDecision = "warning"

	// HandoffDecisionSecurityBlock means the message must NOT be forwarded;
	// hosts should replace it with a block notice.
	HandoffDecisionSecurityBlock HandoffDecision = "security_block"
)

// HandoffRequest describes the outbound message under review.
type HandoffRequest struct {
	// FromAgentID identifies the sub-agent sending the message.
	FromAgentID string

	// FromAgentType is the agent type (e.g. "Explore", "Plan").
	FromAgentType string

	// ToAgentID is the intended recipient (empty = broadcast / parent).
	ToAgentID string

	// Message is the message text being handed off.
	Message string

	// Transcript carries the conversation context a real classifier would
	// inspect. Optional — the stub ignores it.
	Transcript []Message
}

// HandoffClassifier is the function signature a real classifier must
// implement. It returns a decision plus an optional reason string that
// hosts can surface in the warning/block banner.
type HandoffClassifier func(ctx context.Context, req HandoffRequest) (HandoffDecision, string, error)

// ---------------------------------------------------------------------------
// Package-level pluggable classifier.
// ---------------------------------------------------------------------------

// classifierCell wraps an atomic.Value so the classifier can be swapped
// at runtime (tests use this to install a fake classifier).
type classifierCell struct{ v atomic.Value }

var activeHandoffClassifier classifierCell

// RegisterHandoffClassifier installs a classifier. Pass nil to restore the
// default pass-through behaviour. Returns the previously installed
// classifier (nil if none) so callers can restore it in defers.
func RegisterHandoffClassifier(fn HandoffClassifier) HandoffClassifier {
	prev := loadClassifier()
	if fn == nil {
		activeHandoffClassifier.v.Store((*classifierHolder)(nil))
	} else {
		activeHandoffClassifier.v.Store(&classifierHolder{fn: fn})
	}
	return prev
}

// HasHandoffClassifier reports whether a non-default classifier is
// registered. Useful for conditional logging.
func HasHandoffClassifier() bool { return loadClassifier() != nil }

// ClassifyHandoffIfNeeded runs the installed classifier (or the default
// pass-through) against req. Returns (decision, reason, error).
// Never returns a non-nil error from the default classifier — only a
// host-supplied classifier can surface errors.
func ClassifyHandoffIfNeeded(ctx context.Context, req HandoffRequest) (HandoffDecision, string, error) {
	if fn := loadClassifier(); fn != nil {
		return fn(ctx, req)
	}
	return HandoffDecisionPass, "", nil
}

// classifierHolder lets atomic.Value hold a function value without type
// punning issues.
type classifierHolder struct{ fn HandoffClassifier }

func loadClassifier() HandoffClassifier {
	v := activeHandoffClassifier.v.Load()
	if v == nil {
		return nil
	}
	h, _ := v.(*classifierHolder)
	if h == nil {
		return nil
	}
	return h.fn
}
