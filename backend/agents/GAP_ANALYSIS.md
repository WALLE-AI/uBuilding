# QueryEngine TypeScript → Go: 深度源码分析与完整复制执行方案

> 生成日期: 2025-07  
> 目标: 将 `opensource/claude-code-main` 中 QueryEngine 核心引擎及所有系统 Prompt 完整复制到 `backend/agents` (Go)

---

## 一、TypeScript 源码架构总览

### 1.1 核心文件清单与职责

| TS 文件 | 行数 | 核心职责 |
|---------|------|---------|
| `QueryEngine.ts` | 1296 | 会话级编排器: submitMessage(), processUserInput, 系统Prompt构建, 结果处理 |
| `query.ts` | 1730 | queryLoop 状态机: 15个阶段, 12+退出路径, 压缩/API调用/工具执行/恢复 |
| `query/deps.ts` | 41 | 依赖注入接口: callModel, microcompact, autocompact, uuid |
| `query/stopHooks.ts` | 474 | 停止钩子: handleStopHooks, executeStopHooks, TaskCompleted, TeammateIdle, 后台任务 |
| `query/tokenBudget.ts` | 100 | Token预算: checkTokenBudget, 延续/停止决策 |
| `query/config.ts` | 75 | 特性开关快照: buildQueryConfig, GrowthBook集成 |
| `constants/prompts.ts` | 915 | 完整系统Prompt: 14+静态/动态节, getSystemPrompt(), computeEnvInfo() |
| `constants/systemPromptSections.ts` | 69 | 节缓存: memoized sections, DANGEROUS_uncached, resolveSystemPromptSections |
| `context.ts` | 190 | 上下文: getUserContext (CLAUDE.md), getSystemContext (git), getGitStatus |
| `utils/queryContext.ts` | 180 | Prompt组装: fetchSystemPromptParts, buildSideQuestionFallbackParams |
| `utils/systemPrompt.ts` | 124 | 优先级选择: buildEffectiveSystemPrompt (5级优先) |
| `utils/api.ts` | 719 | API工具: toolToAPISchema, filterSwarmFields, CacheScope, SystemPromptBlock |
| `services/tools/toolOrchestration.ts` | 189 | 工具编排: runTools, partitionToolCalls (并发安全分区), 串行/并行执行 |
| `services/tools/StreamingToolExecutor.ts` | 531 | 流式工具执行器: addTool, getRemainingResults, 并发控制, 错误传播 |
| `services/tools/toolExecution.ts` | 1700+ | 单个工具执行: runToolUse, 权限检查, hooks, 结果处理 |
| `services/tools/toolHooks.ts` | 700+ | 工具钩子: preToolUse, postToolUse, shellHooks |
| `services/compact/autoCompact.ts` | 400+ | 自动压缩: autoCompactIfNeeded, LLM驱动摘要 |
| `services/compact/microCompact.ts` | 600+ | 微压缩: 折叠重复读/搜索 |
| `services/compact/compact.ts` | 1800+ | 核心压缩: LLM摘要生成, 上下文保留 |

### 1.2 完整执行流程图 (15个阶段)

```
用户输入
  │
  ▼
┌─────────────────────────────────────────────────────────┐
│ QueryEngine.submitMessage()                              │
│  ├─ processUserInput() — 本地命令检测, 附件注入           │
│  ├─ fetchSystemPromptParts() — 默认Prompt + 上下文       │
│  ├─ buildEffectiveSystemPrompt() — 5级优先选择            │
│  ├─ recordTranscript() — 会话持久化                       │
│  ├─ 构建 QueryParams                                     │
│  └─ 启动 query() → queryLoop()                           │
└───────────────────────┬─────────────────────────────────┘
                        │
                        ▼
┌─────────────────────────────────────────────────────────┐
│ queryLoop() — 核心循环状态机                              │
│                                                          │
│  Phase 1:  getMessagesAfterCompactBoundary()             │
│            ↓                                             │
│  Phase 2:  snipCompact (HISTORY_SNIP gate)               │
│            ↓                                             │
│  Phase 2b: applyToolResultBudget()                       │
│            ↓                                             │
│  Phase 3:  microcompact (折叠重复读/搜索)                 │
│            ↓                                             │
│  Phase 4:  contextCollapse (增量上下文折叠)               │
│            ↓                                             │
│  Phase 5:  构建完整系统Prompt (static + dynamic)          │
│            ↓                                             │
│  Phase 6:  autocompact (LLM驱动摘要)                     │
│            ├── task_budget 调整                           │
│            ├── tracking 重置                              │
│            └── 发射 compact_boundary                      │
│            ↓                                             │
│  Phase 7:  Token limit check / blocking preempt          │
│            ↓                                             │
│  Phase 7b: startRelevantMemoryPrefetch() — 异步预取       │
│            ↓                                             │
│  Phase 8:  准备API调用 (工具定义, 模型选择, 用户上下文)   │
│            ↓                                             │
│  Phase 9:  callModel (流式API调用)                        │
│            ├── 流式事件消费                               │
│            ├── 回退重试 (FallbackTriggeredError)          │
│            ├── 流式回退 (tombstone orphaned messages)     │
│            ├── 图片错误处理                               │
│            └── withheld 错误 (prompt_too_long, max_tokens)│
│            ↓                                             │
│  Phase 10: Post-streaming abort check                    │
│            ↓                                             │
│  Phase 11: Handle no-tool-use (end_turn)                 │
│            ├── 11a: prompt-too-long 恢复链                │
│            │    ├── context collapse drain                │
│            │    └── reactive compact                      │
│            ├── 11b: max_output_tokens 恢复               │
│            │    ├── escalate (→64k)                       │
│            │    └── multi-turn recovery (nudge msg)       │
│            ├── 11c: skip stop hooks for API error        │
│            ├── 11d: handleStopHooks()                    │
│            │    ├── executeStopHooks (用户自定义钩子)      │
│            │    ├── executeTeammateIdleHooks              │
│            │    ├── executeTaskCompletedHooks             │
│            │    ├── prompt suggestion (后台)              │
│            │    ├── memory extraction (后台)              │
│            │    └── auto-dream (后台)                     │
│            └── 11e: token budget continuation             │
│            ↓                                             │
│  Phase 12: Execute Tools                                 │
│            ├── partitionToolCalls (并发安全分区)           │
│            ├── StreamingToolExecutor / runTools           │
│            ├── tool hooks (pre/post)                     │
│            ├── permission checking                        │
│            └── context modifier application              │
│            ↓                                             │
│  Phase 12b: Post-tool abort check                        │
│            ↓                                             │
│  Phase 12c: Attachment pipeline + memory consume         │
│            ├── consume memory prefetch                    │
│            ├── file change notifications                  │
│            ├── date changes                               │
│            └── queued commands                            │
│            ↓                                             │
│  Phase 13: MaxTurns check                                │
│            ↓                                             │
│  Phase 14: Refresh tools between turns                   │
│            ↓                                             │
│  Phase 15: Prepare next iteration (state transition)     │
│            └── 循环回到 Phase 1                           │
└─────────────────────────────────────────────────────────┘
```

### 1.3 Prompt 系统架构

```
┌─────────────────────────────────────────────────────────────┐
│ getSystemPrompt() — 完整Prompt组装                            │
│                                                              │
│  ═══ 静态节 (cross-org cacheable) ═══                        │
│  [1] getSimpleIntroSection()     — 身份 + 安全前言            │
│  [2] getSimpleSystemSection()    — 系统行为规则               │
│  [3] getSimpleDoingTasksSection() — 任务执行规则 (isAnt分支)  │
│  [4] getActionsSection()          — 谨慎操作指南              │
│  [5] getUsingYourToolsSection()   — 工具使用指南              │
│  [6] getSimpleToneAndStyleSection() — 语气/风格               │
│  [7] getOutputEfficiencySection()  — 输出效率 (ant/non-ant)   │
│                                                              │
│  ═══ SYSTEM_PROMPT_DYNAMIC_BOUNDARY ═══                      │
│                                                              │
│  ═══ 动态节 (session-specific) ═══                           │
│  [8]  getSessionSpecificGuidanceSection() — 会话级指导        │
│  [9]  computeSimpleEnvInfo()     — 环境信息 (cwd,git,os,shell)│
│  [10] loadMemoryPrompt()         — CLAUDE.md 记忆加载         │
│  [11] getLanguageSection()       — 语言偏好                   │
│  [12] getOutputStyleSection()    — 输出风格覆盖               │
│  [13] getMcpInstructionsSection() — MCP服务器指令             │
│  [14] getScratchpadInstructions() — 临时目录指令              │
│  [15] getFunctionResultClearingSection() — FRC指令            │
│  [16] SummarizeToolResultsSection — 工具结果总结指令           │
│  [17] getNumericLengthAnchorsSection() — ant输出长度锚点      │
│  [18] getTokenBudgetSection()    — Token预算指令              │
│  [19] getProactiveSection()      — 自主模式Prompt (可选)      │
│  [20] getBriefSection()          — 简洁模式 (可选)            │
└─────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────┐
│ buildEffectiveSystemPrompt() — 5级优先选择                    │
│                                                              │
│  优先级 1: overrideSystemPrompt  → 完全替换                   │
│  优先级 2: coordinatorPrompt     → 协调者模式                  │
│  优先级 3: agentSystemPrompt     → Agent模式                   │
│            ├── proactive: 追加到default                        │
│            └── normal: 替换default                             │
│  优先级 4: customSystemPrompt    → 替换default                 │
│  优先级 5: defaultSystemPrompt   → 回退                        │
│  + appendSystemPrompt 总是追加 (除 override)                   │
└─────────────────────────────────────────────────────────────┘
```

### 1.4 停止钩子系统 (stopHooks.ts 详细)

```
handleStopHooks()
  │
  ├─ executeStopHooks()
  │   ├── 用户自定义 shell hooks (settings.json 中配置)
  │   ├── 每个 hook 返回 blocking errors 或 prevent continuation
  │   └── 结果合并到 StopHookResult
  │
  ├─ executeTaskCompletedHooks() (多 agent 模式)
  │   └── 检查 in-progress tasks, 调用 onTaskCompleted
  │
  ├─ executeTeammateIdleHooks() (多 agent 模式)
  │   └── 通知 orchestrator 当前 agent idle
  │
  ├─ 后台任务 (非 bare mode):
  │   ├── promptSuggestion — 建议后续提示
  │   ├── memoryExtraction — 提取记忆到 CLAUDE.md
  │   └── autoDream — 自动梦境摘要
  │
  └─ 返回 StopHookResult:
      ├── blockingErrors → 注入历史, 继续循环
      ├── preventContinuation → 终止循环
      └── hookMessages → 额外消息
```

### 1.5 工具执行管线 (services/tools/)

```
Phase 12: Execute Tools
  │
  ├─ StreamingToolExecutor (streaming gate = true)
  │   ├── addTool() — 流式添加工具到执行队列
  │   ├── startExecution() — 基于并发安全性分区执行
  │   ├── getRemainingResults() — 收集结果, 保持顺序
  │   ├── 并发控制: MAX_TOOL_USE_CONCURRENCY (默认10)
  │   └── 错误传播: siblingAbortController
  │
  └─ runTools() (non-streaming fallback)
      ├── partitionToolCalls() — 分区:
      │   ├── isConcurrencySafe = true → 并行批次
      │   └── isConcurrencySafe = false → 串行单个
      ├── runToolsConcurrently() — 并行执行
      └── runToolsSerially() — 串行执行

每个工具执行 (runToolUse):
  ├── findToolByName() — 查找工具定义
  ├── inputSchema.safeParse() — 输入验证
  ├── canUseTool() — 权限检查
  ├── preToolUseHooks() — 前置钩子
  ├── tool.call() — 实际执行
  ├── postToolUseHooks() — 后置钩子
  └── 返回 MessageUpdateLazy (message + contextModifier)
```

### 1.6 压缩管线 (services/compact/)

```
压缩层级 (从轻量到重量):

1. snipCompact (L401-410)
   ├── 作用: 删除最早的消息段以释放token
   ├── 门控: HISTORY_SNIP gate
   └── 输出: messages, tokensFreed, boundaryMessage

2. applyToolResultBudget (L369-394)
   ├── 作用: 截断过大的工具结果
   ├── 策略: 每消息聚合预算, 超出部分截断
   └── 输出: 修改后的messages

3. microcompact (L414-426)
   ├── 作用: 折叠重复的读/搜索操作
   ├── 策略: 本地模式匹配, 无LLM调用
   └── 输出: messages, applied flag

4. contextCollapse (L440-447)
   ├── 作用: 增量上下文折叠 (staged)
   ├── 门控: CONTEXT_COLLAPSE gate
   ├── 策略: 渐进式折叠, 非全量压缩
   └── drain: 在 overflow recovery 时全量提交

5. autocompact (L454-543)
   ├── 作用: LLM驱动的对话摘要
   ├── 触发: token使用超阈值
   ├── 策略: 生成摘要, 替换旧消息
   └── 副作用: compact_boundary, task_budget调整

6. reactiveCompact (recovery path)
   ├── 触发: prompt_too_long 或 media_size 错误
   ├── 作用: 紧急压缩
   └── 副作用: hasAttemptedReactiveCompact guard
```

---

## 二、Go 实现现状 (已完成模块)

### 2.1 文件清单 (49 .go files)

| 包 | 文件 | 对应 TS | 完成度 |
|---|------|---------|--------|
| `agents` | `engine.go` (566L) | QueryEngine.ts | ✅ 90% |
| `agents` | `queryloop.go` (1008L) | query.ts | ✅ 95% |
| `agents` | `deps.go` (268L) | query/deps.ts | ✅ 100% |
| `agents` | `config.go` (74L) | query/config.ts | ✅ 100% |
| `agents` | `types.go` (440L) | types/message.ts | ✅ 95% |
| `agents` | `stop_hooks.go` (457L) | query/stopHooks.ts | ✅ 80% |
| `agents` | `token_budget.go` (242L) | query/tokenBudget.ts | ✅ 100% |
| `agents` | `tool_use_context.go` (217L) | Tool.ts (ToolUseContext) | ✅ 90% |
| `prompt` | `prompts.go` (516L) | constants/prompts.ts | ✅ 95% |
| `prompt` | `sections.go` (115L) | constants/systemPromptSections.ts | ✅ 100% |
| `prompt` | `context.go` (338L) | context.ts | ✅ 95% |
| `prompt` | `query_context.go` (91L) | utils/queryContext.ts | ✅ 90% |
| `prompt` | `effective.go` (118L) | utils/systemPrompt.ts | ✅ 100% |
| `prompt` | `env_info.go` (292L) | computeEnvInfo/computeSimpleEnvInfo | ✅ 100% |
| `prompt` | `session_guidance.go` (186L) | session guidance sections | ✅ 95% |
| `prompt` | `api_context.go` (86L) | utils/api.ts (context injection) | ✅ 100% |
| `prompt` | `system.go` (238L) | 6-layer builder + BuildFullSystemPrompt | ✅ 100% |
| `prompt` | `message_normalize.go` (231L) | utils/messages.ts | ✅ 90% |
| `compact` | `auto.go` (9061B) | services/compact/autoCompact.ts | ✅ 85% |
| `compact` | `micro.go` (4342B) | services/compact/microCompact.ts | ✅ 70% |
| `compact` | `snip.go` (3611B) | snipCompact.ts | ✅ 80% |
| `compact` | `context_collapse.go` (7258B) | contextCollapse/ | ✅ 75% |
| `compact` | `reactive.go` (1873B) | reactiveCompact.ts | ✅ 60% |
| `compact` | `tool_result_budget.go` (13633B) | toolResultStorage.ts | ✅ 95% |
| `tool` | `orchestration.go` (10015B) | toolOrchestration.ts | ✅ 80% |
| `tool` | `streaming_executor.go` (10704B) | StreamingToolExecutor.ts | ✅ 75% |
| `tool` | `tool.go` (6731B) | Tool.ts | ✅ 80% |
| `tool` | `registry.go` (2671B) | tool registry | ✅ 70% |
| `provider` | `anthropic.go` / `openai_compat.go` | services/api/ | ✅ 60% |
| `permission` | `checker.go` / `types.go` | hooks/useCanUseTool | ✅ 50% |

### 2.2 测试覆盖

- `agents/` — 40+ tests passing ✅
- `compact/` — tool_result_budget_test (8), context_collapse_test, snip_test ✅
- `prompt/` — prompt_test (16) ✅
- `tool/` — orchestration_test ✅

---

## 三、差距分析 (Gap Analysis)

### 3.1 高优先级差距 (功能性缺失)

#### GAP-1: processUserInput 未实现 (engine.go L436-451)
- **TS 位置**: QueryEngine.ts L540-635
- **缺失**: 本地命令处理 (/clear, /compact, /help, /config, /init)
- **缺失**: 用户输入附件处理 (文件引用, URL, 图片)
- **缺失**: slash command 检测和展开
- **影响**: 用户输入预处理完全缺失

#### GAP-2: toolToAPISchema 未实现
- **TS 位置**: utils/api.ts L1-200
- **缺失**: Tool → Anthropic API schema 转换
- **缺失**: filterSwarmFieldsFromSchema
- **缺失**: defer_loading, cache_control, strict mode
- **缺失**: schema caching (weakMap)
- **影响**: buildToolDefinitions() (queryloop.go L832-837) 返回 nil

#### GAP-3: 工具执行管线不完整
- **TS 位置**: services/tools/toolExecution.ts (1700+ lines)
- **缺失**: runToolUse 完整实现 (权限检查 → pre hooks → execute → post hooks)
- **缺失**: preToolUseHooks / postToolUseHooks 
- **缺失**: shell hooks (user-prompt-submit, pre/post tool hooks)
- **缺失**: 工具输入验证 (inputSchema.safeParse)
- **影响**: 工具执行流程骨架存在但细节不完整

#### GAP-4: 后台停止钩子任务缺失 (stop_hooks.go)
- **TS 位置**: query/stopHooks.ts L100-474
- **缺失**: promptSuggestion (建议后续提示)
- **缺失**: memoryExtraction (提取记忆到 CLAUDE.md)
- **缺失**: autoDream (自动梦境摘要)
- **缺失**: template job classification
- **缺失**: CU lock cleanup (Chicago MCP mode)
- **影响**: 后台智能功能缺失

#### GAP-5: executeStopHooks 用户 shell hooks 缺失
- **TS 位置**: query/stopHooks.ts L20-80
- **缺失**: 用户配置的 shell hooks (settings.json)
- **缺失**: hook command 执行和结果解析
- **影响**: 用户自定义钩子无法执行

### 3.2 中优先级差距 (完整性缺失)

#### GAP-6: Prompt 缓存系统 (SystemPromptBlock + CacheScope)
- **TS 位置**: utils/api.ts L50-120
- **缺失**: CacheScope ("global" | "turn") 标记
- **缺失**: SystemPromptBlock 类型 (text + cache_control)
- **缺失**: Prompt cache breakpoint 管理 (4个 breakpoints)
- **影响**: API调用不携带缓存控制, cache hit rate 低

#### GAP-7: microcompact 规则不完整
- **TS 位置**: services/compact/microCompact.ts (600+ lines)
- **Go 现状**: compact/micro.go (4342B) — 基本框架
- **缺失**: 完整的折叠规则 (repeated reads, searches, glob results)
- **缺失**: apiMicrocompact 路径 (API-side micro compaction)
- **缺失**: timeBasedMCConfig (时间相关配置)

#### GAP-8: autocompact LLM 摘要完整性
- **TS 位置**: services/compact/autoCompact.ts + compact.ts + prompt.ts
- **Go 现状**: compact/auto.go (9061B) — 基本框架
- **缺失**: compact prompt 完整模板 (services/compact/prompt.ts, 16652B)
- **缺失**: sessionMemoryCompact (21688B)
- **缺失**: postCompactCleanup (3855B)
- **缺失**: grouping 逻辑 (2857B)

#### GAP-9: StreamingToolExecutor 完整性
- **TS 位置**: StreamingToolExecutor.ts (531 lines)
- **Go 现状**: tool/streaming_executor.go (10704B) — 基本框架
- **缺失**: discard() 机制 (流式回退时丢弃)
- **缺失**: siblingAbortController (bash错误传播)
- **缺失**: progressAvailableResolve (进度信号)
- **缺失**: 严格的结果顺序保证

#### GAP-10: message_normalize 缺失规则
- **Go 现状**: prompt/message_normalize.go (231L) — 基本实现
- **缺失**: tombstone 消息处理
- **缺失**: snip_boundary 消息处理
- **缺失**: tool_use_summary 消息处理
- **缺失**: stream_event 消息过滤

### 3.3 低优先级差距 (增强功能)

#### GAP-11: 完整的 TS 服务模块
- `services/PromptSuggestion/` — 提示建议
- `services/SessionMemory/` — 会话记忆
- `services/extractMemories/` — 记忆提取
- `services/autoDream/` — 自动梦境
- `services/AgentSummary/` — Agent摘要
- `services/MagicDocs/` — 魔法文档
- `services/analytics/` — 分析追踪
- `services/tokenEstimation.ts` — Token估算
- `services/toolUseSummary/` — 工具使用摘要生成

#### GAP-12: Provider 层完整性
- **缺失**: 完整的 Anthropic streaming API 对接
- **缺失**: 速率限制处理 (rateLimitMessages.ts)
- **缺失**: 重试逻辑 (services/api/withRetry.ts)
- **缺失**: API 键值管理
- **缺失**: 多 provider 路由 (Claude AI limits)

#### GAP-13: 会话持久化
- **缺失**: 完整的 transcript 持久化
- **缺失**: session storage (sessionStorage.ts)
- **缺失**: resume 功能
- **缺失**: conversation history export

---

## 四、详细执行方案

### Phase 6: 工具管线完善 (高优先级)

**目标**: 实现完整的工具执行管线, 包括 API schema 转换和工具执行

#### 6.1 toolToAPISchema — 工具 Schema 转换
- **新文件**: `tool/api_schema.go`
- **内容**:
  - `ToolToAPISchema(tool Tool, opts SchemaOpts) APIToolSchema`
  - `FilterSwarmFieldsFromSchema(schema, toolName) schema`
  - `APIToolSchema` 类型 (name, description, input_schema, strict, defer_loading, cache_control)
  - schema caching (sync.Map)
- **对应 TS**: utils/api.ts L1-200
- **依赖**: tool/tool.go

#### 6.2 buildToolDefinitions 补全
- **修改文件**: `queryloop.go` L832-837
- **内容**: 将 `buildToolDefinitions()` 连接到 `tool/api_schema.go`
- **将**: `return nil` → 实际转换工具定义

#### 6.3 工具执行完善
- **修改文件**: `tool/orchestration.go`
- **新增**: 权限检查集成 (`canUseTool` 回调)
- **新增**: preToolUseHooks / postToolUseHooks 框架
- **新增**: 工具输入验证
- **修改文件**: `tool/streaming_executor.go`
- **新增**: discard() 方法
- **新增**: siblingAbort 机制 (child context)
- **新增**: 严格结果顺序保证

#### 6.4 测试
- `tool/api_schema_test.go` — schema 转换测试
- `tool/orchestration_test.go` — 补充权限检查测试
- `tool/streaming_executor_test.go` — 并发/顺序/discard 测试

**估计行数**: ~600 新增, ~200 修改  
**估计工时**: 3-4 天

---

### Phase 7: processUserInput 实现 (高优先级)

**目标**: 实现用户输入预处理管线

#### 7.1 本地命令处理
- **修改文件**: `engine.go` L436-451
- **内容**:
  - `/clear` — 清空历史, 重置 section cache
  - `/compact` — 手动触发压缩
  - `/help` — 返回帮助信息
  - `/config` — 配置查看/修改
  - `/init` — 初始化 CLAUDE.md
  - 其他 slash commands 分发框架
- **新文件**: `commands.go` — 本地命令注册和执行

#### 7.2 附件处理
- **新文件**: `attachments.go`
- **内容**:
  - 文件引用检测和内容加载
  - URL 引用检测
  - 图片处理 (base64 encoding)
  - 附件消息生成

#### 7.3 测试
- `commands_test.go` — 各命令测试
- `attachments_test.go` — 附件处理测试

**估计行数**: ~500 新增, ~100 修改  
**估计工时**: 2-3 天

---

### Phase 8: 停止钩子系统补全 (中优先级)

**目标**: 补全后台任务和用户 shell hooks

#### 8.1 用户 shell hooks
- **新文件**: `shell_hooks.go`
- **内容**:
  - 从 settings 配置加载 hook commands
  - hook command 执行 (os/exec)
  - 结果解析 (stdout → blocking errors)
  - 超时控制

#### 8.2 后台任务框架
- **新文件**: `background_tasks.go`
- **内容**:
  - `PromptSuggestion` — 建议后续提示 (goroutine)
  - `MemoryExtraction` — 提取记忆到 CLAUDE.md (goroutine)
  - `AutoDream` — 自动摘要 (goroutine)
  - 统一的后台任务管理器

#### 8.3 修改 stop_hooks.go
- 集成 shell hooks 到 HandleStopHooks
- 集成后台任务到 end_turn 路径
- 添加 bare mode 检查

#### 8.4 测试
- `shell_hooks_test.go`
- `background_tasks_test.go`

**估计行数**: ~400 新增, ~100 修改  
**估计工时**: 2-3 天

---

### Phase 9: Prompt 缓存系统 (中优先级)

**目标**: 实现 API-level prompt 缓存控制

#### 9.1 CacheScope + SystemPromptBlock
- **新文件**: `prompt/cache.go`
- **内容**:
  - `CacheScope` 类型 ("global" | "turn")
  - `SystemPromptBlock` 类型 (type: "text", text, cache_control)
  - `BuildSystemPromptBlocks()` — 将 string prompt 转为带缓存控制的 blocks
  - 4个 breakpoint 管理: static/dynamic boundary, user context, system context

#### 9.2 集成到 API 调用
- **修改文件**: `provider/anthropic.go`
- **内容**: 使用 SystemPromptBlock[] 代替 string system prompt

#### 9.3 测试
- `prompt/cache_test.go`

**估计行数**: ~300 新增, ~50 修改  
**估计工时**: 1-2 天

---

### Phase 10: 压缩管线深化 (中优先级)

**目标**: 补全 microcompact/autocompact/reactive compact 规则

#### 10.1 microcompact 规则完善
- **修改文件**: `compact/micro.go`
- **内容**:
  - 重复读折叠 (同一文件多次 Read)
  - 重复搜索折叠 (同一 Grep/Glob 多次)
  - 空结果折叠
  - apiMicrocompact 路径

#### 10.2 autocompact 完善
- **修改文件**: `compact/auto.go`
- **新文件**: `compact/prompt.go` — 压缩 prompt 模板
- **新文件**: `compact/grouping.go` — 消息分组逻辑
- **新文件**: `compact/cleanup.go` — 后压缩清理
- **内容**:
  - 完整的 LLM 摘要 prompt (从 TS compact/prompt.ts 翻译)
  - 消息分组 (tool pairs, thinking blocks)
  - 后压缩清理 (orphaned tool results, empty messages)

#### 10.3 reactive compact 完善
- **修改文件**: `compact/reactive.go`
- **内容**: 完善紧急压缩逻辑, 与 autocompact 复用

#### 10.4 测试
- `compact/micro_test.go` — 各折叠规则测试
- `compact/auto_test.go` — 摘要和清理测试

**估计行数**: ~800 新增, ~300 修改  
**估计工时**: 3-4 天

---

### Phase 11: Provider 层完善 (中优先级)

**目标**: 完善 Anthropic API 对接

#### 11.1 Streaming API
- **修改文件**: `provider/anthropic.go`
- **内容**:
  - 完整的 SSE streaming 解析
  - content_block_start/delta/stop 处理
  - thinking block 处理
  - tool_use block 流式处理
  - 错误类型映射 (overloaded, rate_limit, prompt_too_long)

#### 11.2 重试逻辑
- **新文件**: `provider/retry.go`
- **内容**:
  - 指数退避重试
  - 速率限制处理 (429 → Retry-After)
  - FallbackTriggeredError 触发
  - 过载检测

#### 11.3 测试
- `provider/anthropic_test.go`
- `provider/retry_test.go`

**估计行数**: ~600 新增, ~200 修改  
**估计工时**: 3-4 天

---

### Phase 12: 消息规范化补全 (低优先级)

**目标**: 补全消息类型处理

#### 12.1 消息类型补全
- **修改文件**: `prompt/message_normalize.go`
- **内容**:
  - tombstone 消息处理
  - snip_boundary 处理
  - tool_use_summary 处理
  - stream_event 过滤
  - progress 消息处理

#### 12.2 测试
- `prompt/message_normalize_test.go`

**估计行数**: ~200 新增, ~50 修改  
**估计工时**: 1 天

---

### Phase 13: 集成服务 (低优先级)

**目标**: 实现辅助服务

#### 13.1 Token 估算
- **新文件**: `util/token_estimation.go`
- **内容**: 精确的 token 计数 (替代当前的 4chars/token 粗估)

#### 13.2 会话持久化
- **新文件**: `state/session.go`
- **内容**: 会话保存/恢复, transcript 持久化

#### 13.3 Prompt Suggestion
- **新文件**: `services/prompt_suggestion.go`
- **内容**: 建议后续提示

#### 13.4 Memory Extraction
- **新文件**: `services/memory_extraction.go`
- **内容**: 从对话中提取记忆

**估计行数**: ~1000 新增  
**估计工时**: 4-5 天

---

## 五、Phase 依赖图

```
Phase 6 (工具管线) ──────────────┐
                                  │
Phase 7 (processUserInput) ──────┤
                                  ├── Phase 9 (Prompt缓存)
Phase 8 (停止钩子) ──────────────┤         │
                                  │         ▼
                                  ├── Phase 11 (Provider层)
                                  │         │
Phase 10 (压缩深化) ─────────────┘         ▼
                                      Phase 12 (消息规范化)
                                            │
                                            ▼
                                      Phase 13 (集成服务)
```

**关键路径**: Phase 6 → Phase 11 → Phase 13  
**可并行**: Phase 7, Phase 8, Phase 9, Phase 10 互不依赖

---

## 六、实施优先级排序

| 优先级 | Phase | 估计工时 | 核心交付物 |
|--------|-------|---------|-----------|
| P0 | Phase 6: 工具管线 | 3-4天 | toolToAPISchema, buildToolDefinitions, 权限集成 |
| P0 | Phase 7: processUserInput | 2-3天 | 本地命令, 附件处理 |
| P1 | Phase 8: 停止钩子 | 2-3天 | shell hooks, 后台任务 |
| P1 | Phase 10: 压缩深化 | 3-4天 | micro/auto/reactive compact 规则 |
| P1 | Phase 11: Provider层 | 3-4天 | streaming API, 重试逻辑 |
| P2 | Phase 9: Prompt缓存 | 1-2天 | CacheScope, SystemPromptBlock |
| P2 | Phase 12: 消息规范化 | 1天 | tombstone, snip_boundary 等 |
| P3 | Phase 13: 集成服务 | 4-5天 | token估算, 会话持久化, 记忆提取 |

**总估计工时**: 20-28 天 (单人)

---

## 七、验证策略

### 7.1 单元测试
- 每个新文件必须有对应的 `_test.go`
- 测试覆盖率目标: ≥ 90%
- 使用 mock QueryDeps 进行隔离测试

### 7.2 集成测试
- `integration_test.go` 扩展: 端到端 submitMessage 测试
- Mock provider 返回预设响应, 验证完整循环
- 压缩管线集成测试 (snip → micro → auto → reactive chain)

### 7.3 对照验证
- 每个 Phase 完成后, 对照 TS 源码逐行验证核心逻辑
- 特别关注: 状态转换边界条件, 错误恢复路径, 并发安全性

### 7.4 回归测试
- 所有现有 40+ 测试必须持续通过
- `go test ./agents/...` 作为 CI gate

---

## 八、设计决策记录

| 决策点 | Go 方案 | TS 原方案 | 理由 |
|--------|---------|----------|------|
| Async Generator | `chan StreamEvent` | `AsyncGenerator<yield>` | Go channel 是自然映射 |
| AbortController | `context.WithCancel` | `AbortController` | Go context 是惯用方式 |
| Memoize | `sync.Once` | `lodash memoize` | 无 lodash 依赖 |
| DI | `QueryDeps` interface | `QueryDeps` object | Go interface 更惯用 |
| Feature flags | `QueryGates` struct + env | GrowthBook | 避免外部依赖 |
| Schema cache | `sync.Map` | `WeakMap` | Go 无 WeakMap |
| Concurrent tools | goroutine + semaphore | `all()` + maxConcurrency | Go 原生并发 |
| Tool hooks | interface + registry | function callbacks | Go interface 更可测试 |
| Prompt sections | `SectionCache` (sync.Map) | memoize + closure state | Thread-safe |
| Import cycle | injected `BuildSystemPromptFn` | direct import | Go 包循环限制 |
