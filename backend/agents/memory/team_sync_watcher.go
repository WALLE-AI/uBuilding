package memory

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ---------------------------------------------------------------------------
// D4.I3 · Team memory sync watcher — detects local changes in the
// team memory directory and reports pending changes.
//
// This is a lightweight polling-based watcher (no fsnotify dependency)
// that compares file hashes between scans.
// ---------------------------------------------------------------------------

// DefaultWatchInterval is the polling interval for team memory changes.
const DefaultWatchInterval = 30 * time.Second

// TeamSyncWatcher polls the team memory directory for changes.
type TeamSyncWatcher struct {
	mu       sync.Mutex
	teamDir  string
	lastScan map[string]int64 // filename → modtime unix nanos
	onChange func(changed []string)
	interval time.Duration
	stopCh   chan struct{}
	logger   *slog.Logger
}

// NewTeamSyncWatcher creates a new watcher for the given team memory dir.
// onChange is called with the list of changed file basenames.
func NewTeamSyncWatcher(
	teamDir string,
	onChange func(changed []string),
	logger *slog.Logger,
) *TeamSyncWatcher {
	if logger == nil {
		logger = slog.Default()
	}
	return &TeamSyncWatcher{
		teamDir:  teamDir,
		lastScan: make(map[string]int64),
		onChange:  onChange,
		interval: DefaultWatchInterval,
		stopCh:   make(chan struct{}),
		logger:   logger,
	}
}

// SetInterval overrides the polling interval (primarily for tests).
func (w *TeamSyncWatcher) SetInterval(d time.Duration) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.interval = d
}

// Start begins the polling loop in a background goroutine.
func (w *TeamSyncWatcher) Start() {
	go w.run()
}

// Stop signals the watcher to stop.
func (w *TeamSyncWatcher) Stop() {
	select {
	case <-w.stopCh:
	default:
		close(w.stopCh)
	}
}

func (w *TeamSyncWatcher) run() {
	// Initial scan to seed the baseline.
	w.scan()

	w.mu.Lock()
	interval := w.interval
	w.mu.Unlock()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-w.stopCh:
			return
		case <-ticker.C:
			w.scan()
		}
	}
}

// scan reads the directory and compares modtimes to detect changes.
func (w *TeamSyncWatcher) scan() {
	entries, err := os.ReadDir(w.teamDir)
	if err != nil {
		if !os.IsNotExist(err) {
			w.logger.Debug("team_sync_watcher: scan error", "error", err)
		}
		return
	}

	current := make(map[string]int64, len(entries))
	var changed []string

	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(strings.ToLower(name), ".md") {
			continue
		}

		info, infoErr := e.Info()
		if infoErr != nil {
			continue
		}
		mtime := info.ModTime().UnixNano()
		current[name] = mtime

		w.mu.Lock()
		prev, existed := w.lastScan[name]
		w.mu.Unlock()

		if !existed || prev != mtime {
			changed = append(changed, name)
		}
	}

	// Detect deletions.
	w.mu.Lock()
	for name := range w.lastScan {
		if _, ok := current[name]; !ok {
			changed = append(changed, name)
		}
	}
	w.lastScan = current
	w.mu.Unlock()

	if len(changed) > 0 && w.onChange != nil {
		w.onChange(changed)
	}
}

// ScanOnce performs a single scan and returns changed files since the
// last scan. Useful for testing without starting the poll loop.
func (w *TeamSyncWatcher) ScanOnce() []string {
	entries, err := os.ReadDir(w.teamDir)
	if err != nil {
		return nil
	}

	current := make(map[string]int64, len(entries))
	var changed []string

	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(strings.ToLower(name), ".md") {
			continue
		}
		info, infoErr := e.Info()
		if infoErr != nil {
			continue
		}
		mtime := info.ModTime().UnixNano()
		current[name] = mtime

		w.mu.Lock()
		prev, existed := w.lastScan[name]
		w.mu.Unlock()

		if !existed || prev != mtime {
			changed = append(changed, name)
		}
	}

	w.mu.Lock()
	for name := range w.lastScan {
		if _, ok := current[name]; !ok {
			changed = append(changed, name)
		}
	}
	w.lastScan = current
	w.mu.Unlock()

	return changed
}

// GetTrackedFiles returns the list of currently tracked filenames.
func (w *TeamSyncWatcher) GetTrackedFiles() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	files := make([]string, 0, len(w.lastScan))
	for name := range w.lastScan {
		files = append(files, name)
	}
	return files
}

// CreateTeamDir creates the team directory if it doesn't exist.
// Returns the absolute path.
func CreateTeamDir(cwd string, settings SettingsProvider) (string, error) {
	dir := GetTeamMemPath(cwd, settings)
	if dir == "" {
		return "", filepath.ErrBadPattern
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}
