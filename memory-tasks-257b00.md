# Memory Module Task-Level Execution Plan

将 D1-D5 五个 Phase 拆分为 20 个独立 task，每个 task = 1 个源文件 + 对应测试文件，按依赖顺序排列。

---

## 依赖关系总览

```
T01 ──→ T02 ──→ T03 ──→ T04 ──→ T05
                                  ↓
T06 ──→ T07 ──→ T08 ──→ T09     T15 (D3)
                          ↓       ↓
                         T10     T16 (D5 wiring)
                                  ↑
T11 ──→ T12 ──→ T13 ────────────→↑
                                  ↑
                         T14 ────→↑
```

---

## Phase D1: AutoDream (T01-T05)

### T01 — `autodream_config.go` + test (~60L)
- **新建**: `memory/autodream_config.go`
- **内容**:
  - `AutoDreamConfig{MinHours int, MinSessions int}` 结构体
  - `DefaultAutoDreamConfig` 常量 (24h, 3 sessions)
  - `IsAutoDreamEnabled(cfg EngineConfig, settings SettingsProvider) bool`
  - `GetAutoDreamConfig() AutoDreamConfig` — 从 env 读取覆盖值
  - `EnvAutoDreamEnabled = "UBUILDING_ENABLE_AUTO_DREAM"`
- **测试**: `autodream_config_test.go` — env 覆盖、默认值、disabled 判断
- **TS 对标**: `autoDream/config.ts` (22L)
- **依赖**: 无（仅依赖已有 `EngineConfig`, `SettingsProvider`）
- **验收**: `go test ./backend/agents/memory/ -run TestAutoDreamConfig`

### T02 — `consolidation_lock.go` + test (~130L)
- **新建**: `memory/consolidation_lock.go`
- **内容**:
  - 锁文件路径: `<autoMemPath>/.consolidate-lock`
  - `ReadLastConsolidatedAt(autoMemPath string) (time.Time, error)` — stat mtime
  - `TryAcquireConsolidationLock(autoMemPath string) (*time.Time, error)` — 写 PID, 重读验证, stale 检测(1hr)
  - `RollbackConsolidationLock(autoMemPath string, priorMtime *time.Time) error` — 恢复 mtime 或删除
  - `ListSessionsTouchedSince(projectDir string, since time.Time) ([]string, error)` — 扫描 *.jsonl
  - `RecordConsolidation(autoMemPath string) error` — touch mtime
  - `isProcessRunning(pid int) bool` — os.FindProcess + signal(0)
- **测试**: `consolidation_lock_test.go` — acquire/rollback/stale PID/竞争/空目录
- **TS 对标**: `autoDream/consolidationLock.ts` (141L)
- **依赖**: T01（使用 autoMemPath）
- **验收**: `go test ./backend/agents/memory/ -run TestConsolidationLock`

### T03 — `consolidation_prompt.go` + test (~70L)
- **新建**: `memory/consolidation_prompt.go`
- **内容**:
  - `BuildConsolidationPrompt(memoryRoot, transcriptDir, extra string) string`
  - 4 阶段结构: Orient → Gather recent signal → Consolidate → Prune and index
  - 引用已有常量: `DirExistsGuidance`, `autoMemEntrypoint`, `MaxEntrypointLines`, `MemoryFrontmatterExample`
- **测试**: `consolidation_prompt_test.go` — 包含 4 阶段关键词、路径替换验证
- **TS 对标**: `autoDream/consolidationPrompt.ts` (66L)
- **依赖**: 无（仅引用 memdir.go 已有常量）
- **验收**: `go test ./backend/agents/memory/ -run TestConsolidationPrompt`

### T04 — `autodream.go` + test (~200L)
- **新建**: `memory/autodream.go`
- **内容**:
  - `AutoDreamService` 结构体: cfg, lastSessionScan, mu, sideQueryFn, logger
  - `NewAutoDreamService(cwd string, cfg EngineConfig, settings SettingsProvider) *AutoDreamService`
  - `ExecuteAutoDream(ctx context.Context, messages []Message) error` — 主入口
  - Gate 链（最廉价优先）:
    1. `IsAutoDreamEnabled` 检查
    2. 时间门: hours since last consolidation ≥ minHours
    3. 扫描节流: SESSION_SCAN_INTERVAL = 10min
    4. 会话门: sessionCount ≥ minSessions
    5. 锁获取: TryAcquireConsolidationLock
  - 核心流程: BuildConsolidationPrompt → DefaultSideQueryFn → 解析文件操作 → 写入
  - 失败回滚: RollbackConsolidationLock(priorMtime)
- **测试**: `autodream_test.go` — gate 链各分支(mock time/sessions/lock), 成功/失败回滚
- **TS 对标**: `autoDream/autoDream.ts` + main orchestration
- **依赖**: T01, T02, T03
- **验收**: `go test ./backend/agents/memory/ -run TestAutoDream`

### T05 — `dream_task.go` + test (~100L)
- **新建**: `memory/dream_task.go`
- **内容**:
  - `DreamPhase string` ("starting", "updating", "complete", "failed")
  - `DreamTurn{Text string, ToolUseCount int}`
  - `DreamTaskState{Phase, SessionsReviewing, FilesTouched, Turns, PriorMtime, StartTime, Error}`
  - `NewDreamTaskState(sessions int, priorMtime *time.Time) *DreamTaskState`
  - `AddDreamTurn / CompleteDreamTask / FailDreamTask` 方法
  - `IsDreamTask(result interface{}) bool` 类型断言
  - 与 `BackgroundTaskManager` 的 factory 函数: `AutoDreamTaskFactory`
- **测试**: `dream_task_test.go` — 状态转换、phase 推进、filesTouched 追踪
- **TS 对标**: `tasks/DreamTask/` (158L)
- **依赖**: T04, `background_tasks.go`
- **验收**: `go test ./backend/agents/memory/ -run TestDreamTask`

---

## Phase D2: SessionMemory (T06-T10)

### T06 — `session_memory_config.go` + test (~80L)
- **新建**: `memory/session_memory_config.go`
- **内容**:
  - `SessionMemoryConfig{MinTokensToInit, MinTokensBetweenUpdate, ToolCallsBetweenUpdates}`
  - `DefaultSessionMemoryConfig` (10000, 5000, 3)
  - `EnvSessionMemoryEnabled = "UBUILDING_ENABLE_SESSION_MEMORY"`
  - `IsSessionMemoryEnabled(cfg EngineConfig, settings SettingsProvider) bool`
  - `SessionMemoryState` 结构体 (mu, initialized, lastSummarizedMsgUUID, extractionStartedAt, tokensAtLastExtraction)
  - `WaitForExtraction(timeout 15s, stale 60s) bool`
  - `HasMetInitThreshold / HasMetUpdateThreshold`
  - `MarkExtractionStarted / MarkExtractionCompleted`
  - `RecordExtractionTokenCount / ResetState`
- **测试**: `session_memory_config_test.go` — 阈值判定矩阵、wait 语义(timeout/stale)、reset
- **TS 对标**: `SessionMemory/sessionMemoryUtils.ts` (208L)
- **依赖**: 无
- **验收**: `go test ./backend/agents/memory/ -run TestSessionMemoryConfig`

### T07 — `session_memory_prompts.go` + test (~160L)
- **新建**: `memory/session_memory_prompts.go`
- **内容**:
  - `DefaultSessionMemoryTemplate` 常量 — 10 section markdown:
    Session Title, Current State, Task Spec, Key Files, Workflow Decisions,
    Active Issues, Codebase Insights, Key Learnings, Key Results, Work Log
  - `LoadSessionMemoryTemplate(configDir string) string` — 自定义模板加载
  - `LoadSessionMemoryPrompt(configDir string) string` — 自定义 prompt
  - `BuildSessionMemoryUpdatePrompt(currentNotes, notesPath string) string`
  - `AnalyzeSectionSizes(content string) map[string]int` — 解析 ## 标题, 估算 tokens
  - `GenerateSectionReminders(sizes map[string]int, totalTokens int) string`
  - `TruncateSessionMemoryForCompact(content string) (string, bool)` — MAX_SECTION_LENGTH=2000, MAX_TOTAL=12000
  - `IsSessionMemoryEmpty(content, template string) bool`
  - `substituteVariables(template string, vars map[string]string) string` — {{variable}} 替换
- **测试**: `session_memory_prompts_test.go` — 模板内容验证, 截断边界, section 分析, 变量替换
- **TS 对标**: `SessionMemory/prompts.ts` (325L)
- **依赖**: 无
- **验收**: `go test ./backend/agents/memory/ -run TestSessionMemoryPrompts`

### T08 — `session_memory.go` + test (~250L)
- **新建**: `memory/session_memory.go`
- **内容**:
  - `SessionMemoryService` 结构体:
    - cfg, state, sideQueryFn, mu, inProgress, pendingCtx, logger
    - cwd, sessionID, configDir
  - `NewSessionMemoryService(cwd, sessionID string, cfg EngineConfig, settings SettingsProvider)`
  - `ShouldExtract(messages []Message, tokenCount int) bool`:
    - 初始化阈值 OR 更新阈值 + 工具调用计数 + 末轮有 tool call
  - `Extract(ctx context.Context, messages []Message) error` — 主入口:
    1. setupFile → 读当前内容
    2. BuildSessionMemoryUpdatePrompt
    3. DefaultSideQueryFn
    4. 解析 Edit 操作 → 应用到文件
    5. 更新 lastSummarizedMsgUUID
  - `ManualExtract(messages []Message) error` — /summary 用
  - `setupFile() (path, content string, err error)` — mkdir + write template if not exists
  - `countToolCallsSince(messages, sinceUUID) int`
  - trailing run: inProgress 时暂存 pendingCtx, 完成后自动运行
- **测试**: `session_memory_test.go` — ShouldExtract 4 维矩阵, extraction 流程, trailing run, setupFile 幂等
- **TS 对标**: `SessionMemory/sessionMemory.ts` (496L)
- **依赖**: T06, T07
- **验收**: `go test ./backend/agents/memory/ -run TestSessionMemory`

### T09 — `session_memory_compact.go` + test (~200L)
- **新建**: `memory/session_memory_compact.go`
- **内容**:
  - `ShouldUseSessionMemoryCompaction(cfg EngineConfig, settings SettingsProvider) bool`
    - env: `UBUILDING_ENABLE_SM_COMPACT` / `UBUILDING_DISABLE_SM_COMPACT`
  - `CalculateMessagesToKeepIndex(messages []Message, lastSummarizedIdx int) int`
    - 从 lastSummarizedIdx+1 向后扩展
    - 满足 minTokens + minTextBlockMessages 双阈值
    - 地板: isCompactBoundaryMessage
    - 天花板: maxTokens
  - `TrySessionMemoryCompaction(ctx, messages, sessionMemSvc, threshold) (*CompactionResult, error)`
    - waitForExtraction → read sessionMemory → empty 检查 → 计算 keep
    - 构建 compaction result (planAttachment, boundaryMarker)
  - `isCompactBoundaryMessage(msg Message) bool`
  - 复用已有 `compact.AdjustIndexToPreserveAPIInvariants`
- **测试**: `session_memory_compact_test.go` — index 计算, 双阈值, API invariant, SM vs legacy 对比
- **TS 对标**: `compact/sessionMemoryCompact.ts` 编排部分 (631L)
- **依赖**: T06, T08, 已有 `compact/session_memory.go`
- **验收**: `go test ./backend/agents/memory/ -run TestSessionMemoryCompact`

### T10 — `session_memory_paths.go` + test (~50L)
- **新建**: `memory/session_memory_paths.go`
- **内容**:
  - `GetSessionMemoryDir(sessionID string) string` — `<configHome>/session-memory/<sessionID>/`
  - `GetSessionMemoryPath(sessionID string) string` — `.../notes.md`
  - `EnvSessionMemoryDir = "UBUILDING_SESSION_MEMORY_DIR"` override
- **测试**: `session_memory_paths_test.go` — 默认路径, env override, sessionID 清理
- **TS 对标**: `utils/permissions/filesystem.ts:getSessionMemoryPath`
- **依赖**: 无
- **验收**: `go test ./backend/agents/memory/ -run TestSessionMemoryPaths`

---

## Phase D3: ExtractMemories Upgrade (T15)

### T15 — 升级 `extract_memories.go` + test (~100L 修改)
- **修改**: `memory/extract_memories.go`
- **新增内容**:
  - `pendingCtx *ExtractionContext` 字段 — trailing run 暂存
  - `drainCh chan struct{}` + `DrainPending(timeout time.Duration) bool` — graceful shutdown
  - `agentID string` 字段 + `SetAgentID(id string)` — 仅主 agent 运行
  - `CreateAutoMemCanUseTool(memoryDir string) func(toolName, path string) bool`:
    - Read/Grep/Glob: 允许任意路径
    - Edit/Write: 仅限 memoryDir 内
    - Bash: 只读
  - OnTurnEnd: inProgress 时暂存 pendingCtx 而非丢弃
  - defer 块: 完成后检查 pendingCtx, 有则自动运行
  - 写入路径收集: 返回 `[]string` filePaths
  - appendSystemMessage 回调: extraction 完成后注入系统消息
- **测试更新**: `extract_memories_test.go` 新增:
  - TestTrailingRun: 验证 queue → run after in-flight
  - TestDrain: 验证 graceful shutdown
  - TestCanUseTool: 白名单/黑名单断言
  - TestAgentIDFilter: 子 agent 跳过
- **TS 对标**: `extractMemories/extractMemories.ts` 缺失特性
- **依赖**: 无（修改已有文件）
- **验收**: `go test ./backend/agents/memory/ -run TestExtract`

---

## Phase D4: TeamMemorySync Skeleton (T11-T14)

### T11 — `team_sync_types.go` + test (~80L)
- **新建**: `memory/team_sync_types.go`
- **内容**:
  - `SyncState{LastKnownChecksum, ServerChecksums, KnownDeletes, ServerMaxEntries, PushSuppression}`
  - `TeamMemoryData{OrganizationID, Repo, Version, LastModified, Checksum, Content}`
  - `TeamMemoryContent{Entries map[string]string, EntryChecksums map[string]string}`
  - `TeamMemorySyncFetchResult{Success, Data, IsEmpty, NotModified, Checksum, Error, ErrorType, HTTPStatus}`
  - `TeamMemorySyncPushResult{Success, FilesUploaded, Checksum, Conflict, Error, SkippedSecrets, ErrorType, HTTPStatus}`
  - `TeamMemoryHashesResult{Success, Version, Checksum, EntryChecksums, Error, ErrorType, HTTPStatus}`
  - `TeamMemorySyncUploadResult{Success, Checksum, LastModified, Conflict, Error, ErrorType, HTTPStatus, ServerErrorCode, ServerMaxEntries}`
  - `SkippedSecretFile{Path, RuleID, Label}`
- **测试**: `team_sync_types_test.go` — JSON 序列化/反序列化 round-trip
- **TS 对标**: `teamMemorySync/types.ts` (157L)
- **依赖**: 无
- **验收**: `go test ./backend/agents/memory/ -run TestTeamSyncTypes`

### T12 — `team_sync.go` + test (~120L)
- **新建**: `memory/team_sync.go`
- **内容**:
  - `TeamMemorySyncBackend` 接口:
    ```go
    Pull(ctx, state) (*TeamMemorySyncFetchResult, error)
    Push(ctx, state, entries) (*TeamMemorySyncPushResult, error)
    FetchHashes(ctx, state) (*TeamMemoryHashesResult, error)
    IsAvailable() bool
    ```
  - `TeamMemorySyncService{backend, state, teamDir, mu}`
  - `NewTeamMemorySyncService(backend, teamDir) *TeamMemorySyncService`
  - `ReadLocalTeamMemory(teamDir string, maxEntries *int) (map[string]string, []SkippedSecretFile, error)`
    - 扫描 .md 文件 + 复用 `ScanForSecrets` 过滤
    - MAX_FILE_SIZE = 250KB
  - `WriteRemoteEntriesToLocal(teamDir string, entries map[string]string) (int, error)`
  - `HashContent(content string) string` — `"sha256:" + hex(sha256(content))`
  - `BatchDeltaByBytes(delta map[string]string, maxBytes int) []map[string]string`
- **测试**: `team_sync_test.go` — ReadLocal(含 secret 过滤), HashContent, BatchDelta 分批逻辑
- **TS 对标**: `teamMemorySync/index.ts` 核心函数
- **依赖**: T11, 已有 `secret_scanner.go`
- **验收**: `go test ./backend/agents/memory/ -run TestTeamSync`

### T13 — `team_sync_watcher.go` + test (~60L)
- **新建**: `memory/team_sync_watcher.go`
- **内容**:
  - `TeamMemoryWatcher{service, debounceMs(2000), pushTimer, suppressed, mu}`
  - `NewTeamMemoryWatcher(service *TeamMemorySyncService) *TeamMemoryWatcher`
  - `Start() error` — `// TODO: implement with fsnotify` 占位
  - `Stop() error` — 清理 timer
  - `NotifyWrite()` — 触发 debounced push (reset timer)
  - `schedulePush()` — 内部: debounce 后调用 service.Push
  - `IsPermanentFailure(result *TeamMemorySyncPushResult) bool` — auth/no_repo → true
- **测试**: `team_sync_watcher_test.go` — debounce 逻辑, permanent failure 判断, NotifyWrite 合并
- **TS 对标**: `teamMemorySync/watcher.ts` (388L) 骨架
- **依赖**: T12
- **验收**: `go test ./backend/agents/memory/ -run TestTeamSyncWatcher`

### T14 — (无源文件，仅 D4 阶段验证)
- 运行全部 D4 测试: `go test ./backend/agents/memory/ -run "TestTeamSync"`
- 验证 `go build ./backend/...` 无编译错误

---

## Phase D5: Integration & Wiring (T16-T20)

### T16 — 更新 `background_tasks.go` (~30L 修改)
- **修改**: `agents/background_tasks.go`
- **内容**:
  - 导入 `BackgroundTaskContext` 增加 `SessionID` 字段(已有)
  - 添加 `autoDreamFactory` 和 `sessionMemoryFactory` 示例注释
  - 确保 `RunEndOfTurnTasks` 能正确串联新 task type
- **依赖**: T05, T08
- **验收**: `go build ./backend/agents/...`

### T17 — 更新 `stop_hooks.go` (~20L 修改)
- **修改**: `agents/stop_hooks.go`
- **内容**:
  - `BackgroundTaskStopHook` 增加 `SessionMemorySvc *SessionMemoryService` 可选字段
  - 确认现有 `RunEndOfTurnTasks` 机制无需改动（factory 注册制已支持）
- **依赖**: T16
- **验收**: `go build ./backend/agents/...`

### T18 — 更新 `doc.go` (~20L 修改)
- **修改**: `memory/doc.go`
- **内容**: 新增文档条目:
  - M14: `autodream_config.go / consolidation_lock.go / consolidation_prompt.go / autodream.go / dream_task.go`
  - M15: `session_memory_config.go / session_memory_prompts.go / session_memory.go / session_memory_compact.go / session_memory_paths.go`
  - M16: `extract_memories.go` 升级说明
  - M17: `team_sync_types.go / team_sync.go / team_sync_watcher.go`
- **依赖**: 全部源文件 task 完成
- **验收**: `go build ./backend/agents/memory/`

### T19 — `integration_test.go` (~60L)
- **新建**: `memory/integration_test.go`
- **内容**:
  - 模拟完整 turn 结束后:
    1. ExtractMemories 触发并写入 memory 文件
    2. AutoDream gate 链判定（mock 通过时执行 consolidation）
    3. SessionMemory 阈值判定 + extraction
  - 验证三个 service 可以共存于同一 autoMemPath 不冲突
  - 验证 BackgroundTaskManager 注册 3 个 factory 后 RunEndOfTurnTasks 全部触发
- **依赖**: T05, T08, T15, T16
- **验收**: `go test ./backend/agents/memory/ -run TestIntegration`

### T20 — 全量回归
- 运行全部测试: `go test ./backend/agents/... ./backend/agents/memory/... ./backend/agents/compact/...`
- 验证 `go build ./backend/...` 零错误
- 验证无 import cycle

---

## 执行顺序建议

```
Week 1:  T01 → T02 → T03 → T06 → T07 → T10 → T11  (无依赖的基础文件)
Week 2:  T04 → T05 → T08 → T09 → T12 → T13         (有依赖的核心逻辑)
Week 3:  T15 → T16 → T17 → T18 → T19 → T20          (升级 + 集成)
```

可并行的 task 对:
- T01 ∥ T06 ∥ T10 ∥ T11（全部无依赖）
- T03 ∥ T07（互不依赖）
- T12 ∥ T04（互不依赖）

---

## 总量统计

| 类别 | 数量 |
|------|------|
| 新建源文件 | 13 |
| 新建测试文件 | 13 |
| 修改源文件 | 4 |
| 新增代码行 | ~1620L |
| 修改代码行 | ~170L |
| Task 总数 | 20 |
