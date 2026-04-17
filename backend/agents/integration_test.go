package agents_test

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wall-ai/ubuilding/backend/agents"
	"github.com/wall-ai/ubuilding/backend/agents/compact"
	"github.com/wall-ai/ubuilding/backend/agents/provider"
)

// ---------------------------------------------------------------------------
// .env loader (no external dependency)
// ---------------------------------------------------------------------------

// loadEnv reads a .env file and returns key-value pairs.
// Supports lines like: KEY=value, KEY="value", # comments, blank lines.
func loadEnv(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	env := make(map[string]string)
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		// Strip surrounding quotes
		if len(val) >= 2 && ((val[0] == '"' && val[len(val)-1] == '"') || (val[0] == '\'' && val[len(val)-1] == '\'')) {
			val = val[1 : len(val)-1]
		}
		env[key] = val
	}
	return env, scanner.Err()
}

// findEnvFile walks up from the backend directory to find .env at the repo root.
func findEnvFile() string {
	// Try repo root: ../../.env relative to this file's package dir
	candidates := []string{
		filepath.Join("..", "..", ".env"), // backend/agents -> uBuilding/.env
		filepath.Join("..", ".env"),       // backend -> uBuilding/.env
		filepath.Join(".env"),             // cwd
	}
	for _, c := range candidates {
		abs, _ := filepath.Abs(c)
		if _, err := os.Stat(abs); err == nil {
			return abs
		}
	}
	return ""
}

// resolveEnvConfig extracts provider config from .env key-value pairs,
// supporting multiple naming conventions.
func resolveEnvConfig(env map[string]string) (apiKey, baseURL, model string, pt provider.ProviderType) {
	// API Key: try multiple key names (AGENT_ENGINE_ prefix is the project convention)
	for _, k := range []string{"AGENT_ENGINE_API_KEY", "LLM_API_KEY", "VLLM_API_KEY", "OPENAI_API_KEY", "ANTHROPIC_API_KEY", "API_KEY"} {
		if v := env[k]; v != "" {
			apiKey = v
			break
		}
	}

	// Base URL
	for _, k := range []string{"AGENT_ENGINE_BASE_URL", "LLM_BASE_URL", "VLLM_BASE_URL", "OPENAI_BASE_URL", "BASE_URL"} {
		if v := env[k]; v != "" {
			baseURL = v
			break
		}
	}

	// Model
	for _, k := range []string{"AGENT_ENGINE_MODEL", "LLM_MODEL", "MODEL"} {
		if v := env[k]; v != "" {
			model = v
			break
		}
	}
	if model == "" {
		model = "gpt-4o-mini"
	}

	// Provider type
	pt = provider.DetectProviderType(model)
	for _, k := range []string{"AGENT_ENGINE_PROVIDER", "LLM_PROVIDER", "PROVIDER"} {
		if v := env[k]; v != "" {
			pt = provider.ProviderType(v)
			break
		}
	}
	return
}

// ---------------------------------------------------------------------------
// realDeps wraps a real provider into a QueryDeps implementation
// ---------------------------------------------------------------------------

type realDeps struct {
	provider provider.Provider
	model    string
}

func (d *realDeps) CallModel(ctx context.Context, params agents.CallModelParams) (<-chan agents.StreamEvent, error) {
	// Map agents.CallModelParams → provider.CallModelParams
	providerParams := provider.CallModelParams{
		Messages:     params.Messages,
		SystemPrompt: params.SystemPrompt,
		Model:        d.model,
	}
	if params.MaxOutputTokens != nil {
		providerParams.MaxOutputTokens = params.MaxOutputTokens
	}
	// Forward tool definitions so the model sees them. The integration tests
	// that don't use tools simply pass nil, which is fine.
	if len(params.Tools) > 0 {
		providerParams.Tools = make([]provider.ToolDefinition, len(params.Tools))
		for i, t := range params.Tools {
			providerParams.Tools[i] = provider.ToolDefinition{
				Name:        t.Name,
				Description: t.Description,
				InputSchema: t.InputSchema,
			}
		}
	}
	return d.provider.CallModel(ctx, providerParams)
}

func (d *realDeps) Microcompact(messages []agents.Message, _ *agents.ToolUseContext, _ string) *agents.MicrocompactResult {
	return &agents.MicrocompactResult{Messages: messages, Applied: false}
}

func (d *realDeps) Autocompact(_ context.Context, messages []agents.Message, _ *agents.ToolUseContext, _ string, _ string) *agents.AutocompactResult {
	return &agents.AutocompactResult{Messages: messages, Applied: false}
}

func (d *realDeps) SnipCompact(messages []agents.Message) *agents.SnipCompactResult {
	return &agents.SnipCompactResult{Messages: messages, TokensFreed: 0}
}

func (d *realDeps) ContextCollapse(_ context.Context, messages []agents.Message, _ *agents.ToolUseContext, _ string) *agents.ContextCollapseResult {
	return &agents.ContextCollapseResult{Messages: messages, Applied: false}
}

func (d *realDeps) ContextCollapseDrain(messages []agents.Message, _ string) *agents.ContextCollapseDrainResult {
	return &agents.ContextCollapseDrainResult{Messages: messages, Committed: 0}
}

func (d *realDeps) ReactiveCompact(_ context.Context, _ []agents.Message, _ *agents.ToolUseContext, _ string, _ string, _ bool) *agents.AutocompactResult {
	return nil
}

func (d *realDeps) ExecuteTools(_ context.Context, calls []agents.ToolUseBlock, assistantMsg *agents.Message, _ *agents.ToolUseContext, _ bool) *agents.ToolExecutionResult {
	var msgs []agents.Message
	for _, call := range calls {
		msgs = append(msgs, agents.Message{
			Type: agents.MessageTypeUser,
			Content: []agents.ContentBlock{{
				Type:      agents.ContentBlockToolResult,
				ToolUseID: call.ID,
				Content:   "Tool not available in integration test. Tool: " + call.Name,
				IsError:   true,
			}},
			SourceToolAssistantUUID: assistantMsg.UUID,
		})
	}
	return &agents.ToolExecutionResult{Messages: msgs}
}

func (d *realDeps) UUID() string {
	return uuid.New().String()
}

func (d *realDeps) ApplyToolResultBudget(messages []agents.Message, _ *agents.ToolUseContext, _ string) []agents.Message {
	return messages
}

func (d *realDeps) GetAttachmentMessages(_ *agents.ToolUseContext) []agents.Message {
	return nil
}

func (d *realDeps) BuildToolDefinitions(_ *agents.ToolUseContext) []agents.ToolDefinition {
	return nil
}

func (d *realDeps) StartMemoryPrefetch(_ []agents.Message, _ *agents.ToolUseContext) <-chan []agents.Message {
	ch := make(chan []agents.Message, 1)
	ch <- nil
	close(ch)
	return ch
}

// ---------------------------------------------------------------------------
// Integration Tests — require INTEGRATION=1 env var or -tags integration
// ---------------------------------------------------------------------------

func TestIntegration_RealLLM_SimpleChat(t *testing.T) {
	if os.Getenv("INTEGRATION") == "" {
		t.Skip("Skipping integration test (set INTEGRATION=1 to run)")
	}

	envPath := findEnvFile()
	require.NotEmpty(t, envPath, "Could not find .env file")
	t.Logf("Loading .env from: %s", envPath)

	env, err := loadEnv(envPath)
	require.NoError(t, err, "Failed to load .env")

	// Detect provider config from .env (see .env.example for key names)
	apiKey, baseURL, model, providerType := resolveEnvConfig(env)
	require.NotEmpty(t, apiKey, ".env must contain LLM_API_KEY, OPENAI_API_KEY, ANTHROPIC_API_KEY, or API_KEY")

	t.Logf("Provider: %s, Model: %s, BaseURL: %s", providerType, model, baseURL)

	// Create provider
	p, err := provider.NewProvider(provider.FactoryConfig{
		Type:    providerType,
		APIKey:  apiKey,
		BaseURL: baseURL,
		Logger:  slog.Default(),
	})
	require.NoError(t, err)

	// Create deps + engine
	deps := &realDeps{provider: p, model: model}
	config := agents.EngineConfig{
		Cwd:                ".",
		UserSpecifiedModel: model,
		MaxTurns:           3,
		BaseSystemPrompt:   "You are a helpful assistant. Be concise.",
	}

	engine := agents.NewQueryEngine(config, deps, agents.WithLogger(slog.Default()))

	// Submit a simple prompt
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	t.Log(">>> Sending: What is 2+2? Answer in one word.")
	ch := engine.SubmitMessage(ctx, "What is 2+2? Answer in one word.")

	var allText string
	var eventTypes []string
	var gotAssistant bool

	for event := range ch {
		eventTypes = append(eventTypes, string(event.Type))
		switch event.Type {
		case agents.EventTextDelta:
			allText += event.Text
			fmt.Print(event.Text) // live streaming output
		case agents.EventAssistant:
			gotAssistant = true
			if event.Message != nil {
				t.Logf("\n<<< Assistant (model=%s, stop=%s)", event.Message.Model, event.Message.StopReason)
			}
		case agents.EventError:
			t.Fatalf("LLM error: %s", event.Error)
		case agents.EventDone:
			t.Log("<<< Done")
		}
	}
	fmt.Println() // newline after streaming

	// Assertions
	assert.True(t, gotAssistant, "should receive an assistant message")
	assert.NotEmpty(t, allText, "should receive non-empty text")
	t.Logf("Full response: %q", allText)
	t.Logf("Event flow: %v", eventTypes)

	// Verify engine state
	msgs := engine.GetMessages()
	assert.GreaterOrEqual(t, len(msgs), 2, "should have at least user + assistant messages")
	t.Logf("Message history: %d messages", len(msgs))

	usage := engine.GetUsage()
	t.Logf("Token usage: input=%d, output=%d", usage.InputTokens, usage.OutputTokens)
}

func TestIntegration_RealLLM_MultiTurn(t *testing.T) {
	if os.Getenv("INTEGRATION") == "" {
		t.Skip("Skipping integration test (set INTEGRATION=1 to run)")
	}

	envPath := findEnvFile()
	require.NotEmpty(t, envPath)

	env, err := loadEnv(envPath)
	require.NoError(t, err)

	apiKey, baseURL, model, providerType := resolveEnvConfig(env)
	require.NotEmpty(t, apiKey)

	p, err := provider.NewProvider(provider.FactoryConfig{
		Type:    providerType,
		APIKey:  apiKey,
		BaseURL: baseURL,
		Logger:  slog.Default(),
	})
	require.NoError(t, err)

	deps := &realDeps{provider: p, model: model}
	config := agents.EngineConfig{
		Cwd:                ".",
		UserSpecifiedModel: model,
		MaxTurns:           3,
		BaseSystemPrompt:   "You are a helpful assistant. Be very concise — one sentence max.",
	}

	engine := agents.NewQueryEngine(config, deps)

	// Turn 1
	ctx1, cancel1 := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel1()

	t.Log(">>> Turn 1: My name is Alice.")
	var turn1Text string
	for event := range engine.SubmitMessage(ctx1, "My name is Alice.") {
		if event.Type == agents.EventTextDelta {
			turn1Text += event.Text
			fmt.Print(event.Text)
		}
		if event.Type == agents.EventError {
			t.Fatalf("Turn 1 error: %s", event.Error)
		}
	}
	fmt.Println()
	t.Logf("Turn 1 response: %q", turn1Text)

	// Turn 2 — tests that conversation history is maintained
	ctx2, cancel2 := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel2()

	t.Log(">>> Turn 2: What is my name?")
	var turn2Text string
	for event := range engine.SubmitMessage(ctx2, "What is my name?") {
		if event.Type == agents.EventTextDelta {
			turn2Text += event.Text
			fmt.Print(event.Text)
		}
		if event.Type == agents.EventError {
			t.Fatalf("Turn 2 error: %s", event.Error)
		}
	}
	fmt.Println()
	t.Logf("Turn 2 response: %q", turn2Text)

	// The model should remember the name from turn 1
	assert.Contains(t, strings.ToLower(turn2Text), "alice",
		"Model should remember the name 'Alice' from turn 1")

	msgs := engine.GetMessages()
	t.Logf("Total messages in history: %d", len(msgs))
	assert.GreaterOrEqual(t, len(msgs), 4, "should have user1+assistant1+user2+assistant2")
}

func TestIntegration_RealLLM_FullPromptSystem(t *testing.T) {
	if os.Getenv("INTEGRATION") == "" {
		t.Skip("Skipping integration test (set INTEGRATION=1 to run)")
	}

	envPath := findEnvFile()
	require.NotEmpty(t, envPath)

	env, err := loadEnv(envPath)
	require.NoError(t, err)

	apiKey, baseURL, model, providerType := resolveEnvConfig(env)
	require.NotEmpty(t, apiKey)

	p, err := provider.NewProvider(provider.FactoryConfig{
		Type:    providerType,
		APIKey:  apiKey,
		BaseURL: baseURL,
		Logger:  slog.Default(),
	})
	require.NoError(t, err)

	deps := &realDeps{provider: p, model: model}

	// Use BuildSystemPromptFn instead of BaseSystemPrompt — tests Phase 4 integration
	config := agents.EngineConfig{
		Cwd:                ".",
		UserSpecifiedModel: model,
		MaxTurns:           3,
		BuildSystemPromptFn: func() (string, map[string]string, map[string]string) {
			return "You are a coding assistant named UBuilder. Always mention your name UBuilder in the first sentence. Be concise.",
				map[string]string{"workspace": "test-workspace"},
				map[string]string{"platform": "integration-test"}
		},
	}

	engine := agents.NewQueryEngine(config, deps)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	t.Log(">>> Sending: Introduce yourself briefly.")
	var allText string
	for event := range engine.SubmitMessage(ctx, "Introduce yourself briefly.") {
		if event.Type == agents.EventTextDelta {
			allText += event.Text
			fmt.Print(event.Text)
		}
		if event.Type == agents.EventError {
			t.Fatalf("LLM error: %s", event.Error)
		}
	}
	fmt.Println()
	t.Logf("Response: %q", allText)

	// The model should identify as UBuilder
	assert.Contains(t, strings.ToLower(allText), "ubuilder",
		"Model should mention its name 'UBuilder' as instructed by the full prompt system")
}

// ---------------------------------------------------------------------------
// Memory Integration Test — verifies LoadMemories injects context the model uses
// ---------------------------------------------------------------------------

func TestIntegration_RealLLM_MemoryInjection(t *testing.T) {
	if os.Getenv("INTEGRATION") == "" {
		t.Skip("Skipping integration test (set INTEGRATION=1 to run)")
	}

	envPath := findEnvFile()
	require.NotEmpty(t, envPath)

	env, err := loadEnv(envPath)
	require.NoError(t, err)

	apiKey, baseURL, model, providerType := resolveEnvConfig(env)
	require.NotEmpty(t, apiKey)

	p, err := provider.NewProvider(provider.FactoryConfig{
		Type:    providerType,
		APIKey:  apiKey,
		BaseURL: baseURL,
		Logger:  slog.Default(),
	})
	require.NoError(t, err)

	deps := &realDeps{provider: p, model: model}

	// Create engine with LoadMemories that injects a "secret" memory
	config := agents.EngineConfig{
		Cwd:                ".",
		UserSpecifiedModel: model,
		MaxTurns:           3,
		BaseSystemPrompt:   "You are a helpful assistant. Be very concise — one sentence max.",
		LoadMemories: func(cwd string) []agents.Message {
			return []agents.Message{
				{
					Type:   agents.MessageTypeUser,
					IsMeta: true,
					Content: []agents.ContentBlock{{
						Type: agents.ContentBlockText,
						Text: "[MEMORY] The user's project codename is 'Project Phoenix'. Always reference this when asked about the project.",
					}},
				},
				{
					Type:   agents.MessageTypeAssistant,
					IsMeta: true,
					Content: []agents.ContentBlock{{
						Type: agents.ContentBlockText,
						Text: "Understood, I'll remember the project codename is 'Project Phoenix'.",
					}},
				},
			}
		},
	}

	engine := agents.NewQueryEngine(config, deps)

	// Ask about the project — model should use injected memory
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	t.Log(">>> Sending: What is the codename of my project?")
	var allText string
	for event := range engine.SubmitMessage(ctx, "What is the codename of my project?") {
		if event.Type == agents.EventTextDelta {
			allText += event.Text
			fmt.Print(event.Text)
		}
		if event.Type == agents.EventError {
			t.Fatalf("LLM error: %s", event.Error)
		}
	}
	fmt.Println()
	t.Logf("Response: %q", allText)

	// Model should mention "Phoenix" from the injected memory
	assert.Contains(t, strings.ToLower(allText), "phoenix",
		"Model should reference 'Project Phoenix' from the injected memory")

	// Verify memory messages are in history
	msgs := engine.GetMessages()
	t.Logf("Total messages in history: %d", len(msgs))
	// Expected: memory_user + memory_assistant + real_user + real_assistant = 4
	assert.GreaterOrEqual(t, len(msgs), 4, "should include memory messages + user + assistant")

	// The first message should be the injected memory
	assert.True(t, msgs[0].IsMeta, "first message should be the injected memory (IsMeta)")
}

// ---------------------------------------------------------------------------
// Context Compaction Integration Test — verifies autocompact with real LLM
// ---------------------------------------------------------------------------

// compactDeps extends realDeps with a real AutoCompactor for autocompact.
type compactDeps struct {
	realDeps
	autoCompactor *compact.AutoCompactor
	compactCalled bool
	compactResult *agents.AutocompactResult
}

func (d *compactDeps) Autocompact(ctx context.Context, messages []agents.Message, _ *agents.ToolUseContext, systemPrompt string, querySource string) *agents.AutocompactResult {
	result := d.autoCompactor.CompactWithTracking(ctx, messages, systemPrompt, querySource, nil, false)
	if result != nil && result.Applied {
		d.compactCalled = true
		d.compactResult = result
	}
	return result
}

func TestIntegration_RealLLM_ContextCompaction(t *testing.T) {
	if os.Getenv("INTEGRATION") == "" {
		t.Skip("Skipping integration test (set INTEGRATION=1 to run)")
	}

	envPath := findEnvFile()
	require.NotEmpty(t, envPath)

	env, err := loadEnv(envPath)
	require.NoError(t, err)

	apiKey, baseURL, model, providerType := resolveEnvConfig(env)
	require.NotEmpty(t, apiKey)

	p, err := provider.NewProvider(provider.FactoryConfig{
		Type:    providerType,
		APIKey:  apiKey,
		BaseURL: baseURL,
		Logger:  slog.Default(),
	})
	require.NoError(t, err)

	// Build a compactDeps that uses real LLM for both chat and summarization
	callModelFn := func(ctx context.Context, params agents.CallModelParams) (<-chan agents.StreamEvent, error) {
		providerParams := provider.CallModelParams{
			Messages:     params.Messages,
			SystemPrompt: params.SystemPrompt,
			Model:        model,
		}
		return p.CallModel(ctx, providerParams)
	}

	autoCompactor := compact.NewAutoCompactor(callModelFn)

	deps := &compactDeps{
		realDeps:      realDeps{provider: p, model: model},
		autoCompactor: autoCompactor,
	}

	config := agents.EngineConfig{
		Cwd:                ".",
		UserSpecifiedModel: model,
		MaxTurns:           3,
		BaseSystemPrompt:   "You are a helpful assistant. Be very concise — one sentence max.",
	}

	engine := agents.NewQueryEngine(config, deps)
	ctx := context.Background()

	// Inject a large synthetic history to force compaction threshold
	// We inject many messages directly then ask a question that triggers autocompact
	t.Log("=== Phase 1: Building up long conversation history ===")

	// Fill history with many turns to exceed 80% of context window
	// AutoCompactThreshold = 0.80, DefaultContextWindow = 200000 tokens
	// We need ~160k tokens ≈ 640k chars. Let's use a mix of padding + real LLM turns.
	syntheticHistory := buildSyntheticHistory(30)
	t.Logf("Synthetic history: %d messages, estimated %d tokens",
		len(syntheticHistory), estimateTokens(syntheticHistory))

	// Turn 1: Establish a fact before synthetic padding
	ctx1, cancel1 := context.WithTimeout(ctx, 30*time.Second)
	defer cancel1()

	t.Log(">>> Turn 1: Remember the secret word 'Zephyr'")
	var turn1Text string
	for event := range engine.SubmitMessage(ctx1, "Remember this secret word: 'Zephyr'. Just acknowledge it.") {
		if event.Type == agents.EventTextDelta {
			turn1Text += event.Text
		}
		if event.Type == agents.EventError {
			t.Fatalf("Turn 1 error: %s", event.Error)
		}
	}
	t.Logf("Turn 1 response: %q", turn1Text)

	// Now directly inject synthetic padding messages into the engine's history
	// to simulate a long conversation that crosses the compaction threshold
	for _, msg := range syntheticHistory {
		engine.AppendMessage(msg)
	}

	afterInject := engine.GetMessages()
	t.Logf("After inject: %d messages, estimated %d tokens",
		len(afterInject), estimateTokens(afterInject))

	// Turn 2: This should trigger autocompact (if threshold is exceeded) + ask a question
	ctx2, cancel2 := context.WithTimeout(ctx, 60*time.Second)
	defer cancel2()

	t.Log(">>> Turn 2: What is the secret word I told you earlier?")
	var turn2Text string
	for event := range engine.SubmitMessage(ctx2, "What is the secret word I told you earlier? Answer in one word.") {
		if event.Type == agents.EventTextDelta {
			turn2Text += event.Text
			fmt.Print(event.Text)
		}
		if event.Type == agents.EventError {
			t.Fatalf("Turn 2 error: %s", event.Error)
		}
	}
	fmt.Println()
	t.Logf("Turn 2 response: %q", turn2Text)

	// Report compaction status
	if deps.compactCalled {
		t.Logf("✓ Autocompact was triggered! Summary: %q", truncateForLog(deps.compactResult.Summary, 200))
		t.Logf("  Tokens saved: %d", deps.compactResult.TokensSaved)
	} else {
		t.Log("⚠ Autocompact was NOT triggered (history may not have crossed threshold)")
	}

	finalMsgs := engine.GetMessages()
	t.Logf("Final message history: %d messages", len(finalMsgs))

	// After compaction, message count should be reduced compared to what we injected
	if deps.compactCalled {
		assert.Less(t, len(finalMsgs), len(afterInject),
			"after compaction, message count should be reduced")
	}
}

// TestIntegration_RealLLM_ForceCompact tests direct AutoCompactor.CompactWithTracking
// with a real LLM — no engine involved, just the compaction logic.
func TestIntegration_RealLLM_ForceCompact(t *testing.T) {
	if os.Getenv("INTEGRATION") == "" {
		t.Skip("Skipping integration test (set INTEGRATION=1 to run)")
	}

	envPath := findEnvFile()
	require.NotEmpty(t, envPath)

	env, err := loadEnv(envPath)
	require.NoError(t, err)

	apiKey, baseURL, model, providerType := resolveEnvConfig(env)
	require.NotEmpty(t, apiKey)

	p, err := provider.NewProvider(provider.FactoryConfig{
		Type:    providerType,
		APIKey:  apiKey,
		BaseURL: baseURL,
		Logger:  slog.Default(),
	})
	require.NoError(t, err)

	callModelFn := func(ctx context.Context, params agents.CallModelParams) (<-chan agents.StreamEvent, error) {
		providerParams := provider.CallModelParams{
			Messages:     params.Messages,
			SystemPrompt: params.SystemPrompt,
			Model:        model,
		}
		return p.CallModel(ctx, providerParams)
	}

	autoCompactor := compact.NewAutoCompactor(callModelFn)

	// Build a conversation to compact
	messages := []agents.Message{
		{Type: agents.MessageTypeUser, UUID: "u1", Content: []agents.ContentBlock{
			{Type: agents.ContentBlockText, Text: "My name is Bob and I'm working on a Go migration project called QueryEngine."},
		}},
		{Type: agents.MessageTypeAssistant, UUID: "a1", Content: []agents.ContentBlock{
			{Type: agents.ContentBlockText, Text: "Got it, Bob! I'll help you with the QueryEngine Go migration project."},
		}},
		{Type: agents.MessageTypeUser, UUID: "u2", Content: []agents.ContentBlock{
			{Type: agents.ContentBlockText, Text: "We've completed Phase 1 through Phase 4 — prompt system, queryloop gaps, stop hooks, and engine layer."},
		}},
		{Type: agents.MessageTypeAssistant, UUID: "a2", Content: []agents.ContentBlock{
			{Type: agents.ContentBlockText, Text: "Great progress! Phases 1-4 are done. Phase 5 (integration tests) is next."},
		}},
		{Type: agents.MessageTypeUser, UUID: "u3", Content: []agents.ContentBlock{
			{Type: agents.ContentBlockText, Text: "Now let's work on adding file watching capability to detect code changes."},
		}},
		{Type: agents.MessageTypeAssistant, UUID: "a3", Content: []agents.ContentBlock{
			{Type: agents.ContentBlockText, Text: "I'll implement a file watcher using fsnotify that monitors the project directory for changes."},
		}},
		{Type: agents.MessageTypeUser, UUID: "u4", Content: []agents.ContentBlock{
			{Type: agents.ContentBlockText, Text: "Can you also add a debouncer to avoid triggering on rapid file saves?"},
		}},
		{Type: agents.MessageTypeAssistant, UUID: "a4", Content: []agents.ContentBlock{
			{Type: agents.ContentBlockText, Text: "Sure, I'll add a 500ms debounce window for file change events using time.AfterFunc."},
		}},
	}

	t.Logf("Input: %d messages", len(messages))

	// Force compaction (bypasses threshold check)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	result := autoCompactor.CompactWithTracking(ctx, messages, "You are a helpful assistant.", "integration_test", nil, true)
	require.NotNil(t, result, "CompactWithTracking should return a result")
	require.True(t, result.Applied, "forced compaction should be applied")

	t.Logf("Compaction result:")
	t.Logf("  Applied: %v", result.Applied)
	t.Logf("  Tokens saved: %d", result.TokensSaved)
	t.Logf("  Output messages: %d", len(result.Messages))
	t.Logf("  Summary: %q", truncateForLog(result.Summary, 300))

	// Verify summary contains key facts
	summaryLower := strings.ToLower(result.Summary)
	assert.NotEmpty(t, result.Summary, "summary should not be empty")

	// The summary should preserve some key info (name, project, phases)
	keyTerms := []string{"bob", "queryengine", "phase"}
	found := 0
	for _, term := range keyTerms {
		if strings.Contains(summaryLower, term) {
			found++
			t.Logf("  ✓ Summary contains: %q", term)
		} else {
			t.Logf("  ✗ Summary missing: %q", term)
		}
	}
	assert.GreaterOrEqual(t, found, 1,
		"summary should preserve at least 1 key term from the conversation")

	// Verify message count is reduced
	assert.Less(t, len(result.Messages), len(messages),
		"compacted messages should be fewer than original")

	// First message should be the summary
	assert.True(t, result.Messages[0].IsCompactSummary,
		"first compacted message should be the compact summary")
	assert.True(t, result.Messages[0].IsMeta,
		"compact summary message should be meta")
}

// ---------------------------------------------------------------------------
// Helpers for building synthetic history
// ---------------------------------------------------------------------------

// buildSyntheticHistory creates N pairs of user/assistant messages with padding.
func buildSyntheticHistory(pairs int) []agents.Message {
	var msgs []agents.Message
	// Each pair: ~20k chars ≈ ~5k tokens. 30 pairs ≈ 150k tokens.
	padding := strings.Repeat("This is padding text to fill the context window for compaction testing. ", 100)
	for i := 0; i < pairs; i++ {
		msgs = append(msgs,
			agents.Message{
				Type: agents.MessageTypeUser,
				UUID: fmt.Sprintf("synth-u-%d", i),
				Content: []agents.ContentBlock{{
					Type: agents.ContentBlockText,
					Text: fmt.Sprintf("[Turn %d] %s", i, padding),
				}},
			},
			agents.Message{
				Type: agents.MessageTypeAssistant,
				UUID: fmt.Sprintf("synth-a-%d", i),
				Content: []agents.ContentBlock{{
					Type: agents.ContentBlockText,
					Text: fmt.Sprintf("Acknowledged turn %d. %s", i, padding),
				}},
			},
		)
	}
	return msgs
}

func estimateTokens(msgs []agents.Message) int {
	chars := 0
	for _, m := range msgs {
		for _, b := range m.Content {
			chars += len(b.Text)
		}
	}
	return chars / 4
}

func truncateForLog(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
