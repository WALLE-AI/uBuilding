package agents_test

// Real-LLM integration tests for the three built-in agent modes:
//   general-purpose · Explore · Plan
//
// Each sub-test spawns a child QueryEngine via engine.SpawnSubAgent so the
// child's system prompt, tool pool, and PermissionMode overlay are exercised
// through the same path the production code takes. Tool calls made by the
// child accumulate in the shared toolsDeps.toolCallLog because the child
// inherits the parent's deps pointer.
//
// Gate: INTEGRATION=1 (same convention as tools_real_llm_test.go).
// Subset: AGENT_MODES_SUBSET=Explore runs only the Explore sub-test.
//
// Timeouts per sub-test:
//   general-purpose — 120 s (file-read + report)
//   Explore         — 120 s (glob/grep search)
//   Plan            — 180 s (multi-step analysis + structured output)

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wall-ai/ubuilding/backend/agents"
	"github.com/wall-ai/ubuilding/backend/agents/tool"
	"github.com/wall-ai/ubuilding/backend/agents/tool/builtin"
	"github.com/wall-ai/ubuilding/backend/agents/tool/webfetch"
)

// ---------------------------------------------------------------------------
// Parent test — gated on INTEGRATION=1
// ---------------------------------------------------------------------------

func TestIntegration_RealLLM_AgentModes(t *testing.T) {
	env := setupRealLLMEnv(t)

	t.Run("GeneralPurpose", func(t *testing.T) {
		agentModeSubsetFilter(t, "GeneralPurpose")
		testRealAgentMode_GeneralPurpose(t, env)
	})
	t.Run("Explore", func(t *testing.T) {
		agentModeSubsetFilter(t, "Explore")
		testRealAgentMode_Explore(t, env)
	})
	t.Run("Plan", func(t *testing.T) {
		agentModeSubsetFilter(t, "Plan")
		testRealAgentMode_Plan(t, env)
	})
}

// agentModeSubsetFilter honours AGENT_MODES_SUBSET to narrow which sub-tests run.
func agentModeSubsetFilter(t *testing.T, name string) {
	t.Helper()
	raw := os.Getenv("AGENT_MODES_SUBSET")
	if raw == "" {
		return
	}
	for _, part := range strings.Split(raw, ",") {
		if strings.EqualFold(strings.TrimSpace(part), name) {
			return
		}
	}
	t.Skipf("Skipping %s (not in AGENT_MODES_SUBSET=%s)", name, raw)
}

// ---------------------------------------------------------------------------
// Fixture helper — engine with real tools + ResolveSubagentTools wired
// ---------------------------------------------------------------------------

// agentModeEngine builds a parent QueryEngine whose SpawnSubAgent call
// will create child engines with the correct tool pool and system prompt
// for each AgentDefinition. The registry is pre-populated with the full
// builtin tool set scoped to dir.
//
// ResolveSubagentTools is wired so that Explore/Plan agents actually receive
// their reduced pool (Edit/Write/Task stripped) when the child engine is
// configured.
func agentModeEngine(t *testing.T, env *realLLMEnv, dir string) (*agents.QueryEngine, *toolsDeps) {
	t.Helper()

	reg := tool.NewRegistry()
	builtin.RegisterAll(reg, builtin.Options{
		WorkspaceRoots:  []string{dir},
		WebFetchOptions: []webfetch.Option{webfetch.WithAllowLoopback()},
	})

	deps := &toolsDeps{
		realDeps: realDeps{provider: env.provider, model: env.model},
		registry: reg,
	}

	// parentTools converts the registry into the []interface{} pool that
	// EngineConfig.Tools holds. We materialise it once; child configs will
	// draw from this slice via ResolveSubagentTools.
	regTools := reg.GetTools()
	parentTools := make([]interface{}, 0, len(regTools))
	for _, t := range regTools {
		parentTools = append(parentTools, t)
	}

	cfg := agents.EngineConfig{
		Cwd:                dir,
		UserSpecifiedModel: env.model,
		MaxTurns:           10,
		Tools:              parentTools,

		// Wire tool resolution so child engines for Explore/Plan get their
		// restricted pool (DisallowedTools applied against the parent set).
		ResolveSubagentTools: func(parent []interface{}, def *agents.AgentDefinition, isAsync bool) []interface{} {
			converted := make(tool.Tools, 0, len(parent))
			for _, t := range parent {
				if v, ok := t.(tool.Tool); ok {
					converted = append(converted, v)
				}
			}
			resolved := tool.ResolveAgentTools(converted, def, isAsync, false)
			out := make([]interface{}, 0, len(resolved.ResolvedTools))
			for _, t := range resolved.ResolvedTools {
				out = append(out, t)
			}
			return out
		},
	}

	engine := agents.NewQueryEngine(cfg, deps)
	return engine, deps
}

// spawnAndDrain calls engine.SpawnSubAgent and returns (result, toolsCalledByChild, err).
// toolsCalledByChild is the slice of tool names appended to deps.toolCallLog
// during the spawn (child shares the same deps pointer as parent).
func spawnAndDrain(
	ctx context.Context,
	engine *agents.QueryEngine,
	deps *toolsDeps,
	params agents.SubAgentParams,
) (result string, childTools []string, err error) {
	before := len(deps.toolCallLog)
	result, err = engine.SpawnSubAgent(ctx, params)
	childTools = append([]string{}, deps.toolCallLog[before:]...)
	return
}

// ---------------------------------------------------------------------------
// Sub-test: general-purpose
// ---------------------------------------------------------------------------

// testRealAgentMode_GeneralPurpose verifies that the general-purpose agent:
//  1. Receives its own system prompt (broad instructions, full tool access).
//  2. Uses at least one read tool to satisfy a file-content query.
//  3. Returns a response containing the sentinel value planted in the file.
func testRealAgentMode_GeneralPurpose(t *testing.T, env *realLLMEnv) {
	dir := t.TempDir()
	sentinel := fmt.Sprintf("GPSENTINEL%d", time.Now().UnixNano())
	target := filepath.Join(dir, "data.txt")
	require.NoError(t, os.WriteFile(target, []byte(sentinel+"\nsome other content\n"), 0o644))

	engine, deps := agentModeEngine(t, env, dir)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	prompt := fmt.Sprintf(
		"Read the file at path %s and report its exact first line. Include the full line verbatim in your answer.",
		target,
	)

	result, childTools, err := spawnAndDrain(ctx, engine, deps, agents.SubAgentParams{
		SubagentType: "general-purpose",
		Prompt:       prompt,
		Description:  "read file and report sentinel",
	})

	t.Logf("[GeneralPurpose] tools=%v result=%q err=%v", childTools, truncateForLog(result, 300), err)

	if err != nil {
		t.Fatalf("SpawnSubAgent(general-purpose) error: %v", err)
	}

	// Soft-assert: at least one read-type tool should have been invoked.
	readToolCalled := false
	for _, name := range childTools {
		if name == "Read" || name == "Grep" || name == "Glob" || name == "Bash" {
			readToolCalled = true
			break
		}
	}
	if !readToolCalled {
		t.Logf("WARN: no read-type tool observed in child (tools=%v) — model may have answered from prompt context", childTools)
	}

	// Hard-assert: result must contain the sentinel.
	assert.Contains(t, result, sentinel,
		"general-purpose agent must surface the sentinel from the file")
}

// ---------------------------------------------------------------------------
// Sub-test: Explore
// ---------------------------------------------------------------------------

// testRealAgentMode_Explore verifies that the Explore agent:
//  1. Operates in read-only mode (no Edit/Write calls).
//  2. Uses search tools (Glob / Grep / Read) to locate a sentinel in the workspace.
//  3. Returns a response that references the target file or its content.
func testRealAgentMode_Explore(t *testing.T, env *realLLMEnv) {
	dir := t.TempDir()
	sentinel := fmt.Sprintf("EXPLORETOKEN%d", time.Now().UnixNano())

	// Populate a small workspace so the Explore agent has something to search.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.go"),
		[]byte("package main\n\nfunc main() {}\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.go"),
		[]byte(fmt.Sprintf("package main\n\nconst Token = %q\n", sentinel)), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "internal"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "internal", "util.go"),
		[]byte("package internal\n\nfunc Noop() {}\n"), 0o644))

	engine, deps := agentModeEngine(t, env, dir)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	prompt := fmt.Sprintf(
		"Search the workspace under %s for the string %q. "+
			"Report which file contains it and quote the line where it appears. "+
			"Thoroughness level: medium.",
		dir, sentinel,
	)

	result, childTools, err := spawnAndDrain(ctx, engine, deps, agents.SubAgentParams{
		SubagentType: "Explore",
		Prompt:       prompt,
		Description:  "locate sentinel token in workspace",
	})

	t.Logf("[Explore] tools=%v result=%q err=%v", childTools, truncateForLog(result, 300), err)

	if err != nil {
		t.Fatalf("SpawnSubAgent(Explore) error: %v", err)
	}

	// Hard-assert: Explore must never call Edit/Write/NotebookEdit.
	for _, name := range childTools {
		if name == "Edit" || name == "Write" || name == "NotebookEdit" {
			t.Errorf("Explore agent called prohibited write tool %q — read-only contract violated", name)
		}
	}

	// Soft-assert: at least one search tool should appear.
	searchToolCalled := false
	for _, name := range childTools {
		if name == "Grep" || name == "Glob" || name == "Read" || name == "Bash" {
			searchToolCalled = true
			break
		}
	}
	if !searchToolCalled {
		t.Logf("WARN: Explore agent called no search tools (tools=%v) — model may have refused or answered from context", childTools)
	}

	// Soft-assert: result should reference the sentinel or the file that holds it.
	if !strings.Contains(result, sentinel) && !strings.Contains(strings.ToLower(result), "config.go") {
		t.Logf("WARN: Explore result does not clearly identify the sentinel location. result=%q", truncateForLog(result, 400))
	}
}

// ---------------------------------------------------------------------------
// Sub-test: Plan
// ---------------------------------------------------------------------------

// testRealAgentMode_Plan verifies that the Plan agent:
//  1. Operates in read-only mode (no Edit/Write calls).
//  2. Explores the workspace before producing a plan.
//  3. Returns structured output that includes implementation steps and a
//     "Critical Files" section (as required by its system prompt).
func testRealAgentMode_Plan(t *testing.T, env *realLLMEnv) {
	dir := t.TempDir()

	// Minimal Go project scaffold for the agent to analyse.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.go"),
		[]byte("package main\n\nimport \"net/http\"\n\nfunc main() { http.ListenAndServe(\":8080\", nil) }\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "handler.go"),
		[]byte("package main\n\nimport \"net/http\"\n\nfunc pingHandler(w http.ResponseWriter, _ *http.Request) {\n\tw.Write([]byte(\"pong\"))\n}\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"),
		[]byte("module example.com/demo\n\ngo 1.22\n"), 0o644))

	engine, deps := agentModeEngine(t, env, dir)

	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	prompt := fmt.Sprintf(
		"You are given a small Go HTTP server in directory %s. "+
			"Design an implementation plan to add a /healthz endpoint that returns "+
			`{"status":"ok"} as JSON. `+
			"Follow the plan format required by your instructions.",
		dir,
	)

	result, childTools, err := spawnAndDrain(ctx, engine, deps, agents.SubAgentParams{
		SubagentType: "Plan",
		Prompt:       prompt,
		Description:  "plan healthz endpoint",
	})

	t.Logf("[Plan] tools=%v result=%q err=%v", childTools, truncateForLog(result, 500), err)

	if err != nil {
		t.Fatalf("SpawnSubAgent(Plan) error: %v", err)
	}

	// Hard-assert: Plan must never call Edit/Write/NotebookEdit.
	for _, name := range childTools {
		if name == "Edit" || name == "Write" || name == "NotebookEdit" {
			t.Errorf("Plan agent called prohibited write tool %q — read-only contract violated", name)
		}
	}

	// Soft-assert: result should include structured plan markers.
	lower := strings.ToLower(result)
	hasPlanContent := strings.Contains(lower, "step") ||
		strings.Contains(lower, "implement") ||
		strings.Contains(lower, "handler") ||
		strings.Contains(lower, "healthz")
	if !hasPlanContent {
		t.Logf("WARN: Plan result may be missing structured plan content. result=%q", truncateForLog(result, 500))
	}

	// Soft-assert: "Critical Files" section is part of the Plan agent's required output.
	if !strings.Contains(strings.ToLower(result), "critical") &&
		!strings.Contains(strings.ToLower(result), "files") {
		t.Logf("WARN: Plan result may be missing 'Critical Files' section — check system prompt compliance")
	}

	// At minimum the result must be non-empty.
	require.NotEmpty(t, strings.TrimSpace(result),
		"Plan agent must return a non-empty plan")
}
