package memory

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// D4.T1 · Team sync types tests — JSON round-trip and zero-value safety.
// ---------------------------------------------------------------------------

func TestTeamMemoryContentStorage_JSON(t *testing.T) {
	cs := TeamMemoryContentStorage{
		RelativePath: "prefs.md",
		Content:      "# Preferences",
		Hash:         "abc123",
	}
	data, err := json.Marshal(cs)
	require.NoError(t, err)

	var decoded TeamMemoryContentStorage
	require.NoError(t, json.Unmarshal(data, &decoded))
	assert.Equal(t, cs, decoded)
}

func TestTeamMemoryData_JSON(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	d := TeamMemoryData{
		Files: []TeamMemoryContentStorage{
			{RelativePath: "a.md", Content: "hello"},
		},
		UpdatedAt: now,
		Version:   2,
	}
	data, err := json.Marshal(d)
	require.NoError(t, err)

	var decoded TeamMemoryData
	require.NoError(t, json.Unmarshal(data, &decoded))
	assert.Equal(t, 1, len(decoded.Files))
	assert.Equal(t, 2, decoded.Version)
}

func TestTeamMemoryFetchResult_Success(t *testing.T) {
	r := TeamMemoryFetchResult{Success: true, Data: &TeamMemoryData{Version: 1}}
	data, err := json.Marshal(r)
	require.NoError(t, err)

	var decoded TeamMemoryFetchResult
	require.NoError(t, json.Unmarshal(data, &decoded))
	assert.True(t, decoded.Success)
	assert.NotNil(t, decoded.Data)
}

func TestTeamMemoryFetchResult_Error(t *testing.T) {
	r := TeamMemoryFetchResult{
		Success: false,
		Error:   &TeamSyncError{Code: "NOT_FOUND", Message: "team not found"},
	}
	data, err := json.Marshal(r)
	require.NoError(t, err)
	assert.Contains(t, string(data), "NOT_FOUND")
}

func TestTeamMemoryPushResult_Skipped(t *testing.T) {
	r := TeamMemoryPushResult{
		Success: true,
		SkippedFiles: []TeamMemorySkippedFile{
			{RelativePath: "secrets.md", Reason: "contains API key"},
		},
	}
	data, err := json.Marshal(r)
	require.NoError(t, err)

	var decoded TeamMemoryPushResult
	require.NoError(t, json.Unmarshal(data, &decoded))
	assert.Equal(t, 1, len(decoded.SkippedFiles))
	assert.Equal(t, "secrets.md", decoded.SkippedFiles[0].RelativePath)
}

func TestTeamMemorySyncStatus_ZeroValue(t *testing.T) {
	var s TeamMemorySyncStatus
	assert.True(t, s.LastFetchAt.IsZero())
	assert.True(t, s.LastPushAt.IsZero())
	assert.Empty(t, s.LastError)
	assert.Equal(t, 0, s.PendingChanges)
}
