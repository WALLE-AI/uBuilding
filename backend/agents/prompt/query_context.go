package prompt

// ---------------------------------------------------------------------------
// Query Context — assembles system prompt parts for a query call
// Maps to utils/queryContext.ts: fetchSystemPromptParts()
// ---------------------------------------------------------------------------

// SystemPromptParts holds the assembled prompt components used for the API call.
// Maps to the return type of fetchSystemPromptParts() in queryContext.ts.
type SystemPromptParts struct {
	// DefaultSystemPrompt is the full default system prompt sections.
	// Empty slice when customSystemPrompt is set (custom replaces default).
	DefaultSystemPrompt []string

	// UserContext maps to { claudeMd, currentDate } injected before messages.
	UserContext map[string]string

	// SystemContext maps to { gitStatus } appended to the system prompt.
	SystemContext map[string]string
}

// FetchSystemPromptPartsConfig holds parameters for FetchSystemPromptParts.
type FetchSystemPromptPartsConfig struct {
	// GetSystemPromptCfg is the configuration for GetSystemPrompt.
	GetSystemPromptCfg GetSystemPromptConfig

	// ContextProvider provides user/system context.
	ContextProvider *ContextProvider

	// CustomSystemPrompt overrides the default system prompt when set.
	// When non-empty, DefaultSystemPrompt is returned as empty and
	// SystemContext is returned as empty (matching TS behavior).
	CustomSystemPrompt string
}

// FetchSystemPromptParts computes the system prompt parts for a query.
// Maps to fetchSystemPromptParts() in utils/queryContext.ts.
//
// When a customSystemPrompt is provided:
//   - DefaultSystemPrompt is returned as [] (empty)
//   - SystemContext is returned as {} (empty)
//   - UserContext is still computed normally
//
// This matches the TypeScript behavior where a custom prompt skips both
// the default prompt computation and the system context.
func FetchSystemPromptParts(cfg FetchSystemPromptPartsConfig) *SystemPromptParts {
	parts := &SystemPromptParts{
		DefaultSystemPrompt: []string{},
		UserContext:         make(map[string]string),
		SystemContext:       make(map[string]string),
	}

	// Default system prompt (skipped when custom prompt is set)
	if cfg.CustomSystemPrompt == "" {
		parts.DefaultSystemPrompt = GetSystemPrompt(cfg.GetSystemPromptCfg)
	}

	// User context (always computed)
	if cfg.ContextProvider != nil {
		uc := cfg.ContextProvider.GetUserContext()
		if uc != nil {
			parts.UserContext = uc.ToMap()
		}
	}

	// System context (skipped when custom prompt is set)
	if cfg.CustomSystemPrompt == "" && cfg.ContextProvider != nil {
		sc := cfg.ContextProvider.GetSystemContext()
		if sc != nil {
			parts.SystemContext = sc.ToMap()
		}
	}

	return parts
}

// FetchSystemPromptPartsForSideQuestion builds cache-safe prompt parts for
// side questions (compact, session_memory, etc.) that need to share the
// same cache key prefix as the main query.
// Maps to buildSideQuestionParams() in queryContext.ts.
func FetchSystemPromptPartsForSideQuestion(
	mainParts *SystemPromptParts,
) *SystemPromptParts {
	// Side questions reuse the main query's prompt parts to share cache prefix.
	return &SystemPromptParts{
		DefaultSystemPrompt: mainParts.DefaultSystemPrompt,
		UserContext:         mainParts.UserContext,
		SystemContext:       mainParts.SystemContext,
	}
}
