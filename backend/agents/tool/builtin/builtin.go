// Package builtin exposes the default built-in tool set (WebSearch, WebFetch)
// and a convenience Register helper. It lives in a separate package to avoid
// an import cycle between `tool` and its sub-packages.
package builtin

import (
	"runtime"

	"github.com/wall-ai/ubuilding/backend/agents/tool"
	"github.com/wall-ai/ubuilding/backend/agents/tool/agenttool"
	"github.com/wall-ai/ubuilding/backend/agents/tool/askuser"
	"github.com/wall-ai/ubuilding/backend/agents/tool/bash"
	"github.com/wall-ai/ubuilding/backend/agents/tool/bg"
	"github.com/wall-ai/ubuilding/backend/agents/tool/brief"
	"github.com/wall-ai/ubuilding/backend/agents/tool/browser"
	"github.com/wall-ai/ubuilding/backend/agents/tool/fileio"
	"github.com/wall-ai/ubuilding/backend/agents/tool/glob"
	"github.com/wall-ai/ubuilding/backend/agents/tool/grep"
	"github.com/wall-ai/ubuilding/backend/agents/tool/mcp"
	"github.com/wall-ai/ubuilding/backend/agents/tool/notebook"
	"github.com/wall-ai/ubuilding/backend/agents/tool/planmode"
	"github.com/wall-ai/ubuilding/backend/agents/tool/powershell"
	"github.com/wall-ai/ubuilding/backend/agents/tool/taskgraph"
	"github.com/wall-ai/ubuilding/backend/agents/tool/todo"
	"github.com/wall-ai/ubuilding/backend/agents/tool/webfetch"
	"github.com/wall-ai/ubuilding/backend/agents/tool/websearch"
)

// Options tunes the set of tools returned by Tools()/Register().
type Options struct {
	// WebSearchAPIKey overrides the search API key; empty falls back to env
	// (AGENT_ENGINE_SEARCH_API_KEY or BRAVE_SEARCH_API_KEY).
	WebSearchAPIKey string
	// WebSearchBaseURL overrides the DuckDuckGo fallback endpoint.
	WebSearchBaseURL string
	// WebFetchOptions are passed through to webfetch.New.
	WebFetchOptions []webfetch.Option
	// WorkspaceRoots restricts file-oriented tools (Read/Edit/Write/NotebookEdit)
	// to paths inside one of the listed absolute roots. Empty = no restriction.
	WorkspaceRoots []string
	// BashOptions customises the Bash (unix) tool.
	BashOptions []bash.Option
	// PowerShellOptions customises the PowerShell (windows) tool.
	PowerShellOptions []powershell.Option
	// AgentToolOptions customises the AgentTool / Task subagent.
	AgentToolOptions []agenttool.Option
	// DisableBashAlias, when true, skips aliasing the Windows PowerShell tool
	// as "Bash"; the platform-specific name ("PowerShell") is advertised
	// instead.
	DisableBashAlias bool
}

// Tools returns the default built-in tool set.
func Tools(opts ...Options) tool.Tools {
	var o Options
	if len(opts) > 0 {
		o = opts[0]
	}
	return tool.Tools{
		websearch.New(o.WebSearchAPIKey, o.WebSearchBaseURL),
		webfetch.New(o.WebFetchOptions...),
	}
}

// Register installs every built-in tool into r, tagged with WithBuiltin so
// AssembleToolPool places them in the cache-friendly prefix of the final pool.
func Register(r *tool.Registry, opts ...Options) {
	for _, t := range Tools(opts...) {
		r.Register(t, tool.WithBuiltin())
	}
}

// AllTools returns the complete ported tool set, mirroring claude-code-main's
// getAllBaseTools() (modulo feature-gated extras). Alphabetical primary names:
//
//	AskUserQuestion, Bash (or PowerShell on Windows), Edit, EnterPlanMode,
//	ExitPlanMode, Glob, Grep, ListMcpResourcesTool, NotebookEdit, Read,
//	ReadMcpResourceTool, SendUserMessage, Task (AgentTool subagent),
//	TaskCreate, TaskGet, TaskList, TaskOutput, TaskStop, TaskUpdate,
//	TodoWrite, WebFetch, WebSearch, Write.
//
// Upstream tools not yet ported: SkillTool (default set), SendMessageTool
// (coordinator/agent-swarm extension). See backend/agents/REAL_LLM_TOOLS_AUDIT.md.
func AllTools(opts ...Options) tool.Tools {
	var o Options
	if len(opts) > 0 {
		o = opts[0]
	}
	ts := tool.Tools{
		websearch.New(o.WebSearchAPIKey, o.WebSearchBaseURL),
		webfetch.New(o.WebFetchOptions...),
		fileio.NewReadTool(o.WorkspaceRoots...),
		fileio.NewEditTool(o.WorkspaceRoots...),
		fileio.NewWriteTool(o.WorkspaceRoots...),
		notebook.New(o.WorkspaceRoots...),
		glob.New(),
		grep.New(),
		todo.New(),
		askuser.New(),
		planmode.New(),
		planmode.NewEnter(),
		// BriefTool = SendUserMessage; host wires EventBrief to its UI.
		brief.New(),
		// MCP resource tools (nil-safe — they error cleanly without a registry).
		mcp.NewListTool(),
		mcp.NewReadTool(),
		// Background-shell tools.
		bg.NewOutputTool(),
		bg.NewStopTool(),
		// TodoV2 task-graph CRUD (TaskStop is the shared bg.StopTool above).
		taskgraph.NewCreateTool(),
		taskgraph.NewGetTool(),
		taskgraph.NewUpdateTool(),
		taskgraph.NewListTool(),
		agenttool.New(o.AgentToolOptions...),
		// Browser automation tool (go-rod based).
		browser.New(),
	}
	ts = append(ts, shellTool(o))
	return ts
}

// RegisterAll installs the complete ported tool set into r.
func RegisterAll(r *tool.Registry, opts ...Options) {
	for _, t := range AllTools(opts...) {
		r.Register(t, tool.WithBuiltin())
	}
}

// shellTool returns the platform-appropriate shell tool. On Windows the
// PowerShell tool is aliased to "Bash" by default so model prompts stay
// platform-agnostic; set Options.DisableBashAlias=true to keep "PowerShell".
func shellTool(o Options) tool.Tool {
	if runtime.GOOS == "windows" {
		psOpts := o.PowerShellOptions
		if !o.DisableBashAlias {
			psOpts = append([]powershell.Option{powershell.WithAlias("Bash")}, psOpts...)
		}
		return powershell.New(psOpts...)
	}
	return bash.New(o.BashOptions...)
}
