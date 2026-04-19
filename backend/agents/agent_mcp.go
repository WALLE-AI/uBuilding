// Package agents — per-agent MCP server orchestration.
//
// Tasks C01 · C02 · C14 · port
// src/tools/AgentTool/runAgent.ts::initializeAgentMcpServers. Responsibility:
//   - Accept both shapes the frontmatter allows (`"name"` string reference
//     vs `{"name": {config...}}` inline definition), already normalized to
//     `AgentMcpServerSpec`.
//   - Delegate the actual connect to a host-provided MCPConnector so this
//     package doesn't depend on a concrete MCP client.
//   - Memoise connectors across multiple sub-agent spawns (C14) so a
//     string reference re-used by many agents reuses the same live client.
//   - Return an explicit cleanup closure the caller runs on agent exit.
//
// Hosts usually wire the connector via `EngineConfig.MCPConnector`. When
// unset, SpawnSubAgent skips MCP server initialisation entirely (matches
// TS: an agent with frontmatter MCP that the host doesn't support is a
// no-op rather than a crash).
package agents

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
)

// AgentMCPClient is a minimal interface the host's MCP connector returns
// for each connected server. Kept intentionally tiny: we only need the
// tool list during agent spawn; clean-up is handled via the Cleanup method
// so inline servers can tear down reliably.
type AgentMCPClient interface {
	// Name returns the server name as declared.
	Name() string

	// Status reports "connected", "pending", "failed", etc. (TS parity).
	Status() string

	// Tools returns the tools this server advertises. Empty when Status
	// isn't "connected".
	Tools() []MCPTool

	// Cleanup disconnects the server. Must be idempotent — the engine may
	// call it multiple times (e.g. error path + defer). Nil-safe.
	Cleanup(ctx context.Context) error
}

// MCPConnector is the host-provided factory. It's called once per unique
// `ref` (server name OR inline spec key). When spec.ByName is set, the
// connector should prefer a memoised parent-level client; inline specs
// always produce a fresh client.
type MCPConnector interface {
	// Connect establishes (or returns) a client for spec. The agentID arg
	// is informational (analytics / logging).
	Connect(ctx context.Context, agentID string, spec AgentMcpServerSpec) (AgentMCPClient, error)
}

// AgentMCPBundle is the result of InitializeAgentMcpServers — ready to
// merge into the sub-agent's ToolUseOptions.
type AgentMCPBundle struct {
	// Clients lists every server connected for this agent (both shared and
	// inline).
	Clients []AgentMCPClient

	// Tools aggregates all tools advertised by Clients, de-duplicated by
	// name. Tool instances are opaque (interface{}); callers merge them
	// into their existing tool pool via uniqBy(name).
	Tools []MCPTool

	// Cleanup disconnects every inline server. Shared clients are left
	// alone — the parent engine owns their lifecycle.
	Cleanup func(ctx context.Context) error
}

// mcpSharedCache memoises connector lookups by server name across the
// package. Inline servers bypass the cache (they're scoped to a single
// agent invocation by design).
type mcpSharedCache struct {
	mu    sync.Mutex
	items map[string]AgentMCPClient
}

var sharedMCP = &mcpSharedCache{items: map[string]AgentMCPClient{}}

// resetSharedMCPForTests flushes the shared cache. Exported for tests
// only; production hosts should never call this.
func resetSharedMCPForTests() {
	sharedMCP.mu.Lock()
	defer sharedMCP.mu.Unlock()
	sharedMCP.items = map[string]AgentMCPClient{}
}

// ResetAgentMCPCache flushes the package-level shared MCP client cache.
// Intended for integration tests that must observe connector.Connect
// being called; production code should never invoke it.
func ResetAgentMCPCache() { resetSharedMCPForTests() }

// InitializeAgentMcpServers connects every MCP server declared on def.
// Returns an empty AgentMCPBundle (no cleanup needed) when def has no
// MCP specs, or when connector is nil.
func InitializeAgentMcpServers(
	ctx context.Context,
	connector MCPConnector,
	agentID string,
	def *AgentDefinition,
) (*AgentMCPBundle, error) {
	bundle := &AgentMCPBundle{Cleanup: noopCleanup}
	if def == nil || len(def.MCPServers) == 0 || connector == nil {
		return bundle, nil
	}

	// Plugin-only policy (parity with TS isRestrictedToPluginOnly('mcp')):
	// user-source agents cannot spin up inline MCP servers. Built-in and
	// plugin/policy agents are admin-trusted. Hosts that don't enforce a
	// plugin-only mode can ignore this check — MCPConnector.Connect is the
	// authoritative gate.
	adminTrusted := def.Source.IsAdminTrusted()

	type connected struct {
		client AgentMCPClient
		inline bool
	}
	var conns []connected
	var inlineClients []AgentMCPClient

	for _, spec := range def.MCPServers {
		if spec.ByName == "" && len(spec.Inline) == 0 {
			continue
		}
		inline := spec.ByName == ""
		// Enforce plugin-only restriction for inline specs on user agents.
		if inline && !adminTrusted {
			// Skip but don't fail the spawn — mirrors the TS warn path.
			continue
		}

		// Shared-cache lookup for named refs.
		if spec.ByName != "" {
			if cached := sharedMCP.get(spec.ByName); cached != nil {
				conns = append(conns, connected{client: cached})
				continue
			}
		}

		client, err := connector.Connect(ctx, agentID, spec)
		if err != nil {
			return nil, fmt.Errorf("agent %q: connect mcp %s: %w", def.AgentType, mcpLabel(spec), err)
		}
		if client == nil {
			continue
		}
		if spec.ByName != "" {
			sharedMCP.put(spec.ByName, client)
		}
		conns = append(conns, connected{client: client, inline: inline})
		if inline {
			inlineClients = append(inlineClients, client)
		}
	}

	seenTool := make(map[string]struct{})
	for _, c := range conns {
		bundle.Clients = append(bundle.Clients, c.client)
		if strings.EqualFold(c.client.Status(), "connected") {
			for _, t := range c.client.Tools() {
				if _, dup := seenTool[t.Name]; dup {
					continue
				}
				seenTool[t.Name] = struct{}{}
				bundle.Tools = append(bundle.Tools, t)
			}
		}
	}

	if len(inlineClients) == 0 {
		return bundle, nil
	}

	bundle.Cleanup = func(cleanupCtx context.Context) error {
		var firstErr error
		for _, c := range inlineClients {
			if c == nil {
				continue
			}
			if err := c.Cleanup(cleanupCtx); err != nil && firstErr == nil {
				firstErr = err
			}
		}
		return firstErr
	}
	return bundle, nil
}

// HasRequiredMcpServers mirrors the TS helper of the same name. Returns
// false when any entry in def.RequiredMcpServers is missing from
// connectedServerNames.
func HasRequiredMcpServers(def *AgentDefinition, connectedServerNames []string) bool {
	if def == nil || len(def.RequiredMcpServers) == 0 {
		return true
	}
	connected := make(map[string]struct{}, len(connectedServerNames))
	for _, n := range connectedServerNames {
		connected[strings.ToLower(strings.TrimSpace(n))] = struct{}{}
	}
	for _, required := range def.RequiredMcpServers {
		need := strings.ToLower(strings.TrimSpace(required))
		if need == "" {
			continue
		}
		matched := false
		for have := range connected {
			if strings.Contains(have, need) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	return true
}

// FilterAgentsByMcpRequirements removes agents whose RequiredMcpServers
// aren't satisfied by connectedServerNames. Matches TS
// filterAgentsByMcpRequirements — used when rendering the Task tool prompt
// so agents with unsatisfied deps don't appear in the picker.
func FilterAgentsByMcpRequirements(defs []*AgentDefinition, connectedServerNames []string) []*AgentDefinition {
	if len(defs) == 0 {
		return nil
	}
	out := make([]*AgentDefinition, 0, len(defs))
	for _, d := range defs {
		if d == nil {
			continue
		}
		if HasRequiredMcpServers(d, connectedServerNames) {
			out = append(out, d)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func (c *mcpSharedCache) get(name string) AgentMCPClient {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.items[name]
}

func (c *mcpSharedCache) put(name string, client AgentMCPClient) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items[name] = client
}

func mcpLabel(spec AgentMcpServerSpec) string {
	if spec.ByName != "" {
		return spec.ByName
	}
	for k := range spec.Inline {
		return k
	}
	return "(unnamed)"
}

func noopCleanup(_ context.Context) error { return nil }

// ErrAgentMCPUnsupported is returned when an agent declares MCP servers
// but the engine is not wired with a connector. Kept exported for hosts
// that want to surface a user-friendly message.
var ErrAgentMCPUnsupported = errors.New("agent MCP connector unavailable")
