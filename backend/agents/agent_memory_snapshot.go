// Package agents — agent memory snapshot state machine.
//
// Task C13 · port src/tools/AgentTool/agentMemorySnapshot.ts. The host
// provides a canonical "template memory" snapshot (e.g. the corporate
// default review checklist). The state machine reconciles that snapshot
// against the live MEMORY.md file and reports one of three states:
//
//   - none           · snapshot is up to date; nothing to do
//   - initialize     · no memory yet → write snapshot verbatim
//   - prompt-update  · memory exists but snapshot has drifted; caller
//                      prompts the user before overwriting
//
// The sync record lives alongside the memory directory as
// `.snapshot-synced.json` with the hash of the snapshot that was written
// most recently. `MarkSnapshotSynced` updates it after a successful
// write, so a subsequent CheckAgentMemorySnapshot returns "none".
//
// All writes go through the same atomic tmp+rename helpers as
// WriteAgentMemory so a crash mid-update leaves the previous state
// intact.
package agents

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// SnapshotState enumerates the three states the reconciler can report.
type SnapshotState string

const (
	// SnapshotStateNone means the stored memory already matches the
	// snapshot — no write is needed.
	SnapshotStateNone SnapshotState = "none"

	// SnapshotStateInitialize means there is no memory yet (or no sync
	// record). The caller should write the snapshot verbatim via
	// InitializeFromSnapshot.
	SnapshotStateInitialize SnapshotState = "initialize"

	// SnapshotStatePromptUpdate means a memory exists but the snapshot
	// has drifted since the last sync. The caller should confirm with the
	// user, then apply via ReplaceFromSnapshot.
	SnapshotStatePromptUpdate SnapshotState = "prompt-update"
)

// SnapshotDecision is the structured result of CheckAgentMemorySnapshot.
// The `Reason` field provides a short human-readable explanation the
// host can surface in logs / UI.
type SnapshotDecision struct {
	State       SnapshotState
	Reason      string
	CurrentHash string // hash of the snapshot currently being evaluated
	SyncedHash  string // hash of the last-synced snapshot ("" when none)
}

// snapshotSyncRecord is the shape persisted to `.snapshot-synced.json`.
type snapshotSyncRecord struct {
	Hash     string    `json:"hash"`
	SyncedAt time.Time `json:"synced_at"`
}

// snapshotSyncFilename is the sidecar file that tracks the last synced
// snapshot hash. Kept next to MEMORY.md so the memory directory is
// self-contained.
const snapshotSyncFilename = ".snapshot-synced.json"

// ---------------------------------------------------------------------------
// Path helpers
// ---------------------------------------------------------------------------

// snapshotSyncPath returns the absolute path of the sync record for
// (agentType, scope, cfg). Returns "" when the memory dir can't be
// resolved (e.g. scope=none).
func snapshotSyncPath(agentType string, scope AgentMemoryScope, cfg AgentMemoryConfig) string {
	dir := AgentMemoryDir(agentType, scope, cfg)
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, snapshotSyncFilename)
}

// hashSnapshot returns the canonical sha256 of the snapshot body. Whitespace
// is trimmed on both ends to make hash calculation resilient to trailing
// newlines (WriteAgentMemory always appends exactly one trailing newline).
func hashSnapshot(body string) string {
	trimmed := strings.TrimSpace(body)
	sum := sha256.Sum256([]byte(trimmed))
	return hex.EncodeToString(sum[:])
}

// readSyncRecord loads the sync record for the given scope. Missing file
// returns (nil, nil) so callers can distinguish "never synced" from "I/O
// error".
func readSyncRecord(agentType string, scope AgentMemoryScope, cfg AgentMemoryConfig) (*snapshotSyncRecord, error) {
	path := snapshotSyncPath(agentType, scope, cfg)
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var rec snapshotSyncRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		return nil, fmt.Errorf("snapshot sync: parse %s: %w", path, err)
	}
	return &rec, nil
}

// writeSyncRecord atomically writes the sync record. Creates the memory
// directory first so the sidecar and MEMORY.md live together.
func writeSyncRecord(agentType string, scope AgentMemoryScope, cfg AgentMemoryConfig, rec snapshotSyncRecord) error {
	path := snapshotSyncPath(agentType, scope, cfg)
	if path == "" {
		return fmt.Errorf("snapshot sync: invalid scope %q for agent %q", scope, agentType)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("snapshot sync: mkdir: %w", err)
	}
	if rec.SyncedAt.IsZero() {
		rec.SyncedAt = time.Now().UTC()
	}
	buf, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, buf, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// ---------------------------------------------------------------------------
// Public API
// ---------------------------------------------------------------------------

// CheckAgentMemorySnapshot reconciles the current on-disk memory with the
// supplied snapshot body and returns the next action. Never mutates state.
//
// Decision rules (mirroring TS):
//
//   - Empty snapshot           → None (nothing to sync).
//   - No MEMORY.md yet         → Initialize.
//   - Sync record missing      → PromptUpdate (we have memory but don't
//                                 know whether it's derived from a prior
//                                 snapshot — safest to prompt).
//   - Sync hash == snapshot    → None.
//   - Sync hash != snapshot    → PromptUpdate.
func CheckAgentMemorySnapshot(
	agentType string,
	scope AgentMemoryScope,
	cfg AgentMemoryConfig,
	snapshot string,
) (SnapshotDecision, error) {
	currentHash := hashSnapshot(snapshot)
	decision := SnapshotDecision{CurrentHash: currentHash}

	if strings.TrimSpace(snapshot) == "" {
		decision.State = SnapshotStateNone
		decision.Reason = "empty snapshot"
		return decision, nil
	}
	if scope == AgentMemoryScopeNone || agentType == "" {
		decision.State = SnapshotStateNone
		decision.Reason = "invalid scope or agent type"
		return decision, nil
	}

	body, err := ReadAgentMemory(agentType, scope, cfg)
	if err != nil {
		return decision, fmt.Errorf("snapshot check: read memory: %w", err)
	}

	rec, err := readSyncRecord(agentType, scope, cfg)
	if err != nil {
		return decision, err
	}
	if rec != nil {
		decision.SyncedHash = rec.Hash
	}

	if strings.TrimSpace(body) == "" {
		decision.State = SnapshotStateInitialize
		decision.Reason = "no memory on disk"
		return decision, nil
	}
	if rec == nil {
		decision.State = SnapshotStatePromptUpdate
		decision.Reason = "memory exists without sync record — user confirmation required"
		return decision, nil
	}
	if rec.Hash == currentHash {
		decision.State = SnapshotStateNone
		decision.Reason = "snapshot hash matches last sync"
		return decision, nil
	}
	decision.State = SnapshotStatePromptUpdate
	decision.Reason = "snapshot has changed since last sync"
	return decision, nil
}

// InitializeFromSnapshot writes the snapshot as the agent's MEMORY.md
// (via WriteAgentMemory) and records the sync. Intended for the
// SnapshotStateInitialize path. An empty snapshot is a no-op.
func InitializeFromSnapshot(
	agentType string,
	scope AgentMemoryScope,
	cfg AgentMemoryConfig,
	snapshot string,
) error {
	if strings.TrimSpace(snapshot) == "" {
		return nil
	}
	if err := WriteAgentMemory(agentType, scope, cfg, snapshot); err != nil {
		return err
	}
	return MarkSnapshotSynced(agentType, scope, cfg, snapshot)
}

// ReplaceFromSnapshot overwrites the existing memory with snapshot and
// records the new sync hash. Intended for the SnapshotStatePromptUpdate
// path after the host has obtained user consent.
func ReplaceFromSnapshot(
	agentType string,
	scope AgentMemoryScope,
	cfg AgentMemoryConfig,
	snapshot string,
) error {
	// Replace is semantically identical to Initialize on disk — both
	// overwrite MEMORY.md atomically. The distinction is preserved at the
	// API level so hosts can log intent separately.
	return InitializeFromSnapshot(agentType, scope, cfg, snapshot)
}

// MarkSnapshotSynced records that the given snapshot body is now the
// last-synced version. Use this after writing memory directly (e.g.
// after a merged user edit) so subsequent CheckAgentMemorySnapshot
// returns None.
func MarkSnapshotSynced(
	agentType string,
	scope AgentMemoryScope,
	cfg AgentMemoryConfig,
	snapshot string,
) error {
	if scope == AgentMemoryScopeNone || agentType == "" {
		return fmt.Errorf("snapshot sync: invalid scope %q for agent %q", scope, agentType)
	}
	return writeSyncRecord(agentType, scope, cfg, snapshotSyncRecord{
		Hash:     hashSnapshot(snapshot),
		SyncedAt: time.Now().UTC(),
	})
}
