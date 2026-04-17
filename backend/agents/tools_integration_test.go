package agents_test

// End-to-end tests that exercise the QueryEngine together with the built-in
// tool set (WebSearch + WebFetch) from agents/tool/builtin.
//
// Two flavours:
//   1. TestIntegration_Tools_WebFetchPlumbing — no LLM. Verifies that a real
//      tool Registry + StreamingToolExecutor round-trips a WebFetch call
//      against an httptest server. Always runs.
//   2. TestIntegration_RealLLM_WithTools_WebFetch — gated on INTEGRATION=1.
//      Asks the real LLM to fetch an httptest URL and summarize it;
//      confirms the tool_use/tool_result loop completes end-to-end.

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wall-ai/ubuilding/backend/agents"
	"github.com/wall-ai/ubuilding/backend/agents/provider"
	"github.com/wall-ai/ubuilding/backend/agents/tool"
	"github.com/wall-ai/ubuilding/backend/agents/tool/builtin"
	"github.com/wall-ai/ubuilding/backend/agents/tool/webfetch"
)

// ---------------------------------------------------------------------------
// toolsDeps — realDeps + a real tool.Registry, for tool-enabled integration.
// ---------------------------------------------------------------------------

type toolsDeps struct {
	realDeps
	registry *tool.Registry
	// toolCallLog records every tool invocation the engine routes to us.
	toolCallLog []string
}

// BuildToolDefinitions enumerates the registry and emits agents.ToolDefinition
// records. Invoked once per turn by QueryLoop.
func (d *toolsDeps) BuildToolDefinitions(_ *agents.ToolUseContext) []agents.ToolDefinition {
	tools := d.registry.GetTools()
	defs := make([]agents.ToolDefinition, 0, len(tools))
	for _, t := range tools {
		defs = append(defs, agents.ToolDefinition{
			Name:        t.Name(),
			Description: t.Prompt(tool.PromptOptions{}),
			InputSchema: t.InputSchema(),
		})
	}
	return defs
}

// ExecuteTools routes each ToolUseBlock through a StreamingToolExecutor so
// the real Tool.Call implementations run (concurrency-safe tools in parallel).
func (d *toolsDeps) ExecuteTools(
	ctx context.Context,
	calls []agents.ToolUseBlock,
	assistantMsg *agents.Message,
	toolCtx *agents.ToolUseContext,
	_ bool,
) *agents.ToolExecutionResult {
	if toolCtx == nil || toolCtx.Ctx == nil {
		toolCtx = &agents.ToolUseContext{Ctx: ctx}
	}
	execPool := d.registry.GetTools()
	exec := tool.NewStreamingToolExecutor(execPool, nil, toolCtx, slog.Default())
	for _, call := range calls {
		d.toolCallLog = append(d.toolCallLog, call.Name)
		exec.AddTool(call, assistantMsg)
	}
	msgs, _ := exec.GetAllResults()
	// Tag every result with source assistant UUID (QueryLoop relies on this
	// to stitch tool_result blocks back to the tool_use parent).
	for i := range msgs {
		msgs[i].SourceToolAssistantUUID = assistantMsg.UUID
	}
	return &agents.ToolExecutionResult{Messages: msgs}
}

// ---------------------------------------------------------------------------
// Test #1 — plumbing-only, no LLM. Always runs.
// ---------------------------------------------------------------------------

// TestIntegration_Tools_WebFetchPlumbing verifies that toolsDeps.ExecuteTools
// actually dispatches into webfetch.WebFetchTool.Call via the registry and
// surfaces the fetched content back through the QueryDeps boundary.
func TestIntegration_Tools_WebFetchPlumbing(t *testing.T) {
	const pageBody = `<html><body><h1>Plumbing OK</h1><p>WebFetch round-trip works.</p></body></html>`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, pageBody)
	}))
	defer srv.Close()

	reg := tool.NewRegistry()
	builtin.Register(reg, builtin.Options{
		WebFetchOptions: []webfetch.Option{webfetch.WithAllowLoopback()},
	})
	deps := &toolsDeps{registry: reg}

	// Sanity: BuildToolDefinitions surfaces both tools.
	defs := deps.BuildToolDefinitions(nil)
	names := make([]string, len(defs))
	for i, d := range defs {
		names[i] = d.Name
	}
	assert.ElementsMatch(t, []string{"WebSearch", "WebFetch"}, names,
		"BuildToolDefinitions should expose WebSearch and WebFetch")

	// Hand-craft a tool_use block the way the LLM would emit it.
	inputJSON, _ := json.Marshal(map[string]string{"url": srv.URL})
	assistantMsg := &agents.Message{UUID: "asst_integration_1", Type: agents.MessageTypeAssistant}
	calls := []agents.ToolUseBlock{{
		ID:    "tu_integration_1",
		Name:  "WebFetch",
		Input: inputJSON,
	}}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	toolCtx := &agents.ToolUseContext{Ctx: ctx}

	result := deps.ExecuteTools(ctx, calls, assistantMsg, toolCtx, false)
	require.NotNil(t, result)
	require.Len(t, result.Messages, 1, "expected exactly one tool_result message")
	require.Len(t, result.Messages[0].Content, 1)

	block := result.Messages[0].Content[0]
	assert.Equal(t, agents.ContentBlockToolResult, block.Type)
	assert.Equal(t, "tu_integration_1", block.ToolUseID)
	assert.Equal(t, "asst_integration_1", result.Messages[0].SourceToolAssistantUUID)
	assert.False(t, block.IsError, "tool result should not be an error (got content: %v)", block.Content)

	content, _ := block.Content.(string)
	assert.Contains(t, content, "Plumbing OK",
		"tool result should contain content fetched from the httptest server")
	assert.Contains(t, strings.ToLower(content), "webfetch round-trip works")
	assert.Equal(t, []string{"WebFetch"}, deps.toolCallLog)
}

// ---------------------------------------------------------------------------
// Test #2 — full round-trip with a real LLM. Gated on INTEGRATION=1.
// ---------------------------------------------------------------------------

// TestIntegration_RealLLM_WithTools_WebFetch runs the full QueryEngine loop
// with an httptest server as the fetch target and a real LLM provider. The
// model is asked to use WebFetch on the local URL and report what's there.
//
// Assertions are intentionally lenient — different providers/models have
// different tool-calling reliability. The test fails only if:
//  1. The engine crashes, or
//  2. The LLM called WebFetch but the tool pipeline did not produce content.
//
// If the LLM chooses not to call the tool, the test logs a warning and passes.
func TestIntegration_RealLLM_WithTools_WebFetch(t *testing.T) {
	if os.Getenv("INTEGRATION") == "" {
		t.Skip("Skipping integration test (set INTEGRATION=1 to run)")
	}

	envPath := findEnvFile()
	require.NotEmpty(t, envPath, "Could not find .env file")
	env, err := loadEnv(envPath)
	require.NoError(t, err)

	apiKey, baseURL, model, providerType := resolveEnvConfig(env)
	require.NotEmpty(t, apiKey)
	t.Logf("Provider: %s, Model: %s", providerType, model)

	p, err := provider.NewProvider(provider.FactoryConfig{
		Type: providerType, APIKey: apiKey, BaseURL: baseURL, Logger: slog.Default(),
	})
	require.NoError(t, err)

	// Serve a small HTML page on localhost; the model must fetch it.
	const secretToken = "ZEPHYRFLAME-2187"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, `<html><body><h1>Status Page</h1>
<p>The deployment marker is <code>%s</code>.</p>
<p>Report this marker verbatim when asked.</p>
</body></html>`, secretToken)
	}))
	defer srv.Close()

	// Build registry with WebFetch in loopback mode so it can talk to httptest.
	reg := tool.NewRegistry()
	builtin.Register(reg, builtin.Options{
		WebFetchOptions: []webfetch.Option{webfetch.WithAllowLoopback()},
	})
	deps := &toolsDeps{
		realDeps: realDeps{provider: p, model: model},
		registry: reg,
	}

	engine := agents.NewQueryEngine(
		agents.EngineConfig{
			Cwd:                ".",
			UserSpecifiedModel: model,
			MaxTurns:           6, // allow tool_use + tool_result + final answer
			BaseSystemPrompt: "You are an assistant with access to the WebFetch tool. " +
				"When the user gives you a URL, you MUST call WebFetch to retrieve " +
				"its contents before answering. Be concise.",
		},
		deps,
		agents.WithLogger(slog.Default()),
	)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	prompt := fmt.Sprintf(
		"Please fetch %s and tell me the exact deployment marker value shown on that page.",
		srv.URL,
	)
	t.Logf(">>> %s", prompt)

	var (
		allText    strings.Builder
		evtKinds   []string
		gotErr     string
		gotToolUse bool
	)
	for event := range engine.SubmitMessage(ctx, prompt) {
		evtKinds = append(evtKinds, string(event.Type))
		switch event.Type {
		case agents.EventTextDelta:
			allText.WriteString(event.Text)
			fmt.Print(event.Text)
		case agents.EventToolUse:
			gotToolUse = true
			t.Logf("\n[tool_use event] %s", event.Text)
		case agents.EventError:
			gotErr = event.Error
		case agents.EventAssistant:
			if event.Message != nil {
				for _, b := range event.Message.Content {
					if b.Type == agents.ContentBlockToolUse {
						gotToolUse = true
					}
				}
			}
		}
	}
	fmt.Println()

	require.Empty(t, gotErr, "engine produced an error event: %s", gotErr)

	finalText := strings.ToLower(allText.String())
	t.Logf("Tool calls routed through deps: %v", deps.toolCallLog)
	t.Logf("Event flow: %v", evtKinds)
	t.Logf("Final assistant text: %q", allText.String())

	switch {
	case len(deps.toolCallLog) > 0:
		// Tool was called — require the fetched marker to appear in the answer.
		assert.Contains(t, deps.toolCallLog, "WebFetch",
			"deps.ExecuteTools should have seen a WebFetch invocation")
		assert.True(t, gotToolUse, "assistant message must contain a tool_use block")
		assert.Contains(t, finalText, strings.ToLower(secretToken),
			"after WebFetch, final answer should echo the deployment marker %q", secretToken)
	default:
		// The LLM chose not to call the tool. Not a hard failure — log and pass.
		t.Logf("WARNING: LLM did not call WebFetch; tool-calling skipped by model. " +
			"Plumbing-only test TestIntegration_Tools_WebFetchPlumbing guarantees " +
			"the execution path is wired correctly.")
		assert.NotEmpty(t, allText.String(), "even without tool use, the model must respond")
	}

	// Token usage sanity.
	usage := engine.GetUsage()
	t.Logf("Usage: input=%d output=%d cache_read=%d",
		usage.InputTokens, usage.OutputTokens, usage.CacheReadInputTokens)
}
