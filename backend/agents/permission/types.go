package permission

// ---------------------------------------------------------------------------
// Permission types — maps to TypeScript permissions/ module
// ---------------------------------------------------------------------------

// Mode represents the current permission operating mode. Matches the
// five-state enum in src/utils/permissions/PermissionMode.ts. Older aliases
// (ModeAutoAccept, ModeBypassAll) are retained for source compatibility and
// map to the TS canonical spellings.
type Mode string

const (
	// ModeDefault requires explicit approval for destructive operations.
	ModeDefault Mode = "default"

	// ModePlan — PLAN MODE: no file mutations, Tasks may still run. Used by
	// the Plan built-in agent and /plan slash flows.
	ModePlan Mode = "plan"

	// ModeAcceptEdits — user pre-approves edit tools (FileEdit/Write/NotebookEdit)
	// but destructive bash / MCP still prompt.
	ModeAcceptEdits Mode = "acceptEdits"

	// ModeBypassPermissions — caller takes full responsibility; every tool
	// runs without prompts.
	ModeBypassPermissions Mode = "bypassPermissions"

	// ModeAuto — TS's "auto" mode delegates to a runtime classifier.
	// Currently a feature-gated experimental mode; we surface the constant
	// so code can round-trip a value even when the classifier is absent.
	ModeAuto Mode = "auto"

	// --- legacy aliases -----------------------------------------------------

	// ModeAutoAccept is the legacy name for ModeAcceptEdits. Kept so pre-B10
	// callers continue to compile; prefer ModeAcceptEdits in new code.
	ModeAutoAccept Mode = "auto_accept"

	// ModeBypassAll is the legacy name for ModeBypassPermissions. Prefer
	// ModeBypassPermissions going forward.
	ModeBypassAll Mode = "bypass_all"
)

// NormalizeMode collapses legacy aliases onto the canonical claude-code
// spellings so callers can compare modes safely regardless of source.
//
//	"auto_accept" → "acceptEdits"
//	"bypass_all"  → "bypassPermissions"
//
// Any other value (including "default", "plan", etc.) is returned unchanged.
func NormalizeMode(m Mode) Mode {
	switch m {
	case ModeAutoAccept:
		return ModeAcceptEdits
	case ModeBypassAll:
		return ModeBypassPermissions
	default:
		return m
	}
}

// IsLaxerThanDefault reports whether m grants more than ModeDefault — used
// by sub-agent mode overriding to avoid downgrading a parent that already
// opted into acceptEdits / bypassPermissions.
func (m Mode) IsLaxerThanDefault() bool {
	switch NormalizeMode(m) {
	case ModeAcceptEdits, ModeBypassPermissions:
		return true
	}
	return false
}

// Result represents the outcome of a permission check.
type Result struct {
	Behavior string `json:"behavior"` // "allow" | "deny" | "ask"
	Message  string `json:"message,omitempty"`
}

// Allowed returns true if the permission was granted.
func (r *Result) Allowed() bool {
	return r.Behavior == "allow"
}

// Denied returns true if the permission was denied.
func (r *Result) Denied() bool {
	return r.Behavior == "deny"
}

// Rule defines a single permission rule for tool matching.
type Rule struct {
	// Tool is the tool name (or "*" for all tools).
	Tool string `json:"tool"`

	// Pattern is an optional pattern for tool-specific matching (e.g., "git *" for Bash).
	Pattern string `json:"pattern,omitempty"`
}
