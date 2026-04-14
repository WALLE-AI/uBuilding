package prompt

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// SystemPromptBuilder — 6-layer system prompt construction
// Maps to TypeScript utils/systemPrompt.ts + prompt/system.go from the plan
// ---------------------------------------------------------------------------

// SystemPromptBuilder constructs the multi-layer system prompt.
// Layers (ordered for prompt cache stability):
//   [1] Base prompt (most stable, highest cache hit)
//   [2] Tool descriptions (changes when tools change)
//   [3] Memories (CLAUDE.md content)
//   [4] Environment context (dynamic: OS, time, cwd, git)
//   [5] Custom system prompt (user-provided)
//   [6] Append system prompt (additional instructions)
type SystemPromptBuilder struct {
	basePrompt       string
	toolDescriptions string
	memories         string
	envContext       string
	customPrompt     string
	appendPrompt     string
}

// NewSystemPromptBuilder creates a new builder.
func NewSystemPromptBuilder() *SystemPromptBuilder {
	return &SystemPromptBuilder{}
}

// WithBasePrompt sets the base prompt (layer 1).
func (b *SystemPromptBuilder) WithBasePrompt(prompt string) *SystemPromptBuilder {
	b.basePrompt = prompt
	return b
}

// WithToolDescriptions sets the tool descriptions (layer 2).
func (b *SystemPromptBuilder) WithToolDescriptions(desc string) *SystemPromptBuilder {
	b.toolDescriptions = desc
	return b
}

// WithMemories sets the memory/CLAUDE.md content (layer 3).
func (b *SystemPromptBuilder) WithMemories(memories string) *SystemPromptBuilder {
	b.memories = memories
	return b
}

// WithEnvironmentContext sets the environment context (layer 4).
func (b *SystemPromptBuilder) WithEnvironmentContext(ctx string) *SystemPromptBuilder {
	b.envContext = ctx
	return b
}

// WithCustomPrompt sets the user-provided custom prompt (layer 5).
func (b *SystemPromptBuilder) WithCustomPrompt(prompt string) *SystemPromptBuilder {
	b.customPrompt = prompt
	return b
}

// WithAppendPrompt sets the append prompt (layer 6).
func (b *SystemPromptBuilder) WithAppendPrompt(prompt string) *SystemPromptBuilder {
	b.appendPrompt = prompt
	return b
}

// Build assembles the final system prompt from all layers.
func (b *SystemPromptBuilder) Build() string {
	var parts []string

	if b.basePrompt != "" {
		parts = append(parts, b.basePrompt)
	}

	if b.toolDescriptions != "" {
		parts = append(parts, b.toolDescriptions)
	}

	if b.memories != "" {
		parts = append(parts, b.memories)
	}

	if b.envContext != "" {
		parts = append(parts, b.envContext)
	} else {
		// Generate default environment context
		parts = append(parts, BuildEnvironmentContext(""))
	}

	if b.customPrompt != "" {
		parts = append(parts, b.customPrompt)
	}

	if b.appendPrompt != "" {
		parts = append(parts, b.appendPrompt)
	}

	return strings.Join(parts, "\n\n")
}

// BuildEnvironmentContext generates the environment context string.
// Maps to TypeScript envContext.ts.
func BuildEnvironmentContext(cwd string) string {
	if cwd == "" {
		cwd, _ = os.Getwd()
	}

	now := time.Now()
	parts := []string{
		"<environment>",
		fmt.Sprintf("<os>%s/%s</os>", runtime.GOOS, runtime.GOARCH),
		fmt.Sprintf("<cwd>%s</cwd>", cwd),
		fmt.Sprintf("<date>%s</date>", now.Format("2006-01-02")),
		fmt.Sprintf("<time>%s</time>", now.Format("15:04:05 MST")),
	}

	// Add shell info based on OS
	shell := os.Getenv("SHELL")
	if shell == "" {
		if runtime.GOOS == "windows" {
			shell = "powershell"
		} else {
			shell = "/bin/bash"
		}
	}
	parts = append(parts, fmt.Sprintf("<shell>%s</shell>", shell))

	parts = append(parts, "</environment>")
	return strings.Join(parts, "\n")
}

// BuildToolDescriptions generates the tool descriptions block from a list of tools.
// Each tool contributes its Prompt() output, concatenated in stable order.
func BuildToolDescriptions(toolPrompts []string) string {
	if len(toolPrompts) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("# Tools\n\n")
	sb.WriteString("You have access to the following tools:\n\n")
	for _, prompt := range toolPrompts {
		sb.WriteString(prompt)
		sb.WriteString("\n\n")
	}
	return sb.String()
}
