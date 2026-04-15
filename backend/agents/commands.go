package agents

import (
	"context"
	"strings"
)

// ---------------------------------------------------------------------------
// Local Command Framework
// Maps to TypeScript commands.ts / types/command.ts
// ---------------------------------------------------------------------------

// CommandType distinguishes local commands from prompt-expanding commands.
type CommandType string

const (
	// CommandTypeLocal commands are handled entirely within the REPL.
	// They do not invoke the model. Maps to TS 'local'.
	CommandTypeLocal CommandType = "local"

	// CommandTypePrompt commands expand into content sent to the model.
	// Maps to TS 'prompt'.
	CommandTypePrompt CommandType = "prompt"
)

// LocalCommandResult is the outcome of executing a local command.
type LocalCommandResult struct {
	// Type is "text", "compact", or "skip".
	Type string

	// Value is the text output for "text" type results.
	Value string

	// CompactResult is set when Type is "compact".
	CompactResult *AutocompactResult

	// Messages are additional messages to inject into the conversation.
	Messages []Message
}

// CommandContext provides context available to command implementations.
type CommandContext struct {
	Ctx      context.Context
	Messages []Message
	ToolCtx  *ToolUseContext

	// SetMessages replaces the message array (used by commands like /clear).
	SetMessages func(fn func([]Message) []Message)
}

// Command defines a slash command that can be invoked by the user.
type Command struct {
	// Name is the command name without the leading slash (e.g. "clear", "compact").
	Name string

	// Aliases are alternative names for the command.
	Aliases []string

	// Description is a short user-facing description.
	Description string

	// Type classifies the command behavior.
	Type CommandType

	// IsEnabled returns whether the command is currently available.
	// Nil means always enabled.
	IsEnabled func() bool

	// IsHidden hides the command from help/typeahead when true.
	IsHidden bool

	// Call executes a local command. Only used when Type == CommandTypeLocal.
	Call func(args string, ctx CommandContext) (*LocalCommandResult, error)

	// GetPrompt expands a prompt command. Only used when Type == CommandTypePrompt.
	// Returns content blocks to inject as the user message.
	GetPrompt func(args string, ctx CommandContext) ([]ContentBlock, error)

	// ProgressMessage is shown while a prompt command is executing.
	ProgressMessage string

	// AllowedTools restricts which tools can be used during prompt execution.
	AllowedTools []string

	// Model overrides the model for this command's execution.
	Model string
}

// ---------------------------------------------------------------------------
// Command Registry
// ---------------------------------------------------------------------------

// CommandRegistry holds registered commands with lookup by name/alias.
type CommandRegistry struct {
	commands []Command
	byName   map[string]*Command
}

// NewCommandRegistry creates a new empty command registry.
func NewCommandRegistry() *CommandRegistry {
	return &CommandRegistry{
		byName: make(map[string]*Command),
	}
}

// Register adds a command to the registry.
func (r *CommandRegistry) Register(cmd Command) {
	r.commands = append(r.commands, cmd)
	c := &r.commands[len(r.commands)-1]
	r.byName[cmd.Name] = c
	for _, alias := range cmd.Aliases {
		r.byName[alias] = c
	}
}

// Find looks up a command by name or alias.
func (r *CommandRegistry) Find(name string) *Command {
	return r.byName[name]
}

// GetAll returns all registered commands.
func (r *CommandRegistry) GetAll() []Command {
	return r.commands
}

// GetEnabled returns commands that are currently enabled.
func (r *CommandRegistry) GetEnabled() []Command {
	var result []Command
	for _, cmd := range r.commands {
		if cmd.IsEnabled != nil && !cmd.IsEnabled() {
			continue
		}
		result = append(result, cmd)
	}
	return result
}

// GetVisible returns commands that are enabled and not hidden.
func (r *CommandRegistry) GetVisible() []Command {
	var result []Command
	for _, cmd := range r.commands {
		if cmd.IsEnabled != nil && !cmd.IsEnabled() {
			continue
		}
		if cmd.IsHidden {
			continue
		}
		result = append(result, cmd)
	}
	return result
}

// ---------------------------------------------------------------------------
// Command parsing
// ---------------------------------------------------------------------------

// ParsedCommand holds the result of parsing a user input as a slash command.
type ParsedCommand struct {
	// Name is the command name (without leading slash).
	Name string

	// Args is the remainder of the input after the command name.
	Args string

	// Command is the matched Command, or nil if not found.
	Command *Command
}

// ParseSlashCommand checks if input starts with "/" and parses the command.
// Returns nil if the input is not a slash command.
func ParseSlashCommand(input string, registry *CommandRegistry) *ParsedCommand {
	trimmed := strings.TrimSpace(input)
	if !strings.HasPrefix(trimmed, "/") {
		return nil
	}

	// Strip leading slash
	rest := trimmed[1:]
	if rest == "" {
		return nil
	}

	// Split into command name and args
	var name, args string
	if idx := strings.IndexByte(rest, ' '); idx >= 0 {
		name = rest[:idx]
		args = strings.TrimSpace(rest[idx+1:])
	} else {
		name = rest
	}

	cmd := registry.Find(name)
	return &ParsedCommand{
		Name:    name,
		Args:    args,
		Command: cmd,
	}
}

// ---------------------------------------------------------------------------
// Built-in commands
// ---------------------------------------------------------------------------

// RegisterBuiltinCommands adds the core commands to the registry.
// Maps to the built-in commands in TS commands.ts (/clear, /compact, /help, /cost, /exit).
func RegisterBuiltinCommands(registry *CommandRegistry) {
	registry.Register(Command{
		Name:        "clear",
		Description: "Clear conversation history",
		Type:        CommandTypeLocal,
		Call: func(args string, ctx CommandContext) (*LocalCommandResult, error) {
			if ctx.SetMessages != nil {
				ctx.SetMessages(func(_ []Message) []Message {
					return nil
				})
			}
			return &LocalCommandResult{Type: "text", Value: "Conversation cleared."}, nil
		},
	})

	registry.Register(Command{
		Name:        "compact",
		Aliases:     []string{"c"},
		Description: "Compact conversation to save context",
		Type:        CommandTypeLocal,
		Call: func(args string, ctx CommandContext) (*LocalCommandResult, error) {
			// Compact is delegated to the autocompact dep; the actual call
			// happens in processUserInput which has access to deps.
			return &LocalCommandResult{Type: "compact"}, nil
		},
	})

	registry.Register(Command{
		Name:        "help",
		Aliases:     []string{"?"},
		Description: "Show available commands",
		Type:        CommandTypeLocal,
		Call: func(args string, ctx CommandContext) (*LocalCommandResult, error) {
			// Build help text from visible commands
			var sb strings.Builder
			sb.WriteString("Available commands:\n")
			// The actual command list is injected by the caller
			return &LocalCommandResult{Type: "text", Value: sb.String()}, nil
		},
	})

	registry.Register(Command{
		Name:        "exit",
		Aliases:     []string{"quit", "q"},
		Description: "Exit the session",
		Type:        CommandTypeLocal,
		Call: func(args string, ctx CommandContext) (*LocalCommandResult, error) {
			return &LocalCommandResult{Type: "skip"}, nil
		},
	})
}
