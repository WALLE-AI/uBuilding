package memory

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/wall-ai/ubuilding/backend/agents"
)

// ---------------------------------------------------------------------------
// M1.I1 · MemoryType + MemoryFileInfo
// Maps to src/utils/memory/types.ts and MemoryFileInfo in
// src/utils/claudemd.ts.
// ---------------------------------------------------------------------------

// MemoryType identifies which tier a memory file belongs to. The tiers
// are ordered from lowest to highest priority in the aggregate output
// produced by GetMemoryFiles — later tiers override or follow earlier
// ones in the rendered system prompt.
type MemoryType string

const (
	// MemoryTypeManaged is an IDE/MDM-managed instruction file (highest
	// trust, lowest recency). Managed files are exempt from user
	// `claudeMdExcludes` and from the external-include approval prompt.
	MemoryTypeManaged MemoryType = "Managed"

	// MemoryTypeUser is the user's personal ~/.claude/CLAUDE.md.
	MemoryTypeUser MemoryType = "User"

	// MemoryTypeProject is a repository-level CLAUDE.md walked from
	// repo root down to cwd.
	MemoryTypeProject MemoryType = "Project"

	// MemoryTypeLocal is a repo-level CLAUDE.local.md (typically
	// .gitignored). Walked in the same order as Project.
	MemoryTypeLocal MemoryType = "Local"

	// MemoryTypeAutoMem is the auto-managed MEMORY.md entrypoint that
	// the model itself curates (memdir).
	MemoryTypeAutoMem MemoryType = "AutoMem"

	// MemoryTypeTeamMem is the shared team memory entrypoint. Loaded
	// only when team memory is enabled.
	MemoryTypeTeamMem MemoryType = "TeamMem"
)

// AllMemoryTypes enumerates every MemoryType in canonical load order
// (Managed → User → Project → Local → AutoMem → TeamMem). The renderer
// uses this order so that the system prompt mirrors upstream TS.
var AllMemoryTypes = [...]MemoryType{
	MemoryTypeManaged,
	MemoryTypeUser,
	MemoryTypeProject,
	MemoryTypeLocal,
	MemoryTypeAutoMem,
	MemoryTypeTeamMem,
}

// String reports the canonical casing used in logs and debug output.
func (t MemoryType) String() string { return string(t) }

// IsValid returns true iff t is a recognised memory type. The empty
// string is treated as invalid.
func (t MemoryType) IsValid() bool {
	switch t {
	case MemoryTypeManaged, MemoryTypeUser, MemoryTypeProject,
		MemoryTypeLocal, MemoryTypeAutoMem, MemoryTypeTeamMem:
		return true
	}
	return false
}

// ParseMemoryType returns the MemoryType matching raw (case-insensitive).
// Returns ("" , false) if raw does not correspond to a known type.
func ParseMemoryType(raw string) (MemoryType, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", false
	}
	lower := strings.ToLower(trimmed)
	for _, t := range AllMemoryTypes {
		if strings.ToLower(string(t)) == lower {
			return t, true
		}
	}
	return "", false
}

// MarshalJSON emits the canonical-cased string.
func (t MemoryType) MarshalJSON() ([]byte, error) {
	if t == "" {
		return []byte("null"), nil
	}
	return json.Marshal(string(t))
}

// UnmarshalJSON accepts any case and maps to the canonical value.
func (t *MemoryType) UnmarshalJSON(data []byte) error {
	if len(data) == 0 || string(data) == "null" {
		*t = ""
		return nil
	}
	var raw string
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	parsed, ok := ParseMemoryType(raw)
	if !ok {
		return fmt.Errorf("memory: unknown MemoryType %q", raw)
	}
	*t = parsed
	return nil
}

// MemoryFileInfo is a single resolved memory file with the metadata the
// loader collects while walking the hierarchy. Mirrors the MemoryFileInfo
// type in src/utils/claudemd.ts.
type MemoryFileInfo struct {
	// Path is the absolute filesystem path. Always set.
	Path string `json:"path"`

	// Type is the tier this file belongs to.
	Type MemoryType `json:"type"`

	// Parent is the abs path that @-included this file, or "" for
	// entrypoint files reached directly by the hierarchy walker.
	Parent string `json:"parent,omitempty"`

	// Globs holds frontmatter `paths:` globs (already split + brace
	// expanded) for conditional rule files loaded from `.claude/rules/`.
	// nil for non-conditional files.
	Globs []string `json:"globs,omitempty"`

	// RawContent is the verbatim file bytes as read from disk, before
	// frontmatter + HTML-comment stripping or truncation. Kept for
	// tool-attribution and telemetry purposes.
	RawContent string `json:"raw_content,omitempty"`

	// Content is the post-processing content injected into the prompt
	// (frontmatter removed, HTML comments stripped, optional truncation
	// applied).
	Content string `json:"content"`

	// ContentDiffersFromDisk is true when Content != RawContent. The
	// loader sets this so downstream auditing can flag "the model sees
	// a trimmed view of this file".
	ContentDiffersFromDisk bool `json:"content_differs_from_disk,omitempty"`
}

// ---------------------------------------------------------------------------
// M1.I1 · Re-exports for ergonomic downstream use.
// ---------------------------------------------------------------------------

// AgentMemoryScope is re-exported so callers can import only the
// `memory` package. Mirrors agents.AgentMemoryScope.
type AgentMemoryScope = agents.AgentMemoryScope

// Scope-level aliases.
const (
	AgentMemoryScopeNone    = agents.AgentMemoryScopeNone
	AgentMemoryScopeUser    = agents.AgentMemoryScopeUser
	AgentMemoryScopeProject = agents.AgentMemoryScopeProject
	AgentMemoryScopeLocal   = agents.AgentMemoryScopeLocal
)
