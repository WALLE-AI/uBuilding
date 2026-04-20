package memory

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wall-ai/ubuilding/backend/agents"
)

// ---------------------------------------------------------------------------
// M13 · Secret scanner tests.
// ---------------------------------------------------------------------------

func TestScanForSecrets_Clean(t *testing.T) {
	matches := ScanForSecrets("This is a normal markdown file with no secrets.")
	assert.Empty(t, matches)
}

func TestScanForSecrets_AWSKey(t *testing.T) {
	content := "Here is a key: AKIAIOSFODNN7EXAMPLE"
	matches := ScanForSecrets(content)
	require.Len(t, matches, 1)
	assert.Equal(t, "aws-access-token", matches[0].RuleID)
	assert.Equal(t, "AWS Access Token", matches[0].Label)
}

func TestScanForSecrets_GitHubPAT(t *testing.T) {
	content := "token: ghp_xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"
	matches := ScanForSecrets(content)
	require.Len(t, matches, 1)
	assert.Equal(t, "github-pat", matches[0].RuleID)
}

func TestScanForSecrets_SlackBot(t *testing.T) {
	content := "xoxb-1234567890123-1234567890123-abc"
	matches := ScanForSecrets(content)
	require.Len(t, matches, 1)
	assert.Equal(t, "slack-bot-token", matches[0].RuleID)
}

func TestScanForSecrets_PrivateKey(t *testing.T) {
	content := `-----BEGIN RSA PRIVATE KEY-----
MIIEowIBAAKCAQEA0Z3VS5JJcds3xfn/ygWyF3PBfW4OzGemZUwHGU2Dh1VzVGdb
ypWrNPWMfcsSjMhJZlRo3LCBxmFp0a3PAh6j3bDjjp0bF3OSkMGZ4z2F0gP+VGka
+0SHWHblqf4sDTXFMI2eLGeLqoERi8DKTQ1RjKrVFMdoHNWJa3klpg4Tvm1jvkqw
-----END RSA PRIVATE KEY-----`
	matches := ScanForSecrets(content)
	require.Len(t, matches, 1)
	assert.Equal(t, "private-key", matches[0].RuleID)
	assert.Equal(t, "Private Key", matches[0].Label)
}

func TestScanForSecrets_Multiple(t *testing.T) {
	content := "AKIAIOSFODNN7EXAMPLE and ghp_xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"
	matches := ScanForSecrets(content)
	assert.GreaterOrEqual(t, len(matches), 2)
	ruleIDs := make([]string, len(matches))
	for i, m := range matches {
		ruleIDs[i] = m.RuleID
	}
	assert.Contains(t, ruleIDs, "aws-access-token")
	assert.Contains(t, ruleIDs, "github-pat")
}

func TestScanForSecrets_Dedup(t *testing.T) {
	content := "AKIAIOSFODNN7EXAMPLE and also AKIAIOSFODNN7EXAMPLE2"
	matches := ScanForSecrets(content)
	// Only one match per rule ID.
	awsCount := 0
	for _, m := range matches {
		if m.RuleID == "aws-access-token" {
			awsCount++
		}
	}
	assert.Equal(t, 1, awsCount)
}

func TestScanForSecrets_StripeKey(t *testing.T) {
	content := "sk_test_1234567890abcdefghij"
	matches := ScanForSecrets(content)
	require.Len(t, matches, 1)
	assert.Equal(t, "stripe-access-token", matches[0].RuleID)
}

func TestScanForSecrets_NPMToken(t *testing.T) {
	content := "npm_abcdefghijklmnopqrstuvwxyz1234567890"
	matches := ScanForSecrets(content)
	require.Len(t, matches, 1)
	assert.Equal(t, "npm-access-token", matches[0].RuleID)
}

// ---------------------------------------------------------------------------
// RuleIDToLabel tests.
// ---------------------------------------------------------------------------

func TestRuleIDToLabel(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"github-pat", "GitHub PAT"},
		{"aws-access-token", "AWS Access Token"},
		{"private-key", "Private Key"},
		{"openai-api-key", "OpenAI API Key"},
		{"unknown-rule", "Unknown Rule"},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, RuleIDToLabel(tt.input), "input=%q", tt.input)
	}
}

// ---------------------------------------------------------------------------
// M13 · Write-allowlist tests.
// ---------------------------------------------------------------------------

func TestIsAutoMemWriteAllowed_Enabled(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(EnvCoworkMemoryPathOverride, dir)
	t.Setenv(EnvEnableAutoMemory, "1")
	defer ResetMemoryBaseDirCache()

	// Clear override so the carve-out fires.
	t.Setenv(EnvCoworkMemoryPathOverride, "")

	cfg := agents.EngineConfig{AutoMemoryEnabled: true}

	// Path under auto-mem should be allowed.
	autoDir := GetAutoMemPath(dir, NopSettingsProvider)
	if autoDir == "" {
		t.Skip("cannot resolve auto-mem path in test env")
	}
	targetPath := filepath.Join(strings.TrimRight(autoDir, `/\`), "note.md")
	result := IsAutoMemWriteAllowed(targetPath, dir, NopSettingsProvider, cfg)
	assert.Equal(t, "allow", result.Behavior)
	assert.Contains(t, result.Reason, "auto memory")
}

func TestIsAutoMemWriteAllowed_Disabled(t *testing.T) {
	cfg := agents.EngineConfig{AutoMemoryEnabled: false}
	result := IsAutoMemWriteAllowed("/some/path", "/cwd", NopSettingsProvider, cfg)
	assert.Equal(t, "", result.Behavior, "should not allow when auto-memory is off")
}

func TestIsAutoMemWriteAllowed_WithOverride(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(EnvCoworkMemoryPathOverride, dir)
	t.Setenv(EnvEnableAutoMemory, "1")
	defer ResetMemoryBaseDirCache()

	cfg := agents.EngineConfig{AutoMemoryEnabled: true}

	// Even though path is under auto-mem, override disables carve-out.
	result := IsAutoMemWriteAllowed(filepath.Join(dir, "note.md"), dir, NopSettingsProvider, cfg)
	assert.Equal(t, "", result.Behavior, "should not allow when override is set")
}

func TestIsAutoMemWriteAllowed_OutsidePath(t *testing.T) {
	t.Setenv(EnvEnableAutoMemory, "1")
	t.Setenv(EnvCoworkMemoryPathOverride, "")
	defer ResetMemoryBaseDirCache()

	cfg := agents.EngineConfig{AutoMemoryEnabled: true}
	result := IsAutoMemWriteAllowed("/completely/other/path.md", "/cwd", NopSettingsProvider, cfg)
	assert.Equal(t, "", result.Behavior, "should not allow outside auto-mem path")
}

// ---------------------------------------------------------------------------
// M13 · CheckTeamMemSecrets tests.
// ---------------------------------------------------------------------------

func TestCheckTeamMemSecrets_Disabled(t *testing.T) {
	cfg := agents.EngineConfig{}
	msg := CheckTeamMemSecrets("/some/file.md", "AKIAIOSFODNN7EXAMPLE", "/cwd", NopSettingsProvider, cfg)
	assert.Empty(t, msg, "should be empty when team memory is disabled")
}

func TestCheckTeamMemSecrets_NotTeamPath(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(EnvCoworkMemoryPathOverride, dir)
	t.Setenv(EnvEnableAutoMemory, "1")
	defer ResetMemoryBaseDirCache()

	cfg := agents.EngineConfig{AutoMemoryEnabled: true, TeamMemoryEnabled: true}
	msg := CheckTeamMemSecrets("/random/file.md", "AKIAIOSFODNN7EXAMPLE", dir, NopSettingsProvider, cfg)
	assert.Empty(t, msg, "should be empty for non-team path")
}

func TestCheckTeamMemSecrets_CleanContent(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(EnvCoworkMemoryPathOverride, dir)
	t.Setenv(EnvEnableAutoMemory, "1")
	defer ResetMemoryBaseDirCache()

	cfg := agents.EngineConfig{AutoMemoryEnabled: true, TeamMemoryEnabled: true}
	teamDir := GetTeamMemPath(dir, NopSettingsProvider)
	if teamDir == "" {
		t.Skip("cannot resolve team-mem path")
	}
	teamFile := filepath.Join(strings.TrimRight(teamDir, `/\`), "clean.md")
	msg := CheckTeamMemSecrets(teamFile, "Just a normal note", dir, NopSettingsProvider, cfg)
	assert.Empty(t, msg)
}

func TestCheckTeamMemSecrets_WithSecret(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(EnvCoworkMemoryPathOverride, dir)
	t.Setenv(EnvEnableAutoMemory, "1")
	defer ResetMemoryBaseDirCache()

	cfg := agents.EngineConfig{AutoMemoryEnabled: true, TeamMemoryEnabled: true}
	teamDir := GetTeamMemPath(dir, NopSettingsProvider)
	if teamDir == "" {
		t.Skip("cannot resolve team-mem path")
	}
	teamFile := filepath.Join(strings.TrimRight(teamDir, `/\`), "secret.md")

	content := "Here is a key: AKIAIOSFODNN7EXAMPLE"
	msg := CheckTeamMemSecrets(teamFile, content, dir, NopSettingsProvider, cfg)
	assert.Contains(t, msg, "secrets")
	assert.Contains(t, msg, "AWS Access Token")
	assert.Contains(t, msg, "team memory")
}
