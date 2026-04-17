package webfetch

import (
	"context"
	"fmt"
	"strings"

	"github.com/wall-ai/ubuilding/backend/agents"
)

// ---------------------------------------------------------------------------
// ProviderSideQuerier — an adapter that plugs any agents.CallModel function
// into the WebFetch SideQuerier contract. Maps to claude-code-main's pattern
// of re-using the primary provider for the WebFetchTool summarization call
// (instead of hard-coding a Haiku/mini-model preset).
// ---------------------------------------------------------------------------

// CallModelFn matches agents.QueryDeps.CallModel. Used to decouple this file
// from the full QueryDeps interface.
type CallModelFn func(ctx context.Context, params agents.CallModelParams) (<-chan agents.StreamEvent, error)

// ProviderSideQuerier adapts a provider CallModel function to the SideQuerier
// interface consumed by WebFetchTool.
type ProviderSideQuerier struct {
	call  CallModelFn
	model string // passed through to CallModelParams.Model (may be "")
}

// NewProviderSideQuerier constructs an adapter. The model argument is
// informational — the underlying deps/provider ultimately picks the actual
// model. Pass "" to rely on the deps default.
func NewProviderSideQuerier(call CallModelFn, model string) *ProviderSideQuerier {
	return &ProviderSideQuerier{call: call, model: model}
}

// Query implements webfetch.SideQuerier. It feeds the prompt to the model as
// a single user message, prefixed with a system prompt, and collects all
// text_delta events until the stream closes.
func (p *ProviderSideQuerier) Query(ctx context.Context, prompt string, opts SideQueryOpts) (*SideQueryResult, error) {
	if p == nil || p.call == nil {
		return nil, fmt.Errorf("ProviderSideQuerier: no CallModelFn configured")
	}
	sys := opts.SystemPrompt
	model := opts.Model
	if model == "" {
		model = p.model
	}

	params := agents.CallModelParams{
		Messages: []agents.Message{{
			Type:    agents.MessageTypeUser,
			Content: []agents.ContentBlock{{Type: agents.ContentBlockText, Text: prompt}},
		}},
		SystemPrompt: sys,
		Model:        model,
		QuerySource:  "webfetch_summarize",
	}
	if opts.MaxTokens > 0 {
		mt := opts.MaxTokens
		params.MaxOutputTokens = &mt
	}

	eventCh, err := p.call(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("ProviderSideQuerier: call failed: %w", err)
	}

	var sb strings.Builder
	for ev := range eventCh {
		switch ev.Type {
		case agents.EventTextDelta:
			sb.WriteString(ev.Text)
		case agents.EventError:
			if ev.Error != "" {
				return nil, fmt.Errorf("ProviderSideQuerier: stream error: %s", ev.Error)
			}
		}
	}
	return &SideQueryResult{Text: sb.String()}, nil
}

// Compile-time assertion.
var _ SideQuerier = (*ProviderSideQuerier)(nil)
