package prompt

import (
	"fmt"
	"strings"
)

// ---------------------------------------------------------------------------
// API Context Injection — prepend user context / append system context
// Maps to prependUserContext() and appendSystemContext() in TS utils/api.ts
// ---------------------------------------------------------------------------

// MessageLike is a minimal message interface for context injection.
// The actual Message type is defined in the agents package; this interface
// allows the prompt package to operate on messages without importing agents.
type MessageLike struct {
	Type    string
	Content string
}

// PrependUserContextToPrompt prepends user context (claudeMd, currentDate)
// to the first user message's content. Returns the modified content string.
// Maps to prependUserContext() in utils/api.ts.
//
// The user context is injected as XML-tagged blocks before the actual message:
//
//	<claudeMd>...content...</claudeMd>
//	<currentDate>Today's date is YYYY-MM-DD.</currentDate>
//	[actual user message]
func PrependUserContextToPrompt(userContent string, userContext map[string]string) string {
	if len(userContext) == 0 {
		return userContent
	}

	var prefix []string

	// Order matters for cache stability — claudeMd first, then currentDate
	if v, ok := userContext["claudeMd"]; ok && v != "" {
		prefix = append(prefix, fmt.Sprintf("<claudeMd>\n%s\n</claudeMd>", v))
	}
	if v, ok := userContext["currentDate"]; ok && v != "" {
		prefix = append(prefix, fmt.Sprintf("<currentDate>%s</currentDate>", v))
	}

	// Any additional context keys
	for k, v := range userContext {
		if k == "claudeMd" || k == "currentDate" || v == "" {
			continue
		}
		prefix = append(prefix, fmt.Sprintf("<%s>%s</%s>", k, v, k))
	}

	if len(prefix) == 0 {
		return userContent
	}

	return strings.Join(prefix, "\n") + "\n\n" + userContent
}

// AppendSystemContextToPrompt appends system context (gitStatus, etc.)
// to the system prompt string. Returns the modified prompt.
// Maps to the system context injection logic in the TS API layer.
//
// Each context entry is appended as an XML-tagged block:
//
//	<gitStatus>...git status content...</gitStatus>
func AppendSystemContextToPrompt(systemPrompt string, systemContext map[string]string) string {
	if len(systemContext) == 0 {
		return systemPrompt
	}

	var suffix []string
	for k, v := range systemContext {
		if v == "" {
			continue
		}
		suffix = append(suffix, fmt.Sprintf("<%s>\n%s\n</%s>", k, v, k))
	}

	if len(suffix) == 0 {
		return systemPrompt
	}

	return systemPrompt + "\n\n" + strings.Join(suffix, "\n\n")
}
