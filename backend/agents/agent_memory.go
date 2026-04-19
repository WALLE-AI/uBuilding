// Package agents — persistent agent memory.
//
// Tasks C07 · C08 · C12 · port src/tools/AgentTool/agentMemory.ts's three
// scopes (user / project / local) into Go. Each scope maps to a different
// directory on disk; the sub-agent spawn path (C08) composes the memory
// prompt into the child's system prompt when the AgentDefinition declares
// a non-empty Memory scope.
//
// Directory layout (external build):
//
//   user    → $XDG_CONFIG_HOME/ubuilding/agent-memory/<agentType>/
//             fallback: os.UserConfigDir()/ubuilding/agent-memory/<agentType>/
//   project → <cwd>/.claude/agent-memory/<agentType>/
//   local   → $UBUILDING_REMOTE_MEMORY_DIR/projects/<sanitized-cwd>/agent-memory-local/<agentType>/
//             fallback: <cwd>/.claude/agent-memory-local/<agentType>/
//
// Writes are atomic (tmp-file + rename). File names containing colons
// (plugin-namespaced agents) are sanitised with dashes so Windows stays
// happy. A single MEMORY.md entrypoint is the canonical "load this" file;
// the loader inlines it verbatim as a system-prompt attachment so the
// model sees it at every sub-agent spawn.
package agents

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// AgentMemoryConfig overrides where each scope lives. Populate fields in
// tests to point at tmp dirs; leave zero for production defaults.
type AgentMemoryConfig struct {
	// UserDir is the user-scope base. When empty, DefaultUserMemoryDir is
	// consulted.
	UserDir string

	// ProjectDir is the project-scope base. When empty, the loader derives
	// it from Cwd.
	ProjectDir string

	// LocalDir is the local-scope base. When empty, the loader derives it
	// from Cwd (or CLAUDE_CODE_REMOTE_MEMORY_DIR when set).
	LocalDir string

	// Cwd is the working directory used to compute project/local defaults.
	// Zero-value falls back to os.Getwd().
	Cwd string
}

// DefaultUserMemoryDir returns the canonical user-scope memory base
// ($XDG_CONFIG_HOME/ubuilding or os.UserConfigDir equivalent).
func DefaultUserMemoryDir() string {
	if v := strings.TrimSpace(os.Getenv("UBUILDING_MEMORY_DIR")); v != "" {
		return v
	}
	if v := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); v != "" {
		return filepath.Join(v, "ubuilding", "agent-memory")
	}
	if dir, err := os.UserConfigDir(); err == nil && dir != "" {
		return filepath.Join(dir, "ubuilding", "agent-memory")
	}
	return ""
}

// resolveMemoryCfg fills in zero-value fields with sensible defaults.
func resolveMemoryCfg(cfg AgentMemoryConfig) AgentMemoryConfig {
	if cfg.Cwd == "" {
		if wd, err := os.Getwd(); err == nil {
			cfg.Cwd = wd
		}
	}
	if cfg.UserDir == "" {
		cfg.UserDir = DefaultUserMemoryDir()
	}
	if cfg.ProjectDir == "" && cfg.Cwd != "" {
		cfg.ProjectDir = filepath.Join(cfg.Cwd, ".claude", "agent-memory")
	}
	if cfg.LocalDir == "" {
		if remote := strings.TrimSpace(os.Getenv("UBUILDING_REMOTE_MEMORY_DIR")); remote != "" && cfg.Cwd != "" {
			cfg.LocalDir = filepath.Join(remote, "projects", sanitizePath(cfg.Cwd), "agent-memory-local")
		} else if cfg.Cwd != "" {
			cfg.LocalDir = filepath.Join(cfg.Cwd, ".claude", "agent-memory-local")
		}
	}
	return cfg
}

// sanitizeAgentTypeForPath replaces characters that Windows disallows
// (mainly ':'  used by plugin-namespaced agent types) with '-'.
func sanitizeAgentTypeForPath(agentType string) string {
	replaced := strings.NewReplacer(":", "-", string(os.PathSeparator), "-").Replace(agentType)
	return strings.TrimSpace(replaced)
}

// sanitizePath produces a stable fs-safe label for a cwd, used as a
// namespace under the remote-memory base. Mirrors TS sanitizePath.
func sanitizePath(p string) string {
	cleaned := filepath.Clean(p)
	cleaned = strings.Trim(cleaned, string(os.PathSeparator))
	cleaned = strings.NewReplacer(string(os.PathSeparator), "_", ":", "_", " ", "_").Replace(cleaned)
	if cleaned == "" {
		return "_"
	}
	return cleaned
}

// AgentMemoryDir returns the directory where MEMORY.md + extra files for
// agentType/scope live. Returns "" when scope is none or when the
// configured base directory can't be resolved.
func AgentMemoryDir(agentType string, scope AgentMemoryScope, cfg AgentMemoryConfig) string {
	if agentType == "" || scope == AgentMemoryScopeNone {
		return ""
	}
	cfg = resolveMemoryCfg(cfg)
	subdir := sanitizeAgentTypeForPath(agentType)
	switch scope {
	case AgentMemoryScopeUser:
		if cfg.UserDir == "" {
			return ""
		}
		return filepath.Join(cfg.UserDir, subdir)
	case AgentMemoryScopeProject:
		if cfg.ProjectDir == "" {
			return ""
		}
		return filepath.Join(cfg.ProjectDir, subdir)
	case AgentMemoryScopeLocal:
		if cfg.LocalDir == "" {
			return ""
		}
		return filepath.Join(cfg.LocalDir, subdir)
	}
	return ""
}

// AgentMemoryEntrypoint is the canonical "always load" file.
func AgentMemoryEntrypoint(agentType string, scope AgentMemoryScope, cfg AgentMemoryConfig) string {
	dir := AgentMemoryDir(agentType, scope, cfg)
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "MEMORY.md")
}

// IsAgentMemoryPath guards against path-traversal attempts — it reports
// whether absolutePath lives under one of the memory roots for cfg.
// Used by the file tools to allow Write() inside the memory directory
// without a user prompt.
func IsAgentMemoryPath(absolutePath string, cfg AgentMemoryConfig) bool {
	if absolutePath == "" {
		return false
	}
	clean := filepath.Clean(absolutePath)
	cfg = resolveMemoryCfg(cfg)
	roots := []string{cfg.UserDir, cfg.ProjectDir, cfg.LocalDir}
	for _, r := range roots {
		if r == "" {
			continue
		}
		cleanRoot := filepath.Clean(r)
		if clean == cleanRoot {
			return true
		}
		if strings.HasPrefix(clean, cleanRoot+string(os.PathSeparator)) {
			return true
		}
	}
	return false
}

// BuildMemoryPrompt assembles the memory section appended to the agent's
// system prompt. Returns "" when no memory exists for (agentType, scope).
// Side effect: ensures the directory exists (fire-and-forget — errors are
// swallowed because the sub-agent can still run without prior memory).
func BuildMemoryPrompt(agentType string, scope AgentMemoryScope, cfg AgentMemoryConfig) string {
	dir := AgentMemoryDir(agentType, scope, cfg)
	if dir == "" {
		return ""
	}
	_ = os.MkdirAll(dir, 0o755)
	entrypoint := filepath.Join(dir, "MEMORY.md")
	content, err := os.ReadFile(entrypoint)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return ""
	}
	scopeNote := memoryScopeGuidance(scope)

	body := strings.TrimSpace(string(content))
	inner := body
	if inner == "" {
		inner = "(no persistent notes yet — write here to help future spawns)"
	}

	return fmt.Sprintf("<persistent_agent_memory>\n%s\n\nScope: %s\nStorage: %s\nGuidelines:\n- %s\n- Write durable, task-agnostic facts (e.g. project conventions, shared paths).\n- Keep entries short and dated; prune stale ones.\n</persistent_agent_memory>",
		inner, scope, dir, scopeNote)
}

// memoryScopeGuidance returns the scope-specific advice text.
func memoryScopeGuidance(scope AgentMemoryScope) string {
	switch scope {
	case AgentMemoryScopeUser:
		return "User scope: learnings apply across projects — keep them general."
	case AgentMemoryScopeProject:
		return "Project scope: shared via version control — tailor to this project."
	case AgentMemoryScopeLocal:
		return "Local scope: private to this machine — include machine-specific details freely."
	default:
		return ""
	}
}

// WriteAgentMemory atomically writes content to the canonical MEMORY.md
// entrypoint for (agentType, scope). Creates parent directories as needed
// and trims the content to a single trailing newline. Returns an error
// when the scope is invalid.
func WriteAgentMemory(agentType string, scope AgentMemoryScope, cfg AgentMemoryConfig, content string) error {
	dir := AgentMemoryDir(agentType, scope, cfg)
	if dir == "" {
		return fmt.Errorf("agent memory: invalid scope %q for agent %q", scope, agentType)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("agent memory: mkdir %s: %w", dir, err)
	}
	target := filepath.Join(dir, "MEMORY.md")
	tmp := target + ".tmp"
	trimmed := strings.TrimRight(content, "\n") + "\n"
	if err := os.WriteFile(tmp, []byte(trimmed), 0o644); err != nil {
		return fmt.Errorf("agent memory: write tmp: %w", err)
	}
	if err := os.Rename(tmp, target); err != nil {
		// Clean up the tmp if rename failed.
		_ = os.Remove(tmp)
		return fmt.Errorf("agent memory: rename: %w", err)
	}
	return nil
}

// ReadAgentMemory returns the stored MEMORY.md content for (agentType,
// scope) or "" when the file doesn't exist. Errors other than NotExist
// propagate so callers can distinguish empty vs broken state.
func ReadAgentMemory(agentType string, scope AgentMemoryScope, cfg AgentMemoryConfig) (string, error) {
	entry := AgentMemoryEntrypoint(agentType, scope, cfg)
	if entry == "" {
		return "", nil
	}
	data, err := os.ReadFile(entry)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", nil
		}
		return "", err
	}
	return string(data), nil
}
