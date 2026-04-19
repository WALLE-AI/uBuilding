// Package compact — fork-based summarizer adapter.
//
// Task D18 · drive the AutoCompactor's LLM summarization pass through
// agents.RunForkedAgent so the summarization request reuses the parent's
// prompt-cache prefix (system prompt + earliest history). Mirrors the TS
// summarization fork in src/compact/compact.ts.
//
// Integration pattern:
//
//	auto := compact.NewAutoCompactor(compact.NewForkCompactCallModel())
//
// Under the hood NewForkCompactCallModel returns a function with the
// AutoCompactor.CallModel signature. Each invocation:
//
//  1. Pulls the latest `agents.CacheSafeParams` (saved by the engine after
//     every main-loop response).
//  2. Wraps the summarization prompt in a fork user message.
//  3. Calls `agents.RunForkedAgent`; the registered ForkRunner drives a
//     short-lived child QueryEngine that shares the parent's cached prompt.
//  4. Bridges the fork's final text into a `<-chan agents.StreamEvent`
//     shaped exactly like the legacy direct-call path so AutoCompactor
//     can consume it unchanged.
//
// A nil CacheSafeParams (no prior main-loop response) falls back through
// the optional fallback CallModel when provided; otherwise the call
// returns an explicit "fork path unavailable" error so the caller can
// degrade gracefully (AutoCompactor already handles this by skipping
// compaction for that turn).
package compact

import (
	"context"
	"errors"
	"fmt"

	"github.com/wall-ai/ubuilding/backend/agents"
)

// ForkSummarizerCallModel matches the signature of AutoCompactor.CallModel
// (and SessionMemoryCompactor.CallModel). Hosts install this as the
// summarizer by passing it to the relevant compactor constructor.
type ForkSummarizerCallModel func(ctx context.Context, params agents.CallModelParams) (<-chan agents.StreamEvent, error)

// ForkSummarizerOptions tunes the fork-backed summarizer.
type ForkSummarizerOptions struct {
	// MaxTurns caps the fork child's turn count. 0 defaults to 2 (one
	// request → one summary response — summarization never needs more).
	MaxTurns int

	// Fallback is used when RunForkedAgent is unavailable (no runner
	// registered) OR when no CacheSafeParams snapshot has been captured
	// yet. When nil, the summarizer returns an error so the compactor
	// skips this pass.
	Fallback ForkSummarizerCallModel

	// QuerySource tags the fork for analytics. Defaults to "compact".
	QuerySource string
}

// NewForkCompactCallModel returns a CallModel function that routes every
// summarization request through agents.RunForkedAgent. Safe to register as
// AutoCompactor.CallModel.
func NewForkCompactCallModel(opts ForkSummarizerOptions) ForkSummarizerCallModel {
	if opts.MaxTurns <= 0 {
		opts.MaxTurns = 2
	}
	if opts.QuerySource == "" {
		opts.QuerySource = "compact"
	}

	return func(ctx context.Context, params agents.CallModelParams) (<-chan agents.StreamEvent, error) {
		cacheSafe := agents.GetLastCacheSafeParams()
		if cacheSafe == nil || !agents.HasForkRunner() {
			if opts.Fallback != nil {
				return opts.Fallback(ctx, params)
			}
			return nil, errors.New("fork summarizer unavailable: no cache-safe params or fork runner")
		}

		// The summarization request is already a fully-formed
		// CallModelParams — convert its Messages into the fork prompt
		// block. AutoCompactor uses a single user message.
		if len(params.Messages) == 0 {
			return nil, errors.New("fork summarizer: empty params.Messages")
		}

		forkParams := agents.ForkedAgentParams{
			PromptMessages:  params.Messages,
			CacheSafeParams: *cacheSafe,
			MaxTurns:        opts.MaxTurns,
			QuerySource:     opts.QuerySource,
			SkipTranscript:  true, // summarization shouldn't pollute sidechain
			SkipCacheWrite:  false,
		}

		res, err := agents.RunForkedAgent(ctx, forkParams)
		if err != nil {
			return nil, fmt.Errorf("fork summarizer: %w", err)
		}
		if res == nil {
			return nil, errors.New("fork summarizer: runner returned nil result")
		}

		return bridgeFinalTextToStream(res.FinalText, res.Messages), nil
	}
}

// bridgeFinalTextToStream emits a single EventAssistant then closes the
// channel — enough for AutoCompactor.callForSummary's consumption loop
// (which reads event.Message.Content for assistant events).
func bridgeFinalTextToStream(finalText string, _ []agents.Message) <-chan agents.StreamEvent {
	ch := make(chan agents.StreamEvent, 1)
	go func() {
		defer close(ch)
		if finalText == "" {
			return
		}
		ch <- agents.StreamEvent{
			Type: agents.EventAssistant,
			Message: &agents.Message{
				Type: agents.MessageTypeAssistant,
				Content: []agents.ContentBlock{
					{Type: agents.ContentBlockText, Text: finalText},
				},
			},
		}
	}()
	return ch
}

// ---------------------------------------------------------------------------
// Convenience wiring
// ---------------------------------------------------------------------------

// EnableForkSummarizerForCompactor attaches a fork-backed CallModel to the
// supplied AutoCompactor. Useful in engine bootstrap: after the fork
// runner is wired (e.g. via agents.EnableEngineDrivenForkRunner), call
// EnableForkSummarizerForCompactor(autoCompactor, opts) to upgrade the
// summarization path. When AutoCompactor already has a CallModel set, it
// becomes the fallback automatically.
func EnableForkSummarizerForCompactor(ac *AutoCompactor, opts ForkSummarizerOptions) {
	if ac == nil {
		return
	}
	if opts.Fallback == nil && ac.CallModel != nil {
		opts.Fallback = ForkSummarizerCallModel(ac.CallModel)
	}
	ac.CallModel = NewForkCompactCallModel(opts)
}
