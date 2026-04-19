# backend/agents 技术架构深度分析

深度分析 `backend/agents` 包全景架构、运行时数据流、扩展点与并发模型的综合文档（中文 + ASCII 图）。

---

## 一、全景包结构

```
backend/agents/
├── [core]           engine.go · queryloop.go · types.go · deps.go · config.go
│                    coordinator.go · token_budget.go
├── [subagent]       subagent.go · subagent_context.go · localagent_task.go
│                    sidechain.go · fork.go · fork_subagent.go · fork_analytics.go
├── [agent-def]      agent_definition.go · agent_loader.go · agent_builtin.go
│                    agent_model.go · agent_tool_constants.go
├── [agent-ext]      agent_mcp.go · agent_hooks.go · agent_memory.go
│                    agent_memory_snapshot.go
├── [hooks]          stop_hooks.go · shell_hooks.go · agent_hooks.go
├── [infra]          tool_use_context.go · attachments.go · background_tasks.go
│                    commands.go · commands_agents_fork.go · memory_extraction.go
│                    prompt_suggestion.go · skills_resolve.go
├── tool/            [工具子系统，见第三节]
├── compact/         [压缩子系统，见第四节]
├── provider/        [LLM 适配层，见第五节]
├── permission/      [权限子系统，见第六节]
├── prompt/          [提示词构建，见第七节]
├── state/           [线程安全状态存储]
├── util/            [UUID · 上下文 · 消息构建工具]
└── worktree/        [Git worktree 隔离沙盒]
```

---

## 二、核心分层架构

```
┌─────────────────────────────────────────────────────────────┐
│                     调用方 (Host)                            │
│   CLI / WebSocket / SDK / 测试                               │
└───────────────────────────┬─────────────────────────────────┘
                            │ SubmitMessage(ctx, prompt)
                            ▼
┌─────────────────────────────────────────────────────────────┐
│                  QueryEngine  (engine.go)                    │
│  · 会话级编排器（1 实例 = 1 对话）                            │
│  · 管理 messages[]、sessionID、totalUsage、cancelFunc         │
│  · 构造 ToolUseContext，启动 QueryLoop goroutine              │
│  · 返回 <-chan StreamEvent（替代 TS AsyncGenerator）          │
└───────────────────────────┬─────────────────────────────────┘
                            │
                            ▼
┌─────────────────────────────────────────────────────────────┐
│                  QueryLoop  (queryloop.go)                   │
│  for { ... }  状态机 — 映射自 TS query.ts (~1730行)           │
│                                                              │
│  ① SnipCompact → Microcompact → AutoCompact                  │
│  ② ContextCollapse → ToolResultBudget → BuildToolDefs        │
│  ③ CallModel → <-chan StreamEvent (流式解码)                  │
│  ④ ExecuteTools (并发/串行 分组执行)                          │
│  ⑤ StopHookRegistry 决策 → continue / stop                  │
│                                                              │
│  出口路径 (12+):  completed · max_turns · model_error         │
│    prompt_too_long · budget_exhausted · hook_stopped         │
│    max_output_tokens_recovery · reactive_compact_retry …    │
└───────────────────────────┬─────────────────────────────────┘
                            │ QueryDeps 接口注入
          ┌─────────────────┼────────────────────┐
          ▼                 ▼                    ▼
  ┌──────────────┐  ┌──────────────┐   ┌──────────────────┐
  │  provider/   │  │  compact/    │   │  tool/           │
  │  LLM 适配层  │  │  压缩流水线  │   │  工具子系统       │
  └──────────────┘  └──────────────┘   └──────────────────┘
```

---

## 三、工具子系统 (tool/)

```
tool/
├── tool.go           Tool 接口 (Name/Call/CheckPermissions/IsConcurrencySafe/…)
├── registry.go       Registry  —  map[name]Tool + Register/Lookup
├── build.go          BuildTool 工厂 (struct → Tool 装配)
├── assembly.go       AssembleToolPool / FilterByDenyRules
├── orchestration.go  Orchestrator.RunTools()
│                     → partitionToolCalls(并发安全组 vs 串行组)
│                     → errgroup.Group 并发执行
├── streaming_executor.go  StreamingToolExecutor (流式工具执行)
├── hooks.go          工具前/后 Hook (PreToolUse / PostToolUse)
├── api_schema.go     ToolDefinition → API JSON Schema 转换
├── resolve.go        工具名解析 (alias 展开)
├── mcp/              MCP 动态工具注入
├── builtin/          内置工具聚合 (Register / RegisterAll)
│   ├── fileio/       Read · Edit · Write (绝对路径+ReadFileState 门控)
│   ├── bash/         Bash (安全 deny 规则 + ask 回退)
│   ├── powershell/   PowerShell (Windows 对等)
│   ├── shell/        跨平台 executor (超时·截断·tree-kill)
│   ├── glob/         Glob 文件查找
│   ├── grep/         Grep (ripgrep 优先 + Go regex 回退)
│   ├── notebook/     NotebookEdit
│   ├── webfetch/     WebFetch (SSRF 防护)
│   ├── websearch/    WebSearch (Brave/DuckDuckGo)
│   ├── todo/         TodoWrite
│   ├── askuser/      AskUserQuestion
│   ├── planmode/     ExitPlanMode
│   ├── task/         Task (子代理调度)
│   ├── agenttool/    SpawnSubAgent 调用路径
│   ├── bg/           Background job 工具
│   ├── brief/        Brief 状态播报工具
│   └── taskgraph/    TaskGraph 工具
└── tool.go           ToolResult · JSONSchema · PermissionResult

并发执行模型:
  ┌──────────────────────────────────────────────────────┐
  │ tool calls batch                                      │
  │   partitionToolCalls()                                │
  │     ├── ConcurrencySafe=true  → Group A (parallel)   │
  │     │     errgroup → goroutine per call               │
  │     └── ConcurrencySafe=false → Group B (serial)     │
  │           顺序单独执行，等待前一个完成                  │
  └──────────────────────────────────────────────────────┘
```

---

## 四、压缩流水线 (compact/)

```
QueryLoop 每轮迭代压缩顺序:

  raw messages
      │
      ▼
  SnipCompact         snip.go           — 截断过早的历史段 (HISTORY_SNIP gate)
      │
      ▼
  Microcompact        micro.go          — 本地折叠重复 Read/Search (无 LLM)
  + api_micro.go      API 调用的 micro  — 精简 API 消息体积
      │
      ▼
  ContextCollapse     context_collapse.go — 增量折叠 (staged, 非溢出时)
      │
      ▼
  AutoCompact         auto.go           — LLM 驱动的全局摘要 (token 阈值触发)
      │ (emergency)
      ▼
  ReactiveCompact     reactive.go       — prompt_too_long / image_error 紧急压缩
      │
      ▼
  ToolResultBudget    tool_result_budget.go — 截断过大 tool_result (磁盘持久化)
      │
      ▼
  SessionMemory       session_memory.go — 会话记忆摘要 (fork_summarizer.go 生成)
```

---

## 五、LLM 适配层 (provider/)

```
provider/
├── provider.go       Provider 接口: CallModel(ctx, params) (<-chan StreamEvent, error)
├── anthropic.go      AnthropicProvider — 官方 SDK 流式解码 (SSE)
│                     + 自动 fallback 模型切换
├── openai_compat.go  OpenAICompatProvider — OpenAI / Ollama / vLLM 兼容
├── factory.go        NewProvider(config) 工厂
└── retry.go          RetryWithBackoff — 指数退避重试 (RateLimit / ServerError)

CallModel 参数结构:
  CallModelParams {
    Messages, SystemPrompt, ThinkingConfig,
    Tools []ToolDefinition, Model, FallbackModel,
    MaxTokens, Temperature, StopSequences,
    AnthropicBeta, ...
  }
```

---

## 六、权限子系统 (permission/)

```
permission/
├── types.go      Mode (default|plan|acceptEdits|bypassPermissions|auto)
│                 Rule { Tool, Pattern }
│                 Result { Behavior: allow|deny|ask }
├── rule_parser.go  Rule 解析 (glob 语法)
├── rule_tiers.go   三级规则链:
│                     AlwaysDeny  → 直接拒绝
│                     AlwaysAllow → 直接放行
│                     AlwaysAsk   → 弹出确认
└── checker.go      Checker.Check(toolName, input, toolCtx) → Result
                    + Mode 模式快捷路径 (bypassPermissions → 全放行)

调用位置:
  Orchestrator.RunTools()
      └── canUseTool(call) → permission.Checker.Check()
              ├── allow → 执行
              ├── deny  → 返回 tool_error result
              └── ask   → ToolUseContext.AskUser() → 等待 host 回调
```

---

## 七、Prompt 构建层 (prompt/)

```
prompt/
├── prompts.go        6层 System Prompt Builder:
│                       Layer1: 核心身份 + 操作规则
│                       Layer2: 工具列表 (AssembleToolPool 生成)
│                       Layer3: 环境信息 (env_info.go: OS/shell/cwd/git)
│                       Layer4: CLAUDE.md 用户上下文
│                       Layer5: 代理特定扩展 (AgentDefinition.SystemPrompt)
│                       Layer6: 附件 (agent memory / file changes / date)
├── system.go         BuildSystemPrompt 入口
├── effective.go      EffectiveSystemPromptConfig (base + append 合并)
├── context.go        用户上下文 (CLAUDE.md) 加载
├── env_info.go       环境信息采集 (OS/平台/git状态/工作目录)
├── cache.go          prompt cache 标记 (Anthropic cache_control)
├── message_normalize.go  消息标准化 (thinking block 处理)
├── query_context.go  QueryContext 传递
├── sections.go       Prompt 段落构建工具
└── session_guidance.go   会话引导规则注入
```

---

## 八、子代理与 Coordinator 架构

```
主代理 (QueryEngine)
    │
    │  Task 工具触发 SpawnSubAgent / SpawnAsyncSubAgent
    │
    ├─── 同步子代理 (subagent.go)
    │     SpawnSubAgent():
    │       1. 解析 AgentDefinition (type → def)
    │       2. 校验 ctx subagentDepthKey (≤ DefaultMaxSubagentDepth=3)
    │       3. InitializeAgentMcpServers (agent_mcp.go)
    │       4. RegisterFrontmatterHooks (agent_hooks.go)
    │       5. LoadAgentMemory → 注入 system prompt (agent_memory.go)
    │       6. 构造子 QueryEngine (共享 deps, 独立 messages)
    │       7. 排空 SubmitMessage 流，返回最后 assistant text
    │
    └─── 异步子代理 (localagent_task.go)
          SpawnAsyncSubAgent():
            1. 创建 LocalAgentTask { pending → running }
            2. goroutine 执行 SpawnSubAgent
            3. 完成后 → EncodeTaskNotification (XML)
            4. 注入 coordinator 的 messages (user-role)
            5. 状态更新 → completed | failed | killed

Coordinator 模式 (coordinator.go):
    CoordinatorConfig → BuildCoordinatorEngineConfig
        ├── 专属 system prompt
        ├── 工具白名单: Task / TaskStop / SendMessage / SyntheticOutput
        └── ForceAllAsync=true → 所有 Task 强制异步

子代理嵌套深度跟踪:
    ctx → subagentDepthKey → int (每层 +1, 超限 → ErrSubagentDepthExceeded)

Sidechain 持久化 (sidechain.go):
    .claude/subagents/<agentId>.jsonl     ← 逐条 JSONL 追加
    .claude/subagents/<agentId>.meta.json ← AgentMetadata
    ResumeAgent() → 过滤孤儿 tool_use / 空白 assistant → 重建 messages
```

---

## 九、MCP 扩展机制 (agent_mcp.go)

```
AgentDefinition.McpServers []AgentMcpServerSpec
    │
    ├── ByName: "server-name"     → mcpSharedCache 查找/复用已有连接
    │                               (跨子代理共享, 父引擎管理生命周期)
    └── Inline: { name: {config}} → 每次新建连接
                                    (子代理退出时 Cleanup)

MCPConnector 接口 (host 提供):
    Connect(ctx, agentID, spec) → AgentMCPClient

AgentMCPClient 接口:
    Name() / Status() / Tools() []MCPTool / Cleanup(ctx)

AgentMCPBundle → 合并到子代理 ToolUseOptions.MCPTools
```

---

## 十、Hooks 系统

```
两类 Hook 并行存在:

A. StopHookRegistry (stop_hooks.go)  — queryloop 出口决策
   ├── MaxTurnsHook        : 达到 MaxTurns → stop
   ├── ApiErrorSkipHook    : 跳过可恢复 API 错误
   └── 自定义 StopHook     : 实现 StopHook 接口即可注册

B. ShellHookRegistry (shell_hooks.go) — 工具前后生命周期
   HookEvent 枚举:
     PreToolUse · PostToolUse
     SubagentStart · SubagentStop · SubagentStopFailure
     Stop · StopFailure
   每个 HookCommand 以 shell 命令执行, 可注入 AdditionalContext

C. AgentFrontmatterHooks (agent_hooks.go) — 代理作用域隔离
   RegisterFrontmatterHooks(scopeID, hooks, isAgent):
     isAgent=true → Stop 重写为 SubagentStop (隔离父引擎)
   ClearSessionHooks(scopeID) → 清理单个代理的所有 hook
```

---

## 十一、持久化记忆 (agent_memory.go)

```
AgentMemoryScope:
  user    → $XDG_CONFIG_HOME/ubuilding/agent-memory/<agentType>/
  project → <cwd>/.claude/agent-memory/<agentType>/
  local   → $UBUILDING_REMOTE_MEMORY_DIR/.../agent-memory-local/<agentType>/

读取: LoadAgentMemory() → 读 MEMORY.md → 注入 system prompt
写入: WriteAgentMemory() → tmp-file + rename (原子写)
快照: agent_memory_snapshot.go → 定期 snapshot, MemoryExtractionJob 提取
```

---

## 十二、运行时数据流（一次完整 query）

```
用户输入 "实现 foo 功能"
    │
    ▼ QueryEngine.SubmitMessage(ctx, prompt)
    │   · append user Message → messages[]
    │   · 构造 ToolUseContext (权限/工具池/AskUser回调/状态)
    │   · go QueryLoop(ctx, params, deps, ch)
    │
    ▼ QueryLoop 第1轮
    │   ① SnipCompact(messages)            无变化
    │   ② Microcompact(messages, toolCtx)  折叠重复读
    │   ③ ContextCollapse()                未达阈值, skip
    │   ④ BuildToolDefs(toolCtx) → []ToolDefinition
    │   ⑤ CallModel(params) → evCh
    │   │
    │   ▼ 流式解码 evCh:
    │     EventTextDelta → ch ← text chunk
    │     EventToolUse   → 收集 tool_use block
    │     EventAssistant → 消息完成, stop_reason="tool_use"
    │
    ▼ ExecuteTools([Read("foo.go"), Bash("go test")])
    │   partitionToolCalls:
    │     Read → ConcurrencySafe=true  ─┐
    │     Bash → ConcurrencySafe=false ─┤ 分组
    │   组1(并发): [Read] → errgroup → goroutine
    │     permission.Check("Read","foo.go") → allow
    │     fileio.Read.Call() → ToolResult
    │   组2(串行): [Bash]
    │     permission.Check("Bash","go test") → ask
    │     AskUser() → host 回调 → 用户确认
    │     bash.Call() → ToolResult
    │   → messages += tool_result[]
    │
    ▼ StopHookRegistry.Eval() → continue (有 tool_use)
    │
    ▼ QueryLoop 第2轮 (携带 tool_result)
    │   CallModel → EventTextDelta "测试通过，代码已实现"
    │   stop_reason = "end_turn"
    │
    ▼ StopHookRegistry.Eval() → completed
    │   Shell PostToolUse hooks 执行
    │
    ▼ ch ← EventResult { terminal: "completed" }
    ▼ ch ← EventDone
    close(ch)
    │
    ▼ 调用方消费完毕
```

---

## 十三、并发模型总结

| 机制 | Go 原语 | 场景 |
|------|---------|------|
| 流式输出 | `chan StreamEvent` + goroutine | QueryLoop → Host |
| 工具并发执行 | `errgroup.Group` | ConcurrencySafe 工具组 |
| 异步子代理 | goroutine + `LocalAgentTask` | Coordinator 模式 |
| 上下文取消 | `context.WithCancel` | 替代 TS AbortController |
| 状态读写 | `sync.RWMutex` | QueryEngine 字段保护 |
| MCP 连接缓存 | `sync.Mutex` + map | mcpSharedCache |
| Hook 作用域 | `sync.Mutex` + map | scopedHookTracker |
| 内存预取 | goroutine + `<-chan []Message` | StartMemoryPrefetch |
| Sidechain 写入 | `sync.Mutex` + append-only JSONL | sub-agent 持久化 |

---

## 十四、关键接口与扩展点速查

| 扩展点 | 接口/函数 | 注册方式 |
|--------|---------|---------|
| 新增 LLM Provider | `provider.Provider` | `NewProvider()` 工厂或直接注入 `QueryDeps.CallModelFn` |
| 新增工具 | `tool.Tool` | `registry.Register(tool)` |
| 工具权限规则 | `permission.Rule` + `AlwaysAllow/Deny/Ask` | `ToolPermissionContext` 字段 |
| 自定义 Stop Hook | `StopHook` 接口 | `stopHookRegistry.Register()` |
| Shell Hooks | `HookCommand` | `AgentDefinition.Hooks` 前置元数据 |
| 新 MCP Server | `MCPConnector` 接口 | `EngineConfig.MCPConnector` 注入 |
| 新代理定义 | `AgentDefinition` | `agent_builtin.go` 或 `*.md` 前置元数据文件 |
| 代理记忆 | `AgentMemoryScope` | `AgentDefinition.Memory` 字段 |
| 提示词注入 | `prompt.BuildSystemPrompt` 6层 | 实现对应 Layer 接口 |
| 沙盒隔离 | `AgentIsolation` = `worktree` | `AgentDefinition.Isolation` + `worktree/` |
