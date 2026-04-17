# agents/tool — Built-in Tool Set

This directory ports the claude-code-main tool surface into Go. Tools are
provider-agnostic and plug into the `QueryEngine` via the `tool.Tool`
interface.

## Packages

| Package | Tool name | Purpose |
|---|---|---|
| `websearch` | `WebSearch` | Web search (DuckDuckGo fallback + Brave when API key set). |
| `webfetch` | `WebFetch` | HTTP GET + HTML→Markdown + optional SideQuerier summarisation. |
| `fileio` | `Read`, `Edit`, `Write` | Filesystem access with ReadFileState fresh-read gate. |
| `notebook` | `NotebookEdit` | Jupyter `.ipynb` cell replace/insert (no deletion). |
| `glob` | `Glob` | `**`-aware glob, newest-first sort, 1000-match cap. |
| `grep` | `Grep` | `ripgrep` (when available) or Go regex fallback. |
| `bash` | `Bash` | Unix shell. Deny list + read-only allowlist, pluggable extensions. |
| `powershell` | `PowerShell` (aliased to `Bash` on Windows) | Windows shell, same security model as `bash`. |
| `shell` | — | Shared cross-platform executor (timeout, truncation, tree-kill). |
| `todo` | `TodoWrite` | Session-scoped in-memory todo list with validation. |
| `askuser` | `AskUserQuestion` | Emits `EventAskUser`, collects the user's choice via `ToolUseContext.AskUser`. |
| `planmode` | `ExitPlanMode` | Flips `ToolUseContext.PlanMode` and emits `EventPlanModeChange`. |
| `task` | `TaskStart`/`TaskStatus`/`TaskList`/`TaskKill` | Background-job manager. |
| `agenttool` | `Task` | Dispatches a subagent via `ToolUseContext.SpawnSubAgent`. |
| `builtin` | — | Aggregator. `Tools()` returns the legacy 2-tool set; `AllTools()` / `RegisterAll()` return the full ported set. |

## Platform routing

`builtin.AllTools` picks the shell tool at registration time:

- **Unix / macOS** → `bash.New(...)` (tool name `"Bash"`).
- **Windows** → `powershell.New(powershell.WithAlias("Bash"), ...)`
  (tool name `"Bash"`, backed by `powershell.exe -NoProfile -NonInteractive`).

Set `builtin.Options{DisableBashAlias: true}` on Windows to keep the native
`"PowerShell"` name instead.

## Extension points on `ToolUseContext`

The ported tools read/mutate the following fields (added in this port):

- `AskUser`   — handler for `AskUserQuestion`.
- `EmitEvent` — optional sink for ancillary `StreamEvent`s.
- `PlanMode`  — flipped by `ExitPlanMode`; hosts initialise to `"plan"` or `"normal"`.
- `TodoStore` — `*todo.Store` the engine installs once per session.
- `TaskManager` — `*task.Manager` backing the `Task*` tools.
- `SpawnSubAgent` — engine-provided callback used by the `Task` (subagent) tool.
- `ReadFileState` — existing cache; `fileio.Read` populates it and `fileio.Edit` / `fileio.Write` (overwrite) require a fresh entry.

## Registering the full set

```go
reg := tool.NewRegistry()
builtin.RegisterAll(reg, builtin.Options{
    WorkspaceRoots: []string{cwd},
    BashOptions: []bash.Option{
        bash.WithAllowlist("make"),
    },
})
```

Wire up per-session resources on each `ToolUseContext`:

```go
tc := &agents.ToolUseContext{
    Ctx:           ctx,
    ReadFileState: agents.NewFileStateCache(),
    TodoStore:     todo.NewStore(),
    TaskManager:   task.NewManager(),
    AskUser:       myAskUser,
    EmitEvent:     engine.EmitEvent,
    SpawnSubAgent: engine.SpawnSubAgent,
}
```

## Security model (shell tools)

Both `bash` and `powershell` apply a three-tier classification:

1. **Deny** — hardcoded regex patterns (e.g. `rm -rf /`, `curl|sh`, `mkfs`,
   `Remove-Item -Recurse -Force C:\`, `IEX (New-Object Net.WebClient)`).
   `CheckPermissions` returns `PermissionDeny` with a reason.
2. **Read-only allowlist** — command roots (`ls`, `cat`, `grep`, `git status`,
   `Get-ChildItem`, `Select-String`, …) that all pipeline segments must match.
   Returns `PermissionAllow`.
3. **Everything else** — `PermissionAsk`. Hosts supply a `CanUseToolFn` to
   surface a user prompt; when none is configured, the engine treats this as
   allowed (matching existing WebFetch behaviour).

Use `bash.WithAllowlist(...)` / `bash.WithDenylistReason(...)` (or the
PowerShell equivalents) to extend either list.

## Tests

Each package ships a unit test suite (`*_test.go`). End-to-end plumbing tests
live in `agents/tools_integration_ported_test.go`; they wire the full
`AllTools` set through the same `ExecuteTools` bridge the engine uses at
runtime, without an LLM.
