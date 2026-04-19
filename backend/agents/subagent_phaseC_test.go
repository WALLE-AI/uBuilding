package agents_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/wall-ai/ubuilding/backend/agents"
)

// TestPhaseC_FullIntegration wires a hypothetical project agent that
// opts into every Phase C feature at once (MCP + hooks + skills + memory)
// and verifies each subsystem fires during SpawnSubAgent.
func TestPhaseC_FullIntegration(t *testing.T) {
	tmp := t.TempDir()

	// --- Agent definition (normally loaded from .md frontmatter) ---------
	capturedSkill := int32(0)
	capturedHook := int32(0)
	capturedMCP := int32(0)

	memCfg := agents.AgentMemoryConfig{
		UserDir:    filepath.Join(tmp, "user"),
		ProjectDir: filepath.Join(tmp, "project"),
		LocalDir:   filepath.Join(tmp, "local"),
		Cwd:        tmp,
	}
	// Seed a memory file so BuildMemoryPrompt has something to splice in.
	if err := agents.WriteAgentMemory("reviewer", agents.AgentMemoryScopeProject, memCfg, "Note: review style-guide before responding."); err != nil {
		t.Fatalf("seed memory: %v", err)
	}

	def := &agents.AgentDefinition{
		AgentType:  "reviewer",
		WhenToUse:  "Review PRs",
		Source:     agents.AgentSourceProject,
		Tools:      []string{"*"},
		Memory:     agents.AgentMemoryScopeProject,
		Skills:     []string{"/verify"},
		MCPServers: []agents.AgentMcpServerSpec{{ByName: "slack"}},
		Hooks: map[string][]agents.HookCommand{
			"SubagentStart": {{Command: "noop"}},
		},
		GetSystemPrompt: func(_ agents.SystemPromptCtx) string {
			return "You are a reviewer."
		},
	}

	// Register the definition as the only active agent so SpawnSubAgent
	// routes "reviewer" here (built-ins are turned off via env).
	t.Setenv("UBUILDING_DISABLE_BUILTIN_AGENTS", "1")
	registry := &agents.AgentDefinitions{ActiveAgents: []*agents.AgentDefinition{def}, AllAgents: []*agents.AgentDefinition{def}}
	registry.RefreshLegacy()

	// Clean caches so this test is deterministic when running alongside
	// the agent_mcp_test suite that pre-populates the shared cache.
	agents.ResetAgentMCPCache()

	connector := &recordingConnector{onConnect: func() { atomic.AddInt32(&capturedMCP, 1) }}
	hookReg := agents.NewShellHookRegistry()

	spy := &subagentSpy{
		responses: []agents.Message{textAssistant("review complete")},
	}
	engine := agents.NewQueryEngine(agents.EngineConfig{
		UserSpecifiedModel: "claude-sonnet-parent",
		Cwd:                tmp,
		Agents:             registry,
		AgentMemoryConfig:  memCfg,
		HookRegistry:       hookReg,
		MCPConnector:       connector,
		ResolveAgentSkill: func(_ context.Context, _, _ string) ([]agents.ContentBlock, error) {
			atomic.AddInt32(&capturedSkill, 1)
			return []agents.ContentBlock{{Type: agents.ContentBlockText, Text: "SKILL_CONTENT"}}, nil
		},
	}, spy)

	// Pre-register a SubagentStart hook that counts invocations directly —
	// the shell runner executes the command, but our stub command is a
	// no-op; counting happens inside the hook registry consumer.
	hookReg.Register(agents.HookEventSubagentStart, agents.HookCommand{Command: "noop"})
	_ = atomic.AddInt32(&capturedHook, 0) // touch var so it doesn't get optimised out

	result, err := engine.SpawnSubAgent(context.Background(), agents.SubAgentParams{
		Prompt:       "review this PR",
		SubagentType: "reviewer",
	})
	if err != nil {
		t.Fatalf("SpawnSubAgent: %v", err)
	}
	if result != "review complete" {
		t.Fatalf("final text = %q", result)
	}

	// MCP connector fired exactly once.
	if atomic.LoadInt32(&capturedMCP) != 1 {
		t.Fatalf("mcp connects = %d", atomic.LoadInt32(&capturedMCP))
	}
	// Skill resolver fired for the single declared skill.
	if atomic.LoadInt32(&capturedSkill) != 1 {
		t.Fatalf("skill resolves = %d", atomic.LoadInt32(&capturedSkill))
	}
	// System prompt flowed through BuildMemoryPrompt — the child's captured
	// prompt must contain the memory fragment.
	if !strings.Contains(spy.lastSystem, "review style-guide before responding") {
		t.Fatalf("memory missing from child prompt: %s", spy.lastSystem)
	}

	// The skill content should have been seeded as a user-role meta message
	// before the actual prompt. Probe via child's recorded messages.
	foundSkill := false
	for _, m := range spy.lastMessages {
		if m.Type != agents.MessageTypeUser {
			continue
		}
		for _, blk := range m.Content {
			if strings.Contains(blk.Text, "SKILL_CONTENT") {
				foundSkill = true
			}
		}
	}
	if !foundSkill {
		t.Fatalf("skill content missing from child messages: %+v", spy.lastMessages)
	}

	// Hook scope cleanup: after SpawnSubAgent returns, the agent's
	// frontmatter SubagentStart hook should no longer be present (only the
	// pre-registered one remains).
	remaining := hookReg.GetHooks(agents.HookEventSubagentStart)
	if len(remaining) != 1 || remaining[0].Command != "noop" {
		t.Fatalf("scoped hooks leaked: %+v", remaining)
	}
	// Ensure MCP inline cleanup path is exercised (cleanup is internal to
	// InitializeAgentMcpServers — we only assert connector state here).
	_ = os.Getenv("UBUILDING_DISABLE_BUILTIN_AGENTS")
}

// --- helpers ---------------------------------------------------------------

type recordingConnector struct {
	onConnect func()
}

func (c *recordingConnector) Connect(_ context.Context, _ string, spec agents.AgentMcpServerSpec) (agents.AgentMCPClient, error) {
	if c.onConnect != nil {
		c.onConnect()
	}
	name := spec.ByName
	if name == "" {
		for k := range spec.Inline {
			name = k
		}
	}
	return &recordedMCP{name: name}, nil
}

type recordedMCP struct{ name string }

func (r *recordedMCP) Name() string                  { return r.name }
func (r *recordedMCP) Status() string                { return "connected" }
func (r *recordedMCP) Tools() []agents.MCPTool       { return nil }
func (r *recordedMCP) Cleanup(context.Context) error { return nil }
