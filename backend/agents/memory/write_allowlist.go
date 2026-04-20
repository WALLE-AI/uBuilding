package memory

import (
	"fmt"
	"strings"

	"github.com/wall-ai/ubuilding/backend/agents"
)

// ---------------------------------------------------------------------------
// M13.I2 · Write-allowlist carve-outs for memory files.
//
// Ports the relevant permission logic from:
//   - src/utils/permissions/filesystem.ts (isAutoMemPath carve-out)
//   - src/services/teamMemorySync/teamMemSecretGuard.ts (secret guard)
//
// These predicates are called by the tool permission layer (Write/Edit/Bash)
// to allow silent writes to memory files without prompting the user,
// while blocking secrets from leaking into team memory.
// ---------------------------------------------------------------------------

// PermissionDecision represents the outcome of a write-permission check.
type PermissionDecision struct {
	// Behavior is "allow", "deny", or "" (no decision — fall through).
	Behavior string
	// Reason is a human-readable explanation for the decision.
	Reason string
}

// IsAutoMemWriteAllowed checks if a write to the given path should be
// silently allowed because it targets the auto-memory directory.
//
// Mirrors the TS carve-out in filesystem.ts:
//
//	if (!hasAutoMemPathOverride() && isAutoMemPath(normalizedPath))
//	  → allow, reason: "auto memory files are allowed for writing"
//
// When the COWORK_MEMORY_PATH_OVERRIDE env var is set, the carve-out is
// disabled — SDK callers should pass an explicit allow rule instead.
func IsAutoMemWriteAllowed(
	normalizedPath, cwd string,
	settings SettingsProvider,
	cfg agents.EngineConfig,
) PermissionDecision {
	if !IsAutoMemoryEnabled(cfg, settings) {
		return PermissionDecision{}
	}
	if HasAutoMemPathOverride() {
		return PermissionDecision{}
	}
	if !IsAutoMemPath(normalizedPath, cwd, settings) {
		return PermissionDecision{}
	}
	return PermissionDecision{
		Behavior: "allow",
		Reason:   "auto memory files are allowed for writing",
	}
}

// CheckTeamMemSecrets checks if a file write/edit to a team memory path
// contains secrets. Returns an error message if secrets are detected,
// or "" if safe.
//
// Callers can invoke this unconditionally — when team memory is not
// enabled, the function returns "".
//
// Mirrors TS `checkTeamMemSecrets` in teamMemSecretGuard.ts.
func CheckTeamMemSecrets(
	filePath, content, cwd string,
	settings SettingsProvider,
	cfg agents.EngineConfig,
) string {
	if !IsTeamMemoryEnabled(cfg, settings) {
		return ""
	}
	if !IsTeamMemPath(filePath, cwd, settings) {
		return ""
	}

	matches := ScanForSecrets(content)
	if len(matches) == 0 {
		return ""
	}

	labels := make([]string, len(matches))
	for i, m := range matches {
		labels[i] = m.Label
	}
	return fmt.Sprintf(
		"Content contains potential secrets (%s) and cannot be written to team memory. "+
			"Team memory is shared with all repository collaborators. "+
			"Remove the sensitive content and try again.",
		strings.Join(labels, ", "))
}
