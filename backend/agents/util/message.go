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
