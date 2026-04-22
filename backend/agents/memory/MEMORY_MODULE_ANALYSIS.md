# uBuilding 记忆模块深度分析报告

> **分析范围**: `backend/agents/memory/` + `backend/agents/session_memory/`
> **代码基础**: Go 语言移植自 claude-code-main TypeScript 原版
> **生成时间**: 2026-04-22

---

## 一、总体架构概览

```
┌────────────────────────────────────────────────────────────────────┐
│                        记忆模块分层总图                               │
│                                                                    │
│  ┌──────────────────────────────────────────────────────────────┐  │
│  │            加载层 (静态 · 启动时/每轮 Prompt 构建)              │  │
│  │   Managed → User → Project → Local → AutoMem → TeamMem      │  │
│  │              CLAUDE.md 六层层级系统 (GetMemoryFiles)           │  │
│  └──────────────────────────────────────────────────────────────┘  │
│                              ▼                                     │
│  ┌──────────────────────────────────────────────────────────────┐  │
│  │            召回层 (动态 · 每次用户 Query 触发)                  │  │
│  │         FindRelevantMemories  ← SideQueryFn (小模型)          │  │
│  │         最多返回 5 个最相关文件注入对话                           │  │
│  └──────────────────────────────────────────────────────────────┘  │
│                              ▼                                     │
│  ┌──────────────────────────────────────────────────────────────┐  │
│  │         写入层 (背景写入 · 每轮结束后异步触发)                    │  │
│  │  ExtractMemoriesService   │   SessionMemoryExtractor         │  │
│  │  (长期记忆提取)              │   (会话内摘要)                    │  │
│  └──────────────────────────────────────────────────────────────┘  │
│                              ▼                                     │
│  ┌──────────────────────────────────────────────────────────────┐  │
│  │         汇总层 (AutoDream · 夜间/跨会话定期触发)                 │  │
│  │    AutoDreamService: Orient→Gather→Consolidate→Prune         │  │
│  │    4阶段 Consolidation Prompt + SideQueryFn (大模型)           │  │
│  └──────────────────────────────────────────────────────────────┘  │
│                              ▼                                     │
│  ┌──────────────────────────────────────────────────────────────┐  │
│  │              协作层 (Team Memory Sync · 可选)                  │  │
│  │      TeamMemorySyncer: push/fetch + secret scan               │  │
│  │      TeamMemorySyncWatcher: 轮询变更检测                       │  │
│  └──────────────────────────────────────────────────────────────┘  │
└────────────────────────────────────────────────────────────────────┘
```

---

## 二、记忆层级系统（6层）

### 2.1 MemoryType 层级定义

`types.go` 中定义的六层 `MemoryType`，按加载顺序从低到高优先级排列：

```go
var AllMemoryTypes = [...]MemoryType{
    MemoryTypeManaged,   // 企业/MDM 托管，最高信任
    MemoryTypeUser,      // ~/.config/ubuilding/CLAUDE.md
    MemoryTypeProject,   // 仓库 root → cwd 所有 CLAUDE.md
    MemoryTypeLocal,     // 仓库 root → cwd 的 CLAUDE.local.md
    MemoryTypeAutoMem,   // 自动管理的 MEMORY.md 入口
    MemoryTypeTeamMem,   // 团队共享记忆入口（opt-in）
}
```

### 2.2 层级优先级与加载顺序

```
优先级（低 → 高）
─────────────────────────────────────────────────────────────
Managed   ← 企业/MDM 托管，最高信任，豁免用户 exclude 规则
  ↓
User      ← ~/.config/ubuilding/CLAUDE.md  个人全局指令
  ↓
Project   ← 仓库根到 cwd 沿路所有 CLAUDE.md（向上遍历）
  ↓
Local     ← 仓库根到 cwd 的 CLAUDE.local.md（通常 .gitignored）
  ↓
AutoMem   ← 自动管理的 MEMORY.md 入口（模型自己维护）
  ↓
TeamMem   ← 团队共享记忆入口（仅 team memory 开启时）
─────────────────────────────────────────────────────────────
后加载 = 更高优先级 / 覆盖前层
```

### 2.3 文件加载流程

```
GetMemoryFiles(LoaderConfig)
    │
    ├─[1] Managed tier ──────────────── ManagedClaudeMd + ManagedRulesDir
    │
    ├─[2] User tier ─────────────────── <base>/CLAUDE.md
    │
    ├─[3] Project tier ──────────────── 从 cwd 向上遍历到根
    │         ├── CLAUDE.md                 每级检查
    │         └── .claude/rules/*.md        glob 匹配 paths: frontmatter
    │
    ├─[4] Local tier ────────────────── 同上路径，文件名=CLAUDE.local.md
    │
    ├─[5] AutoMem tier (opt-in) ─────── GetAutoMemPath(cwd, settings)
    │         └── MEMORY.md               TruncateEntrypointContent(200行/25KB)
    │
    └─[6] TeamMem tier (opt-in) ─────── GetTeamMemPath(cwd, settings)
              └── MEMORY.md               同样截断逻辑

    最终输出: []MemoryFileInfo  →  BuildUserContextClaudeMd()  →  注入 System Prompt
```

### 2.4 @include 递归解析（最深 5 层）

`claudemd.go` 中 `MaxIncludeDepth = 5`，防止病态配置爆栈。

---

## 三、记忆内容四型分类（MemoryContentType）

定义于 `memory_types.go`：

| 类型 | 作用域 | 内容 | 保存时机 |
|------|--------|------|---------|
| `user` | 始终私有 | 用户角色/目标/偏好/知识背景 | 了解用户任何背景信息时 |
| `feedback` | 默认私有；项目级约定可团队 | 行为指导：纠正 + 肯定 | 用户纠正/确认非显而易见的做法 |
| `project` | 偏向团队 | 进行中工作、目标、Bug、决策 | 了解 who/what/why/when，含绝对日期 |
| `reference` | 通常团队 | 外部系统指针（Linear/Slack等） | 了解外部资源位置和用途 |

**每个文件的 frontmatter 格式**：

```yaml
---
name: 简短标题
description: 一句话描述
type: user | feedback | project | reference
---
内容正文…
```

---

## 四、触发机制详解

### 4.1 触发链总图

```
[模型完成一轮回复]
       │
       ▼
BackgroundTaskStopHook.RunEndOfTurnTasks()
       │
       ├──► ExtractMemoriesService.OnTurnEnd()  ─── [长期记忆提取]
       │
       ├──► SessionMemoryExtractor.ShouldExtractMemory() ─── [会话摘要]
       │
       └──► AutoDreamTaskFactory()  ────────────────── [夜间汇总]
```

### 4.2 长期记忆提取触发门链（`ExtractMemoriesService`）

```
OnTurnEnd(messages)
    │
    ├─[Gate 1] IsEnabled()?
    │   env: UBUILDING_ENABLE_EXTRACT_MEMORIES=1
    │   AND: IsAutoMemoryEnabled(cfg, settings)
    │   └── NO → skip
    │
    ├─[Gate 2] DefaultSideQueryFn != nil?
    │   └── NO → warn + skip
    │
    ├─[Gate 3] agentID == "" (仅主 Agent)?
    │   └── NO → skip (子Agent不提取)
    │
    ├─[Gate 4] inProgress == false?
    │   └── YES (已在运行) → 排队为 trailing run（不丢弃！）
    │
    ├─[Gate 5] turnsSinceLastExtraction >= extractEveryNTurns?
    │   env: UBUILDING_EXTRACT_EVERY_N_TURNS (默认=1)
    │   └── NO → throttle skip
    │
    └── ✅ PASS → go runLoop(messages)  [goroutine 异步执行]
```

**Trailing Run 机制**（防丢失）：

```
运行中 ──遇到新请求──► pendingCtx = 新消息  (替换队列)
                              │
                    当前运行完成后
                              ▼
                    runLoop 检查 pendingCtx
                              │
                    pendingCtx != nil → 立即执行 trailing extraction
```

### 4.3 会话记忆提取触发条件（`SessionMemoryExtractor`）

```
ShouldExtractMemory(messages, currentTokenCount)
    │
    ├─[Gate 1] 初始化门：currentTokens >= 10,000?
    │   └── NO → false
    │
    ├─[Gate 2] Token 增量门：(currentTokens - tokensAtLastExtraction) >= 5,000?
    │
    ├─[Gate 3] 工具调用门：toolCallsSinceLastUUID >= 3?
    │
    └─ shouldExtract = (G2 AND G3) OR (G2 AND 上一轮无工具调用)
       ─────────────────────────────────────────────────────────
       含义：内容足够多且活跃 → 提取；或内容足够且本轮是纯文本回复（稳定点）
```

**默认配置参数**（`session_memory/config.go`）：

```go
var DefaultSessionMemoryConfig = SessionMemoryConfig{
    MinimumMessageTokensToInit: 10_000,
    MinimumTokensBetweenUpdate: 5_000,
    ToolCallsBetweenUpdates:    3,
}
```

### 4.4 AutoDream 夜间汇总触发门链（5 重门）

```
ExecuteAutoDream(ctx, messages)
    │
    ├─[Gate 1] IsAutoDreamEnabled(cfg, settings)?
    │   env: UBUILDING_ENABLE_AUTO_DREAM=1
    │   前提: IsAutoMemoryEnabled 也必须为真
    │   └── NO → skip(auto_dream_disabled)
    │
    ├─[Gate 2] 时间门：hoursSince(lastConsolidation) >= MinHours(默认24h)?
    │   从 .consolidate-lock 文件 mtime 读取上次时间
    │   └── NO → skip(time_gate: Xh < 24h)
    │
    ├─[Gate 3] 扫描节流：distance(lastSessionScan) >= 10min?
    │   防止同一进程短时间内重复扫描 transcript 目录
    │   └── NO → skip(scan_throttle)
    │
    ├─[Gate 4] 会话数门：len(sessionsTouched) >= MinSessions(默认3)?
    │   扫描 <base>/projects/<key>/*.jsonl 的 mtime > lastConsolidation
    │   排除当前 sessionID
    │   └── NO → skip(session_gate: N < 3)
    │
    ├─[Gate 5] 获取文件系统锁：TryAcquireConsolidationLock()?
    │   写入当前 PID 到 .consolidate-lock
    │   若已有活跃进程持锁(PID存活 且 锁<1h) → 阻断
    │   └── NO → skip(lock_held / lock_error)
    │
    └── ✅ ALL PASS → BuildConsolidationPrompt() + DefaultSideQueryFn()
```

---

## 五、召回策略

### 5.1 静态召回（每次构建 System Prompt）

```
启动 / 新会话
    │
    ▼
GetMemoryFiles() ─── 按优先级加载所有 CLAUDE.md 层级
    │
    ├── MEMORY.md 入口 ─── TruncateEntrypointContent()
    │   最多 200 行 / 25KB，超出加 WARNING 块
    │
    └── BuildUserContextClaudeMd() ─── 渲染成 System Prompt 的 <memory> 段
```

### 5.2 动态召回（每次用户 Query）

**流程**（`find_relevant.go` · `FindRelevantMemories`）：

```
用户提问 query
    │
    ▼
FindRelevantMemories(ctx, query, memoryDir, recentTools, alreadySurfaced)
    │
    ├─[1] ScanMemoryFiles(memoryDir)
    │       读取所有 .md 文件的 frontmatter（name + description）
    │       排除已展示过的文件 (alreadySurfaced)
    │
    ├─[2] FormatMemoryManifest(headers) → 格式化清单
    │       "filename.md — description" 列表
    │
    ├─[3] 构建 Selector 请求：
    │       system: SelectMemoriesPrompt (选择器系统提示)
    │       user:   "Query: ...\nAvailable memories:\n...\nRecently used tools: ..."
    │
    ├─[4] DefaultSideQueryFn() → 轻量 LLM 调用
    │       返回: {"selected_memories": ["file1.md", "file2.md"]}
    │
    ├─[5] parseSelectedMemories() → 支持 JSON / 纯文本降级解析
    │
    └─[6] 返回 []RelevantMemory{Path, MtimeMs}（最多5个）
              │
              └── ReadMemoryFileContent() → 加 freshness 时间戳注入对话
```

**Selector 关键策略**（`SelectMemoriesPrompt`）：

- 只选"明确有用"的文件，不确定就不选
- **已在用的工具的参考文档不选**（已在行动，不需要文档）
- **仍然选**工具的 warnings/gotchas（正在用时恰恰最需要）
- 最多返回 5 个

---

## 六、夜间做梦（AutoDream）汇总机制

### 6.1 四阶段 Consolidation 工作流

`BuildConsolidationPrompt()` 生成注入 `AutoDreamService.DefaultSideQueryFn` 的提示：

```
┌─────────────────────────────────────────────────────────────┐
│  Phase 1 — ORIENT（定向）                                    │
│  - ls memory 目录，了解已有什么                               │
│  - 读 MEMORY.md 理解当前索引                                  │
│  - 浏览现有主题文件，避免创建重复                               │
└─────────────────────────────────────────────────────────────┘
    │
    ▼
┌─────────────────────────────────────────────────────────────┐
│  Phase 2 — GATHER RECENT SIGNAL（采集新信号）                  │
│  优先级顺序:                                                  │
│  1. logs/YYYY/MM/YYYY-MM-DD.md（追加流日志）                  │
│  2. 已漂移记忆（与当前代码库矛盾的事实）                         │
│  3. Transcript 搜索（grep *.jsonl，窄词搜索，非全读）           │
│  ─────────────────────────────────────────────────────────  │
│  工具约束: Bash 仅限只读命令(ls/find/grep/cat/stat...)         │
│           写操作会被拒绝                                       │
└─────────────────────────────────────────────────────────────┘
    │
    ▼
┌─────────────────────────────────────────────────────────────┐
│  Phase 3 — CONSOLIDATE（整合）                               │
│  - 新信号合入已有主题文件（不创建近重复文件）                    │
│  - 相对日期 → 绝对日期（"昨天" → "2026-04-21"）               │
│  - 删除被推翻的旧事实，原地修正                                 │
└─────────────────────────────────────────────────────────────┘
    │
    ▼
┌─────────────────────────────────────────────────────────────┐
│  Phase 4 — PRUNE AND INDEX（修剪与索引）                      │
│  - 更新 MEMORY.md，保持 < 200 行 AND < 25KB                  │
│  - 每条目: "- [Title](file.md) — 一行摘要(≤150字符)"          │
│  - 删除指向已过时/已替代记忆的指针                              │
│  - 把冗长的条目内容移入主题文件，条目只保留摘要                  │
│  - 解决两个文件之间的矛盾                                      │
└─────────────────────────────────────────────────────────────┘
```

### 6.2 Consolidation Lock 文件锁机制

`.consolidate-lock` 文件位于 `autoMemPath/.consolidate-lock`：

```
.consolidate-lock
    │
    ├── mtime = 上次成功 Consolidation 的时间戳
    ├── 内容 = 持锁进程的 PID
    │
    ├── 获取锁:
    │   ├─ 无文件         → 直接创建，写入当前 PID ✅
    │   ├─ 有文件 + mtime < 1h + 持锁 PID 存活 → 阻断 🚫
    │   └─ 有文件 + 已过期 or PID 死亡 → 抢占(reclaim) ✅
    │       └── 重新读取验证 PID（防竞争）
    │
    ├── 回滚锁 (失败时):
    │   ├─ 首次(无 prior) → 删除文件
    │   └─ 有 prior      → 清空 PID 内容，恢复 prior mtime
    │
    └── Windows/Unix 双平台兼容:
        ├── Unix:    kill -0 (Signal 0)
        └── Windows: OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION)
```

### 6.3 DreamTask 状态机

```
DreamPhase 状态转移:

  starting ──[首次ToolUse]──► updating ──[成功]──► complete
     │                                    │
     │                             [失败]──► failed
     │
  [Kill]──► killed
```

---

## 七、会话记忆（Session Memory）子系统

### 7.1 与长期记忆的区别

| 维度 | 长期记忆 (extract_memories) | 会话记忆 (session_memory) |
|------|---------------------------|--------------------------|
| 范围 | 跨会话持久，项目级 | 单会话内有效，会话结束后辅助 compact |
| 存储 | `memory/<key>/topic.md` | `sessions/<session_id>/notes.md` |
| 触发 | 每 N 轮结束后 | Token 累积 + 工具调用次数 |
| 内容 | 结构化 4 型分类记忆文件 | 当前会话进行中的工作记录 |
| Compact 集成 | 无 | 注入 compact 上下文，保证 API 不变量 |

### 7.2 Session Memory 提取流程

```
Extract(ctx, messages, currentTokenCount)
    │
    ├─[1] 防重入: IsExtractionInProgress() → return nil
    ├─[2] EnsureNotesFile() → 若无文件则用模板创建
    ├─[3] ReadFile(notesPath) → 当前 notes 内容
    ├─[4] BuildSessionMemoryUpdatePrompt(notes, notesPath, configHome)
    │       system prompt: 告知模型如何更新 notes
    ├─[5] buildConversationContext(messages, lastUUID)
    │       提取 sinceUUID 后的文本块，截断至 50,000 字符
    ├─[6] sideQuery(ctx, updatePrompt, conversationCtx)
    │       LLM 直接返回新的 notes.md 全文
    └─[7] WriteFile(notesPath, response, 0600)
          RecordExtractionTokenCount(currentTokenCount)
```

---

## 八、团队记忆同步（M17）

```
TeamMemorySyncer
    │
    ├── BuildPushPayload()
    │   ├─ 扫描 teamDir/*.md
    │   ├─ ScanForSecrets() ← gitleaks 规则，含密钥则跳过
    │   └─ SHA-256 哈希每个文件
    │
    └── ApplyFetchResult(data)
        ├─ 路径安全校验（拒绝 ".." 和绝对路径）
        └─ 写入 teamDir/

TeamMemorySyncWatcher（轮询式）
    ├─ 定期检查 teamDir 文件变更
    └─ 触发 RecordPendingChanges → status.PendingChanges++
```

---

## 九、关键路径：完整端到端流程图

```
用户发送消息
     │
     ▼
[System Prompt 构建]
  GetMemoryFiles()
  ├── CLAUDE.md 6层加载
  ├── MEMORY.md 截断注入
  └── Team MEMORY.md (opt-in)
     │
     ▼
[Query 阶段] ← 可选动态召回
  FindRelevantMemories()
  └── SideQueryFn(Selector) → 注入最多5个相关文件到对话
     │
     ▼
[模型推理 + 流式输出]
     │
     ▼
[回复完成 → BackgroundTaskStopHook]
     │
     ├──────────────────────────────────────────────┐
     │                                              │
     ▼                                              ▼
[ExtractMemoriesService.OnTurnEnd]    [SessionMemoryExtractor]
  Gate 1~5 检查                         Token/ToolCall 门检查
  └─► go runLoop(messages)              └─► Extract() via SideQuery
        │                                     └─► 写 notes.md
        ├─► runExtraction()
        │   ├─ countModelVisibleMessages    [AutoDreamService]
        │   ├─ hasMemoryWritesSince          Gate 1~5 检查
        │   ├─ ScanMemoryFiles → manifest   └─► BuildConsolidationPrompt
        │   ├─ BuildExtract*Prompt              + SideQueryFn → 4阶段Dream
        │   ├─ SideQueryFn → JSON              └─► 写/更新/删 memory/*.md
        │   └─ parseAndWriteMemories            └─► 更新 MEMORY.md 索引
        │       ├─ 写 topic.md 文件
        │       └─ 更新 MEMORY.md 索引
        │
        └─► [trailing run] pendingCtx != nil → 再次执行
```

---

## 十、环境变量开关汇总

| 变量 | 控制 | 默认 |
|------|------|------|
| `UBUILDING_ENABLE_AUTO_MEMORY` | 自动记忆系统总开关 | `off` |
| `UBUILDING_DISABLE_AUTO_MEMORY` | 强制禁用（优先级高于 Enable） | — |
| `UBUILDING_ENABLE_EXTRACT_MEMORIES` | 长期记忆提取 | `off` |
| `UBUILDING_EXTRACT_EVERY_N_TURNS` | 提取频率（每N轮） | `1` |
| `UBUILDING_ENABLE_AUTO_DREAM` | 夜间汇总 | `off` |
| `UBUILDING_AUTO_DREAM_MIN_HOURS` | 汇总最小间隔小时数 | `24` |
| `UBUILDING_AUTO_DREAM_MIN_SESSIONS` | 汇总最小会话数 | `3` |
| `UBUILDING_ENABLE_SESSION_MEMORY` | 会话记忆 | `off` |
| `UBUILDING_MEMORY_SKIP_INDEX` | 跳过 MEMORY.md 索引更新 | `off` |
| `UBUILDING_REMOTE_MEMORY_DIR` | 覆盖记忆基础目录 | `<UserConfigDir>/ubuilding` |

---

## 十一、设计亮点与关键决策

1. **SideQueryFn 依赖注入**：记忆包不导入 API 客户端，所有 LLM 调用通过 `DefaultSideQueryFn` 注入，解除记忆层与传输层耦合。

2. **Trailing Run 不丢失机制**：提取正在进行时到来的新请求不会丢弃，而是替换队列，保证最后一轮的信息必然被处理。

3. **5重门 AutoDream 保护**：时间门 + 节流门 + 会话数门 + PID文件锁，多重保护防止频繁或并发的 Consolidation 引发无限 LLM 消耗。

4. **Secret Scan 前置**：团队记忆推送前必经 gitleaks 规则扫描，含密钥的文件静默跳过而非报错，防止敏感信息泄露至共享团队空间。

5. **WriteAllowlist 工具约束**：`CreateAutoMemCanUseTool` 实现只读工具全放行 + 写工具仅限 `memoryDir` 前缀，防止提取 Agent 误改项目文件。

6. **Compact 集成**：`session_memory/compact_bridge.go` 在 compact 时保留会话记忆摘要，维护 API 不变量（首消息为 user，最后为 assistant），确保 token 压缩后记忆不丢失上下文。

---

## 十二、文件索引

### `backend/agents/memory/`

| 文件 | 模块 | 职责 |
|------|------|------|
| `types.go` | M1 | MemoryType、MemoryFileInfo 数据模型 |
| `paths.go` | M2 | 路径解析、GetAutoMemPath、IsAutoMemoryEnabled |
| `claudemd.go` | M3 | GetMemoryFiles 主入口，6层加载器 |
| `claudemd_parse.go` | M3 | frontmatter解析、@include递归、HTML注释剥离 |
| `claudemd_rules.go` | M3 | `.claude/rules/` 条件规则文件处理 |
| `memdir.go` | M4 | MEMORY.md 截断、EnsureMemoryDirExists |
| `memory_types.go` | M5 | 4型分类提示常量（TypesSectionCombined等） |
| `team_paths.go` | M6 | 团队记忆路径校验，symlink感知 |
| `memory_prompt.go` | M7 | BuildMemoryLines、LoadMemoryPrompt |
| `render.go` | M8 | BuildUserContextClaudeMd、GetLargeMemoryFiles |
| `detection.go` | M9 | IsAutoMemFile、IsMemoryDirectory等分类谓词 |
| `memory_scan.go` | M10 | ScanMemoryFiles、FormatMemoryManifest |
| `memory_age.go` | M11 | MemoryFreshnessNote、MemoryAgeDays |
| `find_relevant.go` | M12 | FindRelevantMemories、SideQueryFn |
| `secret_scanner.go` | M13 | ScanForSecrets（gitleaks规则） |
| `write_allowlist.go` | M13 | IsAutoMemWriteAllowed、CheckTeamMemSecrets |
| `extract_memories.go` | M16 | ExtractMemoriesService，trailing run队列 |
| `extract_prompts.go` | M14 | BuildExtractAutoOnlyPrompt、BuildExtractCombinedPrompt |
| `autodream_config.go` | M14 | AutoDreamConfig、IsAutoDreamEnabled |
| `consolidation_lock.go` | M14 | PID文件锁、ListSessionsTouchedSince |
| `consolidation_prompt.go` | M14 | BuildConsolidationPrompt（4阶段） |
| `autodream.go` | M14 | AutoDreamService、ExecuteAutoDream |
| `dream_task.go` | M14 | DreamTaskState状态机、AutoDreamTaskFactory |
| `team_sync_types.go` | M17 | TeamMemoryData等API载荷类型 |
| `team_sync.go` | M17 | TeamMemorySyncer push/fetch |
| `team_sync_watcher.go` | M17 | 轮询变更检测 |

### `backend/agents/session_memory/`

| 文件 | 职责 |
|------|------|
| `config.go` | SessionMemoryConfig、IsSessionMemoryEnabled |
| `state.go` | SessionStateManager（并发安全） |
| `prompts.go` | BuildSessionMemoryUpdatePrompt、模板加载 |
| `extractor.go` | SessionMemoryExtractor、ShouldExtractMemory、Extract |
| `compact_bridge.go` | TruncateForCompact、API不变量保证 |
| `paths.go` | 会话级路径解析 |
