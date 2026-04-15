package agents

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ---------------------------------------------------------------------------
// Attachment Framework
// Maps to TypeScript attachments.ts — getAttachments() and processAtMentionedFiles()
// ---------------------------------------------------------------------------

// AttachmentType enumerates the recognized attachment types.
type AttachmentType string

const (
	AttachmentTypeFile              AttachmentType = "file"
	AttachmentTypeDirectory         AttachmentType = "directory"
	AttachmentTypeQueuedCommand     AttachmentType = "queued_command"
	AttachmentTypeDateChange        AttachmentType = "date_change"
	AttachmentTypeDiagnostics       AttachmentType = "diagnostics"
	AttachmentTypeOutputStyle       AttachmentType = "output_style"
	AttachmentTypeTokenUsage        AttachmentType = "token_usage"
	AttachmentTypeBudgetUSD         AttachmentType = "budget_usd"
	AttachmentTypeCriticalReminder  AttachmentType = "critical_system_reminder"
	AttachmentTypeTodoReminder      AttachmentType = "todo_reminder"
	AttachmentTypeMaxTurnsReached   AttachmentType = "max_turns_reached"
	AttachmentTypePlanMode          AttachmentType = "plan_mode"
	AttachmentTypeCompactReminder   AttachmentType = "compaction_reminder"
	AttachmentTypeSkillListing      AttachmentType = "skill_listing"
	AttachmentTypeRelevantMemories  AttachmentType = "relevant_memories"
)

// ---------------------------------------------------------------------------
// File attachment — reading @-mentioned files
// ---------------------------------------------------------------------------

// FileAttachmentResult holds the result of reading a file for attachment.
type FileAttachmentResult struct {
	Filename    string `json:"filename"`
	Content     string `json:"content"`
	Truncated   bool   `json:"truncated,omitempty"`
	DisplayPath string `json:"display_path"`
}

// ProcessAtMentionedFiles extracts @-mentioned file paths from input and reads them.
// Maps to TS processAtMentionedFiles() in attachments.ts.
//
// Patterns recognized:
//   - @/absolute/path — absolute file path
//   - @relative/path — relative to cwd
//   - @./relative/path — explicit relative
func ProcessAtMentionedFiles(input string, cwd string, maxBytes int) []FileAttachmentResult {
	if maxBytes == 0 {
		maxBytes = 100_000 // ~100KB default per file
	}

	var results []FileAttachmentResult
	seen := make(map[string]bool)

	// Split on whitespace and find @-prefixed tokens
	for _, token := range strings.Fields(input) {
		if !strings.HasPrefix(token, "@") {
			continue
		}
		path := token[1:]
		if path == "" {
			continue
		}

		// Resolve relative paths
		if !filepath.IsAbs(path) {
			path = filepath.Join(cwd, path)
		}
		path = filepath.Clean(path)

		if seen[path] {
			continue
		}
		seen[path] = true

		// Read the file
		content, truncated, err := readFileForAttachment(path, maxBytes)
		if err != nil {
			continue // skip files that can't be read
		}

		displayPath, _ := filepath.Rel(cwd, path)
		if displayPath == "" {
			displayPath = path
		}

		results = append(results, FileAttachmentResult{
			Filename:    path,
			Content:     content,
			Truncated:   truncated,
			DisplayPath: displayPath,
		})
	}
	return results
}

// readFileForAttachment reads a file with a byte limit.
// Returns (content, truncated, error).
func readFileForAttachment(path string, maxBytes int) (string, bool, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", false, err
	}
	if info.IsDir() {
		return "", false, fmt.Errorf("path is a directory: %s", path)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return "", false, err
	}

	if len(data) > maxBytes {
		return string(data[:maxBytes]), true, nil
	}
	return string(data), false, nil
}

// ---------------------------------------------------------------------------
// Attachment message builders
// ---------------------------------------------------------------------------

// CreateFileAttachmentMessage creates an attachment message for a read file.
func CreateFileAttachmentMessage(file FileAttachmentResult) Message {
	return Message{
		Type: MessageTypeAttachment,
		Attachment: &AttachmentData{
			Type:    string(AttachmentTypeFile),
			Content: file,
		},
	}
}

// CreateQueuedCommandAttachment creates an attachment for a queued command.
func CreateQueuedCommandAttachment(prompt string, sourceUUID string) Message {
	return Message{
		Type: MessageTypeAttachment,
		Attachment: &AttachmentData{
			Type:       string(AttachmentTypeQueuedCommand),
			Prompt:     prompt,
			SourceUUID: sourceUUID,
		},
	}
}

// CreateMaxTurnsAttachment creates an attachment when max turns is reached.
func CreateMaxTurnsAttachment(maxTurns, turnCount int) Message {
	return Message{
		Type: MessageTypeAttachment,
		Attachment: &AttachmentData{
			Type:      string(AttachmentTypeMaxTurnsReached),
			MaxTurns:  maxTurns,
			TurnCount: turnCount,
		},
	}
}

// CreateCriticalReminderAttachment creates a critical system reminder.
func CreateCriticalReminderAttachment(content string) Message {
	return Message{
		Type: MessageTypeAttachment,
		Attachment: &AttachmentData{
			Type:    string(AttachmentTypeCriticalReminder),
			Content: content,
		},
	}
}

// ---------------------------------------------------------------------------
// GetAttachments — main attachment collection function
// ---------------------------------------------------------------------------

// AttachmentOptions configures which attachments to generate.
type AttachmentOptions struct {
	Input    string
	ToolCtx  *ToolUseContext
	Messages []Message
	Cwd      string

	// Feature toggles
	SkipSkillDiscovery bool
	IsSubAgent         bool
}

// AttachmentProvider generates a specific type of attachment.
// Returns nil messages if the attachment doesn't apply.
type AttachmentProvider func(ctx context.Context, opts AttachmentOptions) []Message

// AttachmentCollector manages registered attachment providers.
type AttachmentCollector struct {
	providers []namedProvider
}

type namedProvider struct {
	name     string
	provider AttachmentProvider
}

// NewAttachmentCollector creates a new collector.
func NewAttachmentCollector() *AttachmentCollector {
	return &AttachmentCollector{}
}

// Register adds a named attachment provider.
func (c *AttachmentCollector) Register(name string, provider AttachmentProvider) {
	c.providers = append(c.providers, namedProvider{name: name, provider: provider})
}

// Collect runs all providers and returns the combined attachment messages.
// Providers that return nil are silently skipped.
func (c *AttachmentCollector) Collect(ctx context.Context, opts AttachmentOptions) []Message {
	var all []Message
	for _, np := range c.providers {
		if ctx.Err() != nil {
			break
		}
		msgs := np.provider(ctx, opts)
		all = append(all, msgs...)
	}
	return all
}

// ---------------------------------------------------------------------------
// Built-in attachment providers
// ---------------------------------------------------------------------------

// AtMentionedFilesProvider extracts @-mentioned files from user input.
func AtMentionedFilesProvider(ctx context.Context, opts AttachmentOptions) []Message {
	if opts.Input == "" || !strings.Contains(opts.Input, "@") {
		return nil
	}
	files := ProcessAtMentionedFiles(opts.Input, opts.Cwd, 0)
	var msgs []Message
	for _, f := range files {
		msgs = append(msgs, CreateFileAttachmentMessage(f))
	}
	return msgs
}

// RegisterDefaultProviders adds the built-in attachment providers.
func RegisterDefaultProviders(collector *AttachmentCollector) {
	collector.Register("at_mentioned_files", AtMentionedFilesProvider)
	// Additional providers (diagnostics, IDE selection, etc.) are registered
	// by the caller based on feature flags and platform capabilities.
}
