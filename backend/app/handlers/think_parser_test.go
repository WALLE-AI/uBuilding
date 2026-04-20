package handlers

import (
	"strings"
	"testing"
)

// collect feeds each token one by one and collects all chunks.
func collect(tokens ...string) []parsedChunk {
	var p thinkParser
	var out []parsedChunk
	for _, tok := range tokens {
		out = append(out, p.Feed(tok)...)
	}
	return out
}

// joinByType joins chunks of same type into a single string per type (for easier assertion).
func joinText(chunks []parsedChunk) string {
	var b strings.Builder
	for _, c := range chunks {
		if !c.isThink {
			b.WriteString(c.text)
		}
	}
	return b.String()
}

func joinThink(chunks []parsedChunk) string {
	var b strings.Builder
	for _, c := range chunks {
		if c.isThink {
			b.WriteString(c.text)
		}
	}
	return b.String()
}

func TestNoThinkTag(t *testing.T) {
	chunks := collect("Hello, world!")
	if joinText(chunks) != "Hello, world!" {
		t.Errorf("unexpected text: %q", joinText(chunks))
	}
	if joinThink(chunks) != "" {
		t.Errorf("expected no thinking, got %q", joinThink(chunks))
	}
}

func TestSingleTokenFullTag(t *testing.T) {
	chunks := collect("<think>some reasoning</think>answer")
	if got := joinThink(chunks); got != "some reasoning" {
		t.Errorf("thinking: got %q, want %q", got, "some reasoning")
	}
	if got := joinText(chunks); got != "answer" {
		t.Errorf("text: got %q, want %q", got, "answer")
	}
}

func TestTagSplitAcrossTokens(t *testing.T) {
	// "<think>" split as "<thi" + "nk>"
	chunks := collect("before <thi", "nk>inside</thi", "nk>after")
	if got := joinText(chunks); got != "before after" {
		t.Errorf("text: got %q, want %q", got, "before after")
	}
	if got := joinThink(chunks); got != "inside" {
		t.Errorf("thinking: got %q, want %q", got, "inside")
	}
}

func TestMultipleThinkBlocks(t *testing.T) {
	chunks := collect("<think>r1</think>mid<think>r2</think>end")
	if got := joinThink(chunks); got != "r1r2" {
		t.Errorf("thinking: got %q, want %q", got, "r1r2")
	}
	if got := joinText(chunks); got != "midend" {
		t.Errorf("text: got %q, want %q", got, "midend")
	}
}

func TestOnlyThinkTag(t *testing.T) {
	chunks := collect("<think>only thinking</think>")
	if got := joinThink(chunks); got != "only thinking" {
		t.Errorf("thinking: got %q, want %q", got, "only thinking")
	}
	if got := joinText(chunks); got != "" {
		t.Errorf("text: got %q, want %q", got, "")
	}
}

func TestPartialOpenTagAtEnd(t *testing.T) {
	// Token boundary: "<" arrives alone
	chunks := collect("hello <", "think>thinking</think>world")
	if got := joinText(chunks); got != "hello world" {
		t.Errorf("text: got %q, want %q", got, "hello world")
	}
	if got := joinThink(chunks); got != "thinking" {
		t.Errorf("thinking: got %q, want %q", got, "thinking")
	}
}

func TestNewlineAroundTags(t *testing.T) {
	// Models like QwQ output newlines around the tags
	chunks := collect("<think>\nsome reasoning\n</think>\nFinal answer.")
	if got := joinThink(chunks); got != "\nsome reasoning\n" {
		t.Errorf("thinking: got %q, want %q", got, "\nsome reasoning\n")
	}
	if got := joinText(chunks); got != "\nFinal answer." {
		t.Errorf("text: got %q, want %q", got, "\nFinal answer.")
	}
}

func TestTagOnlyInStream(t *testing.T) {
	// Pure tag token
	var p thinkParser
	c1 := p.Feed("<think>")
	c2 := p.Feed("reasoning")
	c3 := p.Feed("</think>")
	var all []parsedChunk
	all = append(all, c1...)
	all = append(all, c2...)
	all = append(all, c3...)
	if got := joinThink(all); got != "reasoning" {
		t.Errorf("thinking: got %q, want %q", got, "reasoning")
	}
}
