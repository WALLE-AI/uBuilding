package permission

import (
	"encoding/json"
	"path/filepath"
	"strings"

	"github.com/wall-ai/ubuilding/backend/agents"
)

// ---------------------------------------------------------------------------
// Checker — permission check chain (deny → allow → ask)
// Maps to TypeScript useCanUseTool.tsx + permissions/
// ---------------------------------------------------------------------------

// Checker evaluates permission rules for tool execution.
// It implements the deny → allow → ask responsibility chain.
type Checker struct {
	mode       Mode
	denyRules  map[string][]Rule
	allowRules map[string][]Rule
	askRules   map[string][]Rule
	cwd        string
}

// NewChecker creates a new permission Checker.
func NewChecker(ctx *agents.ToolPermissionContext, cwd string) *Checker {
	c := &Checker{
		mode:       Mode(ctx.Mode),
		denyRules:  make(map[string][]Rule),
		allowRules: make(map[string][]Rule),
		askRules:   make(map[string][]Rule),
		cwd:        cwd,
	}

	// Convert from agents.PermissionRule to local Rule
	for tool, rules := range ctx.AlwaysDenyRules {
		for _, r := range rules {
			c.denyRules[tool] = append(c.denyRules[tool], Rule{Tool: r.Tool, Pattern: r.Pattern})
		}
	}
	for tool, rules := range ctx.AlwaysAllowRules {
		for _, r := range rules {
			c.allowRules[tool] = append(c.allowRules[tool], Rule{Tool: r.Tool, Pattern: r.Pattern})
		}
	}
	for tool, rules := range ctx.AlwaysAskRules {
		for _, r := range rules {
			c.askRules[tool] = append(c.askRules[tool], Rule{Tool: r.Tool, Pattern: r.Pattern})
		}
	}

	return c
}

// Check evaluates whether a tool is allowed to run with the given input.
// The evaluation chain is: deny → allow → ask → default behavior.
func (c *Checker) Check(toolName string, input json.RawMessage, toolCtx *agents.ToolUseContext) *Result {
	// Bypass mode
	if c.mode == ModeBypassAll {
		return &Result{Behavior: "allow"}
	}
	if c.mode == ModeAutoAccept {
		return &Result{Behavior: "allow"}
	}

	// Step 1: Check deny rules (highest priority)
	if c.matchesRules(toolName, input, c.denyRules) {
		return &Result{
			Behavior: "deny",
			Message:  "Denied by permission rule",
		}
	}

	// Step 2: Check allow rules
	if c.matchesRules(toolName, input, c.allowRules) {
		return &Result{Behavior: "allow"}
	}

	// Step 3: Check ask rules
	if c.matchesRules(toolName, input, c.askRules) {
		return &Result{
			Behavior: "ask",
			Message:  "Requires user approval",
		}
	}

	// Step 4: Default behavior — read-only tools are allowed, others ask
	// In non-interactive mode, default to allow
	if toolCtx != nil && toolCtx.Options.IsNonInteractiveSession {
		return &Result{Behavior: "allow"}
	}

	// For server-side usage, default to allow (SDK mode)
	return &Result{Behavior: "allow"}
}

// matchesRules checks if any rule in the given ruleset matches the tool/input.
func (c *Checker) matchesRules(toolName string, input json.RawMessage, rules map[string][]Rule) bool {
	// Check tool-specific rules
	if toolRules, ok := rules[toolName]; ok {
		for _, rule := range toolRules {
			if rule.Pattern == "" {
				return true // No pattern means match all invocations
			}
			if c.matchPattern(rule.Pattern, input) {
				return true
			}
		}
	}

	// Check wildcard rules
	if wildcardRules, ok := rules["*"]; ok {
		for _, rule := range wildcardRules {
			if rule.Pattern == "" {
				return true
			}
			if c.matchPattern(rule.Pattern, input) {
				return true
			}
		}
	}

	return false
}

// matchPattern checks if a permission pattern matches the tool input.
func (c *Checker) matchPattern(pattern string, input json.RawMessage) bool {
	if pattern == "" {
		return true
	}

	// Parse input to extract command or path
	var inputMap map[string]interface{}
	if err := json.Unmarshal(input, &inputMap); err != nil {
		return false
	}

	// Match against command field (for Bash)
	if cmd, ok := inputMap["command"].(string); ok {
		if matchGlob(pattern, cmd) {
			return true
		}
	}

	// Match against file_path field (for file tools)
	if path, ok := inputMap["file_path"].(string); ok {
		// Resolve relative paths
		if !filepath.IsAbs(path) && c.cwd != "" {
			path = filepath.Join(c.cwd, path)
		}
		if matchGlob(pattern, path) {
			return true
		}
	}

	return false
}

// matchGlob performs simple glob matching.
func matchGlob(pattern, s string) bool {
	// Simple prefix matching for patterns like "git *"
	if strings.HasSuffix(pattern, " *") {
		prefix := strings.TrimSuffix(pattern, " *")
		return strings.HasPrefix(s, prefix+" ") || s == prefix
	}
	if strings.HasSuffix(pattern, "*") {
		prefix := strings.TrimSuffix(pattern, "*")
		return strings.HasPrefix(s, prefix)
	}
	// Exact match
	return s == pattern
}
