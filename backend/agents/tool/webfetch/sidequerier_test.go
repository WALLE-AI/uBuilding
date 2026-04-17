package webfetch

import (
	"context"
	"strings"
	"testing"

	"github.com/wall-ai/ubuilding/backend/agents"
)

func TestProviderSideQuerier_StreamsTextDelta(t *testing.T) {
	call := func(_ context.Context, _ agents.CallModelParams) (<-chan agents.StreamEvent, error) {
		ch := make(chan agents.StreamEvent, 4)
		ch <- agents.StreamEvent{Type: agents.EventTextDelta, Text: "Hello "}
		ch <- agents.StreamEvent{Type: agents.EventTextDelta, Text: "world"}
		ch <- agents.StreamEvent{Type: agents.EventDone}
		close(ch)
		return ch, nil
	}
	sq := NewProviderSideQuerier(call, "any-model")
	res, err := sq.Query(context.Background(), "say hi", SideQueryOpts{SystemPrompt: "be brief"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "Hello world" {
		t.Errorf("expected 'Hello world', got %q", res.Text)
	}
}

func TestProviderSideQuerier_ForwardsPromptAndOpts(t *testing.T) {
	var seen agents.CallModelParams
	call := func(_ context.Context, params agents.CallModelParams) (<-chan agents.StreamEvent, error) {
		seen = params
		ch := make(chan agents.StreamEvent)
		close(ch)
		return ch, nil
	}
	sq := NewProviderSideQuerier(call, "default-model")
	_, err := sq.Query(context.Background(), "summarize this", SideQueryOpts{
		SystemPrompt: "you are an assistant",
		Model:        "override-model",
		MaxTokens:    1024,
	})
	if err != nil {
		t.Fatal(err)
	}
	if seen.Model != "override-model" {
		t.Errorf("expected override-model, got %q", seen.Model)
	}
	if seen.SystemPrompt != "you are an assistant" {
		t.Errorf("system prompt not forwarded")
	}
	if seen.MaxOutputTokens == nil || *seen.MaxOutputTokens != 1024 {
		t.Errorf("MaxOutputTokens not forwarded, got %v", seen.MaxOutputTokens)
	}
	if len(seen.Messages) != 1 || len(seen.Messages[0].Content) != 1 {
		t.Fatalf("expected 1 user message with 1 block, got %+v", seen.Messages)
	}
	if !strings.Contains(seen.Messages[0].Content[0].Text, "summarize this") {
		t.Errorf("prompt not forwarded")
	}
	if seen.QuerySource != "webfetch_summarize" {
		t.Errorf("expected QuerySource=webfetch_summarize, got %q", seen.QuerySource)
	}
}

func TestProviderSideQuerier_PropagatesErrorEvent(t *testing.T) {
	call := func(_ context.Context, _ agents.CallModelParams) (<-chan agents.StreamEvent, error) {
		ch := make(chan agents.StreamEvent, 2)
		ch <- agents.StreamEvent{Type: agents.EventTextDelta, Text: "partial"}
		ch <- agents.StreamEvent{Type: agents.EventError, Error: "rate limited"}
		close(ch)
		return ch, nil
	}
	sq := NewProviderSideQuerier(call, "m")
	_, err := sq.Query(context.Background(), "x", SideQueryOpts{})
	if err == nil || !strings.Contains(err.Error(), "rate limited") {
		t.Errorf("expected rate-limited error, got %v", err)
	}
}

func TestProviderSideQuerier_NilCallReturnsError(t *testing.T) {
	sq := &ProviderSideQuerier{}
	if _, err := sq.Query(context.Background(), "x", SideQueryOpts{}); err == nil {
		t.Error("expected error when call fn is nil")
	}
}
