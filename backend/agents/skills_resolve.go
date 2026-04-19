// Package agents — skill resolution + per-agent cleanup.
//
// Task C11 · port src/services/skills/resolve.ts + runAgent.ts's
// ClearInvokedSkillsForAgent. A host registers every skill/slash-command
// it knows about; SpawnSubAgent's C06 preload path looks up the declared
// skill names here and expands them into content blocks.
//
// Resolution strategy (mirroring TS ResolveSkillName):
//
//  1. Exact match — "verify" → skill named "verify".
//  2. Plugin prefix — "my-plugin:verify" → skill named "verify" inside
//     the "my-plugin" namespace.
//  3. Suffix — "verify" matches any skill whose name ends with ":verify"
//     (case-insensitive). Picks the lexicographically smallest match so
//     the choice is deterministic.
//
// Resolution returns a Skill descriptor the host can pass to C06's
// ResolveAgentSkill callback. The per-agent invocation log (used for
// analytics + cleanup) lives in SkillInvocationLog.
package agents

import (
	"context"
	"sort"
	"strings"
	"sync"
)

// Skill is the minimal descriptor the package needs to know about a
// skill. The real host stores richer metadata (prompt, description,
// permissions) — that's kept opaque via Extra.
type Skill struct {
	// Name is the unprefixed skill name (unique within a plugin scope).
	Name string

	// Plugin, when non-empty, is the plugin that owns the skill. The full
	// qualified form is "<plugin>:<name>".
	Plugin string

	// Body is the expanded prompt text the skill contributes when invoked.
	Body string

	// Extra is a host-supplied payload (passed through unchanged).
	Extra interface{}
}

// QualifiedName returns "<plugin>:<name>" when Plugin is set, otherwise
// just Name.
func (s Skill) QualifiedName() string {
	if s.Plugin == "" {
		return s.Name
	}
	return s.Plugin + ":" + s.Name
}

// SkillRegistry is an in-memory skill catalogue. Safe for concurrent use.
type SkillRegistry struct {
	mu     sync.RWMutex
	skills map[string]Skill // key = QualifiedName
}

// NewSkillRegistry returns an empty registry.
func NewSkillRegistry() *SkillRegistry {
	return &SkillRegistry{skills: map[string]Skill{}}
}

// Register adds or replaces a skill.
func (r *SkillRegistry) Register(s Skill) {
	if r == nil || s.Name == "" {
		return
	}
	r.mu.Lock()
	r.skills[s.QualifiedName()] = s
	r.mu.Unlock()
}

// All returns a snapshot of every registered skill, sorted by qualified
// name.
func (r *SkillRegistry) All() []Skill {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	out := make([]Skill, 0, len(r.skills))
	for _, s := range r.skills {
		out = append(out, s)
	}
	r.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool {
		return out[i].QualifiedName() < out[j].QualifiedName()
	})
	return out
}

// ResolveSkillName applies the 3-tier lookup strategy. Returns (skill,
// true) when a unique resolution is found. Returns (zero, false) when no
// skill matches.
func (r *SkillRegistry) ResolveSkillName(name string) (Skill, bool) {
	if r == nil {
		return Skill{}, false
	}
	needle := strings.TrimSpace(name)
	if needle == "" {
		return Skill{}, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()

	// 1. Exact qualified/unqualified match.
	if s, ok := r.skills[needle]; ok {
		return s, true
	}
	// 2. Plugin prefix — "my-plugin:verify".
	if idx := strings.Index(needle, ":"); idx > 0 {
		// Need exact plugin:name match — already handled above. Nothing more.
		return Skill{}, false
	}
	// 3. Suffix match (case-insensitive, deterministic).
	lower := strings.ToLower(needle)
	var candidates []string
	for k := range r.skills {
		if strings.ToLower(k) == lower {
			return r.skills[k], true
		}
		if strings.HasSuffix(strings.ToLower(k), ":"+lower) {
			candidates = append(candidates, k)
		}
	}
	if len(candidates) == 0 {
		return Skill{}, false
	}
	sort.Strings(candidates)
	return r.skills[candidates[0]], true
}

// ---------------------------------------------------------------------------
// Per-agent invocation log
// ---------------------------------------------------------------------------

// SkillInvocationLog records which skills were preloaded for each agent.
// ClearInvokedSkillsForAgent flushes an agent's record on cleanup. The
// log is append-only within an agent's lifetime; safe for concurrent use.
type SkillInvocationLog struct {
	mu      sync.Mutex
	byAgent map[string][]Skill
	invoked int // total invocations observed (for analytics)
}

// NewSkillInvocationLog creates an empty log.
func NewSkillInvocationLog() *SkillInvocationLog {
	return &SkillInvocationLog{byAgent: map[string][]Skill{}}
}

// RecordInvocation adds skill to agentID's list.
func (l *SkillInvocationLog) RecordInvocation(agentID string, skill Skill) {
	if l == nil || agentID == "" {
		return
	}
	l.mu.Lock()
	l.byAgent[agentID] = append(l.byAgent[agentID], skill)
	l.invoked++
	l.mu.Unlock()
}

// InvokedFor returns the skills recorded for agentID, in invocation order.
func (l *SkillInvocationLog) InvokedFor(agentID string) []Skill {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	src := l.byAgent[agentID]
	if len(src) == 0 {
		return nil
	}
	return append([]Skill(nil), src...)
}

// ClearInvokedSkillsForAgent deletes agentID's record. Invoked from the
// SpawnSubAgent defer so the log doesn't grow unbounded.
func (l *SkillInvocationLog) ClearInvokedSkillsForAgent(agentID string) {
	if l == nil || agentID == "" {
		return
	}
	l.mu.Lock()
	delete(l.byAgent, agentID)
	l.mu.Unlock()
}

// TotalInvocations returns the running count of every invocation recorded
// across all agents.
func (l *SkillInvocationLog) TotalInvocations() int {
	if l == nil {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.invoked
}

// MakeSkillResolver wires a registry + invocation log into a function
// compatible with EngineConfig.ResolveAgentSkill. Unknown skills return
// (nil, nil) so SpawnSubAgent skips that entry silently (matching TS
// warn-and-continue semantics).
func MakeSkillResolver(reg *SkillRegistry, log *SkillInvocationLog) func(ctx context.Context, agentType, name string) ([]ContentBlock, error) {
	return func(_ context.Context, agentType, name string) ([]ContentBlock, error) {
		if reg == nil {
			return nil, nil
		}
		skill, ok := reg.ResolveSkillName(name)
		if !ok {
			return nil, nil
		}
		if log != nil {
			log.RecordInvocation(agentType, skill)
		}
		if strings.TrimSpace(skill.Body) == "" {
			return nil, nil
		}
		return []ContentBlock{{Type: ContentBlockText, Text: skill.Body}}, nil
	}
}
