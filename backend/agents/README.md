# agents — QueryEngine Core Engine (Go)

Go implementation of the QueryEngine core engine, ported from [claude-code-main](../../opensource/claude-code-main/) TypeScript codebase.

## Architecture

```
User Input → QueryEngine.SubmitMessage()
               │
               ├── Build system prompt (6-layer builder)
               ├── Build ToolUseContext
               └── QueryLoop (for {} state machine):
                     ├── Context compression pipeline
                     │   microcompact → autocompact
                     ├── Token limit check (85% warn / 95% block)
                     ├── CallModel → Provider.CallModel() → <-chan StreamEvent
                     ├── Tool execution (orchestration with concurrency control)
                     └── Continue/Stop decision
                         ├── tool_use present → continue
                         ├── max_tokens → recovery (up to 3x)
                         ├── prompt_too_long → reactive compact
                         ├── budget exhausted → stop
                         └── end_turn → completed
```

## Package Structure

| Package | Description |
|---------|-------------|
| `agents` | Core engine: `QueryEngine`, `QueryLoop`, types, deps, config, budget, hooks |
| `agents/provider` | LLM adapters: Anthropic (official SDK), OpenAI-compatible (Ollama/vLLM/GPT) |
| `agents/tool` | Tool system: `Tool` interface, `BuildTool` factory, `Registry`, `AssembleToolPool`, `FilterByDenyRules`, `Orchestrator`, `StreamingToolExecutor` |
| `agents/tool/builtin` | Built-in tool aggregator. `Register(registry)` = legacy 2-tool set (`WebSearch`, `WebFetch`); `RegisterAll(registry, opts)` = full ported set (adds `Read`/`Edit`/`Write`/`NotebookEdit`/`Glob`/`Grep`/`Bash`-or-`PowerShell`/`TodoWrite`/`AskUserQuestion`/`ExitPlanMode`/`TaskStart`/`TaskStatus`/`TaskList`/`TaskKill`/`Task`-subagent). See `tool/README.md`. |
| `agents/tool/webfetch`, `websearch` | Network tools: SSRF-guarded fetch, Brave/DuckDuckGo search. |
| `agents/tool/fileio`, `notebook`, `glob`, `grep` | Filesystem tools. FileIO enforces an absolute-path + ReadFileState fresh-read gate; Grep prefers ripgrep with a Go regex fallback. |
| `agents/tool/bash`, `powershell`, `shell` | Shell tools + shared cross-platform executor (timeout, truncation, tree-kill). Security: hardcoded deny patterns + read-only allowlist; anything else falls to Ask. |
| `agents/tool/todo`, `askuser`, `planmode` | Session-state tools. `ToolUseContext` gains `TodoStore`, `AskUser`, `PlanMode`, `EmitEvent`; new `StreamEvent`s: `EventAskUser`, `EventPlanModeChange`. |
| `agents/tool/task`, `agenttool` | Background-job manager + subagent dispatch (`ToolUseContext.SpawnSubAgent`). |
| `agents/compact` | Context compression: `MicroCompactor` (local), `AutoCompactor` (LLM-powered) |
| `agents/permission` | Permission chain: deny → allow → ask with glob pattern matching |
| `agents/prompt` | System prompt builder (6-layer) + message normalization + thinking rules |
| `agents/state` | Thread-safe `Store` with `sync.RWMutex` + callback notification |
| `agents/util` | Helpers: UUID, context management, message builders |

## Quick Start

```go
package main

import (
    "context"
    "fmt"

    "github.com/wall-ai/ubuilding/backend/agents"
    "github.com/wall-ai/ubuilding/backend/agents/provider"
)

func main() {
    // Create provider
    p := provider.NewAnthropicProvider(provider.AnthropicConfig{
        APIKey: "sk-ant-...",
    })

    // Create deps
    deps := &agents.ProductionDeps{
        CallModelFn: p.CallModel,
        UUIDFn:      func() string { return "..." },
    }

    // Create engine
    engine := agents.NewQueryEngine(agents.EngineConfig{
        UserSpecifiedModel: "claude-sonnet-4-20250514",
        MaxTurns:           20,
    }, deps)

    // Submit message and consume streaming events
    ctx := context.Background()
    for event := range engine.SubmitMessage(ctx, "Hello!") {
        switch event.Type {
        case agents.EventTextDelta:
            fmt.Print(event.Text)
        case agents.EventToolUse:
            fmt.Printf("\n[Tool: %s]\n", event.Text)
        case agents.EventDone:
            fmt.Println("\n--- Done ---")
        }
    }
}
```

## Key Design Decisions

| TypeScript Pattern | Go Equivalent |
|---|---|
| `AsyncGenerator<StreamEvent>` | `<-chan StreamEvent` + goroutine |
| `AbortController` | `context.Context` + `CancelFunc` |
| `while(true) { state = ...; continue }` | `for { select {} }` state machine |
| `yield*` delegate generator | goroutine writes to same channel |
| `Promise.all` concurrent tools | `errgroup.Group` |
| `EventEmitter` state | `sync.RWMutex` + callback listeners |
| Zod schema validation | struct tags + interface |
| `QueryDeps` dependency injection | Go interface + constructor injection |

## Testing

```bash
go test ./agents/... -v
```

## TypeScript Source Mapping

| Go File | TypeScript Source | Lines |
|---------|-----------------|-------|
| `engine.go` | `QueryEngine.ts` | 1296 |
| `queryloop.go` | `query.ts` | 1730 |
| `config.go` | `query/config.ts` | 47 |
| `deps.go` | `query/deps.ts` | 41 |
| `token_budget.go` | `query/tokenBudget.ts` | 94 |
| `stop_hooks.go` | `query/stopHooks.ts` | 474 |
| `tool/tool.go` | `Tool.ts` | 793 |
| `tool/orchestration.go` | `services/tools/toolOrchestration.ts` | 189 |
| `tool/streaming_executor.go` | `services/tools/StreamingToolExecutor.ts` | 531 |
| `provider/anthropic.go` | `services/api/claude.ts` | ~1000 |
