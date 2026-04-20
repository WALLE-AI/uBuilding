package memory

import (
	"fmt"
	"time"
)

// ---------------------------------------------------------------------------
// M11 · Memory age / freshness helpers.
//
// Ports `src/memdir/memoryAge.ts`:
//
//   - MemoryAgeDays         — days since mtime (floor-rounded)
//   - MemoryAge             — human-readable age string
//   - MemoryFreshnessText   — staleness caveat (>1 day old)
//   - MemoryFreshnessNote   — same wrapped in <system-reminder>
//
// These exist because models are poor at date arithmetic — a raw ISO
// timestamp doesn't trigger staleness reasoning the way "47 days ago"
// does. The freshness caveat addresses stale code-state memories
// (file:line citations to code that has since changed).
// ---------------------------------------------------------------------------

// msPerDay is the number of milliseconds in a day.
const msPerDay int64 = 86_400_000

// MemoryAgeDays returns the number of days elapsed since mtimeMs.
// Floor-rounded — 0 for today, 1 for yesterday, 2+ for older.
// Negative inputs (future mtime, clock skew) clamp to 0.
func MemoryAgeDays(mtimeMs int64) int {
	elapsed := time.Now().UnixMilli() - mtimeMs
	if elapsed < 0 {
		return 0
	}
	return int(elapsed / msPerDay)
}

// MemoryAge returns a human-readable age string. Models are poor at
// date arithmetic — a raw ISO timestamp doesn't trigger staleness
// reasoning the way "47 days ago" does.
func MemoryAge(mtimeMs int64) string {
	d := MemoryAgeDays(mtimeMs)
	switch {
	case d == 0:
		return "today"
	case d == 1:
		return "yesterday"
	default:
		return fmt.Sprintf("%d days ago", d)
	}
}

// MemoryFreshnessText returns a plain-text staleness caveat for memories
// >1 day old. Returns "" for fresh (today/yesterday) memories — warning
// there is noise. Use this when the consumer already provides its own
// wrapping (e.g. relevant_memories → wrapMessagesInSystemReminder).
func MemoryFreshnessText(mtimeMs int64) string {
	d := MemoryAgeDays(mtimeMs)
	if d <= 1 {
		return ""
	}
	return fmt.Sprintf(
		"This memory is %d days old. "+
			"Memories are point-in-time observations, not live state — "+
			"claims about code behavior or file:line citations may be outdated. "+
			"Verify against current code before asserting as fact.", d)
}

// MemoryFreshnessNote returns a per-memory staleness note wrapped in
// <system-reminder> tags. Returns "" for memories ≤ 1 day old.
// Use this for callers that don't add their own system-reminder wrapper
// (e.g. FileReadTool output).
func MemoryFreshnessNote(mtimeMs int64) string {
	text := MemoryFreshnessText(mtimeMs)
	if text == "" {
		return ""
	}
	return "<system-reminder>" + text + "</system-reminder>\n"
}
