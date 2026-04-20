package handlers

import "strings"

// parsedChunk is a segment of streamed text classified as normal or thinking.
type parsedChunk struct {
	text    string
	isThink bool
}

// thinkParser splits a streamed text into normal and <think>...</think> segments.
// It handles tags that span across multiple token boundaries by maintaining a
// partial-tag buffer between Feed calls.
type thinkParser struct {
	thinking bool   // currently inside a <think> block
	buf      string // partial tag accumulation (< len("<think>") chars)
}

const (
	openTag  = "<think>"
	closeTag = "</think>"
)

// Feed processes the next text chunk and returns zero or more classified segments.
// Segments of the same type that are adjacent are merged automatically.
func (p *thinkParser) Feed(input string) []parsedChunk {
	var out []parsedChunk

	emit := func(text string, think bool) {
		if text == "" {
			return
		}
		if len(out) > 0 && out[len(out)-1].isThink == think {
			out[len(out)-1].text += text
			return
		}
		out = append(out, parsedChunk{text: text, isThink: think})
	}

	// Prepend any leftover partial-tag buffer from the previous call.
	s := p.buf + input
	p.buf = ""

	for len(s) > 0 {
		if !p.thinking {
			// ── Normal mode: scan for <think> ──────────────────────────────
			idx := strings.Index(s, openTag)
			if idx == -1 {
				// No complete open tag found. Check whether the tail of s could
				// be the start of <think> (i.e. a partial tag spanning the next token).
				if safe := safeLen(s, openTag); safe < len(s) {
					emit(s[:safe], false)
					p.buf = s[safe:]
					return out
				}
				emit(s, false)
				return out
			}
			// Emit text before the tag, then switch to thinking mode.
			emit(s[:idx], false)
			s = s[idx+len(openTag):]
			p.thinking = true

		} else {
			// ── Thinking mode: scan for </think> ───────────────────────────
			idx := strings.Index(s, closeTag)
			if idx == -1 {
				if safe := safeLen(s, closeTag); safe < len(s) {
					emit(s[:safe], true)
					p.buf = s[safe:]
					return out
				}
				emit(s, true)
				return out
			}
			// Emit thinking content before the close tag, then resume normal mode.
			emit(s[:idx], true)
			s = s[idx+len(closeTag):]
			p.thinking = false
		}
	}
	return out
}

// safeLen returns the length of the prefix of s that is safe to emit immediately,
// i.e. not the start of a partial occurrence of tag at the very end of s.
// If the last n characters of s match a prefix of tag (for any n ≥ 1), those
// characters are held back. Otherwise safeLen == len(s) (nothing held back).
func safeLen(s, tag string) int {
	maxCheck := len(tag) - 1
	if maxCheck > len(s) {
		maxCheck = len(s)
	}
	for n := maxCheck; n >= 1; n-- {
		if strings.HasPrefix(tag, s[len(s)-n:]) {
			return len(s) - n
		}
	}
	return len(s)
}
