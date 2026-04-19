# Sub-agent alignment · implementation progress

Tracks progress on `subagent-alignment-tasks-full-684fac.md`. Short-form
checklist only — detailed designs live in the plan file.

## Wave 1 · P0 (60h / ~9d)

### Phase A · MVP runtime (complete)
- [x] A01 AgentDefinition data model — `agent_definition.go`
- [x] A02 SystemPromptCtx + enhance placeholder — `agent_definition.go`
- [x] A03 Built-in agents (general-purpose, Explore, Plan) — `agent_builtin.go`
- [x] A04 Markdown frontmatter loader — `agent_loader.go`
- [x] A05 agents.json loader — `agent_loader.go`
- [x] A06 Resolve priorities (built-in < user < project < policy) — `agent_loader.go`
- [x] A07 Engine loads agents (EngineConfig.Agents/AgentsLoader) — `types.go`/`subagent.go`
- [x] A08 SpawnSubAgent core — `subagent.go`
- [x] A09 Bind SpawnSubAgent in defaultToolUseContext — `engine.go`
- [x] A10 Task prompt renders agent catalog — `tool/agenttool/agenttool.go`
- [x] A11 AllowedAgentTypes pulled from ToolUseContext — `tool/agenttool/agenttool.go`
- [x] A12 Recursion depth guard (ErrSubagentDepthExceeded) — `subagent.go`
- [x] A13 End-to-end tests — `subagent_e2e_test.go`
- [x] A14 Full AgentDefinition fields (isolation/memory/background/effort/...)
- [x] A15 Task tool discriminated Output + model/run_in_background/... schema
- [x] A16 Tool name constants (4 sets + one-shot) — `agent_tool_constants.go`
- [x] A17 Model resolution chain (GetAgentModel + inherit + alias match)
- [x] A18 ToolUseContext extended (ToolUseID, RenderedSystemPrompt, ContentReplacementState, …)
- [x] A20 Recordable message filter inside drainSubagentStream

### Phase B (Wave 1 P0 subset · complete)
- [x] B01 `resolveAgentTools` — `agents/tool/resolve.go`
- [x] B02 `createSubagentContext` — `agents/subagent_context.go`
- [x] B03 SpawnSubAgent uses B01/B02 via EngineConfig.ResolveSubagentTools hook
- [x] B04 PermissionMode override chain — `permission/checker.go` + `ToolUseOptions.AgentPermissionMode`
- [x] B06 `EnhanceSystemPromptWithEnvDetails` full body (cwd/platform/shell/model/tools/guidelines)
- [x] B08 `filterToolsForAgent` full (4 sets + plan/ExitPlanMode + teammate carve-out)
- [x] B09 Permission rule value parser (`Bash(git *)`, `Agent(name)`) — `permission/rule_parser.go`
- [x] B10 Permission mode five-state + legacy aliases + NormalizeMode

### Phase E (Wave 1 P0 subset · complete)
- [x] E01 CacheSafeParams global slot (Save/Get)
- [x] E02 `runForkedAgent` generic fork API (ForkRunner hook for Phase D)

## Wave 2 · P1 (complete)

### Phase A · polish
- [x] A19 `OmitClaudeMd` override — `types.go` + `subagent.go`
      (child engine skips CLAUDE.md / git-status fragments for Explore/Plan)

### Phase B · tool pool + permissions (complete)
- [x] B05 Session-rule 3-tier allow (user-scope rules enforced)
      → `permission/rule_scope.go`, `permission/checker.go`
- [x] B11 `filterDeniedAgents` (Agent(name) rules applied to catalog)
      → `tool/agenttool/agent_denylist.go`
- [x] B12 Session rules registered per tier (`ScopeSession*` enums)
- [x] B13 `assembleToolPool` host-shim documented; B01/B02 remain authoritative
- [x] B07 Phase B e2e — `subagent_phaseB_test.go`

### Phase C · memory / hooks / MCP / skills (complete)
- [x] C01+C02+C14 Agent MCP · `agent_mcp.go`
      * `InitializeAgentMcpServers`, `HasRequiredMcpServers`,
        `FilterAgentsByMcpRequirements`, shared-cache memoisation (C14)
      * `ResetAgentMCPCache()` exported for integration tests
- [x] C03+C04+C05+C10 Hooks · `agent_hooks.go`
      * `RegisterFrontmatterHooks` (scoped) / `ClearSessionHooks`
      * Stop→SubagentStop rewrite (C10)
      * `ExecuteSubagentStartHooks` + `AttachSubagentStartContext`
      * Duplicate-safe scope removal (`dropHooksByCount`)
- [x] C06 Skills preload · `EngineConfig.ResolveAgentSkill` + subagent wiring
- [x] C07+C08+C12 Memory · `agent_memory.go`
      * `AgentMemoryScope{User,Project,Local}` + sanitised dirs
      * `BuildMemoryPrompt` injected by SpawnSubAgent when Memory ≠ none
      * `IsAgentMemoryPath` helper (for file-tool permission carve-outs)
- [x] C09 Phase C e2e — `subagent_phaseC_test.go`

### Phase D · fork / sidechain / async (complete)
- [x] D01 `ForkSubagentEnabled()` env gate
- [x] D02 `BuildForkedMessages` + `ForkBoilerplateTag` / `ForkDirectivePrefix`
- [x] D03 `IsInForkChild` recursion guard + AgentTool rejects re-entry
- [x] D04 AgentTool routes empty `subagent_type` to fork agent when enabled
- [x] D05 `DefaultForkRunner` + `EnableEngineDrivenForkRunner`
- [x] D11 `ForkPrefixFingerprint` determinism check
- [x] D06 Sidechain transcript (`.claude/subagents/<id>.jsonl`)
      * `RecordSidechainTranscript`, `GetAgentTranscript`
- [x] D07 `AgentMetadata` read/write (`<id>.meta.json`)
- [x] D08 `ResumeAgent` reattaches filtered transcript + prompt
- [x] D13 Filters · `FilterUnresolvedToolUses`,
      `FilterOrphanedThinkingOnlyMessages`,
      `FilterWhitespaceOnlyAssistantMessages`,
      `FilterResumedTranscript`
- [x] D14 `ReconstructForSubagentResume` drops stale replacement records
- [x] D09 `SpawnAsyncSubAgent` → `AsyncAgentHandle` + goroutine runner
- [x] D16 `LocalAgentTaskManager` lifecycle + Kill
- [x] D17 `<task-notification>` XML via `EncodeTaskNotification`
- [x] D12 Phase D e2e — `subagent_phaseD_test.go`

### Phase E · fork utilities (complete)
- [x] E03 `PrepareForkedCommandContext` (slash-command / skill fork prep)
- [x] E04 `ApplyOverridesToContext` — full override surface helper

## Wave 3 · P2 (complete · 21 / 21)

### Extra Wave 2 polish (handoff + analytics + skills)
- [x] C11 · Skills plugin prefix + cleanup — `skills_resolve.go`
      * `SkillRegistry` with exact/qualified/suffix resolution
      * `SkillInvocationLog` + `ClearInvokedSkillsForAgent`
      * `MakeSkillResolver(reg, log)` matches `EngineConfig.ResolveAgentSkill`
      * Wired into `SpawnSubAgent` via `EngineConfig.SkillInvocationLog` defer.
- [x] E05 · queryTracking + fork analytics — `fork_analytics.go`
      * `ForkAnalyticsEvent` + `RegisterForkAnalytics`
      * Auto-emit from `DefaultForkRunner` (captures ChainID/Depth/Usage/CacheHitRate)
- [x] E06 · classifyHandoffIfNeeded interface — `agent_handoff.go`
      * `HandoffClassifier` + `RegisterHandoffClassifier`
      * `HandoffDecision{pass, warning, security_block}` enum
      * Default pass-through; swap-in hook for a real classifier later.

### C13 · Memory snapshot state machine · `agent_memory_snapshot.go`
- [x] `SnapshotState{none, initialize, prompt-update}` enum
- [x] `CheckAgentMemorySnapshot(agentType, scope, cfg, snapshot) → SnapshotDecision`
      with hash fields for drift analysis
- [x] `InitializeFromSnapshot` / `ReplaceFromSnapshot` / `MarkSnapshotSynced`
- [x] `.snapshot-synced.json` sidecar (atomic tmp+rename)
- [x] 8 tests covering initialize / drift / missing-record / replace / validation / whitespace-resilient hash

### D19 · /agents + /fork slash commands · `commands_agents_fork.go`
- [x] `RegisterAgentAndForkCommands(registry, provider)`
- [x] `/agents` (list) · three-column table sorted alphabetically
- [x] `/agents show <type>` · full definition dump (source / tools / memory / skills / isolation / background)
- [x] `/fork <directive>` · `CommandTypePrompt`, auto-hidden when fork disabled,
      routes into the existing AgentTool fork path (D04)
- [x] 11 tests covering list / show / missing / disabled / enabled paths

### D18 · Fork-based summarizer · `compact/fork_summarizer.go`
- [x] `NewForkCompactCallModel(opts)` produces an `AutoCompactor.CallModel`
      that drives `agents.RunForkedAgent` under the hood
- [x] `EnableForkSummarizerForCompactor(ac, opts)` auto-wraps an existing
      CallModel as the fallback (transparent upgrade path)
- [x] Bridges fork `FinalText` into a single-event assistant stream so
      AutoCompactor.callForSummary consumes the same shape as before
- [x] Fallback activates when no fork runner registered OR no CacheSafeParams
- [x] 7 tests covering happy / fallback / unavailable / empty-msgs / runner-error / wrap / nil-safe

### D15 · Worktree module · `agents/worktree/`
- [x] `CreateAgentWorktree(repoRoot, slug)` → `<repoRoot>/.worktrees/<slug>-<ns>`
      on a fresh branch `ubuilding/<slug>-<ns>`
- [x] `HasWorktreeChanges(path)` via `git status --porcelain`
- [x] `RemoveAgentWorktree(path)` via `git worktree remove --force`
- [x] `RunWithCwdOverride(ctx, cwd, fn)` · package-mutex serialised cwd swap
- [x] `sanitizeSlug` · Windows-safe `[A-Za-z0-9_]`, collapsed dashes
- [x] `Available()` / `ErrGitUnavailable` for graceful degradation
- [x] 10 tests, including a real-git integration pass (t.Skip when git absent)

### Coordinator mode runtime · `coordinator.go`
- [x] `CoordinatorConfig` + `DefaultCoordinatorSystemPrompt`
- [x] `BuildCoordinatorEngineConfig(base, cfg)` — clones an `EngineConfig`,
      enables `IsCoordinatorMode`, installs the coordinator system prompt,
      filters `Tools` down to `CoordinatorModeAllowedTools` (override via
      `cfg.AllowedTools`).
- [x] `RouteTaskToAsync(engine, mgr, params, forceAsync)` — coordinator
      mode (or `forceAsync=true`) spawns via `SpawnAsyncSubAgent`; sync
      fallback still registers + completes the task in the manager so
      observers receive one uniform notification stream.
- [x] `NotifyCoordinator(note)` — wraps a `TaskNotification` in a
      user-role `Message` (`Subtype="task_notification"`, UUID
      `task-<id>`) containing the XML blob.
- [x] `CoordinatorInbox` / `NewCoordinatorInbox(mgr, buf, onOverflow)` —
      buffered channel + `TaskObserver` fed by `LocalAgentTaskManager`.
      `cleanup()` unsubscribes and closes the channel exactly once; a
      full buffer triggers the optional overflow callback.
- [x] E2E: `coordinator_e2e_test.go · TestCoordinator_E2E`

### Observer / UI wiring · `localagent_task.go`
- [x] `TaskObserver` type + `AddObserver(fn) func()` fan-out
- [x] Legacy `OnNotification` single-subscriber preserved (called first)
- [x] `ObserverCount()` for probes / tests
- [x] Snapshot observers under RLock so a slow observer can't block
      `AddObserver`/`unsubscribe` calls from other goroutines.

### Engine introspection
- [x] `(*QueryEngine).Config() EngineConfig` — read-only accessor for
      host code that needs to inspect mode flags / filtered tool pool.

## Notes / decisions

- Go tempdir redirected to `D:\go-tmp` (C: drive was 0 GB free). Tests must
  run with `TMP/TEMP/GOTMPDIR/GOCACHE` pointed at D: until C: is cleared.
- Package `agents` now exposes:
  - `AgentDefinition{...}`, `AgentDefinitions`, `AgentDef` (legacy)
  - `DefaultBuiltInAgents()`, `LoadAgentFromMarkdown`, `LoadAgentsFromJSON`,
    `ResolveActiveAgents(LoaderConfig{...})`
  - `(*QueryEngine).SpawnSubAgent(ctx, SubAgentParams) (string, error)`
  - `(*QueryEngine).Agents() *AgentDefinitions`
  - `WithSubagentDepth(ctx, depth)` for external depth seeding
  - `GetAgentModel`, `GetDefaultSubagentModel`, `GetQuerySourceForAgent`
  - `EnhanceSystemPromptWithEnvDetails` (full body)
  - `CreateSubagentContext(parent, SubagentContextOverrides{...})`
  - `CacheSafeParams`, `SaveCacheSafeParams`, `GetLastCacheSafeParams`
  - `RunForkedAgent`, `RegisterForkRunner`, `HasForkRunner`
  - `ExtractForkFinalText` (last-assistant text helper)
- Package `agents/tool` now exposes:
  - `FilterToolsForAgent(tools, FilterToolsForAgentOpts{...})`
  - `ResolveAgentTools(tools, agent, isAsync, isMainThread)` → `ResolvedAgentTools`
- Package `agents/permission` now exposes:
  - Five-state modes (`ModeDefault/Plan/AcceptEdits/BypassPermissions/Auto`) + legacy aliases
  - `NormalizeMode`, `Mode.IsLaxerThanDefault`
  - `ParseRuleValue("Bash(git *)")` + `ParseCommaList("a,b")`
  - `Checker.Check` honours per-invocation `ToolUseContext.Options.AgentPermissionMode`
- Task tool (`tool/agenttool`) exposes:
  - `WithAgentCatalog(fn)`, `AgentCatalogFn`
  - `Input.{Model, RunInBackground, Name, TeamName, Mode, Isolation, Cwd}`
  - `Output.{Status, AgentID, OutputFile}` + `StatusCompleted`/`StatusAsyncLaunched`
- Engine wires `SpawnSubAgent` + `AgentDefinitions` + `AgentPermissionMode`
  overlay into `defaultToolUseContext`; hosts that use
  `WithToolUseContextBuilder` must forward the same fields manually.
- `EngineConfig.ResolveSubagentTools(parent, def, isAsync) []interface{}`
  lets hosts inject `tool.ResolveAgentTools` without an import cycle.
- Fork runtime (`RunForkedAgent`) is behind `RegisterForkRunner`; Phase D
  ships `DefaultForkRunner` +  `EnableEngineDrivenForkRunner(deps)` which
  installs it with a `QueryDeps` source.

### Wave 2 new API surface

- `agents` package additions:
  - Memory · `AgentMemoryScope`, `AgentMemoryConfig`, `AgentMemoryDir`,
    `AgentMemoryEntrypoint`, `IsAgentMemoryPath`, `BuildMemoryPrompt`,
    `ReadAgentMemory`, `WriteAgentMemory`, `DefaultUserMemoryDir`
  - Hooks · `RegisterFrontmatterHooks`, `ClearSessionHooks`,
    `ExecuteSubagentStartHooks`, `ExecuteSubagentStopHooks`,
    `AttachSubagentStartContext`, `AgentFrontmatterHooks`
  - MCP · `AgentMCPClient`, `MCPConnector`, `AgentMCPBundle`,
    `InitializeAgentMcpServers`, `HasRequiredMcpServers`,
    `FilterAgentsByMcpRequirements`, `ResetAgentMCPCache`
  - Fork · `ForkSubagentEnabled`, `ForkBoilerplateTag`,
    `ForkDirectivePrefix`, `ForkPlaceholderResult`, `ForkAgentType`,
    `BuildForkedMessages`, `IsInForkChild`, `ForkPrefixFingerprint`,
    `DefaultForkRunner`, `EnableEngineDrivenForkRunner`
  - Sidechain · `RecordSidechainTranscript`, `GetAgentTranscript`,
    `AgentMetadata`, `WriteAgentMetadata`, `ReadAgentMetadata`,
    `ResumeAgent`, `ResumeAgentOptions`, `ResumedAgent`,
    `FilterUnresolvedToolUses`, `FilterOrphanedThinkingOnlyMessages`,
    `FilterWhitespaceOnlyAssistantMessages`, `FilterResumedTranscript`,
    `ContentReplacementRecord`, `ReconstructForSubagentResume`
  - Async · `LocalAgentTask`, `LocalAgentTaskManager`, `TaskNotification`,
    `TaskUsageSummary`, `AsyncAgentHandle`,
    `(*QueryEngine).SpawnAsyncSubAgent`, `EncodeTaskNotification`
  - Fork helpers · `PreparedForkedContext`,
    `PrepareForkedCommandContext`, `PreparedForkedContext.ApplyAllowedTools`,
    `ApplyOverridesToContext`
- `EngineConfig` Wave 2 fields:
  - `SubagentOmitClaudeMd` (A19)
  - `AgentMemoryConfig` (C07/C08)
  - `ResolveAgentSkill` (C06)
  - `HookRegistry` (C03/C04/C05)
  - `MCPConnector` (C01/C02/C14)

### Wave 3 new API surface

- Coordinator · `CoordinatorConfig`, `DefaultCoordinatorSystemPrompt`,
  `BuildCoordinatorEngineConfig`, `RouteTaskToAsync`,
  `NotifyCoordinator`, `CoordinatorInbox`, `NewCoordinatorInbox`
- Observer · `TaskObserver`, `(*LocalAgentTaskManager).AddObserver`,
  `(*LocalAgentTaskManager).ObserverCount`
- Engine · `(*QueryEngine).Config() EngineConfig`
- Skills (C11) · `Skill`, `SkillRegistry`, `SkillInvocationLog`,
  `MakeSkillResolver` · `EngineConfig.SkillInvocationLog`
- Analytics (E05) · `ForkAnalyticsEvent`, `ForkAnalyticsEmitter`,
  `RegisterForkAnalytics`, `HasForkAnalytics`, `LogForkAgentQueryEvent`
- Handoff (E06) · `HandoffDecision`, `HandoffRequest`,
  `HandoffClassifier`, `RegisterHandoffClassifier`,
  `HasHandoffClassifier`, `ClassifyHandoffIfNeeded`
- Memory snapshot (C13) · `SnapshotState`, `SnapshotDecision`,
  `CheckAgentMemorySnapshot`, `InitializeFromSnapshot`,
  `ReplaceFromSnapshot`, `MarkSnapshotSynced`
- CLI (D19) · `AgentCatalogProvider`, `RegisterAgentAndForkCommands`
- Compact fork summarizer (D18) · `compact.ForkSummarizerCallModel`,
  `compact.NewForkCompactCallModel`, `compact.EnableForkSummarizerForCompactor`
- Worktree (D15) · `worktree.Available`, `worktree.ErrGitUnavailable`,
  `worktree.CreateAgentWorktree`, `worktree.HasWorktreeChanges`,
  `worktree.RemoveAgentWorktree`, `worktree.RunWithCwdOverride`

**72 / 72 tasks complete.** All phases (A–E) across P0, P1, P2 delivered
with unit + integration tests. Package tree `./agents/...` builds and
tests clean on Windows (PowerShell + `D:\go-tmp` redirect).
