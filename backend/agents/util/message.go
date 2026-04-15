package util

import (
	"github.com/wall-ai/ubuilding/backend/agents"
)

// CreateUserMessage builds a user-type Message.
func CreateUserMessage(opts UserMessageOpts) agents.Message {
	msg := agents.Message{
		Type:    agents.MessageTypeUser,
		UUID:    NewUUID(),
		Content: opts.Content,
		IsMeta:  opts.IsMeta,
	}
	if opts.ToolUseResult != "" {
		msg.ToolUseResult = opts.ToolUseResult
	}
	if opts.SourceToolAssistantUUID != "" {
		msg.SourceToolAssistantUUID = opts.SourceToolAssistantUUID
	}
	return msg
}

// UserMessageOpts are options for creating a user message.
type UserMessageOpts struct {
	Content                 []agents.ContentBlock
	IsMeta                  bool
	ToolUseResult           string
	SourceToolAssistantUUID string
}

// CreateSystemMessage builds a system-type Message.
func CreateSystemMessage(text string, level string) agents.Message {
	return agents.Message{
		Type:  agents.MessageTypeSystem,
		UUID:  NewUUID(),
		Level: level,
		Content: []agents.ContentBlock{
			{Type: agents.ContentBlockText, Text: text},
		},
	}
}

// CreateAssistantAPIErrorMessage builds an assistant error message.
func CreateAssistantAPIErrorMessage(content string) agents.Message {
	return agents.Message{
		Type:       agents.MessageTypeAssistant,
		UUID:       NewUUID(),
		IsApiError: true,
		Content: []agents.ContentBlock{
			{Type: agents.ContentBlockText, Text: content},
		},
	}
}

// CreateAttachmentMessage builds an attachment-type Message.
func CreateAttachmentMessage(attachment agents.AttachmentData) agents.Message {
	return agents.Message{
		Type:       agents.MessageTypeAttachment,
		UUID:       NewUUID(),
		Attachment: &attachment,
	}
}

// CreateUserInterruptionMessage builds a system message for user interruption.
func CreateUserInterruptionMessage(toolUse bool) agents.Message {
	text := "[Request interrupted by user]"
	if toolUse {
		text = "[Request interrupted by user during tool execution]"
	}
	return CreateSystemMessage(text, "info")
}

// ExtractToolUseBlocks extracts all tool_use content blocks from a message.
func ExtractToolUseBlocks(msg *agents.Message) []agents.ToolUseBlock {
	var blocks []agents.ToolUseBlock
	for _, b := range msg.Content {
		if b.Type == agents.ContentBlockToolUse {
			blocks = append(blocks, agents.ToolUseBlock{
				ID:    b.ID,
				Name:  b.Name,
				Input: b.Input,
			})
		}
	}
	return blocks
}

// IsPromptTooLongMessage checks if a message is a prompt-too-long API error.
func IsPromptTooLongMessage(msg *agents.Message) bool {
	if msg == nil || !msg.IsApiError {
		return false
	}
	for _, b := range msg.Content {
		if b.Type == agents.ContentBlockText {
			// Check for common prompt-too-long indicators
			if contains(b.Text, "prompt is too long") || contains(b.Text, "prompt_too_long") {
				return true
			}
		}
	}
	return false
}

// IsMaxOutputTokensMessage checks if the stop reason is max_tokens.
func IsMaxOutputTokensMessage(msg *agents.Message) bool {
	return msg != nil && msg.StopReason == "max_tokens"
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// GetMessagesAfterCompactBoundary returns messages after the last compact_boundary.
// Maps to TypeScript getMessagesAfterCompactBoundary() in utils/messages.ts.
// If no compact boundary exists, returns a copy of the full message list.
func GetMessagesAfterCompactBoundary(messages []agents.Message) []agents.Message {
	lastBoundary := -1
	for i, msg := range messages {
		if msg.Subtype == "compact_boundary" || msg.Subtype == "snip_boundary" {
			lastBoundary = i
		}
	}
	if lastBoundary == -1 {
		result := make([]agents.Message, len(messages))
		copy(result, messages)
		return result
	}
	// Include the boundary message itself
	result := make([]agents.Message, len(messages)-lastBoundary)
	copy(result, messages[lastBoundary:])
	return result
}

// NormalizeMessagesForAPI filters and normalizes messages for API consumption.
// Strips system/attachment messages and keeps user/assistant messages that
// the Anthropic API accepts.
func NormalizeMessagesForAPI(messages []agents.Message) []agents.Message {
	var result []agents.Message
	for _, msg := range messages {
		switch msg.Type {
		case agents.MessageTypeUser, agents.MessageTypeAssistant:
			result = append(result, msg)
		}
	}
	return result
}

// IsMediaSizeError checks if a message is a media (image/PDF) size error.
// Maps to TypeScript isWithheldMediaSizeError() in reactiveCompact.ts.
func IsMediaSizeError(msg *agents.Message) bool {
	if msg == nil || !msg.IsApiError {
		return false
	}
	for _, b := range msg.Content {
		if b.Type == agents.ContentBlockText {
			if contains(b.Text, "image exceeds") ||
				contains(b.Text, "image is too large") ||
				contains(b.Text, "too many images") ||
				contains(b.Text, "media_size") {
				return true
			}
		}
	}
	return false
}

// IsWithheldMaxOutputTokens checks if a message is a max_output_tokens error
// that should be withheld during streaming for potential recovery.
// Maps to TypeScript isWithheldMaxOutputTokens() in query.ts L175-179.
func IsWithheldMaxOutputTokens(msg *agents.Message) bool {
	if msg == nil {
		return false
	}
	return msg.Type == agents.MessageTypeAssistant &&
		(msg.StopReason == "max_tokens" || isApiErrorMaxOutputTokens(msg))
}

func isApiErrorMaxOutputTokens(msg *agents.Message) bool {
	if !msg.IsApiError {
		return false
	}
	for _, b := range msg.Content {
		if b.Type == agents.ContentBlockText && contains(b.Text, "max_output_tokens") {
			return true
		}
	}
	return false
}

// PrependUserContext prepends user context entries to the first user message.
// Maps to TypeScript prependUserContext() in utils/api.ts.
func PrependUserContext(messages []agents.Message, userContext map[string]string) []agents.Message {
	if len(userContext) == 0 {
		return messages
	}
	result := make([]agents.Message, len(messages))
	copy(result, messages)
	// Find first user message and prepend context
	for i, msg := range result {
		if msg.Type == agents.MessageTypeUser {
			var prefix string
			for k, v := range userContext {
				prefix += "<" + k + ">\n" + v + "\n</" + k + ">\n"
			}
			if len(msg.Content) > 0 && msg.Content[0].Type == agents.ContentBlockText {
				newContent := make([]agents.ContentBlock, len(msg.Content))
				copy(newContent, msg.Content)
				newContent[0] = agents.ContentBlock{
					Type: agents.ContentBlockText,
					Text: prefix + newContent[0].Text,
				}
				result[i].Content = newContent
			}
			break
		}
	}
	return result
}

// AppendSystemContext appends system context to the system prompt.
// Maps to TypeScript appendSystemContext() in utils/api.ts.
func AppendSystemContext(systemPrompt string, systemContext map[string]string) string {
	if len(systemContext) == 0 {
		return systemPrompt
	}
	result := systemPrompt
	for k, v := range systemContext {
		result += "\n\n<" + k + ">\n" + v + "\n</" + k + ">"
	}
	return result
}
