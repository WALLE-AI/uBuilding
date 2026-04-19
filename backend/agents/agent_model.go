// Package agents — model-resolution helpers for sub-agents.
//
// Task A17 · ports the essential parts of src/utils/model/agent.ts:
//   - GetAgentModel : 4-level resolution chain
//   - GetDefaultSubagentModel : returns "inherit"
//   - GetQuerySourceForAgent : analytics / cache prefix
//
// The TS original pulls in Bedrock region-prefix inheritance and the
// runtime "opusplan" resolver. We keep the resolution rules faithful but
// externalise those specifics via small helpers so the default build stays
// provider-agnostic. Hosts can inject richer helpers later.
package agents

import (
	"os"
	"strings"
)

// AgentModelAlias lists the aliases the Task tool's `model` param accepts.
// Mirrors the TS MODEL_ALIASES union. Runtime model strings may still be
// full provider ids; the alias set is only for frontmatter / tool-arg input.
var AgentModelAlias = []string{"sonnet", "opus", "haiku", "inherit"}

// IsAgentModelAlias reports whether s is one of the public aliases.
func IsAgentModelAlias(s string) bool {
	for _, a := range AgentModelAlias {
		if strings.EqualFold(s, a) {
			return true
		}
	}
	return false
}

// GetDefaultSubagentModel returns the default frontmatter model string for
// sub-agents. TS runs "inherit" so sub-agents share the parent's main loop
// model unless an override is provided.
func GetDefaultSubagentModel() string {
	return "inherit"
}

// GetAgentModel resolves the effective model id for a sub-agent invocation.
//
// Priority (mirrors src/utils/model/agent.ts::getAgentModel):
//  1. CLAUDE_CODE_SUBAGENT_MODEL env var (universal override).
//  2. toolSpecifiedModel  — tool-call parameter (model="sonnet" etc).
//     If the alias matches the parent's tier, inherit parent verbatim so a
//     "sonnet" tool arg doesn't downgrade a pinned Sonnet 4.x parent.
//  3. agentModel          — frontmatter model from AgentDefinition.Model.
//     "inherit" (or blank) → parent model; other values resolve via alias
//     match or pass through as a concrete id.
//  4. parent model         — fallback when nothing above is set.
//
// permissionMode is accepted for future "opusplan"-style runtime resolution
// but is not consulted in this minimal port.
//
// Bedrock region-prefix inheritance is out of scope for the external build;
// hosts that need it can override via CLAUDE_CODE_SUBAGENT_MODEL.
func GetAgentModel(agentModel, parentModel, toolSpecifiedModel, permissionMode string) string {
	// (1) universal env override.
	if v := strings.TrimSpace(os.Getenv("CLAUDE_CODE_SUBAGENT_MODEL")); v != "" {
		return v
	}

	// (2) tool-arg takes precedence over frontmatter.
	if toolSpecifiedModel != "" {
		if aliasMatchesParentTier(toolSpecifiedModel, parentModel) {
			return parentModel
		}
		return toolSpecifiedModel
	}

	// (3) agent frontmatter.
	m := agentModel
	if m == "" {
		m = GetDefaultSubagentModel()
	}
	if strings.EqualFold(m, "inherit") {
		return parentModel
	}
	if aliasMatchesParentTier(m, parentModel) {
		return parentModel
	}
	return m
}

// aliasMatchesParentTier reports whether a bare family alias matches the
// tier of the parent model. Prevents surprising downgrades when a pinned
// model family parent spawns an alias-configured sub-agent.
func aliasMatchesParentTier(alias, parentModel string) bool {
	if alias == "" || parentModel == "" {
		return false
	}
	canonical := strings.ToLower(parentModel)
	switch strings.ToLower(alias) {
	case "opus":
		return strings.Contains(canonical, "opus")
	case "sonnet":
		return strings.Contains(canonical, "sonnet")
	case "haiku":
		return strings.Contains(canonical, "haiku")
	default:
		return false
	}
}

// GetQuerySourceForAgent produces the analytics + cache prefix used on
// QueryLoop. Mirrors src/utils/promptCategory.ts::getQuerySourceForAgent —
// built-in agents get `agent:builtin:<type>`, custom/plugin `agent:user:<type>`.
func GetQuerySourceForAgent(agentType string, isBuiltIn bool) string {
	if agentType == "" {
		if isBuiltIn {
			return "agent:builtin"
		}
		return "agent:user"
	}
	prefix := "agent:user"
	if isBuiltIn {
		prefix = "agent:builtin"
	}
	return prefix + ":" + agentType
}
