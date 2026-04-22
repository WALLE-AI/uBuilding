package memory

import "time"

// ---------------------------------------------------------------------------
// D4.I1 · Team memory sync types — Go equivalents of the Zod schemas
// in `src/services/teamMemorySync/types.ts`.
//
// These types model the server API payloads for team memory
// push/fetch operations. They are kept intentionally slim for
// local-first usage; the full Zod validation is replaced by
// struct-level zero-value checks.
// ---------------------------------------------------------------------------

// TeamMemoryContentStorage represents a single file entry in the
// team memory sync payload (maps to TeamMemoryContentStorageSchema).
type TeamMemoryContentStorage struct {
	RelativePath string `json:"relativePath"`
	Content      string `json:"content"`
	Hash         string `json:"hash,omitempty"`
}

// TeamMemoryData represents the full team memory state on the server
// (maps to TeamMemoryDataSchema).
type TeamMemoryData struct {
	Files     []TeamMemoryContentStorage `json:"files"`
	UpdatedAt time.Time                  `json:"updatedAt"`
	Version   int                        `json:"version,omitempty"`
}

// TeamMemoryFetchResult is the server's response to a fetch request
// (maps to TeamMemoryFetchResult).
type TeamMemoryFetchResult struct {
	Success bool            `json:"success"`
	Data    *TeamMemoryData `json:"data,omitempty"`
	Error   *TeamSyncError  `json:"error,omitempty"`
}

// TeamMemoryPushResult is the server's response to a push request
// (maps to TeamMemoryPushResult).
type TeamMemoryPushResult struct {
	Success      bool                    `json:"success"`
	Error        *TeamSyncError          `json:"error,omitempty"`
	SkippedFiles []TeamMemorySkippedFile `json:"skippedFiles,omitempty"`
}

// TeamSyncError represents an error from the sync API.
type TeamSyncError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// TeamMemorySkippedFile identifies a file that was skipped during
// push because it contained secrets.
type TeamMemorySkippedFile struct {
	RelativePath string `json:"relativePath"`
	Reason       string `json:"reason"`
}

// TeamMemorySyncStatus tracks the status of sync operations.
type TeamMemorySyncStatus struct {
	LastFetchAt    time.Time `json:"lastFetchAt,omitempty"`
	LastPushAt     time.Time `json:"lastPushAt,omitempty"`
	LastError      string    `json:"lastError,omitempty"`
	PendingChanges int       `json:"pendingChanges,omitempty"`
}
