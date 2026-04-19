# backend/agents Real-LLM Tools Audit

Deep audit of the 20+ built-in tools registered by `tool/builtin.AllTools()` against the upstream `opensource/claude-code-main` reference, plus a real-LLM end-to-end test matrix (`INTEGRATION=1`).

- **Reference baseline**: `opensource/claude-code-main/src/tools.ts:193-251` (`getAllBaseTools()`).
- **Go port entry point**: `backend/agents/tool/builtin/builtin.go:77-112` (`AllTools`).
- **Static plumbing test**: `backend/agents/tools_integration_ported_test.go` (no LLM).
- **Real-LLM test file**: `backend/agents/tools_real_llm_test.go` (this audit).

## 1. Tool alignment matrix

Rows marked "default set" = unconditional in claude-code `getAllBaseTools()` (no `USER_TYPE === 'ant'`, no `feature()` flag). TodoV2 quad is `isTodoV2Enabled()`-gated upstream; Go port enables it unconditionally in `AllTools`.

| # | claude-code-main tool (name) | Go port location | Go tool name | Status |
|---|---|---|---|---|
| 1 | `AgentTool` (Task) | `tool/agenttool` | `Task` | ported |
| 2 | `TaskOutputTool` | `tool/bg.NewOutputTool` | `TaskOutput` | ported |
| 3 | `BashTool` / `PowerShellTool` | `tool/bash` + `tool/powershell` (Windows aliased as `Bash`) | `Bash` | ported |
| 4 | `GlobTool` | `tool/glob` | `Glob` | ported |
| 5 | `GrepTool` | `tool/grep` | `Grep` | ported |
| 6 | `ExitPlanModeV2Tool` | `tool/planmode.New` | `ExitPlanMode` | ported |
| 7 | `FileReadTool` | `tool/fileio.NewReadTool` | `Read` | ported |
| 8 | `FileEditTool` | `tool/fileio.NewEditTool` | `Edit` | ported |
| 9 | `FileWriteTool` | `tool/fileio.NewWriteTool` | `Write` | ported |
| 10 | `NotebookEditTool` | `tool/notebook.New` | `NotebookEdit` | ported |
| 11 | `WebFetchTool` | `tool/webfetch.New` | `WebFetch` | ported |
| 12 | `TodoWriteTool` | `tool/todo.New` | `TodoWrite` | ported |
| 13 | `WebSearchTool` | `tool/websearch.New` | `WebSearch` | ported |
| 14 | `TaskStopTool` | `tool/bg.NewStopTool` | `TaskStop` | ported |
| 15 | `AskUserQuestionTool` | `tool/askuser.New` | `AskUserQuestion` | ported |
| 16 | **`SkillTool`** | — | — | **missing** (`prompt/session_guidance.go` references `SkillToolName` constant only) |
| 17 | `EnterPlanModeTool` | `tool/planmode.NewEnter` | `EnterPlanMode` | ported |
| 18 | `BriefTool` (name `SendUserMessage`) | `tool/brief.New` | `SendUserMessage` | ported |
| 19 | **`SendMessageTool`** (unconditional via `getSendMessageTool()` lazy require) | — | — | **missing** (distinct from BriefTool; coordinator message delivery) |
| 20 | `ListMcpResourcesTool` | `tool/mcp.NewListTool` | `ListMcpResourcesTool` | ported |
| 21 | `ReadMcpResourceTool` | `tool/mcp.NewReadTool` | `ReadMcpResourceTool` | ported |
| 22 | `TaskCreateTool` (TodoV2) | `tool/taskgraph.NewCreateTool` | `TaskCreate` | ported |
| 23 | `TaskGetTool` (TodoV2) | `tool/taskgraph.NewGetTool` | `TaskGet` | ported |
| 24 | `TaskUpdateTool` (TodoV2) | `tool/taskgraph.NewUpdateTool` | `TaskUpdate` | ported |
| 25 | `TaskListTool` (TodoV2) | `tool/taskgraph.NewListTool` | `TaskList` | ported |

**Summary**: Go port registers **23 tools**. Relative to claude-code-main's default + TodoV2 set (25), **2 are missing**: `SkillTool`, `SendMessageTool`. See §3 for disposition.

## 2. Per-tool analysis

Each subsection captures: Go prompt summary vs TS upstream; schema differences; real-LLM testability grade; targeted prompt template; and live run results once tests are executed.

Grades: **E** = Easy (LLM self-triggers) · **G** = Guided (needs system prompt hint) · **S** = Stateful (needs pre-seeded context) · **H** = Hard (LLM rarely self-triggers).

### 2.1 `Read` (E)
- Go: `@d:\WALL-AI\uBuilding\backend\agents\tool\fileio\read.go` · TS: `opensource/claude-code-main/src/tools/FileReadTool/prompt.ts`.
- Prompt diff: Go adds `workspaceRoots` restriction (absolute paths must be under allowed roots); upstream has no such constraint but relies on permission context.
- Testability: **E** — simple "read this file and report X" prompt.

### 2.2 `Edit` (E)
- Schema: `file_path`, `old_string`, `new_string` — aligned with upstream.
- Pre-req: must `Read` first to populate `ReadFileState` (enforced by `fileio.NewEditTool`).
- Testability: **E** after Read.

### 2.3 `Write` (E)
- Aligned. Testability: **E**.

### 2.4 `Glob` (E)
- Aligned. Testability: **E**.

### 2.5 `Grep` (E)
- Aligned (supports `pattern`, `path`, `glob`). Testability: **E**.

### 2.6 `Bash` / `PowerShell` (E)
- Windows alias: `tool/builtin/builtin.go:124-132` wraps `PowerShell` with alias `Bash`.
- Prompt risk: LLM may issue POSIX syntax (`ls`, `cat`) on Windows. Prompt should steer to platform-neutral commands or be Windows-aware.
- Testability: **E** with echo-like commands.

### 2.7 `WebFetch` (E)
- Test requires `webfetch.WithAllowLoopback()` for httptest URLs.
- Already covered by `TestIntegration_RealLLM_WithTools_WebFetch` — migrated into new matrix.

### 2.8 `WebSearch` (E but env-dependent)
- Requires `AGENT_ENGINE_SEARCH_API_KEY` / `BRAVE_SEARCH_API_KEY`; falls back to DuckDuckGo scraping.
- Testability: **E** (may be flaky without API key; soft-skip).

### 2.9 `NotebookEdit` (E)
- Aligned. Testability: **E** with pre-seeded `.ipynb`.

### 2.10 `TodoWrite` (G)
- Requires `ToolUseContext.TodoStore = todo.NewStore()`.
- Testability: **G** — needs "track these sub-tasks" prompt.

### 2.11 `AskUserQuestion` (G)
- Requires `ToolUseContext.AskUser` callback.
- Prompt hint: "When the user's request is ambiguous, call AskUserQuestion instead of guessing."

### 2.12 `EnterPlanMode` (G)
- Refuses in sub-agent (`AgentID != ""`) and if already in plan mode.
- Initial `PlanMode = ModeNormal`; prompt should direct multi-step planning requests to `EnterPlanMode`.

### 2.13 `ExitPlanMode` (G)
- Initial `PlanMode = ModePlan`; prompt: "You are in plan mode; once the plan is ready call ExitPlanMode."

### 2.14 `Task` (AgentTool, G)
- Requires `ToolUseContext.SpawnSubAgent`; in tests we stub it to return a canned answer to keep LLM cost bounded.

### 2.15 `TaskCreate` / `TaskGet` / `TaskUpdate` / `TaskList` (G+S)
- Requires `ToolUseContext.TaskGraph = taskgraph.NewStore()`.
- Chain: `TaskCreate` → `TaskUpdate` (status) → `TaskGet` / `TaskList`.

### 2.16 `TaskOutput` (S)
- Requires pre-seeded background job via `bg.Manager.Start`; prompt includes the returned `bash_id`.

### 2.17 `TaskStop` (S × 2)
- Dual dispatch: (a) `bg.Manager` job id or (b) `taskgraph.Store` node id.

### 2.18 `ListMcpResourcesTool` / `ReadMcpResourceTool` (S)
- Requires `ToolUseContext.McpResources` implementing `agents.McpResourceRegistry`.

### 2.19 `SendUserMessage` (BriefTool, H)
- Upstream's primary output channel in chat/assistant mode; surfaced as `EventBrief` by the Go engine.
- Without system prompt guidance, LLMs emit plain text instead of calling the tool.

## 3. Gap disposition

- **`SkillTool`** — Upstream name is `'Skill'` (`opensource/claude-code-main/src/tools/SkillTool/constants.ts:1`). Go port does not register it. The references in `backend/agents/prompt/session_guidance.go:62-73` are **feature-gated** via `enabledTools[SkillToolName]`, so when the tool isn't registered the guidance section is omitted from the system prompt — no dangling prompt output at runtime. **Decision: declared out of scope** (the Skill/DiscoverSkills feature is the interactive-CLI skill expansion surface, not required for the engine's backend-agent use case). No action taken; guidance code is left in place for future forward-compatibility. If the feature is wanted later, port `tools/SkillTool/` end-to-end.
- **`SendMessageTool`** — Upstream lazy-required by `getSendMessageTool()` in `tools.ts`, primarily used by the coordinator/agent-swarm extension (`SEND_MESSAGE_TOOL_NAME`). Distinct from `BriefTool`/`SendUserMessage` (the user-facing channel already ported as `tool/brief`). **Decision: out of scope** until coordinator/agent-swarm features land in the Go port.
- **`builtin.AllTools` doc comment** (`backend/agents/tool/builtin/builtin.go:72-82`) — **done**. Doc comment now enumerates the full alphabetical list of 23 tools and explicitly calls out the two missing upstream tools.

## 4. Run guide

```powershell
# Static (no LLM) regression:
go test ./backend/agents -run Integration_Ported -v

# Plumbing-only WebFetch (no LLM):
go test ./backend/agents -run Integration_Tools_WebFetchPlumbing -v

# Real-LLM per-tool matrix (reads repo-root .env):
$env:INTEGRATION=1
go test ./backend/agents -run RealLLM_AllTools -timeout 20m -v

# Single tool only:
$env:INTEGRATION=1; $env:TOOLS_SUBSET="Read,Edit,Write"
go test ./backend/agents -run RealLLM_AllTools -v

# Smoke (all tools, one session):
$env:INTEGRATION=1
go test ./backend/agents -run RealLLM_AllTools_Smoke -timeout 20m -v
```

## 5. Live run results

Run on Windows (WSL-less), provider=`openai` (OpenAI-compatible), model=`MiniMax-M2.5`, baseURL=`https://ark.cn-beijing.volces.com/api/coding/v3`.

### 5.1 Per-tool matrix (`TestIntegration_RealLLM_AllTools`, 23 subtests)

| tool | tool called? | assertion | notes |
|---|---|---|---|
| `Read` | ✅ | final text contains `ZEPHYRFLAME` | one transient `wsarecv: connection forcibly closed` seen in a retry; passes on subsequent runs |
| `Edit` | ✅ | disk mutated `hello` → `howdy` | model auto-calls `Read` first when prompted to `Edit`; pipeline honours read-first guard |
| `Write` | ✅ | disk contains nonce | |
| `Glob` | ✅ | answer mentions `alpha.go` / `beta.go` | |
| `Grep` | ✅ | answer contains `Zephyr` | |
| `Bash` | ✅ | answer contains routed nonce | windows→powershell alias path exercised via `Write-Output` |
| `WebSearch` | ✅ | answer mentions `go.dev` / `golang.org` | uses DuckDuckGo fallback when no Brave key |
| `WebFetch` | ✅ | answer echoes `ZEPHYR-MARKER-2187` | requires `webfetch.WithAllowLoopback()` for httptest URL |
| `NotebookEdit` | ✅ | on-disk cell source now `2+2` | |
| `TodoWrite` | ✅ | `TodoStore` has 3 items | |
| `AskUserQuestion` | ✅ | `AskUser` callback fired with question text | sometimes called 3× in one turn |
| `EnterPlanMode` | ✅ | `PlanMode == "plan"` post-call | model also probes with `Glob` — expected |
| `ExitPlanMode` | ✅ | `PlanMode == "normal"` post-call | |
| `TaskCreate` | ✅ | `taskgraph.Store` has 1 node | |
| `TaskGet` | ✅ | answer contains `Port QueryEngine` | |
| `TaskUpdate` | ✅ | node status `completed` | |
| `TaskList` | ✅ | answer contains `done-1` | |
| `Task` (AgentTool) | ✅ | `SpawnSubAgent` stub invoked with prompt | stubbed to avoid recursive real-LLM cost |
| `TaskOutput` | ✅ | answer quotes `SEEDED-BG-OK` | pre-seeded bg job via `bg.Manager.Start` |
| `TaskStop` (bg) | ✅ | bg job transitioned off `running` | |
| `TaskStop` (graph) | ✅ | graph node moved off `in_progress` | |
| `ListMcpResourcesTool` | ✅ | answer contains mock resource names | mock registry returns 2 resources |
| `ReadMcpResourceTool` | ✅ | answer contains `SENTINEL-9931` | |
| `SendUserMessage` | ✅ | at least one `EventBrief` emitted | requires `"primary output channel"` hint in system prompt |

**All 23 registered tools verified end-to-end with a real LLM.**

### 5.2 Smoke (`TestIntegration_RealLLM_AllTools_Smoke`, single session)

One engine instance, 21 prompts in sequence. Result:

- **Coverage: 21 / 22 = 95%** (NotebookEdit and TaskStop explicitly excluded from the denominator; dedicated subtests cover them).
- `toolCallLog` (in order of invocation):
  `Read, Write, Edit, Read, Edit, Glob, Grep, Bash, WebFetch, WebSearch, TodoWrite, TaskCreate, TaskList, TaskGet, TaskUpdate, TaskOutput, EnterPlanMode, ExitPlanMode, EnterPlanMode, ExitPlanMode, ListMcpResourcesTool, ReadMcpResourceTool, AskUserQuestion, Task, SendUserMessage`.
- One expected runtime event: `Edit` on step 02 refused with _"refuse to edit: read … first"_; the model retried with `Read` + `Edit` and succeeded. That's the fileio read-first guard working correctly.
- Duration ≈ 4 minutes.

### 5.3 Known flakiness / operational notes

- Transient network resets (`wsarecv: An existing connection was forcibly closed by the remote host`) occasionally abort a single LLM call. Per-subtest soft-assert semantics + retry-by-rerun keep the matrix green.
- `WebSearch` without a Brave/Bing key relies on DuckDuckGo scraping and may return stale/empty results; the test asserts only on `go.dev` or `golang.org` appearing.
- `AgentTool` (`Task`) uses a `SpawnSubAgent` stub to keep real-LLM cost O(1) instead of O(subagent-depth). Real subagent cascades are exercised by engine-level unit tests, not here.
