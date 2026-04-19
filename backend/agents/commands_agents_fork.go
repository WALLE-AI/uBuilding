// Package agents — /agents and /fork slash commands.
//
// Task D19 · ports the CLI-facing slash-command surface from
// claude-code so hosts can list / inspect agent definitions and launch
// forked sub-agents directly from the REPL.
//
// Commands provided:
//
//   /agents               — list active agent definitions (table form)
//   /agents show <type>   — print the full definition for <type>
//   /fork <directive>     — launch a fork sub-agent with the given
//                           directive (only available when
//                           ForkSubagentEnabled() is true)
//
// Registration: host wires `RegisterAgentAndForkCommands(registry, provider)`
// at REPL bootstrap, where `provider` returns the current *AgentDefinitions
// snapshot (typically `engine.Agents()`).
package agents

import (
	"fmt"
	"sort"
	"strings"
)

// AgentCatalogProvider returns the currently active agent definitions.
// Callers typically close over their engine: `func() *AgentDefinitions {
// return engine.Agents() }`. Returning nil is safe — commands render an
// empty list.
type AgentCatalogProvider func() *AgentDefinitions

// RegisterAgentAndForkCommands installs the `/agents` and `/fork`
// commands into registry. `provider` is required; nil provider causes a
// panic so misconfiguration surfaces at startup (not at command dispatch).
func RegisterAgentAndForkCommands(registry *CommandRegistry, provider AgentCatalogProvider) {
	if registry == nil {
		return
	}
	if provider == nil {
		panic("RegisterAgentAndForkCommands: provider is required")
	}
	registry.Register(buildAgentsCommand(provider))
	registry.Register(buildForkCommand())
}

// ---------------------------------------------------------------------------
// /agents
// ---------------------------------------------------------------------------

func buildAgentsCommand(provider AgentCatalogProvider) Command {
	return Command{
		Name:        "agents",
		Description: "List or inspect available agent definitions",
		Type:        CommandTypeLocal,
		Call: func(args string, _ CommandContext) (*LocalCommandResult, error) {
			defs := provider()
			sub, rest := splitSubcommand(args)
			switch sub {
			case "", "list":
				return &LocalCommandResult{Type: "text", Value: renderAgentList(defs)}, nil
			case "show":
				if strings.TrimSpace(rest) == "" {
					return &LocalCommandResult{Type: "text", Value: "Usage: /agents show <type>"}, nil
				}
				return renderAgentShow(defs, rest), nil
			}
			return &LocalCommandResult{
				Type:  "text",
				Value: fmt.Sprintf("Unknown /agents subcommand %q. Use 'list' or 'show <type>'.", sub),
			}, nil
		},
	}
}

// splitSubcommand peels the first whitespace-delimited token off args
// and returns (token, remainder). Leading whitespace in remainder is
// trimmed.
func splitSubcommand(args string) (string, string) {
	trimmed := strings.TrimSpace(args)
	if trimmed == "" {
		return "", ""
	}
	idx := strings.IndexAny(trimmed, " \t")
	if idx == -1 {
		return trimmed, ""
	}
	return trimmed[:idx], strings.TrimSpace(trimmed[idx+1:])
}

// renderAgentList formats every active agent as a three-column table.
// The table uses simple spaces (no ANSI) so it renders in any terminal.
func renderAgentList(defs *AgentDefinitions) string {
	if defs == nil || len(defs.ActiveAgents) == 0 {
		return "No active agents."
	}
	rows := make([][]string, 0, len(defs.ActiveAgents))
	for _, d := range defs.ActiveAgents {
		if d == nil {
			continue
		}
		rows = append(rows, []string{
			d.AgentType,
			string(d.Source),
			truncate(d.WhenToUse, 60),
		})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i][0] < rows[j][0] })

	// Column widths.
	wType, wSrc := len("Type"), len("Source")
	for _, r := range rows {
		if len(r[0]) > wType {
			wType = len(r[0])
		}
		if len(r[1]) > wSrc {
			wSrc = len(r[1])
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%-*s  %-*s  %s\n", wType, "Type", wSrc, "Source", "When to use")
	fmt.Fprintf(&b, "%s  %s  %s\n",
		strings.Repeat("-", wType),
		strings.Repeat("-", wSrc),
		strings.Repeat("-", 11))
	for _, r := range rows {
		fmt.Fprintf(&b, "%-*s  %-*s  %s\n", wType, r[0], wSrc, r[1], r[2])
	}
	return strings.TrimRight(b.String(), "\n")
}

// renderAgentShow prints the full details of a single agent.
func renderAgentShow(defs *AgentDefinitions, agentType string) *LocalCommandResult {
	if defs == nil {
		return &LocalCommandResult{Type: "text", Value: fmt.Sprintf("Agent %q not found.", agentType)}
	}
	name := strings.TrimSpace(agentType)
	def := defs.FindActive(name)
	if def == nil {
		return &LocalCommandResult{Type: "text", Value: fmt.Sprintf("Agent %q not found.", name)}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Agent: %s\n", def.AgentType)
	fmt.Fprintf(&b, "Source: %s\n", def.Source)
	if def.WhenToUse != "" {
		fmt.Fprintf(&b, "When to use:\n  %s\n", indent(def.WhenToUse, "  "))
	}
	if len(def.Tools) > 0 {
		fmt.Fprintf(&b, "Tools: %s\n", strings.Join(def.Tools, ", "))
	}
	if def.Model != "" {
		fmt.Fprintf(&b, "Model: %s\n", def.Model)
	}
	if def.Memory != "" && def.Memory != AgentMemoryScopeNone {
		fmt.Fprintf(&b, "Memory scope: %s\n", def.Memory)
	}
	if len(def.Skills) > 0 {
		fmt.Fprintf(&b, "Skills: %s\n", strings.Join(def.Skills, ", "))
	}
	if def.Background {
		b.WriteString("Background: yes\n")
	}
	if def.Isolation != "" {
		fmt.Fprintf(&b, "Isolation: %s\n", def.Isolation)
	}
	return &LocalCommandResult{Type: "text", Value: strings.TrimRight(b.String(), "\n")}
}

// ---------------------------------------------------------------------------
// /fork
// ---------------------------------------------------------------------------

func buildForkCommand() Command {
	return Command{
		Name:        "fork",
		Description: "Launch a forked sub-agent with the given directive",
		Type:        CommandTypePrompt,
		IsEnabled:   ForkSubagentEnabled,
		IsHidden:    !ForkSubagentEnabled(),
		GetPrompt: func(args string, _ CommandContext) ([]ContentBlock, error) {
			directive := strings.TrimSpace(args)
			if directive == "" {
				return []ContentBlock{{
					Type: ContentBlockText,
					Text: "Usage: /fork <directive>\n\nExamples:\n  /fork audit the feature/x branch for test gaps",
				}}, nil
			}
			if !ForkSubagentEnabled() {
				return []ContentBlock{{
					Type: ContentBlockText,
					Text: "/fork is disabled. Set UBUILDING_FORK_SUBAGENT=1 (and ensure you are not in coordinator/non-interactive mode).",
				}}, nil
			}
			// Prompt-type command: return the directive as the expanded
			// user turn. The host's AgentTool will route this into a
			// fork spawn via D04 (subagent_type=""+ForkAgentType).
			return []ContentBlock{{
				Type: ContentBlockText,
				Text: "[FORK] " + directive,
			}}, nil
		},
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func truncate(s string, max int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= max {
		return s
	}
	return s[:max-1] + "…"
}

// indent applies prefix to every line of s except the first.
func indent(s, prefix string) string {
	lines := strings.Split(s, "\n")
	for i := 1; i < len(lines); i++ {
		lines[i] = prefix + lines[i]
	}
	return strings.Join(lines, "\n")
}
