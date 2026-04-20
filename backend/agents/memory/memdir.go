package memory

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
)

// ---------------------------------------------------------------------------
// M4 · memdir MEMORY.md entrypoint.
//
//   - M4.I1 · TruncateEntrypointContent   (mirrors truncateEntrypointContent)
//   - M4.I2 · LoadAutoMemEntrypoint       (mirrors safelyReadMemoryFileAsync
//                                          for the MemoryTypeAutoMem tier)
//   - M4.I3 · EnsureMemoryDirExists       (mirrors ensureMemoryDirExists)
//
// Byte-identical caps with upstream: 200 lines OR 25,000 bytes, with
// the same warning wording so telemetry-downstream code can match on
// the substring.
// ---------------------------------------------------------------------------

// MaxEntrypointLines caps the MEMORY.md index line count (mirrors TS
// `MAX_ENTRYPOINT_LINES`).
const MaxEntrypointLines = 200

// MaxEntrypointBytes caps the MEMORY.md size in bytes. Chosen to catch
// long-line indexes that slip past the line cap (~125 chars/line at
// 200 lines). Mirrors TS `MAX_ENTRYPOINT_BYTES = 25_000`.
const MaxEntrypointBytes = 25000

// DirExistsGuidance is the shared blurb reassuring the model that
// the memory dir is already present so it should `Write` directly
// instead of `mkdir -p` or `ls` probing.
const DirExistsGuidance = "This directory already exists — write to it directly with the Write tool (do not run mkdir or check for its existence)."

// DirsExistGuidance is the dual-directory variant used by the team
// memory prompt (M7).
const DirsExistGuidance = "Both directories already exist — write to them directly with the Write tool (do not run mkdir or check for their existence)."

// EntrypointTruncation describes the result of a truncation pass so
// callers can surface telemetry without re-inspecting the content.
type EntrypointTruncation struct {
	Content           string
	LineCount         int
	ByteCount         int
	WasLineTruncated  bool
	WasByteTruncated  bool
}

// TruncateEntrypointContent trims raw to fit within MaxEntrypointLines
// and MaxEntrypointBytes. When either cap fires the returned Content
// has a warning block appended explaining which cap tripped.
//
// Byte truncation aligns to the last newline before the cap so we
// never cut mid-line — matches upstream exactly.
func TruncateEntrypointContent(raw string) EntrypointTruncation {
	trimmed := strings.TrimSpace(raw)
	lines := strings.Split(trimmed, "\n")
	lineCount := len(lines)
	byteCount := len(trimmed)

	wasLineTruncated := lineCount > MaxEntrypointLines
	// Check original byte count — long lines are the failure mode the
	// byte cap targets; checking post-line-trunc size would under-report.
	wasByteTruncated := byteCount > MaxEntrypointBytes

	if !wasLineTruncated && !wasByteTruncated {
		return EntrypointTruncation{
			Content:          trimmed,
			LineCount:        lineCount,
			ByteCount:        byteCount,
			WasLineTruncated: false,
			WasByteTruncated: false,
		}
	}

	truncated := trimmed
	if wasLineTruncated {
		truncated = strings.Join(lines[:MaxEntrypointLines], "\n")
	}
	if len(truncated) > MaxEntrypointBytes {
		cutAt := strings.LastIndex(truncated[:MaxEntrypointBytes], "\n")
		if cutAt > 0 {
			truncated = truncated[:cutAt]
		} else {
			truncated = truncated[:MaxEntrypointBytes]
		}
	}

	reason := truncationReason(lineCount, byteCount, wasLineTruncated, wasByteTruncated)
	warning := fmt.Sprintf(
		"\n\n> WARNING: %s is %s. Only part of it was loaded. Keep index entries to one line under ~200 chars; move detail into topic files.",
		autoMemEntrypoint, reason,
	)
	return EntrypointTruncation{
		Content:          truncated + warning,
		LineCount:        lineCount,
		ByteCount:        byteCount,
		WasLineTruncated: wasLineTruncated,
		WasByteTruncated: wasByteTruncated,
	}
}

func truncationReason(lineCount, byteCount int, byLine, byByte bool) string {
	switch {
	case byByte && !byLine:
		return fmt.Sprintf("%s (limit: %s) — index entries are too long",
			formatFileSize(byteCount), formatFileSize(MaxEntrypointBytes))
	case byLine && !byByte:
		return fmt.Sprintf("%d lines (limit: %d)", lineCount, MaxEntrypointLines)
	default:
		return fmt.Sprintf("%d lines and %s", lineCount, formatFileSize(byteCount))
	}
}

// formatFileSize mirrors the TS helper: returns bytes / KB / MB / GB.
// Retains the `.toFixed(1).replace(/\.0$/, '')` behaviour — 1024 → "1KB",
// 1536 → "1.5KB".
func formatFileSize(sizeInBytes int) string {
	kb := float64(sizeInBytes) / 1024
	if kb < 1 {
		return fmt.Sprintf("%d bytes", sizeInBytes)
	}
	if kb < 1024 {
		return trimZero(fmt.Sprintf("%.1f", kb)) + "KB"
	}
	mb := kb / 1024
	if mb < 1024 {
		return trimZero(fmt.Sprintf("%.1f", mb)) + "MB"
	}
	gb := mb / 1024
	return trimZero(fmt.Sprintf("%.1f", gb)) + "GB"
}

func trimZero(s string) string {
	return strings.TrimSuffix(s, ".0")
}

// ---------------------------------------------------------------------------
// M4.I2 · LoadAutoMemEntrypoint.
// ---------------------------------------------------------------------------

// LoadAutoMemEntrypoint reads the MEMORY.md file for cwd's auto-memory
// directory, applies the truncation pass, and returns a MemoryFileInfo
// ready for loader consumption. When the file doesn't exist the call
// returns (nil, nil) — a missing entrypoint is not an error, it just
// means the user hasn't written anything to auto-memory yet.
//
// Pass NopSettingsProvider when no settings store is available.
func LoadAutoMemEntrypoint(
	ctx context.Context,
	cwd string,
	settings SettingsProvider,
) (*MemoryFileInfo, error) {
	_ = ctx // reserved for future cancellation
	path := GetAutoMemEntrypoint(cwd, settings)
	if path == "" {
		return nil, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	content := string(raw)
	trunc := TruncateEntrypointContent(content)
	info := &MemoryFileInfo{
		Path:                   path,
		Type:                   MemoryTypeAutoMem,
		Content:                trunc.Content,
		ContentDiffersFromDisk: trunc.Content != content,
	}
	if info.ContentDiffersFromDisk {
		info.RawContent = content
	}
	return info, nil
}

// ---------------------------------------------------------------------------
// M4.I3 · EnsureMemoryDirExists.
// ---------------------------------------------------------------------------

// EnsureMemoryDirExists creates path with 0o700 permissions (mode
// scoped to the user so team-shared FS tools cannot accidentally
// read the dir). Idempotent — returns nil when the directory already
// exists. Permission errors are returned rather than swallowed so
// callers can surface "memory dir unusable" to the user.
func EnsureMemoryDirExists(path string) error {
	if path == "" {
		return errors.New("memory: EnsureMemoryDirExists called with empty path")
	}
	if err := os.MkdirAll(path, 0o700); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return nil
		}
		return fmt.Errorf("memory: mkdir %q: %w", path, err)
	}
	return nil
}

// init wires the truncation helper into the loader so
// parseMemoryFileContent can apply it without creating an import
// cycle. Without this hook the loader uses the identity function
// (set in claudemd.go) which skips truncation.
func init() {
	truncateEntrypointFn = func(s string) string {
		return TruncateEntrypointContent(s).Content
	}
}
