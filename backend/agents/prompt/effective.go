package prompt

import "strings"

// ---------------------------------------------------------------------------
// Effective System Prompt Builder — priority-based prompt selection
// Maps to utils/systemPrompt.ts: buildEffectiveSystemPrompt()
// ---------------------------------------------------------------------------

// EffectiveSystemPromptConfig holds parameters for BuildEffectiveSystemPrompt.
type EffectiveSystemPromptConfig struct {
	// AgentDefinition is the main-thread agent definition, if any.
	// When set and it has a system prompt, that takes priority over custom/default.
	AgentSystemPrompt string

	// IsBuiltInAgent indicates this is a built-in agent (not user-defined).
	IsBuiltInAgent bool

	// CustomSystemPrompt is the user-provided custom system prompt.
	CustomSystemPrompt string

	// DefaultSystemPrompt is the full default system prompt sections.
	DefaultSystemPrompt []string

	// AppendSystemPrompt is additional instructions always appended.
	AppendSystemPrompt string

	// OverrideSystemPrompt completely replaces all other prompts when set.
	OverrideSystemPrompt string

	// IsCoordinatorMode enables the coordinator mode prompt path.
	IsCoordinatorMode bool

	// CoordinatorSystemPrompt is the coordinator mode prompt (when enabled).
	CoordinatorSystemPrompt string

	// IsProactiveMode enables the proactive mode behavior (agent prompt appended to default).
	IsProactiveMode bool
}

// BuildEffectiveSystemPrompt constructs the effective system prompt based on
// priority rules. Maps to buildEffectiveSystemPrompt() in utils/systemPrompt.ts.
//
// Priority order:
//  1. OverrideSystemPrompt — replaces everything
//  2. CoordinatorMode — uses coordinator prompt + append
//  3. Agent system prompt — in proactive mode: appended to default;
//     otherwise replaces default
//  4. Custom system prompt — replaces default
//  5. Default system prompt — fallback
//
// AppendSystemPrompt is always appended regardless of which path is taken.
func BuildEffectiveSystemPrompt(cfg EffectiveSystemPromptConfig) string {
	// Path 1: Override — replaces everything
	if cfg.OverrideSystemPrompt != "" {
		return cfg.OverrideSystemPrompt
	}

	// Path 2: Coordinator mode
	if cfg.IsCoordinatorMode && cfg.CoordinatorSystemPrompt != "" {
		parts := []string{cfg.CoordinatorSystemPrompt}
		if cfg.AppendSystemPrompt != "" {
			parts = append(parts, cfg.AppendSystemPrompt)
		}
		return strings.Join(parts, "\n\n")
	}

	// Path 3: Agent system prompt
	if cfg.AgentSystemPrompt != "" {
		// Proactive mode: append agent prompt to default (not replace)
		if cfg.IsProactiveMode {
			parts := make([]string, 0, len(cfg.DefaultSystemPrompt)+2)
			parts = append(parts, cfg.DefaultSystemPrompt...)
			parts = append(parts, "\n# Custom Agent Instructions\n"+cfg.AgentSystemPrompt)
			if cfg.AppendSystemPrompt != "" {
				parts = append(parts, cfg.AppendSystemPrompt)
			}
			return strings.Join(parts, "\n\n")
		}

		// Normal mode: agent prompt replaces default
		parts := []string{cfg.AgentSystemPrompt}
		if cfg.AppendSystemPrompt != "" {
			parts = append(parts, cfg.AppendSystemPrompt)
		}
		return strings.Join(parts, "\n\n")
	}

	// Path 4: Custom system prompt — replaces default
	if cfg.CustomSystemPrompt != "" {
		parts := []string{cfg.CustomSystemPrompt}
		if cfg.AppendSystemPrompt != "" {
			parts = append(parts, cfg.AppendSystemPrompt)
		}
		return strings.Join(parts, "\n\n")
	}

	// Path 5: Default system prompt
	parts := make([]string, 0, len(cfg.DefaultSystemPrompt)+1)
	parts = append(parts, cfg.DefaultSystemPrompt...)
	if cfg.AppendSystemPrompt != "" {
		parts = append(parts, cfg.AppendSystemPrompt)
	}
	return strings.Join(parts, "\n\n")
}

// AsSystemPrompt concatenates multiple prompt parts into a single prompt string.
// Maps to asSystemPrompt() in systemPrompt.ts.
func AsSystemPrompt(parts []string) string {
	var nonEmpty []string
	for _, p := range parts {
		if strings.TrimSpace(p) != "" {
			nonEmpty = append(nonEmpty, p)
		}
	}
	return strings.Join(nonEmpty, "\n\n")
}
