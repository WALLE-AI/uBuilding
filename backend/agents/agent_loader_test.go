package agents

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// -----------------------------------------------------------------------------
// A04 · markdown frontmatter loader
// -----------------------------------------------------------------------------

const mdReviewer = `---
description: Review pull requests
name: reviewer
tools:
  - Read
  - Grep
disallowedTools: Bash, Write
model: inherit
permissionMode: plan
maxTurns: 5
memory: project
omitClaudeMd: true
mcpServers:
  - slack
  - slack-dev:
      type: stdio
      command: slack-mcp
---
You are a code reviewer. Always respond in terse bullet points.`

const mdMissingDesc = `---
name: broken
tools: Read
---
Body here.`

const mdNoBody = `---
description: bad
name: bad
---
`

const mdPromptOnly = `---
description: Simple agent
---
Hello world.`

func TestParseMarkdownAgent_Reviewer(t *testing.T) {
	def, err := parseMarkdownAgent("/repo/.claude/agents/reviewer.md", AgentSourceProject, mdReviewer)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if def.AgentType != "reviewer" {
		t.Errorf("agentType = %q", def.AgentType)
	}
	if def.WhenToUse != "Review pull requests" {
		t.Errorf("whenToUse = %q", def.WhenToUse)
	}
	if len(def.Tools) != 2 || def.Tools[0] != "Read" || def.Tools[1] != "Grep" {
		t.Errorf("tools = %v", def.Tools)
	}
	if len(def.DisallowedTools) != 2 {
		t.Errorf("disallowed = %v", def.DisallowedTools)
	}
	if def.Model != "inherit" || def.PermissionMode != "plan" || def.MaxTurns != 5 {
		t.Errorf("scalars: %+v", def)
	}
	if def.Memory != AgentMemoryScopeProject || !def.OmitClaudeMd {
		t.Errorf("flags: %+v", def)
	}
	if len(def.MCPServers) != 2 {
		t.Fatalf("mcp servers = %v", def.MCPServers)
	}
	if def.MCPServers[0].ByName != "slack" {
		t.Errorf("first mcp: %+v", def.MCPServers[0])
	}
	if _, ok := def.MCPServers[1].Inline["slack-dev"]; !ok {
		t.Errorf("second mcp: %+v", def.MCPServers[1])
	}
	if def.Source != AgentSourceProject {
		t.Errorf("source = %v", def.Source)
	}
	got := def.RenderSystemPrompt(SystemPromptCtx{})
	if !strings.Contains(got, "terse bullet points") {
		t.Errorf("prompt body missing: %q", got)
	}
}

func TestParseMarkdownAgent_MissingDescription(t *testing.T) {
	_, err := parseMarkdownAgent("x.md", AgentSourceUser, mdMissingDesc)
	if err == nil || !strings.Contains(err.Error(), "description") {
		t.Fatalf("expected description error, got %v", err)
	}
}

func TestParseMarkdownAgent_MissingBody(t *testing.T) {
	_, err := parseMarkdownAgent("x.md", AgentSourceUser, mdNoBody)
	if err == nil || !strings.Contains(err.Error(), "prompt body") {
		t.Fatalf("expected body error, got %v", err)
	}
}

func TestParseMarkdownAgent_PromptOnly(t *testing.T) {
	def, err := parseMarkdownAgent("/tmp/simple.md", AgentSourceUser, mdPromptOnly)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if def.AgentType != "simple" {
		t.Errorf("agentType derived from filename expected; got %q", def.AgentType)
	}
	if def.WhenToUse != "Simple agent" {
		t.Errorf("whenToUse = %q", def.WhenToUse)
	}
}

func TestLoadAgentsFromDir_MultipleAndErrors(t *testing.T) {
	dir := t.TempDir()
	must := func(name, content string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	must("reviewer.md", mdReviewer)
	must("simple.md", mdPromptOnly)
	must("broken.md", mdMissingDesc)
	must("notes.txt", "ignored")

	defs, errs := loadAgentsFromDir(dir, AgentSourceProject)
	if len(defs) != 2 {
		t.Fatalf("want 2 defs, got %d", len(defs))
	}
	if len(errs) != 1 {
		t.Fatalf("want 1 err, got %d (%v)", len(errs), errs)
	}
	if !strings.Contains(errs[0].Err.Error(), "description") {
		t.Errorf("unexpected err: %v", errs[0].Err)
	}
}

// -----------------------------------------------------------------------------
// A05 · agents.json loader
// -----------------------------------------------------------------------------

const agentsJSONValid = `{
  "builder": {
    "description": "Builds features",
    "prompt": "You are the builder.",
    "tools": ["Read", "Edit"],
    "model": "inherit"
  },
  "researcher": {
    "description": "Does research",
    "prompt": "You are the researcher.",
    "memory": "user",
    "effort": "high"
  }
}`

const agentsJSONBad = `{
  "oops": { "description": "", "prompt": "body" },
  "empty": { "description": "ok", "prompt": "" }
}`

func TestParseJSONAgents_Valid(t *testing.T) {
	defs, errs := parseJSONAgents("/x/agents.json", AgentSourceUser, []byte(agentsJSONValid))
	if len(errs) != 0 {
		t.Fatalf("unexpected errs: %v", errs)
	}
	if len(defs) != 2 {
		t.Fatalf("want 2 defs, got %d", len(defs))
	}
	byType := map[string]*AgentDefinition{}
	for _, d := range defs {
		byType[d.AgentType] = d
	}
	if byType["builder"].Tools[1] != "Edit" {
		t.Errorf("builder tools: %+v", byType["builder"].Tools)
	}
	if byType["researcher"].Effort.Name != "high" {
		t.Errorf("researcher effort: %+v", byType["researcher"].Effort)
	}
}

func TestParseJSONAgents_ValidationErrors(t *testing.T) {
	_, errs := parseJSONAgents("/x/agents.json", AgentSourceUser, []byte(agentsJSONBad))
	if len(errs) != 2 {
		t.Fatalf("want 2 errs, got %d (%v)", len(errs), errs)
	}
}

func TestParseJSONAgents_BadShape(t *testing.T) {
	_, errs := parseJSONAgents("/x/agents.json", AgentSourceUser, []byte(`not json`))
	if len(errs) != 1 {
		t.Fatalf("want 1 err, got %d", len(errs))
	}
}

// -----------------------------------------------------------------------------
// A06 · resolver priority
// -----------------------------------------------------------------------------

func TestResolveActiveAgents_Priority(t *testing.T) {
	tmp := t.TempDir()
	projectDir := filepath.Join(tmp, "proj", ".claude", "agents")
	userDir := filepath.Join(tmp, "user", "agents")
	policyDir := filepath.Join(tmp, "policy", "agents")
	for _, d := range []string{projectDir, userDir, policyDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	userMD := `---
description: User reviewer
---
user version`
	projectMD := `---
description: Project reviewer
---
project version`
	policyMD := `---
description: Policy reviewer
---
policy version`
	if err := os.WriteFile(filepath.Join(userDir, "reviewer.md"), []byte(userMD), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, "reviewer.md"), []byte(projectMD), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(policyDir, "reviewer.md"), []byte(policyMD), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := LoaderConfig{
		ProjectRoot:     filepath.Join(tmp, "proj"),
		UserConfigDir:   filepath.Join(tmp, "user"),
		PolicyConfigDir: filepath.Join(tmp, "policy"),
		IncludeBuiltIn:  true,
	}
	t.Setenv("UBUILDING_DISABLE_BUILTIN_AGENTS", "")

	defs, errs := ResolveActiveAgents(cfg)
	if len(errs) != 0 {
		t.Fatalf("load errs: %v", errs)
	}

	active := defs.FindActive("reviewer")
	if active == nil {
		t.Fatal("reviewer missing")
	}
	if active.WhenToUse != "Policy reviewer" {
		t.Fatalf("policy priority lost: %q", active.WhenToUse)
	}
	if active.Source != AgentSourcePolicy {
		t.Fatalf("source = %v", active.Source)
	}

	// Built-ins still present at lower priority.
	if defs.FindActive("general-purpose") == nil {
		t.Error("general-purpose built-in missing")
	}

	// AllAgents contains all 3 + built-ins; ActiveAgents de-duplicated.
	if len(defs.AllAgents) < 4 {
		t.Errorf("AllAgents len = %d (want >=4 with built-ins)", len(defs.AllAgents))
	}
}

func TestResolveActiveAgents_EnvDisableBuiltIn(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("UBUILDING_DISABLE_BUILTIN_AGENTS", "1")
	cfg := LoaderConfig{
		ProjectRoot:   tmp,
		UserConfigDir: "",
	}
	defs, _ := ResolveActiveAgents(cfg)
	if len(defs.ActiveAgents) != 0 {
		t.Fatalf("want 0 active, got %d", len(defs.ActiveAgents))
	}
}

func TestResolveActiveAgents_ExtraSearchPaths(t *testing.T) {
	tmp := t.TempDir()
	extra := filepath.Join(tmp, "extra")
	if err := os.MkdirAll(extra, 0o755); err != nil {
		t.Fatal(err)
	}
	md := `---
description: Extra agent
---
extra body`
	if err := os.WriteFile(filepath.Join(extra, "helper.md"), []byte(md), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := LoaderConfig{
		ProjectRoot:      tmp,
		ExtraSearchPaths: []string{extra},
		IncludeBuiltIn:   false,
	}
	t.Setenv("UBUILDING_DISABLE_BUILTIN_AGENTS", "1")
	defs, errs := ResolveActiveAgents(cfg)
	if len(errs) != 0 {
		t.Fatalf("errs: %v", errs)
	}
	if defs.FindActive("helper") == nil {
		t.Fatal("helper missing from extra search path")
	}
}

func TestResolveActiveAgents_AgentsJSON(t *testing.T) {
	tmp := t.TempDir()
	userAgents := filepath.Join(tmp, "user", "agents")
	if err := os.MkdirAll(userAgents, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(userAgents, "agents.json"), []byte(agentsJSONValid), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := LoaderConfig{
		ProjectRoot:   tmp,
		UserConfigDir: filepath.Join(tmp, "user"),
		IncludeBuiltIn: false,
	}
	t.Setenv("UBUILDING_DISABLE_BUILTIN_AGENTS", "1")
	defs, errs := ResolveActiveAgents(cfg)
	if len(errs) != 0 {
		t.Fatalf("errs: %v", errs)
	}
	if defs.FindActive("builder") == nil || defs.FindActive("researcher") == nil {
		t.Fatalf("json agents missing: %+v", defs.ActiveTypes())
	}
	defs.RefreshLegacy()
	if len(defs.ActiveLegacy) < 2 {
		t.Errorf("legacy projection: %+v", defs.ActiveLegacy)
	}
}
