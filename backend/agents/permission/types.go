package permission

// ---------------------------------------------------------------------------
// Permission types — maps to TypeScript permissions/ module
// ---------------------------------------------------------------------------

// Mode represents the current permission operating mode.
type Mode string

const (
	// ModeDefault requires explicit approval for destructive operations.
	ModeDefault Mode = "default"

	// ModeAutoAccept automatically accepts all permission requests.
	ModeAutoAccept Mode = "auto_accept"

	// ModeBypassAll bypasses all permission checks (dangerous).
	ModeBypassAll Mode = "bypass_all"
)

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
