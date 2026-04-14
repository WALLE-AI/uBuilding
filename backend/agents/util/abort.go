package util

import "context"

// CreateChildContext creates a child context from a parent, returning the
// child context and its cancel function. This maps to TypeScript's
// createChildAbortController pattern.
func CreateChildContext(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithCancel(parent)
}

// IsAborted checks if the given context has been cancelled.
func IsAborted(ctx context.Context) bool {
	select {
	case <-ctx.Done():
		return true
	default:
		return false
	}
}
