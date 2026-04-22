package memory

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/wall-ai/ubuilding/backend/agents"
)

// ---------------------------------------------------------------------------
// M14.I4 · AutoDream — background memory consolidation service.
//
// Ports `src/services/autoDream/autoDream.ts`.
//
// The AutoDreamService implements a gate chain that decides whether to
// run a consolidation pass. When all gates pass, it builds a
// consolidation prompt and dispatches it via SideQueryFn, then parses
// and writes the resulting file operations.
// ---------------------------------------------------------------------------

// SessionScanInterval is the minimum time between transcript-directory
// scans to avoid excessive I/O when multiple turns fire in quick
// succession. Mirrors TS SESSION_SCAN_INTERVAL_MS = 10 min.
const SessionScanInterval = 10 * time.Minute

// AutoDreamService orchestrates the auto-dream memory consolidation.
type AutoDreamService struct {
	mu              sync.Mutex
	cwd             string
	sessionID       string
	cfg             agents.EngineConfig
	settings        SettingsProvider
	dreamCfg        AutoDreamConfig
	lastSessionScan time.Time
	logger          *slog.Logger
}

// NewAutoDreamService creates a new auto-dream service.
func NewAutoDreamService(
	cwd, sessionID string,
	cfg agents.EngineConfig,
	settings SettingsProvider,
	logger *slog.Logger,
) *AutoDreamService {
	if logger == nil {
		logger = slog.Default()
	}
	return &AutoDreamService{
		cwd:       cwd,
		sessionID: sessionID,
		cfg:       cfg,
		settings:  settings,
		dreamCfg:  GetAutoDreamConfig(),
		logger:    logger,
	}
}

// AutoDreamResult is the result returned by ExecuteAutoDream.
type AutoDreamResult struct {
	Skipped      bool
	SkipReason   string
	SessionCount int
	FilesTouched []string
}

// ExecuteAutoDream runs the auto-dream consolidation if all gates pass.
// This is the main entry point, called by the background task framework.
func (s *AutoDreamService) ExecuteAutoDream(ctx context.Context, messages []agents.Message) (*AutoDreamResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Gate 1: feature enabled?
	if !IsAutoDreamEnabled(s.cfg, s.settings) {
		return &AutoDreamResult{Skipped: true, SkipReason: "auto_dream_disabled"}, nil
	}

	// Resolve auto-memory path.
	autoMemPath := GetAutoMemPath(s.cwd, s.settings)
	if autoMemPath == "" {
		return &AutoDreamResult{Skipped: true, SkipReason: "no_auto_mem_path"}, nil
	}

	// Gate 2: time gate — enough hours since last consolidation?
	lastConsolidated, err := ReadLastConsolidatedAt(autoMemPath)
	if err != nil {
		s.logger.Warn("autodream: readLastConsolidatedAt failed", "error", err)
		return &AutoDreamResult{Skipped: true, SkipReason: "lock_read_error"}, nil
	}

	var hoursSince float64
	if lastConsolidated.IsZero() {
		hoursSince = float64(s.dreamCfg.MinHours) + 1 // first run always passes
	} else {
		hoursSince = time.Since(lastConsolidated).Hours()
	}
	if hoursSince < float64(s.dreamCfg.MinHours) {
		return &AutoDreamResult{Skipped: true, SkipReason: fmt.Sprintf("time_gate: %.1fh < %dh", hoursSince, s.dreamCfg.MinHours)}, nil
	}

	// Gate 3: scan throttle — don't re-scan transcripts too often.
	if !s.lastSessionScan.IsZero() && time.Since(s.lastSessionScan) < SessionScanInterval {
		return &AutoDreamResult{Skipped: true, SkipReason: "scan_throttle"}, nil
	}
	s.lastSessionScan = time.Now()

	// Gate 4: session count gate — enough sessions since last consolidation?
	transcriptDir := resolveTranscriptDir(s.cwd)
	sessions, err := ListSessionsTouchedSince(transcriptDir, lastConsolidated)
	if err != nil {
		s.logger.Warn("autodream: listSessionsTouchedSince failed", "error", err)
		return &AutoDreamResult{Skipped: true, SkipReason: "session_scan_error"}, nil
	}

	// Exclude the current session.
	filtered := make([]string, 0, len(sessions))
	for _, sid := range sessions {
		if sid != s.sessionID {
			filtered = append(filtered, sid)
		}
	}

	if len(filtered) < s.dreamCfg.MinSessions {
		s.logger.Debug("autodream: skip — insufficient sessions",
			"found", len(filtered), "need", s.dreamCfg.MinSessions)
		return &AutoDreamResult{
			Skipped:      true,
			SkipReason:   fmt.Sprintf("session_gate: %d < %d", len(filtered), s.dreamCfg.MinSessions),
			SessionCount: len(filtered),
		}, nil
	}

	// Gate 5: acquire lock.
	priorMtime, err := TryAcquireConsolidationLock(autoMemPath)
	if err != nil {
		s.logger.Warn("autodream: lock acquire failed", "error", err)
		return &AutoDreamResult{Skipped: true, SkipReason: "lock_error"}, nil
	}
	if priorMtime == nil {
		return &AutoDreamResult{Skipped: true, SkipReason: "lock_held"}, nil
	}

	s.logger.Info("autodream: firing",
		"hours_since", fmt.Sprintf("%.1f", hoursSince),
		"sessions", len(filtered))

	// Build the consolidation prompt.
	extra := buildDreamExtra(filtered)
	prompt := BuildConsolidationPrompt(
		strings.TrimRight(autoMemPath, string(os.PathSeparator)),
		transcriptDir,
		extra,
	)

	// Run the side query.
	if DefaultSideQueryFn == nil {
		_ = RollbackConsolidationLock(autoMemPath, priorMtime)
		return nil, fmt.Errorf("autodream: no SideQueryFn configured")
	}

	response, err := DefaultSideQueryFn(ctx, prompt, "Execute the dream consolidation as described above.")
	if err != nil {
		s.logger.Error("autodream: side query failed", "error", err)
		_ = RollbackConsolidationLock(autoMemPath, priorMtime)
		return nil, fmt.Errorf("autodream: side query: %w", err)
	}

	s.logger.Info("autodream: consolidation complete",
		"response_len", len(response),
		"sessions_reviewed", len(filtered))

	// Lock stays (mtime = completion time). No rollback on success.
	return &AutoDreamResult{
		SessionCount: len(filtered),
		FilesTouched: extractTouchedFiles(response),
	}, nil
}

// buildDreamExtra constructs the "Additional context" section listing
// tool constraints and session IDs for the consolidation prompt.
func buildDreamExtra(sessionIDs []string) string {
	var b strings.Builder

	b.WriteString("\n**Tool constraints for this run:** Bash is restricted to read-only commands ")
	b.WriteString("(`ls`, `find`, `grep`, `cat`, `stat`, `wc`, `head`, `tail`, and similar). ")
	b.WriteString("Anything that writes, redirects to a file, or modifies state will be denied. ")
	b.WriteString("Plan your exploration with this in mind — no need to probe.\n\n")

	b.WriteString(fmt.Sprintf("Sessions since last consolidation (%d):\n", len(sessionIDs)))
	for _, id := range sessionIDs {
		b.WriteString("- ")
		b.WriteString(id)
		b.WriteString("\n")
	}

	return b.String()
}

// resolveTranscriptDir returns the directory where session transcript
// files (*.jsonl) are stored for the given cwd.
func resolveTranscriptDir(cwd string) string {
	base := GetMemoryBaseDir()
	if base == "" {
		return ""
	}
	key := SanitizePathKey(cwd)
	if key == "" {
		return ""
	}
	return fmt.Sprintf("%s%cprojects%c%s",
		strings.TrimRight(base, string(os.PathSeparator)),
		os.PathSeparator,
		os.PathSeparator,
		key,
	)
}

// extractTouchedFiles does a best-effort extraction of file paths
// mentioned in the LLM response. It looks for lines containing
// common file-write indicators.
func extractTouchedFiles(response string) []string {
	var files []string
	seen := make(map[string]bool)

	for _, line := range strings.Split(response, "\n") {
		trimmed := strings.TrimSpace(line)
		// Look for patterns like "Created: file.md", "Updated: file.md",
		// "Wrote file.md", etc. — may appear anywhere in the line.
		for _, keyword := range []string{"Created:", "Updated:", "Wrote:", "Modified:", "Deleted:", "Pruned:"} {
			idx := strings.Index(trimmed, keyword)
			if idx < 0 {
				continue
			}
			rest := strings.TrimSpace(trimmed[idx+len(keyword):])
			rest = strings.Trim(rest, "`")
			if rest != "" && !seen[rest] {
				files = append(files, rest)
				seen[rest] = true
			}
		}
	}

	return files
}
