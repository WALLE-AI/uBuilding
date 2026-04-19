package agents_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/wall-ai/ubuilding/backend/agents"
	"github.com/wall-ai/ubuilding/backend/agents/tool"
	"github.com/wall-ai/ubuilding/backend/agents/tool/agenttool"
)

// TestSubagentE2E · A13 · end-to-end round trip:
//
//	Parent engine → agenttool.AgentTool.Call → ToolUseContext.SpawnSubAgent
//	→ child QueryEngine → mock provider text → returned text.
//
// The purpose is to prove that the cross-package bridge compiles cleanly and
// that the agent catalog flows from EngineConfig → ToolUseContext →
// AgentTool validation/prompt so the external tool package can operate
// without reaching back into the engine for internal state.
func TestSubagentE2E(t *testing.T) {
	// Provider that always returns a plain-text assistant response — the
	// sub-agent query needs at least one assistant turn.
	spy := &subagentSpy{
		responses: []agents.Message{textAssistant("42 Go files found")},
	}

	engine := agents.NewQueryEngine(agents.EngineConfig{
		UserSpecifiedModel: "claude-sonnet-parent",
		MaxTurns:           3,
	}, spy)

	// AgentTool wired with the engine's catalog + SpawnSubAgent bridge —
	// same wiring the QueryEngine does in defaultToolUseContext (A09).
	at := agenttool.New(
		agenttool.WithAgentCatalog(func() []*agents.AgentDefinition {
			return engine.Agents().ActiveAgents
		}),
	)
	tc := &agents.ToolUseContext{
		Ctx:           context.Background(),
		SpawnSubAgent: engine.SpawnSubAgent,
		Options: agents.ToolUseOptions{
			AgentDefinitions: engine.Agents(),
		},
	}

	// Build an input that omits subagent_type so the default (general-purpose)
	// is exercised — covers the "empty SubagentType → fallback" path (A08).
	raw, err := json.Marshal(agenttool.Input{
		Description: "Count Go files",
		Prompt:      "Count all Go files under the repo and report the total.",
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	res, err := at.Call(ctx, raw, tc)
	if err != nil {
		t.Fatalf("AgentTool.Call: %v", err)
	}
	out, ok := res.Data.(agenttool.Output)
	if !ok {
		t.Fatalf("unexpected Data type: %T", res.Data)
	}
	if out.Status != agenttool.StatusCompleted {
		t.Errorf("Status = %q; want %q", out.Status, agenttool.StatusCompleted)
	}
	if !strings.Contains(out.Result, "42 Go files found") {
		t.Fatalf("Result = %q", out.Result)
	}

	// Sub-agent must have inherited the parent model (no explicit override).
	if spy.capturedModel != "claude-sonnet-parent" {
		t.Errorf("child model = %q; want parent (inherit)", spy.capturedModel)
	}

	// Agent catalog reached the Task prompt — proves A10 wiring.
	if got := at.Prompt(tool.PromptOptions{}); !strings.Contains(got, "general-purpose") {
		t.Errorf("Task prompt missing general-purpose agent: %s", got)
	}
}

// TestSubagentE2E_WithExplicitAgent covers the subagent_type routing path
// plus the allow list defaulting (A11) fed from AgentDefinitions.
func TestSubagentE2E_WithExplicitAgent(t *testing.T) {
	spy := &subagentSpy{
		responses: []agents.Message{textAssistant("Explore says: 3 matches")},
	}
	engine := agents.NewQueryEngine(agents.EngineConfig{
		UserSpecifiedModel: "claude-sonnet-parent",
	}, spy)
	at := agenttool.New()
	defs := engine.Agents()
	defs.AllowedAgentTypes = []string{"Explore"}
	tc := &agents.ToolUseContext{
		Ctx:           context.Background(),
		SpawnSubAgent: engine.SpawnSubAgent,
		Options:       agents.ToolUseOptions{AgentDefinitions: defs},
	}

	rawAllowed, _ := json.Marshal(agenttool.Input{
		Prompt: "scan", SubagentType: "Explore", Description: "search",
	})
	if v := at.ValidateInput(rawAllowed, tc); !v.Valid {
		t.Fatalf("Explore should have been allowed: %s", v.Message)
	}
	res, err := at.Call(context.Background(), rawAllowed, tc)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	out := res.Data.(agenttool.Output)
	if out.SubagentType != "Explore" {
		t.Errorf("SubagentType = %q", out.SubagentType)
	}

	rawDenied, _ := json.Marshal(agenttool.Input{
		Prompt: "x", SubagentType: "general-purpose", Description: "x",
	})
	if v := at.ValidateInput(rawDenied, tc); v.Valid {
		t.Fatal("general-purpose should be blocked by allowed list")
	}

	// Touch spy so the linter doesn't flag unused binding.
	_ = fmt.Sprintf("calls=%d", spy.calls)
}
