package util

import (
	"encoding/json"
	"unicode/utf8"
)

// ---------------------------------------------------------------------------
// Token Estimation
// Maps to TypeScript utils/tokenEstimation.ts
//
// Provides rough token count estimation without requiring a tokenizer.
// Uses character-based heuristics calibrated against Claude's tokenizer:
//   - English text: ~4 chars per token
//   - CJK text: ~1.5 chars per token
//   - Code: ~3.5 chars per token
//   - Mixed: weighted average
//
// The estimateMessageTokens in microCompact.ts pads by 4/3 for safety.
// ---------------------------------------------------------------------------

// CharsPerToken is the default rough ratio for English/code text.
const CharsPerToken = 4

// ImageMaxTokenSize is the approximate token budget for images/documents.
const ImageMaxTokenSize = 2000

// RoughTokenCount estimates token count for a string using the 4 chars/token heuristic.
func RoughTokenCount(s string) int {
	if s == "" {
		return 0
	}
	return (len(s) + CharsPerToken - 1) / CharsPerToken
}

// RoughTokenCountUTF8 estimates token count with CJK awareness.
// CJK characters use ~1.5 chars/token, Latin ~4 chars/token.
func RoughTokenCountUTF8(s string) int {
	if s == "" {
		return 0
	}

	cjkChars := 0
	latinChars := 0

	for _, r := range s {
		if isCJK(r) {
			cjkChars++
		} else {
			latinChars++
		}
	}

	// CJK: ~1.5 chars/token, Latin: ~4 chars/token
	cjkTokens := float64(cjkChars) / 1.5
	latinTokens := float64(latinChars) / 4.0

	total := int(cjkTokens + latinTokens)
	if total == 0 && utf8.RuneCountInString(s) > 0 {
		return 1
	}
	return total
}

// isCJK returns true for CJK Unified Ideographs and common CJK ranges.
func isCJK(r rune) bool {
	return (r >= 0x4E00 && r <= 0x9FFF) || // CJK Unified Ideographs
		(r >= 0x3400 && r <= 0x4DBF) || // CJK Unified Ideographs Extension A
		(r >= 0x20000 && r <= 0x2A6DF) || // CJK Unified Ideographs Extension B
		(r >= 0x3000 && r <= 0x303F) || // CJK Symbols and Punctuation
		(r >= 0x3040 && r <= 0x309F) || // Hiragana
		(r >= 0x30A0 && r <= 0x30FF) || // Katakana
		(r >= 0xAC00 && r <= 0xD7AF) // Hangul Syllables
}

// EstimateJSONTokens estimates tokens for a JSON-serializable value.
func EstimateJSONTokens(v interface{}) int {
	data, err := json.Marshal(v)
	if err != nil {
		return 0
	}
	return RoughTokenCount(string(data))
}

// PadEstimate applies a conservative padding factor (4/3) to a token estimate.
// This matches TypeScript's estimateMessageTokens padding.
func PadEstimate(tokens int) int {
	return (tokens * 4) / 3
}

// ClampTokens ensures a token count stays within [min, max].
func ClampTokens(tokens, min, max int) int {
	if tokens < min {
		return min
	}
	if tokens > max {
		return max
	}
	return tokens
}

// EstimateContextUsage computes rough context utilization as a fraction [0.0, 1.0].
func EstimateContextUsage(usedTokens, maxContextTokens int) float64 {
	if maxContextTokens <= 0 {
		return 0
	}
	ratio := float64(usedTokens) / float64(maxContextTokens)
	if ratio > 1.0 {
		return 1.0
	}
	return ratio
}
