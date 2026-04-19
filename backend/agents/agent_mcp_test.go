package agents

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
)

// --- Fake MCP stack ---------------------------------------------------------

type fakeMCPClient struct {
	name      string
	status    string
	tools     []MCPTool
	cleaned   int32
	cleanErr  error
}

func (f *fakeMCPClient) Name() string      { return f.name }
func (f *fakeMCPClient) Status() string    { return f.status }
func (f *fakeMCPClient) Tools() []MCPTool  { return f.tools }
func (f *fakeMCPClient) Cleanup(_ context.Context) error {
	atomic.AddInt32(&f.cleaned, 1)
	return f.cleanErr
}

type fakeConnector struct {
	connects int
	byName   map[string]*fakeMCPClient
}

func (c *fakeConnector) Connect(_ context.Context, _ string, spec AgentMcpServerSpec) (AgentMCPClient, error) {
	c.connects++
	if spec.ByName != "" {
		if client, ok := c.byName[spec.ByName]; ok {
			return client, nil
		}
		return &fakeMCPClient{name: spec.ByName, status: "failed"}, nil
	}
	for name, cfg := range spec.Inline {
		_ = cfg
		return &fakeMCPClient{
			name:   name,
			status: "connected",
			tools: []MCPTool{
				{Name: "mcp__" + name + "__ping", ServerName: name},
			},
		}, nil
	}
	return nil, errors.New("empty spec")
}

// --- Tests ---------------------------------------------------------------

func TestInitializeAgentMcpServers_NoSpecsReturnsEmpty(t *testing.T) {
	resetSharedMCPForTests()
	def := &AgentDefinition{Source: AgentSourceBuiltIn}
	bundle, err := InitializeAgentMcpServers(context.Background(), &fakeConnector{}, "agt", def)
	if err != nil {
		t.Fatal(err)
	}
	if bundle == nil || len(bundle.Clients) != 0 {
		t.Fatalf("expected empty bundle: %+v", bundle)
	}
	// cleanup is always non-nil.
	if bundle.Cleanup == nil || bundle.Cleanup(context.Background()) != nil {
		t.Fatal("cleanup must be a no-op success")
	}
}

func TestInitializeAgentMcpServers_NilConnector(t *testing.T) {
	resetSharedMCPForTests()
	def := &AgentDefinition{
		Source: AgentSourceBuiltIn,
		MCPServers: []AgentMcpServerSpec{
			{ByName: "slack"},
		},
	}
	bundle, err := InitializeAgentMcpServers(context.Background(), nil, "agt", def)
	if err != nil {
		t.Fatal(err)
	}
	if len(bundle.Clients) != 0 {
		t.Fatal("no connector → no connections")
	}
}

func TestInitializeAgentMcpServers_NamedRefSharedCache(t *testing.T) {
	resetSharedMCPForTests()
	connector := &fakeConnector{
		byName: map[string]*fakeMCPClient{
			"slack": {name: "slack", status: "connected", tools: []MCPTool{{Name: "mcp__slack__post", ServerName: "slack"}}},
		},
	}
	def := &AgentDefinition{
		Source: AgentSourceBuiltIn,
		MCPServers: []AgentMcpServerSpec{
			{ByName: "slack"},
		},
	}

	bundle1, err := InitializeAgentMcpServers(context.Background(), connector, "agt1", def)
	if err != nil {
		t.Fatal(err)
	}
	bundle2, err := InitializeAgentMcpServers(context.Background(), connector, "agt2", def)
	if err != nil {
		t.Fatal(err)
	}

	if connector.connects != 1 {
		t.Fatalf("named ref should memoise across spawns; connects=%d", connector.connects)
	}
	if len(bundle1.Tools) != 1 || len(bundle2.Tools) != 1 {
		t.Fatalf("tools = %d/%d", len(bundle1.Tools), len(bundle2.Tools))
	}
	// Shared client cleanup is a no-op (we don't cleanup cached shared clients).
	_ = bundle1.Cleanup(context.Background())
}

func TestInitializeAgentMcpServers_InlineCleansUp(t *testing.T) {
	resetSharedMCPForTests()
	connector := &fakeConnector{}
	def := &AgentDefinition{
		Source: AgentSourceBuiltIn, // admin-trusted so inline allowed
		MCPServers: []AgentMcpServerSpec{
			{Inline: map[string]interface{}{"ephemeral": map[string]interface{}{"type": "stdio"}}},
		},
	}
	bundle, err := InitializeAgentMcpServers(context.Background(), connector, "agt", def)
	if err != nil {
		t.Fatal(err)
	}
	if len(bundle.Tools) != 1 || bundle.Tools[0].ServerName != "ephemeral" {
		t.Fatalf("inline tools = %+v", bundle.Tools)
	}
	if err := bundle.Cleanup(context.Background()); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	client := bundle.Clients[0].(*fakeMCPClient)
	if atomic.LoadInt32(&client.cleaned) != 1 {
		t.Fatal("inline cleanup must fire exactly once")
	}
}

func TestInitializeAgentMcpServers_InlineBlockedForUserAgents(t *testing.T) {
	resetSharedMCPForTests()
	connector := &fakeConnector{}
	def := &AgentDefinition{
		Source: AgentSourceUser, // not admin-trusted
		MCPServers: []AgentMcpServerSpec{
			{Inline: map[string]interface{}{"dev": map[string]interface{}{}}},
			{ByName: "slack"},
		},
	}
	connector.byName = map[string]*fakeMCPClient{"slack": {name: "slack", status: "connected"}}
	bundle, err := InitializeAgentMcpServers(context.Background(), connector, "agt", def)
	if err != nil {
		t.Fatal(err)
	}
	// Only the named ref should have been connected.
	if len(bundle.Clients) != 1 || bundle.Clients[0].Name() != "slack" {
		t.Fatalf("expected only slack; got %+v", bundle.Clients)
	}
}

func TestHasRequiredMcpServers(t *testing.T) {
	def := &AgentDefinition{RequiredMcpServers: []string{"slack", "github"}}
	if !HasRequiredMcpServers(def, []string{"slack", "github", "extra"}) {
		t.Fatal("all required present")
	}
	if HasRequiredMcpServers(def, []string{"slack"}) {
		t.Fatal("missing one should fail")
	}
	// Substring matching — "slack-dev" satisfies "slack".
	if !HasRequiredMcpServers(&AgentDefinition{RequiredMcpServers: []string{"slack"}}, []string{"slack-dev"}) {
		t.Fatal("substring match expected")
	}
	// No requirements → pass.
	if !HasRequiredMcpServers(&AgentDefinition{}, nil) {
		t.Fatal("no requirements should always pass")
	}
}

func TestFilterAgentsByMcpRequirements(t *testing.T) {
	defs := []*AgentDefinition{
		{AgentType: "a"},
		{AgentType: "b", RequiredMcpServers: []string{"slack"}},
		{AgentType: "c", RequiredMcpServers: []string{"github"}},
	}
	got := FilterAgentsByMcpRequirements(defs, []string{"slack"})
	if len(got) != 2 {
		t.Fatalf("got %+v", got)
	}
	names := strings.Join([]string{got[0].AgentType, got[1].AgentType}, ",")
	if names != "a,b" {
		t.Fatalf("wrong survivors: %q", names)
	}
}

func TestMcpLabel(t *testing.T) {
	if got := mcpLabel(AgentMcpServerSpec{ByName: "slack"}); got != "slack" {
		t.Fatalf("byname label = %q", got)
	}
	if got := mcpLabel(AgentMcpServerSpec{Inline: map[string]interface{}{"svc": nil}}); got != "svc" {
		t.Fatalf("inline label = %q", got)
	}
}
