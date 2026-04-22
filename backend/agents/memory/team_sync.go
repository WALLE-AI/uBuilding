package memory

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/wall-ai/ubuilding/backend/agents"
)

// ---------------------------------------------------------------------------
// D4.I2 · Team memory sync — local push/fetch operations.
//
// Ports the local-only subset of `src/services/teamMemorySync/`.
//
// The full upstream implementation communicates with a remote API.
// This Go port focuses on the local filesystem operations:
//   - Scanning the team memory directory for .md files
//   - Building content-hashed payloads
//   - Applying fetched content to the local directory
//   - Secret scanning before push
// ---------------------------------------------------------------------------

// TeamMemorySyncer manages team memory sync operations.
type TeamMemorySyncer struct {
	mu       sync.Mutex
	cwd      string
	cfg      agents.EngineConfig
	settings SettingsProvider
	status   TeamMemorySyncStatus
	logger   *slog.Logger
}

// NewTeamMemorySyncer creates a new team memory syncer.
func NewTeamMemorySyncer(
	cwd string,
	cfg agents.EngineConfig,
	settings SettingsProvider,
	logger *slog.Logger,
) *TeamMemorySyncer {
	if logger == nil {
		logger = slog.Default()
	}
	return &TeamMemorySyncer{
		cwd:      cwd,
		cfg:      cfg,
		settings: settings,
		logger:   logger,
	}
}

// Status returns the current sync status.
func (s *TeamMemorySyncer) Status() TeamMemorySyncStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status
}

// BuildPushPayload scans the team memory directory and builds a push
// payload. Files that fail secret scanning are included in the
// skipped list rather than the payload.
func (s *TeamMemorySyncer) BuildPushPayload() (*TeamMemoryData, []TeamMemorySkippedFile, error) {
	teamDir := GetTeamMemPath(s.cwd, s.settings)
	if teamDir == "" {
		return nil, nil, fmt.Errorf("team_sync: cannot resolve team memory path")
	}

	entries, err := os.ReadDir(teamDir)
	if err != nil {
		if os.IsNotExist(err) {
			return &TeamMemoryData{Files: nil, UpdatedAt: time.Now()}, nil, nil
		}
		return nil, nil, fmt.Errorf("team_sync: read dir: %w", err)
	}

	var files []TeamMemoryContentStorage
	var skipped []TeamMemorySkippedFile

	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(strings.ToLower(name), ".md") {
			continue
		}

		path := filepath.Join(teamDir, name)
		content, readErr := os.ReadFile(path)
		if readErr != nil {
			s.logger.Warn("team_sync: skip unreadable file", "path", path, "error", readErr)
			continue
		}

		// Secret scan.
		if secrets := ScanForSecrets(string(content)); len(secrets) > 0 {
			skipped = append(skipped, TeamMemorySkippedFile{
				RelativePath: name,
				Reason:       fmt.Sprintf("contains %d potential secret(s)", len(secrets)),
			})
			continue
		}

		hash := sha256.Sum256(content)
		files = append(files, TeamMemoryContentStorage{
			RelativePath: name,
			Content:      string(content),
			Hash:         hex.EncodeToString(hash[:]),
		})
	}

	return &TeamMemoryData{
		Files:     files,
		UpdatedAt: time.Now(),
	}, skipped, nil
}

// ApplyFetchResult writes fetched team memory files to the local
// team memory directory. Returns the number of files written.
func (s *TeamMemorySyncer) ApplyFetchResult(data *TeamMemoryData) (int, error) {
	if data == nil || len(data.Files) == 0 {
		return 0, nil
	}

	teamDir := GetTeamMemPath(s.cwd, s.settings)
	if teamDir == "" {
		return 0, fmt.Errorf("team_sync: cannot resolve team memory path")
	}

	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		return 0, fmt.Errorf("team_sync: mkdir: %w", err)
	}

	written := 0
	for _, f := range data.Files {
		// Validate path safety.
		if strings.Contains(f.RelativePath, "..") || filepath.IsAbs(f.RelativePath) {
			s.logger.Warn("team_sync: skipping unsafe path", "path", f.RelativePath)
			continue
		}

		target := filepath.Join(teamDir, f.RelativePath)
		if err := os.WriteFile(target, []byte(f.Content), 0o644); err != nil {
			s.logger.Error("team_sync: write failed", "path", target, "error", err)
			continue
		}
		written++
	}

	s.mu.Lock()
	s.status.LastFetchAt = time.Now()
	s.mu.Unlock()

	return written, nil
}

// RecordPush updates the sync status after a successful push.
func (s *TeamMemorySyncer) RecordPush(skippedCount int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status.LastPushAt = time.Now()
	s.status.PendingChanges = 0
	if skippedCount > 0 {
		s.status.LastError = fmt.Sprintf("%d file(s) skipped due to secrets", skippedCount)
	} else {
		s.status.LastError = ""
	}
}

// RecordError stores an error in the sync status.
func (s *TeamMemorySyncer) RecordError(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		s.status.LastError = err.Error()
	}
}
