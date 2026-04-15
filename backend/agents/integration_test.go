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
