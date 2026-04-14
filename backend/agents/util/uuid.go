package util

import "github.com/google/uuid"

// NewUUID generates a new random UUID string.
func NewUUID() string {
	return uuid.New().String()
}
