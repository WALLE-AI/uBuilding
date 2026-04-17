package tool

import (
	"sort"
	"sync"

	"github.com/wall-ai/ubuilding/backend/agents"
)

// ---------------------------------------------------------------------------
// Registry — tool registration and lookup
// Maps to TypeScript tools.ts (getAllBaseTools, getTools, assembleToolPool)
// ---------------------------------------------------------------------------

// Registry manages the set of available tools with stable ordering.
type Registry struct {
	mu        sync.RWMutex
	tools     Tools
	denyList  map[string]struct{}
	isBuiltin map[string]bool
}

// RegisterOption tunes how a tool is recorded in the Registry.
type RegisterOption func(*registerConfig)

type registerConfig struct {
	builtin bool
}

// WithBuiltin marks a tool as a built-in so AssembleToolPool places it in the
// cache-friendly prefix of the tool pool.
func WithBuiltin() RegisterOption {
	return func(c *registerConfig) { c.builtin = true }
}

// NewRegistry creates a new Registry with the given initial tools.
func NewRegistry(tools ...Tool) *Registry {
	r := &Registry{
		tools:     make(Tools, 0, len(tools)),
		denyList:  make(map[string]struct{}),
		isBuiltin: make(map[string]bool),
	}
	r.tools = append(r.tools, tools...)
	r.sortStable()
	return r
}

// Register adds a tool to the registry.
func (r *Registry) Register(t Tool, opts ...RegisterOption) {
	cfg := registerConfig{}
	for _, opt := range opts {
		opt(&cfg)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	for _, existing := range r.tools {
		if existing.Name() == t.Name() {
			return
		}
	}
	r.tools = append(r.tools, t)
	if cfg.builtin {
		r.isBuiltin[t.Name()] = true
	}
	r.sortStable()
}

// Deny adds a tool name to the deny list (prevents it from being returned).
func (r *Registry) Deny(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.denyList[name] = struct{}{}
}

// Undeny removes a tool name from the deny list.
func (r *Registry) Undeny(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.denyList, name)
}

// GetTools returns all enabled, non-denied tools in stable order.
// This is the main accessor used to build tool lists for API calls.
func (r *Registry) GetTools() Tools {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make(Tools, 0, len(r.tools))
	for _, t := range r.tools {
		if _, denied := r.denyList[t.Name()]; denied {
			continue
		}
		if !t.IsEnabled() {
			continue
		}
		result = append(result, t)
	}
	return result
}

// GetToolsForPermCtx returns the same result as GetTools plus the permCtx
// deny-rule filter. Maps to claude-code-main's getTools(permissionContext).
func (r *Registry) GetToolsForPermCtx(permCtx *agents.ToolPermissionContext) Tools {
	base := r.GetTools()
	return FilterByDenyRules(base, permCtx)
}

// IsBuiltin reports whether the named tool was registered with WithBuiltin().
func (r *Registry) IsBuiltin(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.isBuiltin[name]
}

// FindByName returns a tool by name (including aliases), or nil.
func (r *Registry) FindByName(name string) Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.tools.FindByName(name)
}

// All returns all registered tools regardless of deny/enable state.
func (r *Registry) All() Tools {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make(Tools, len(r.tools))
	copy(result, r.tools)
	return result
}

// sortStable sorts tools alphabetically; this keeps the flat registry view
// deterministic. Built-in / MCP partitioning is applied by AssembleToolPool.
func (r *Registry) sortStable() {
	sort.SliceStable(r.tools, func(i, j int) bool {
		return r.tools[i].Name() < r.tools[j].Name()
	})
}
