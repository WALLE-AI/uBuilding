package prompt

import (
	"sync"
)

// ---------------------------------------------------------------------------
// SystemPromptSection — memoized section cache mechanism
// Maps to constants/systemPromptSections.ts
// ---------------------------------------------------------------------------

// ComputeFn is a function that computes a prompt section value.
type ComputeFn func() (string, error)

// SystemPromptSectionDef defines a single system prompt section.
type SystemPromptSectionDef struct {
	// Name is the unique identifier for this section.
	Name string

	// Compute generates the section content.
	Compute ComputeFn

	// CacheBreak when true forces recomputation every turn (DANGEROUS_uncached).
	CacheBreak bool

	// Reason documents why cache-breaking is necessary (for DANGEROUS_uncached).
	Reason string
}

// NewSystemPromptSection creates a memoized system prompt section.
// Computed once, cached until Clear() is called (on /clear or /compact).
// Maps to systemPromptSection() in systemPromptSections.ts.
func NewSystemPromptSection(name string, compute ComputeFn) SystemPromptSectionDef {
	return SystemPromptSectionDef{
		Name:    name,
		Compute: compute,
	}
}

// NewDangerousUncachedSection creates a volatile section that recomputes every turn.
// This WILL break the prompt cache when the value changes.
// Maps to DANGEROUS_uncachedSystemPromptSection() in systemPromptSections.ts.
func NewDangerousUncachedSection(name string, compute ComputeFn, reason string) SystemPromptSectionDef {
	return SystemPromptSectionDef{
		Name:       name,
		Compute:    compute,
		CacheBreak: true,
		Reason:     reason,
	}
}

// SectionCache caches computed system prompt section values.
// Thread-safe via sync.Map. Entries survive until Clear() is called.
type SectionCache struct {
	cache sync.Map // map[string]string
}

// NewSectionCache creates a new empty section cache.
func NewSectionCache() *SectionCache {
	return &SectionCache{}
}

// Get retrieves a cached section value. Returns ("", false) on miss.
func (c *SectionCache) Get(name string) (string, bool) {
	val, ok := c.cache.Load(name)
	if !ok {
		return "", false
	}
	return val.(string), true
}

// Set stores a section value in the cache.
func (c *SectionCache) Set(name string, value string) {
	c.cache.Store(name, value)
}

// Clear removes all cached sections. Called on /clear and /compact.
// Maps to clearSystemPromptSections() in systemPromptSections.ts.
func (c *SectionCache) Clear() {
	c.cache.Range(func(key, _ interface{}) bool {
		c.cache.Delete(key)
		return true
	})
}

// ResolveSystemPromptSections resolves all sections, using the cache for
// non-CacheBreak sections. Returns the resolved prompt strings (empty strings
// for sections that compute to "").
// Maps to resolveSystemPromptSections() in systemPromptSections.ts.
func ResolveSystemPromptSections(sections []SystemPromptSectionDef, cache *SectionCache) ([]string, error) {
	results := make([]string, len(sections))

	for i, s := range sections {
		// Check cache for non-volatile sections
		if !s.CacheBreak {
			if cached, ok := cache.Get(s.Name); ok {
				results[i] = cached
				continue
			}
		}

		// Compute the section
		value, err := s.Compute()
		if err != nil {
			return nil, err
		}

		// Cache it
		cache.Set(s.Name, value)
		results[i] = value
	}

	return results, nil
}
