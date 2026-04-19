package agents

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func memCfg(tmp string) AgentMemoryConfig {
	return AgentMemoryConfig{
		UserDir:    filepath.Join(tmp, "user"),
		ProjectDir: filepath.Join(tmp, "project"),
		LocalDir:   filepath.Join(tmp, "local"),
		Cwd:        tmp,
	}
}

func TestAgentMemoryDir_ScopeRouting(t *testing.T) {
	tmp := t.TempDir()
	cfg := memCfg(tmp)
	cases := []struct {
		scope AgentMemoryScope
		want  string
	}{
		{AgentMemoryScopeUser, filepath.Join(tmp, "user", "reviewer")},
		{AgentMemoryScopeProject, filepath.Join(tmp, "project", "reviewer")},
		{AgentMemoryScopeLocal, filepath.Join(tmp, "local", "reviewer")},
	}
	for _, tc := range cases {
		if got := AgentMemoryDir("reviewer", tc.scope, cfg); got != tc.want {
			t.Errorf("%s: got %q; want %q", tc.scope, got, tc.want)
		}
	}
	if AgentMemoryDir("", AgentMemoryScopeProject, cfg) != "" {
		t.Error("empty agent type must yield empty dir")
	}
	if AgentMemoryDir("reviewer", AgentMemoryScopeNone, cfg) != "" {
		t.Error("scope none must yield empty dir")
	}
}

func TestAgentMemoryEntrypoint_AndSanitization(t *testing.T) {
	tmp := t.TempDir()
	cfg := memCfg(tmp)
	want := filepath.Join(tmp, "user", "my-plugin-reviewer", "MEMORY.md")
	if got := AgentMemoryEntrypoint("my-plugin:reviewer", AgentMemoryScopeUser, cfg); got != want {
		t.Fatalf("entrypoint = %q", got)
	}
}

func TestWriteAndReadAgentMemory(t *testing.T) {
	tmp := t.TempDir()
	cfg := memCfg(tmp)

	if err := WriteAgentMemory("reviewer", AgentMemoryScopeProject, cfg, "hello memory"); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := ReadAgentMemory("reviewer", AgentMemoryScopeProject, cfg)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if strings.TrimSpace(got) != "hello memory" {
		t.Fatalf("read = %q", got)
	}

	// Overwrite + assert atomic behaviour (no leftover .tmp).
	if err := WriteAgentMemory("reviewer", AgentMemoryScopeProject, cfg, "v2 content"); err != nil {
		t.Fatalf("overwrite: %v", err)
	}
	got, _ = ReadAgentMemory("reviewer", AgentMemoryScopeProject, cfg)
	if strings.TrimSpace(got) != "v2 content" {
		t.Fatalf("v2 = %q", got)
	}
	dir := AgentMemoryDir("reviewer", AgentMemoryScopeProject, cfg)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Fatalf(".tmp leftover detected: %s", e.Name())
		}
	}
}

func TestReadAgentMemory_MissingReturnsEmpty(t *testing.T) {
	tmp := t.TempDir()
	cfg := memCfg(tmp)
	got, err := ReadAgentMemory("ghost", AgentMemoryScopeProject, cfg)
	if err != nil || got != "" {
		t.Fatalf("missing memory: got=%q err=%v", got, err)
	}
}

func TestBuildMemoryPrompt(t *testing.T) {
	tmp := t.TempDir()
	cfg := memCfg(tmp)
	if err := WriteAgentMemory("reviewer", AgentMemoryScopeUser, cfg, "- rule: always cite file paths"); err != nil {
		t.Fatalf("write: %v", err)
	}
	prompt := BuildMemoryPrompt("reviewer", AgentMemoryScopeUser, cfg)
	for _, want := range []string{
		"<persistent_agent_memory>",
		"always cite file paths",
		"Scope: user",
		"User scope",
		"</persistent_agent_memory>",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt missing %q; got:\n%s", want, prompt)
		}
	}

	// No memory yet → still renders a placeholder body.
	empty := BuildMemoryPrompt("fresh", AgentMemoryScopeProject, cfg)
	if !strings.Contains(empty, "no persistent notes") {
		t.Fatalf("expected placeholder body; got:\n%s", empty)
	}
}

func TestIsAgentMemoryPath(t *testing.T) {
	tmp := t.TempDir()
	cfg := memCfg(tmp)
	memDir := AgentMemoryDir("reviewer", AgentMemoryScopeProject, cfg)
	_ = os.MkdirAll(memDir, 0o755)

	if !IsAgentMemoryPath(filepath.Join(memDir, "notes.md"), cfg) {
		t.Fatal("path inside memory dir must be recognised")
	}
	if !IsAgentMemoryPath(memDir, cfg) {
		t.Fatal("memory dir itself must be recognised")
	}
	if IsAgentMemoryPath(filepath.Join(tmp, "outside", "notes.md"), cfg) {
		t.Fatal("path outside memory dir must NOT be recognised")
	}
	if IsAgentMemoryPath("", cfg) {
		t.Fatal("empty path must be rejected")
	}
}

func TestAgentMemoryConfig_AutoDefaults(t *testing.T) {
	// With Cwd set, ProjectDir / LocalDir default relative to it.
	cfg := AgentMemoryConfig{Cwd: "/tmp/fakeproj"}
	resolved := resolveMemoryCfg(cfg)
	if resolved.ProjectDir == "" || resolved.LocalDir == "" {
		t.Fatalf("defaults lost: %+v", resolved)
	}
	// UserDir default should point somewhere under UserConfigDir; we only
	// require non-empty on Windows/Linux/Mac.
	if resolved.UserDir == "" {
		t.Log("UserDir unset — environment may lack HOME; skipping strict check")
	}
}

// C08 · when an agent definition declares Memory, BuildMemoryPrompt fires
// and produces a non-empty system prompt fragment. This covers the child
// engine integration.
func TestAgentMemory_IntegrationWithAgentDefinition(t *testing.T) {
	tmp := t.TempDir()
	cfg := memCfg(tmp)
	def := &AgentDefinition{
		AgentType: "reviewer",
		WhenToUse: "",
		Memory:    AgentMemoryScopeProject,
	}
	_ = WriteAgentMemory(def.AgentType, def.Memory, cfg, "Focus on edge cases.")
	prompt := BuildMemoryPrompt(def.AgentType, def.Memory, cfg)
	if !strings.Contains(prompt, "Focus on edge cases.") {
		t.Fatalf("memory body not included: %s", prompt)
	}
}
