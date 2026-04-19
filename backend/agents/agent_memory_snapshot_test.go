package agents

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func snapCfg(tmp string) AgentMemoryConfig {
	return AgentMemoryConfig{
		UserDir:    filepath.Join(tmp, "user"),
		ProjectDir: filepath.Join(tmp, "project"),
		LocalDir:   filepath.Join(tmp, "local"),
		Cwd:        tmp,
	}
}

// 1. None · empty snapshot short-circuits.
func TestSnapshot_EmptySnapshotIsNone(t *testing.T) {
	tmp := t.TempDir()
	dec, err := CheckAgentMemorySnapshot("reviewer", AgentMemoryScopeProject, snapCfg(tmp), "   \n\n")
	if err != nil {
		t.Fatal(err)
	}
	if dec.State != SnapshotStateNone {
		t.Fatalf("state = %s", dec.State)
	}
}

// 2. Initialize · no existing memory → write snapshot, state returns None next time.
func TestSnapshot_Initialize(t *testing.T) {
	tmp := t.TempDir()
	cfg := snapCfg(tmp)
	snap := "- rule: always cite file paths"

	dec, err := CheckAgentMemorySnapshot("reviewer", AgentMemoryScopeProject, cfg, snap)
	if err != nil || dec.State != SnapshotStateInitialize {
		t.Fatalf("first check: state=%s err=%v", dec.State, err)
	}
	if dec.SyncedHash != "" {
		t.Fatal("synced hash should be empty on first check")
	}
	if err := InitializeFromSnapshot("reviewer", AgentMemoryScopeProject, cfg, snap); err != nil {
		t.Fatalf("init: %v", err)
	}

	// MEMORY.md exists with the snapshot body.
	body, _ := ReadAgentMemory("reviewer", AgentMemoryScopeProject, cfg)
	if !strings.Contains(body, "always cite file paths") {
		t.Fatalf("memory body = %q", body)
	}

	// Second check returns None.
	dec2, err := CheckAgentMemorySnapshot("reviewer", AgentMemoryScopeProject, cfg, snap)
	if err != nil || dec2.State != SnapshotStateNone {
		t.Fatalf("second check: state=%s err=%v", dec2.State, err)
	}
	if dec2.SyncedHash != dec2.CurrentHash {
		t.Fatalf("synced hash mismatch: synced=%q current=%q", dec2.SyncedHash, dec2.CurrentHash)
	}
}

// 3. PromptUpdate · drift detected.
func TestSnapshot_DriftTriggersPromptUpdate(t *testing.T) {
	tmp := t.TempDir()
	cfg := snapCfg(tmp)

	if err := InitializeFromSnapshot("reviewer", AgentMemoryScopeProject, cfg, "v1 snapshot"); err != nil {
		t.Fatal(err)
	}
	dec, err := CheckAgentMemorySnapshot("reviewer", AgentMemoryScopeProject, cfg, "v2 SNAPSHOT")
	if err != nil {
		t.Fatal(err)
	}
	if dec.State != SnapshotStatePromptUpdate {
		t.Fatalf("state = %s", dec.State)
	}
	if dec.SyncedHash == dec.CurrentHash {
		t.Fatal("drift test should have differing hashes")
	}
}

// 4. PromptUpdate · memory file exists but no sync record.
func TestSnapshot_MemoryWithoutSyncRecordPromptsUpdate(t *testing.T) {
	tmp := t.TempDir()
	cfg := snapCfg(tmp)

	// Write memory directly (bypassing InitializeFromSnapshot → no sync record).
	if err := WriteAgentMemory("reviewer", AgentMemoryScopeProject, cfg, "legacy memory"); err != nil {
		t.Fatal(err)
	}
	dec, err := CheckAgentMemorySnapshot("reviewer", AgentMemoryScopeProject, cfg, "new snapshot")
	if err != nil {
		t.Fatal(err)
	}
	if dec.State != SnapshotStatePromptUpdate {
		t.Fatalf("state = %s", dec.State)
	}
	if dec.SyncedHash != "" {
		t.Fatal("synced hash should be empty when record is missing")
	}
}

// 5. ReplaceFromSnapshot updates MEMORY.md and syncs the hash.
func TestSnapshot_ReplaceFromSnapshot(t *testing.T) {
	tmp := t.TempDir()
	cfg := snapCfg(tmp)

	_ = InitializeFromSnapshot("reviewer", AgentMemoryScopeProject, cfg, "v1")
	if err := ReplaceFromSnapshot("reviewer", AgentMemoryScopeProject, cfg, "v2"); err != nil {
		t.Fatal(err)
	}
	body, _ := ReadAgentMemory("reviewer", AgentMemoryScopeProject, cfg)
	if !strings.Contains(body, "v2") {
		t.Fatalf("body not replaced: %q", body)
	}
	dec, _ := CheckAgentMemorySnapshot("reviewer", AgentMemoryScopeProject, cfg, "v2")
	if dec.State != SnapshotStateNone {
		t.Fatalf("after replace: state = %s", dec.State)
	}
}

// 6. MarkSnapshotSynced requires a valid scope/agent.
func TestSnapshot_MarkSnapshotSyncedValidation(t *testing.T) {
	tmp := t.TempDir()
	cfg := snapCfg(tmp)
	if err := MarkSnapshotSynced("", AgentMemoryScopeProject, cfg, "x"); err == nil {
		t.Fatal("empty agent should error")
	}
	if err := MarkSnapshotSynced("reviewer", AgentMemoryScopeNone, cfg, "x"); err == nil {
		t.Fatal("scope=none should error")
	}
}

// 7. InitializeFromSnapshot with empty body is a no-op.
func TestSnapshot_InitializeEmptyNoop(t *testing.T) {
	tmp := t.TempDir()
	cfg := snapCfg(tmp)
	if err := InitializeFromSnapshot("reviewer", AgentMemoryScopeProject, cfg, "   "); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(AgentMemoryEntrypoint("reviewer", AgentMemoryScopeProject, cfg)); err == nil {
		t.Fatal("empty snapshot should NOT have created MEMORY.md")
	}
}

// 8. Hash is whitespace-resilient: trailing newlines don't flip state.
func TestSnapshot_HashWhitespaceResilient(t *testing.T) {
	tmp := t.TempDir()
	cfg := snapCfg(tmp)
	_ = InitializeFromSnapshot("reviewer", AgentMemoryScopeProject, cfg, "body content")

	dec, _ := CheckAgentMemorySnapshot("reviewer", AgentMemoryScopeProject, cfg, "body content\n\n")
	if dec.State != SnapshotStateNone {
		t.Fatalf("trailing whitespace triggered drift: %s", dec.State)
	}
}
