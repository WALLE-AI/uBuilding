package provider

import (
	"context"
	"fmt"
	"math"
	"math/rand"
	"strconv"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// Retry Logic
// Maps to TypeScript retry handling in queryModelWithStreaming
//
// Provides configurable retry with exponential backoff, jitter,
// rate-limit Retry-After parsing, and overload detection.
// ---------------------------------------------------------------------------

// RetryConfig controls retry behavior for API calls.
type RetryConfig struct {
	// MaxRetries is the maximum number of retries (0 = no retries).
	MaxRetries int
	// InitialBackoff is the base backoff duration before jitter.
	InitialBackoff time.Duration
	// MaxBackoff caps the backoff duration.
	MaxBackoff time.Duration
	// BackoffMultiplier is the exponential factor (default 2.0).
	BackoffMultiplier float64
	// JitterFraction adds random jitter as a fraction of backoff (0.0-1.0).
	JitterFraction float64
}

// DefaultRetryConfig returns sensible defaults.
func DefaultRetryConfig() RetryConfig {
	return RetryConfig{
		MaxRetries:        2,
		InitialBackoff:    time.Second,
		MaxBackoff:        30 * time.Second,
		BackoffMultiplier: 2.0,
		JitterFraction:    0.1,
	}
}

// RetryClassification describes how an error should be handled.
type RetryClassification int

const (
	// RetryClassNoRetry means the error is not retryable.
	RetryClassNoRetry RetryClassification = iota
	// RetryClassRetry means the error is retryable with standard backoff.
	RetryClassRetry
	// RetryClassRateLimit means the error has a Retry-After hint.
	RetryClassRateLimit
	// RetryClassOverloaded means the API is overloaded; may trigger fallback.
	RetryClassOverloaded
	// RetryClassPromptTooLong means the prompt exceeds the context window.
	RetryClassPromptTooLong
)

// ClassifyError determines the retry classification for an error.
func ClassifyError(errMsg string) RetryClassification {
	lower := strings.ToLower(errMsg)

	switch {
	case strings.Contains(lower, "prompt is too long") ||
		strings.Contains(lower, "prompt_too_long") ||
		strings.Contains(lower, "context_length_exceeded"):
		return RetryClassPromptTooLong

	case strings.Contains(lower, "overloaded") || strings.Contains(lower, "529"):
		return RetryClassOverloaded

	case strings.Contains(lower, "rate_limit") ||
		strings.Contains(lower, "rate limit") ||
		strings.Contains(lower, "429"):
		return RetryClassRateLimit

	case strings.Contains(lower, "503") ||
		strings.Contains(lower, "500") ||
		strings.Contains(lower, "internal server error") ||
		strings.Contains(lower, "bad gateway") ||
		strings.Contains(lower, "service unavailable"):
		return RetryClassRetry

	default:
		return RetryClassNoRetry
	}
}

// ParseRetryAfter extracts a Retry-After duration from an error message
// or header value. Returns 0 if not found or unparseable.
func ParseRetryAfter(s string) time.Duration {
	// Try as seconds (integer)
	if secs, err := strconv.Atoi(strings.TrimSpace(s)); err == nil && secs > 0 {
		return time.Duration(secs) * time.Second
	}

	// Try as HTTP-date (RFC1123)
	if t, err := time.Parse(time.RFC1123, strings.TrimSpace(s)); err == nil {
		d := time.Until(t)
		if d > 0 {
			return d
		}
	}

	return 0
}

// ComputeBackoff calculates the backoff duration for a given attempt.
func ComputeBackoff(config RetryConfig, attempt int) time.Duration {
	if attempt <= 0 {
		return config.InitialBackoff
	}

	backoff := float64(config.InitialBackoff) * math.Pow(config.BackoffMultiplier, float64(attempt))
	if backoff > float64(config.MaxBackoff) {
		backoff = float64(config.MaxBackoff)
	}

	// Add jitter
	if config.JitterFraction > 0 {
		jitter := backoff * config.JitterFraction * (rand.Float64()*2 - 1) // ±jitter
		backoff += jitter
	}

	if backoff < 0 {
		backoff = float64(config.InitialBackoff)
	}

	return time.Duration(backoff)
}

// WaitForRetry blocks until the backoff period elapses or context is cancelled.
// Returns an error if context is cancelled during the wait.
func WaitForRetry(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}

// RetryableError wraps an error with retry classification metadata.
type RetryableError struct {
	Err            error
	Classification RetryClassification
	RetryAfter     time.Duration
	Attempt        int
}

func (e *RetryableError) Error() string {
	return fmt.Sprintf("retryable error (class=%d, attempt=%d): %v", e.Classification, e.Attempt, e.Err)
}

func (e *RetryableError) Unwrap() error {
	return e.Err
}

// ShouldRetry returns true if the error classification allows retry.
func (e *RetryableError) ShouldRetry() bool {
	switch e.Classification {
	case RetryClassRetry, RetryClassRateLimit, RetryClassOverloaded:
		return true
	default:
		return false
	}
}
