# QueryEngine Core Engine — Source Code Analysis & Migration Plan

## 1. TypeScript Architecture Overview

### 1.1 Core Files Map

| TS File | Lines | Go Target | Status |
|---------|-------|-----------|--------|
| `QueryEngine.ts` | 1296 | `engine.go` (718L) | ✅ Complete |
| `query.ts` | 1730 | `queryloop.go` (1003L) | ✅ Complete |
| `query/deps.ts` | 41 | `deps.go` (283L) | ✅ Complete |
| `query/stopHooks.ts` | 474 | `stop_hooks.go` (587L) | ✅ Complete |
| `query/tokenBudget.ts` | 94 | `types.go` (BudgetTracker) | ✅ Complete |
| `constants/prompts.ts` | 915 | `prompt/prompts.go` (516L) | ✅ Complete |
| `constants/systemPromptSections.ts` | 69 | `prompt/sections.go` | ✅ Complete |
| `utils/queryContext.ts` | 180 | `prompt/query_context.go` | ✅ Complete |
| `utils/api.ts` (toolToAPISchema) | 719 | `tool/api_schema.go` | ✅ Complete |
| `services/tools/toolOrchestration.ts` | 189 | `tool/orchestration.go` (319L) | ✅ Complete |
| `services/tools/StreamingToolExecutor.ts` | 531 | `tool/streaming_executor.go` | ✅ Complete |
| `services/tools/toolExecution.ts` | — | `tool/tool.go` (178L) | ✅ Complete |
| `services/tools/toolHooks.ts` | — | `tool/hooks.go` | ✅ Complete |
| `services/compact/*` (11 files) | — | `compact/` (9 files) | ✅ Complete |

### 1.2 QueryEngine.ts Flow (TS L1-1296)

```
submitMessage(prompt)
  │
  ├─ 1. Build user message, append to history
  ├─ 2. Emit system_init event
  ├─ 3. Build system prompt (fetchSystemPromptParts + effective)
  │     ├─ getSystemPrompt() → static + dynamic sections
  │     ├─ getUserContext() → user context map
  │     └─ getSystemContext() → system context map
  ├─ 4. Build ToolUseContext
  ├─ 4b. Pre-query hooks (memory prefetch, skill discovery)
  ├─ 4c. processUserInput (local commands, attachments)
  ├─ 5. Build QueryParams
  ├─ 6. Run queryLoop → collect events on internal channel
  │     ├─ Usage accumulation (L789-816)
  │     ├─ Message sync (assistant/user/system/attachment)
  │     ├─ Max budget USD check (L972-1002)
  │     └─ Forward events to caller
  ├─ 7. Extract result text from last assistant message
  ├─ 8. Determine result subtype (success/error)
  └─ 9. Emit result + done events
```

### 1.3 queryLoop State Machine (TS L1-1730)

```
queryLoop(state) — infinite loop
  │
  ├─ Phase 1:  getMessagesAfterCompactBoundary
  ├─ Phase 2:  Snip compaction (HISTORY_SNIP gate)
  ├─ Phase 2b: Tool result budget enforcement
  ├─ Phase 3:  Microcompact (fold repeated reads)
  ├─ Phase 4:  Context collapse (incremental)
  ├─ Phase 5:  Build full system prompt (+ systemContext)
  ├─ Phase 6:  Autocompact (LLM-powered summarization)
  ├─ Phase 7:  Token limit check / blocking preempt
  ├─ Phase 7b: Start memory prefetch (async)
  ├─ Phase 8:  Prepare API call (tool defs, model selection)
  ├─ Phase 9:  Call Model with streaming + fallback retry
  │     ├─ FallbackTriggeredError → switch model, retry
  │     ├─ ImageSizeError → terminal
  │     ├─ Withhold prompt-too-long, media-size, max-output-tokens
  │     └─ Stream events (text_delta, thinking_delta, assistant, tool_use)
  ├─ Phase 10: Post-streaming abort check
  ├─ Phase 11: Handle no-tool-use (end_turn)
  │     ├─ 11a: Prompt-too-long recovery chain
  │     │     ├─ Context collapse drain
  │     │     └─ Reactive compact
  │     ├─ 11b: Max output tokens recovery (escalate → nudge → exhaust)
  │     ├─ 11c: Skip stop hooks on API error
  │     ├─ 11d: Run stop hooks
  │     └─ 11e: Token budget continuation
  ├─ Phase 12: Execute tools
  │     ├─ 12a: RunTools (concurrent/serial orchestration)
  │     ├─ 12b: Post-tool abort check
  │     └─ 12c: Attachment pipeline + memory consume
  ├─ Phase 13: MaxTurns check
  ├─ Phase 14: Refresh tools between turns
  └─ Phase 15: Prepare next iteration state
```

### 1.4 Prompt System Architecture (TS constants/prompts.ts)

```
getSystemPrompt(tools, model, dirs, mcpClients)
  │
  ├─ Static sections (cacheable, before DYNAMIC_BOUNDARY):
  │     ├─ getSimpleIntroSection (identity + security)
  │     ├─ getSimpleSystemSection (capabilities)
  │     ├─ getSimpleDoingTasksSection (coding instructions)
  │     ├─ getActionsSection (actions guidance)
  │     ├─ getUsingYourToolsSection (tool usage)
  │     ├─ getSimpleToneAndStyleSection (tone)
  │     └─ getOutputEfficiencySection (output efficiency)
  │
  ├─ SYSTEM_PROMPT_DYNAMIC_BOUNDARY (cache scope marker)
  │
  └─ Dynamic sections (registry-managed):
        ├─ session_guidance (session-specific)
        ├─ memory (CLAUDE.md)
        ├─ env_info_simple (environment)
        ├─ language (language)
        ├─ output_style (output style)
        ├─ mcp_instructions (MCP server instructions)
        ├─ scratchpad (scratchpad dir)
        ├─ frc (function result clearing)
        ├─ summarize_tool_results
        ├─ token_budget (budget instructions)
        └─ brief (brief mode)
```

### 1.5 Stop Hooks Architecture (TS query/stopHooks.ts)

```
handleStopHooks(state, stopReason, toolCtx)
  │
  ├─ Guard: recursive stop hook execution
  ├─ Shell hooks (user-defined .claude/hooks/)
  ├─ TeammateIdle hooks (multi-agent)
  ├─ TaskCompleted hooks (multi-agent)
  ├─ Background tasks (async, non-blocking):
  │     ├─ Prompt suggestion
  │     ├─ Memory extraction
  │     └─ Auto-dream
  └─ Merge results → blocking errors, prevent continuation
```

### 1.6 Tool Execution Pipeline (TS services/tools/)

```
runTools / StreamingToolExecutor
  │
  ├─ partitionToolCalls → [concurrent-safe batch, serial batch, ...]
  ├─ For each batch:
  │     ├─ Concurrent: runToolsConcurrently (max 10 parallel)
  │     │     └─ Each: runToolUse → canUseTool check → tool.call()
  │     └─ Serial: runToolsSerially
  │           └─ Each: runToolUse → canUseTool check → tool.call()
  └─ Yield MessageUpdate (message + newContext)
```

---

## 2. Go Implementation Status — Module-by-Module

### 2.1 Core Engine Layer (`engine.go` — 718 lines) ✅

| Feature | TS Reference | Go Status |
|---------|-------------|-----------|
| `QueryEngine` struct | `QueryEngine` class | ✅ |
| `NewQueryEngine` + options | constructor | ✅ |
| `SubmitMessage` → channel | `submitMessage` → AsyncGenerator | ✅ |
| `runQuery` lifecycle | `submitMessage` body | ✅ |
| `buildSystemPrompt` (legacy + full) | prompt assembly | ✅ |
| `processUserInput` (commands, attachments) | L540-580 | ✅ |
| Usage accumulation | L789-816 | ✅ |
| Message sync | L829-935 | ✅ |
| Max budget USD check | L972-1002 | ✅ |
| Result extraction + subtype | L1058-1155 | ✅ |
| `emitResult` | L1156-1200 | ✅ |
| `pruneMessagesBeforeLastBoundary` | L926-933 | ✅ |
| `isResultSuccessful` | helper | ✅ |
| Transcript recording | session persistence | ✅ |
| Permission denial tracking | L1030-1050 | ✅ |

### 2.2 Query Loop (`queryloop.go` — 1003 lines) ✅

| Phase | Feature | Go Status |
|-------|---------|-----------|
| 1 | `getMessagesAfterCompactBoundary` | ✅ |
| 2 | Snip compaction (HISTORY_SNIP) | ✅ |
| 2b | Tool result budget | ✅ |
| 3 | Microcompact | ✅ |
| 4 | Context collapse | ✅ |
| 5 | Full system prompt build | ✅ |
| 6 | Autocompact | ✅ |
| 7 | Token limit / blocking preempt | ✅ |
| 7b | Memory prefetch start | ✅ |
| 8 | Prepare API call | ✅ |
| 9 | Call model + streaming + fallback | ✅ |
| 10 | Post-streaming abort | ✅ |
| 11a | Prompt-too-long recovery chain | ✅ |
| 11b | Max output tokens escalation + recovery | ✅ |
| 11c | Skip hooks on API error | ✅ |
| 11d | Stop hooks execution | ✅ |
| 11e | Token budget continuation | ✅ |
| 12 | Tool execution | ✅ |
| 12b | Post-tool abort | ✅ |
| 12c | Attachment pipeline + memory consume | ✅ |
| 13 | MaxTurns check | ✅ |
| 14 | Refresh tools | ✅ |
| 15 | Next iteration state | ✅ |

### 2.3 Dependencies (`deps.go` — 283 lines) ✅

| Dep | Go Interface Method | Status |
|-----|-------------------|--------|
| `callModel` | `CallModel` | ✅ |
| `microcompact` | `Microcompact` | ✅ |
| `autocompact` | `Autocompact` | ✅ |
| `uuid` | `UUID` | ✅ |
| `snipCompact` | `SnipCompact` | ✅ |
| `contextCollapse` | `ContextCollapse` | ✅ |
| `contextCollapseDrain` | `ContextCollapseDrain` | ✅ |
| `reactiveCompact` | `ReactiveCompact` | ✅ |
| `executeTools` | `ExecuteTools` | ✅ |
| `buildToolDefinitions` | `BuildToolDefinitions` | ✅ |
| `applyToolResultBudget` | `ApplyToolResultBudget` | ✅ |
| `getAttachmentMessages` | `GetAttachmentMessages` | ✅ |
| `startMemoryPrefetch` | `StartMemoryPrefetch` | ✅ |

### 2.4 Stop Hooks (`stop_hooks.go` — 587 lines) ✅

| Hook | Go Type | Status |
|------|---------|--------|
| Registry + HandleStopHooks | `StopHookRegistry` | ✅ |
| MaxTurns | `MaxTurnsHook` | ✅ |
| BudgetExhausted | `BudgetExhaustedHook` | ✅ |
| ApiErrorSkip | `ApiErrorSkipHook` | ✅ |
| TeammateIdle | `TeammateIdleHook` | ✅ |
| TaskCompleted | `TaskCompletedHook` | ✅ |
| CompactWarning | `CompactWarningHook` | ✅ |
| ShellHooks | `ShellHookStopHook` | ✅ |
| BackgroundTasks | `BackgroundTaskStopHook` | ✅ |

### 2.5 Prompt System (`prompt/` — 9 files) ✅

| File | TS Equivalent | Status |
|------|--------------|--------|
| `prompts.go` (516L) | `constants/prompts.ts` | ✅ |
| `sections.go` | `systemPromptSections.ts` | ✅ |
| `context.go` | `context.ts` | ✅ |
| `query_context.go` | `utils/queryContext.ts` | ✅ |
| `effective.go` | effective prompt priority | ✅ |
| `env_info.go` | `computeSimpleEnvInfo` | ✅ |
| `session_guidance.go` | session guidance section | ✅ |
| `api_context.go` | API context helpers | ✅ |
| `system.go` (238L) | `BuildFullSystemPrompt` | ✅ |

### 2.6 Tool Pipeline (`tool/` — 10 files) ✅

| File | TS Equivalent | Status |
|------|--------------|--------|
| `tool.go` (178L) | `Tool.ts` interface | ✅ |
| `registry.go` | `tools.ts` + tool pool | ✅ |
| `orchestration.go` (319L) | `toolOrchestration.ts` | ✅ |
| `streaming_executor.go` | `StreamingToolExecutor.ts` | ✅ |
| `hooks.go` | `toolHooks.ts` | ✅ |
| `api_schema.go` | `utils/api.ts` toolToAPISchema | ✅ |

### 2.6.1 Built-in Tool Set (`tool/<pkg>/` — 15 tools) ✅

Ports the claude-code-main user-facing tool surface. Aggregated by
`builtin.AllTools` / `builtin.RegisterAll`. See `agents/tool/README.md`.

| Package | Tool(s) | TS source | Status |
|---------|---------|-----------|--------|
| `websearch` | `WebSearch` | `tools/WebSearchTool` | ✅ (pre-existing) |
| `webfetch` | `WebFetch` | `tools/WebFetchTool` | ✅ (pre-existing) |
| `shell` | — (executor) | shared bash runner | ✅ |
| `bash` | `Bash` | `tools/BashTool` | ✅ (unix; deny+allow lists) |
| `powershell` | `PowerShell` (aliased to `Bash` on Windows) | `tools/BashTool` on Windows path | ✅ |
| `fileio` | `Read`, `Edit`, `Write` | `tools/FileReadTool`, `FileEditTool`, `FileWriteTool` | ✅ (ReadFileState gate) |
| `notebook` | `NotebookEdit` | `tools/NotebookEditTool` | ✅ (replace+insert; no delete) |
| `glob` | `Glob` | `tools/GlobTool` | ✅ (native Go) |
| `grep` | `Grep` | `tools/GrepTool` | ✅ (rg primary + Go fallback via pluggable `Locator`) |
| `todo` | `TodoWrite` | `tools/TodoWriteTool` | ✅ (session `Store`) |
| `askuser` | `AskUserQuestion` | `tools/AskUserQuestionTool` | ✅ (emits `EventAskUser`) |
| `planmode` | `ExitPlanMode` | `tools/ExitPlanModeTool` | ✅ (emits `EventPlanModeChange`) |
| `task` | `TaskStart`, `TaskStatus`, `TaskList`, `TaskKill` | background-job tools | ✅ (`Manager`-backed) |
| `agenttool` | `Task` (subagent) | `tools/TaskTool` | ✅ (via `ToolUseContext.SpawnSubAgent`) |
| `builtin` | — (aggregator) | tool registry wiring | ✅ |

**Related `ToolUseContext` extensions** (see `tool_use_context.go`):
`AskUser`, `EmitEvent`, `PlanMode`, `TodoStore`, `TaskManager`,
`SpawnSubAgent`. New `StreamEvent` types: `EventAskUser`, `EventPlanModeChange`.

**Integration coverage**: `agents/tools_integration_ported_test.go` exercises
the full `AllTools` set through the same `ExecuteTools` bridge the engine
uses at runtime (no LLM required).

### 2.7 Compact Pipeline (`compact/` — 9 files) ✅

| File | TS Equivalent | Status |
|------|--------------|--------|
| `auto.go` | `autoCompact.ts` | ✅ |
| `micro.go` | `microCompact.ts` | ✅ |
| `snip.go` | snip compaction | ✅ |
| `reactive.go` | `reactiveCompact` | ✅ |
| `context_collapse.go` | `contextCollapse/index.ts` | ✅ |
| `grouping.go` | `grouping.ts` | ✅ |
| `cleanup.go` | `postCompactCleanup.ts` | ✅ |
| `prompt.go` | `prompt.ts` (compact prompt) | ✅ |
| `tool_result_budget.go` | `toolResultStorage.ts` | ✅ |

### 2.8 Other Modules ✅

| File | TS Equivalent | Status |
|------|--------------|--------|
| `config.go` (74L) | `query/config.ts` | ✅ |
| `commands.go` | command registry | ✅ |
| `attachments.go` | `utils/attachments.ts` | ✅ |
| `shell_hooks.go` | `utils/hooks.ts` | ✅ |
| `background_tasks.go` | background task manager | ✅ |
| `memory_extraction.go` | memory extraction | ✅ |
| `prompt_suggestion.go` | prompt suggestion | ✅ |
| `provider/` (5 files) | `services/api/` | ✅ |
| `permission/` (2 files) | permission system | ✅ |

---

## 3. Gap Analysis — Remaining Differences

### 3.1 HIGH Priority (Functional Gaps)

| # | Gap | TS Location | Go Location | Status |
|---|-----|-------------|-------------|--------|
| H1 | **Prompt text completeness** — all 18 sections ported | `constants/prompts.ts:1-800` | `prompt/prompts.go` | ✅ Done (A1–A8, 20 section tests) |
| H2 | **computeSimpleEnvInfo full parity** | `prompts.ts:651-744` | `prompt/env_info.go` | ✅ Done (B1–B5, line-order + Windows path + cutoff tests) |
| H3 | **MCP instructions section** | `prompts.ts:579-604` | `prompt/prompts.go` | ✅ Done |
| H4 | **Proactive/autonomous mode prompt** | `prompts.ts:860-914` | `prompt/prompts.go:587+` | ✅ Done |
| H5 | **Scratchpad instructions** | `prompts.ts:797-819` | `prompt/prompts.go:346` | ✅ Done |
| H6 | **Function Result Clearing section** | `prompts.ts:821-839` | `prompt/prompts.go:369` | ✅ Done |
| H7 | **Token budget prompt section** | `prompts.ts:538-551` | `prompt/prompts.go:380` | ✅ Done |
| H8 | **Numeric length anchors** (ant-only) | `prompts.ts:529-537` | `prompt/prompts.go:385` | ✅ Done |

### 3.2 MEDIUM Priority (Behavioral Refinements)

| # | Gap | TS Location | Go Location | Status |
|---|-----|-------------|-------------|--------|
| M1 | **Section cache (memoization)** — `SectionCache` + `TestSectionCache_*` | `systemPromptSections.ts:43-58` | `prompt/cache.go`, `prompt/sections.go` | ✅ Done |
| M2 | **DANGEROUS_uncachedSystemPromptSection** — cache-break semantics | `systemPromptSections.ts:32-38` | `prompt/sections.go` | ✅ Done |
| M3 | **enhanceSystemPromptWithEnvDetails** — subagent prompt enhancement | `prompts.ts:760-791` | `prompt/env_info.go:188`; `BuildSubagentSystemPrompt` helper added (C3) | ✅ Done |
| M4 | **Knowledge cutoff per model** | `prompts.ts:713-744` | `prompt/env_info.go:166` + all-families test | ✅ Done |
| M5 | **Brief mode section** — Kairos brief mode | `prompts.ts:843-858` | `prompt/prompts.go:393` + guard-matrix test | ✅ Done |
| M6 | **Output style section** | `prompts.ts:151-158` | `prompt/prompts.go:304` | ✅ Done |
| M7 | **OnCompactBoundary hook** — clearSystemPromptSections parity | `query.ts` compact sites | `EngineConfig.OnCompactBoundary` → `QueryParams` → `queryloop.go` (D1/D2) | ✅ Done |
| M8 | **Provider streaming error branches** — fallback / image / prompt-too-long / media-size / max-output-tokens | `services/api/claude.ts` + `errors.ts` | `provider/` + `queryloop.go` withhold branches | ✅ Done |
| M9 | **Provider prompt cache-break** — `SystemPromptBlocks` + `cache_control` | `promptCacheBreakDetection.ts` | `prompt/cache.go` + `provider/anthropic.go` | ✅ Done |
| M10 | **Compact auxiliary** — `apiMicrocompact` / `timeBasedMCConfig` / `sessionMemoryCompact` | `services/compact/` | `compact/api_micro.go`, `compact/time_based.go`, `compact/session_memory.go` (F2–F5) | ✅ Done |

### 3.3 LOW Priority (Polish / Edge Cases)

| # | Gap | Status |
|---|-----|--------|
| L1 | `windowsPathToPosixPath` in shell info | ✅ Done (`prompt/env_info.go:283`) |
| L2 | `getMarketingNameForModel` | ✅ Done (`prompt/env_info.go:237`) |
| L3 | Undercover mode suppression | ✅ Done (`IsUndercover` flag in `EnvInfoConfig`) |
| L4 | `SYSTEM_PROMPT_DYNAMIC_BOUNDARY` cache scope gating | ✅ Done (`UseGlobalCache` flag + `SplitSystemPromptBlocks` in `prompt/cache.go`) |
| L5 | `prefetchAllMcpResources` in API call setup | 🟡 TODO (E4): commented in `queryloop.go` Phase 8; no-op until MCP resource tooling is added |

---

## 4. Detailed Execution Plan

### Phase A: Prompt Text Completeness Audit (H1)

**Goal**: Line-by-line verification that every prompt section in `prompts.ts:1-915` has a Go equivalent.

**Tasks**:
1. Diff `GetSimpleIntroSection` vs TS `getSimpleIntroSection` — verify text match
2. Diff `GetSimpleSystemSection` vs TS `getSimpleSystemSection`
3. Diff `GetSimpleDoingTasksSection` vs TS `getSimpleDoingTasksSection`
4. Diff `GetActionsSection` vs TS `getActionsSection`
5. Diff `GetUsingYourToolsSection` vs TS `getUsingYourToolsSection`
6. Diff `GetSimpleToneAndStyleSection` vs TS `getSimpleToneAndStyleSection`
7. Diff `GetOutputEfficiencySection` vs TS `getOutputEfficiencySection`
8. Diff `GetSessionGuidanceSection` vs TS `getSessionSpecificGuidanceSection`

**Estimated effort**: 2-3 hours
**Files**: `prompt/prompts.go`, `prompt/session_guidance.go`

### Phase B: Missing Dynamic Prompt Sections (H2-H7)

**Goal**: Port the remaining dynamic prompt sections from `prompts.ts:491-555`.

**Tasks**:
1. **H2**: Update `prompt/env_info.go` — add full `computeSimpleEnvInfo` parity:
   - Model family IDs in env output
   - Knowledge cutoff per model (`getKnowledgeCutoff`)
   - Shell info with Windows handling (`getShellInfoLine`)
   - Worktree detection
   - Claude Code availability line
   - Fast mode description line

2. **H3**: Add `GetMcpInstructionsSection` to `prompt/prompts.go`:
   - Accept MCP client configs
   - Filter connected clients with instructions
   - Format instruction blocks

3. **H4**: Add `GetProactiveSection` to `prompt/prompts.go`:
   - Autonomous work instructions (50 lines)
   - Sleep tool usage, pacing, first wake-up
   - Terminal focus handling
   - Brief mode integration

4. **H5**: Add `GetScratchpadInstructions` to `prompt/prompts.go`:
   - Session-specific scratchpad directory
   - Usage instructions

5. **H6**: Add `GetFunctionResultClearingSection` to `prompt/prompts.go`:
   - Config-driven (model support, enabled, keepRecent)

6. **H7**: Add `GetTokenBudgetSection` to `prompt/prompts.go`:
   - Static text about budget continuation

**Estimated effort**: 4-6 hours
**New lines**: ~300
**Files**: `prompt/prompts.go`, `prompt/env_info.go`

### Phase C: Section Assembly Parity (M1-M2)

**Goal**: Ensure `GetSystemPrompt` assembles sections in the same order as TS.

**Tasks**:
1. Verify `GetSystemPrompt` in `prompt/prompts.go` returns sections in this order:
   - Static: intro, system, doing_tasks, actions, using_tools, tone, output_efficiency
   - DYNAMIC_BOUNDARY
   - Dynamic: session_guidance, memory, env_info, language, output_style, mcp_instructions, scratchpad, frc, summarize_tool_results, token_budget, brief
2. Verify `DANGEROUS_uncachedSystemPromptSection` semantics in `prompt/sections.go`
3. Add cache invalidation on `/clear` and `/compact`

**Estimated effort**: 2-3 hours
**Files**: `prompt/prompts.go`, `prompt/sections.go`, `prompt/cache.go`

### Phase D: Subagent Prompt Enhancement (M3)

**Goal**: Port `enhanceSystemPromptWithEnvDetails` for subagent prompt construction.

**Tasks**:
1. Add `EnhanceSystemPromptWithEnvDetails` to `prompt/prompts.go`:
   - Accept existing system prompt parts
   - Append notes, skill discovery guidance, env info
2. Wire into agent tool execution path

**Estimated effort**: 1-2 hours
**New lines**: ~50
**Files**: `prompt/prompts.go`

### Phase E: Integration Verification

**Goal**: Validate end-to-end prompt and query loop behavior.

**Tasks**:
1. Add prompt text comparison tests (Go output vs expected TS output)
2. Add query loop integration test with mock deps
3. Verify all 15 phases execute correctly
4. Verify all 12+ exit paths work
5. Run existing 40+ tests to confirm no regressions

**Estimated effort**: 3-4 hours
**Files**: `prompt/prompt_test.go`, `queryloop_test.go`, `engine_test.go`

---

## 5. Summary

### Current State
The Go implementation is **substantially complete** — all major structural components are ported:
- **61 Go files** across `agents/`, `agents/prompt/`, `agents/tool/`, `agents/compact/`, `agents/provider/`, `agents/permission/`
- **40+ tests** passing
- All 15 query loop phases implemented
- Full stop hooks system with 8 hook types
- Complete tool orchestration with concurrent/serial execution
- Complete compaction pipeline (snip, micro, auto, reactive, context collapse)
- Full prompt system with section caching and priority selection

### Remaining Work
The primary gaps are in **prompt text completeness** (Phases A-D above):
- 6 dynamic prompt sections not yet ported (H3-H7, M5)
- `computeSimpleEnvInfo` needs full parity (H2)
- Subagent prompt enhancement (M3)
- Prompt text audit for exact fidelity (H1)

**Total estimated remaining effort**: 12-18 hours
**Total estimated new/modified lines**: ~500-700
