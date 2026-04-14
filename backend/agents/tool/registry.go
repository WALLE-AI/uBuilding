package tool

import (
	"sort"
	"sync"
)

// ---------------------------------------------------------------------------
// Registry — tool registration and lookup
// Maps to TypeScript tools.ts (getTools, assembleToolPool)
// ---------------------------------------------------------------------------

// Registry manages the set of available tools with stable ordering.
type Registry struct {
	mu    sync.RWMutex
	tools Tools
	// denyList filters out tools by name.
	denyList map[string]struct{}
}

// NewRegistry creates a new Registry with the given initial tools.
func NewRegistry(tools ...Tool) *Registry {
	r := &Registry{
		tools:    make(Tools, 0, len(tools)),
		denyList: make(map[string]struct{}),
	}
	r.tools = append(r.tools, tools...)
	r.sortStable()
	return r
}

// Register adds a tool to the registry.
func (r *Registry) Register(t Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	// Prevent duplicates
	for _, existing := range r.tools {
		if existing.Name() == t.Name() {
			return
		}
	}
	r.tools = append(r.tools, t)
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

// sortStable sorts tools in a stable order to maximize prompt cache hit rate.
// Built-in tools come first (alphabetical), MCP tools come last.
// This maps to TypeScript's CACHE_FRIENDLY_ORDER concept.
func (r *Registry) sortStable() {
	sort.SliceStable(r.tools, func(i, j int) bool {
		return r.tools[i].Name() < r.tools[j].Name()
	})
}
