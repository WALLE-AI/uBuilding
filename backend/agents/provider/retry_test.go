package provider

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestClassifyError_PromptTooLong(t *testing.T) {
	assert.Equal(t, RetryClassPromptTooLong, ClassifyError("prompt is too long for this model"))
	assert.Equal(t, RetryClassPromptTooLong, ClassifyError("error: prompt_too_long"))
	assert.Equal(t, RetryClassPromptTooLong, ClassifyError("context_length_exceeded"))
}

func TestClassifyError_Overloaded(t *testing.T) {
	assert.Equal(t, RetryClassOverloaded, ClassifyError("API is overloaded"))
	assert.Equal(t, RetryClassOverloaded, ClassifyError("status 529"))
}

func TestClassifyError_RateLimit(t *testing.T) {
	assert.Equal(t, RetryClassRateLimit, ClassifyError("rate_limit_error"))
	assert.Equal(t, RetryClassRateLimit, ClassifyError("Rate limit exceeded"))
	assert.Equal(t, RetryClassRateLimit, ClassifyError("HTTP 429"))
}

func TestClassifyError_Retryable(t *testing.T) {
	assert.Equal(t, RetryClassRetry, ClassifyError("503 service unavailable"))
	assert.Equal(t, RetryClassRetry, ClassifyError("500 internal server error"))
}

func TestClassifyError_NoRetry(t *testing.T) {
	assert.Equal(t, RetryClassNoRetry, ClassifyError("invalid API key"))
	assert.Equal(t, RetryClassNoRetry, ClassifyError("unknown error"))
}

func TestParseRetryAfter_Seconds(t *testing.T) {
	d := ParseRetryAfter("5")
	assert.Equal(t, 5*time.Second, d)
}

func TestParseRetryAfter_Zero(t *testing.T) {
	d := ParseRetryAfter("not a number")
	assert.Equal(t, time.Duration(0), d)
}

func TestParseRetryAfter_HTTPDate(t *testing.T) {
	future := time.Now().Add(10 * time.Second).UTC().Format(time.RFC1123)
	d := ParseRetryAfter(future)
	assert.True(t, d > 0 && d <= 11*time.Second)
}

func TestComputeBackoff(t *testing.T) {
	cfg := RetryConfig{
		MaxRetries:        3,
		InitialBackoff:    time.Second,
		MaxBackoff:        10 * time.Second,
		BackoffMultiplier: 2.0,
		JitterFraction:    0.0, // no jitter for deterministic test
	}

	// attempt 0: 1s
	assert.Equal(t, time.Second, ComputeBackoff(cfg, 0))
	// attempt 1: 2s
	assert.Equal(t, 2*time.Second, ComputeBackoff(cfg, 1))
	// attempt 2: 4s
	assert.Equal(t, 4*time.Second, ComputeBackoff(cfg, 2))
	// attempt 3: 8s
	assert.Equal(t, 8*time.Second, ComputeBackoff(cfg, 3))
	// attempt 4: capped at 10s
	assert.Equal(t, 10*time.Second, ComputeBackoff(cfg, 4))
}

func TestComputeBackoff_WithJitter(t *testing.T) {
	cfg := RetryConfig{
		MaxRetries:        3,
		InitialBackoff:    time.Second,
		MaxBackoff:        30 * time.Second,
		BackoffMultiplier: 2.0,
		JitterFraction:    0.2,
	}

	// With jitter, result should be within ±20% of 2s for attempt 1
	for i := 0; i < 100; i++ {
		d := ComputeBackoff(cfg, 1)
		assert.True(t, d >= time.Duration(float64(2*time.Second)*0.8))
		assert.True(t, d <= time.Duration(float64(2*time.Second)*1.2))
	}
}

func TestWaitForRetry_Cancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := WaitForRetry(ctx, time.Second)
	assert.Error(t, err)
}

func TestWaitForRetry_ZeroDuration(t *testing.T) {
	err := WaitForRetry(context.Background(), 0)
	assert.NoError(t, err)
}

func TestRetryableError(t *testing.T) {
	re := &RetryableError{
		Err:            assert.AnError,
		Classification: RetryClassRateLimit,
		Attempt:        1,
	}
	assert.True(t, re.ShouldRetry())
	assert.Contains(t, re.Error(), "retryable error")

	re2 := &RetryableError{
		Err:            assert.AnError,
		Classification: RetryClassNoRetry,
	}
	assert.False(t, re2.ShouldRetry())

	re3 := &RetryableError{
		Err:            assert.AnError,
		Classification: RetryClassPromptTooLong,
	}
	assert.False(t, re3.ShouldRetry())
}

func TestIsPromptTooLongError(t *testing.T) {
	assert.True(t, IsPromptTooLongError("prompt is too long"))
	assert.True(t, IsPromptTooLongError("prompt_too_long"))
	assert.True(t, IsPromptTooLongError("context_length_exceeded"))
	assert.False(t, IsPromptTooLongError("rate limit"))
}
