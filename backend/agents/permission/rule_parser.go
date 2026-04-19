// Package permission — rule value parsing.
//
// Task B09 · ports src/utils/permissions/permissionRuleParser.ts's core
// helper for splitting "ToolName(content)"-style rule strings into their
// name + content parts. Used by:
//
//   - resolveAgentTools (B01/B08) to interpret agent frontmatter tool
//     entries like `Bash(git *)` or `Agent(worker,researcher)`.
//   - filterDeniedAgents (B12) to match `Agent(name)` deny rules against
//     candidate agent types.
//
// The syntax is deliberately lenient: unparseable inputs round-trip as
// {Tool: s} so callers can still make progress (TS does the same).
package permission

import "strings"

// RuleValue represents a parsed permission rule value. Tool is the tool
// name (first segment of the string, before any parens). Pattern is the
// optional inner content (e.g. "git *" for Bash rules, "worker" for Agent
// rules). Zero value means "unparsed"; ok reports whether parentheses were
// present and balanced.
type RuleValue struct {
	Tool    string
	Pattern string
	HasArgs bool
}

// ParseRuleValue breaks a permission rule string of the form
// "Name" or "Name(content)" into its parts. Unbalanced or empty inputs
// return the verbatim string as Tool and HasArgs=false, matching TS's
// permissionRuleValueFromString pass-through behaviour.
//
// Whitespace around the outer boundary is trimmed; inner content is
// preserved verbatim so callers can keep their own globbing semantics.
func ParseRuleValue(s string) RuleValue {
	trim := strings.TrimSpace(s)
	if trim == "" {
		return RuleValue{}
	}
	open := strings.Index(trim, "(")
	if open <= 0 {
		// No "(", or it's the first character — treat as a bare name.
		return RuleValue{Tool: trim}
	}
	if !strings.HasSuffix(trim, ")") {
		// Unbalanced — defensively treat as bare name.
		return RuleValue{Tool: trim}
	}
	name := strings.TrimSpace(trim[:open])
	inner := trim[open+1 : len(trim)-1]
	return RuleValue{Tool: name, Pattern: inner, HasArgs: true}
}

// ParseCommaList splits `content` on commas, trims whitespace, and drops
// empty entries. Used to interpret `Agent(worker, researcher)` style
// rule arguments. Returns nil (not []string{}) when no non-empty entries
// remain — keeps DeepEqual-friendly.
func ParseCommaList(content string) []string {
	if content == "" {
		return nil
	}
	var out []string
	for _, p := range strings.Split(content, ",") {
		t := strings.TrimSpace(p)
		if t != "" {
			out = append(out, t)
		}
	}
	return out
}
