# Tools Full Alignment — Task-Level Breakdown

把 `tools-full-alignment-01677f.md` 的 4 个 Sprint 拆成 46 个可独立交付的 Task，每个标注目标文件、验收标准、规模（S ≤ 50 行 / M 50~200 行 / L > 200 行）和依赖。

---

## Sprint 1 — 修正 Task* 语义（12 tasks, 2 天）

### 1.1 `tool/bg/` 包（后台 shell 作业）

| ID | Task | 目标文件 | 规模 | 依赖 | 验收 |
|---|---|---|---|---|---|
| **S1-T01** | 搬迁 Manager 到新包 | `tool/bg/manager.go`（从 `tool/task/manager.go` 拷贝 + 改 package） | M | — | `go build ./agents/tool/bg/...` 通过 |
| **S1-T02** | 实现 `TaskOutputTool`（只读） | `tool/bg/output_tool.go` | M | T01 | `bash_id` → 返回 `{output, status, exit_code, truncated}`；有单测覆盖"作业完成""作业运行中""未找到" |
| **S1-T03** | 实现 `TaskStopTool`（可停 bg + 任务图） | `tool/bg/stop_tool.go` | M | T01 | 接受任意 `id` 前缀，委托给 `Manager` 或 `TaskGraph`；单测 mock 两路 |
| **S1-T04** | `bg` 包测试 | `tool/bg/bg_test.go` | M | T01-T03 | 覆盖 Start/Output/Stop 完整生命周期 + 并发 |

### 1.2 Bash / PowerShell 扩展 `run_in_background`

| ID | Task | 目标文件 | 规模 | 依赖 | 验收 |
|---|---|---|---|---|---|
| **S1-T05** | Bash 新增 `RunInBackground` 路径 | `tool/bash/bash.go` `Call()` 分支 | S | T01 | `run_in_background:true` 立即返回 `{bash_id}`，`false` 走原同步路径 |
| **S1-T06** | PowerShell 对齐相同分支 | `tool/powershell/powershell.go` | S | T01 | 同上 |
| **S1-T07** | Bash/PowerShell 单测补 | `tool/bash/bash_test.go`、`tool/powershell/powershell_test.go` | S | T05-T06 | 后台分支返回非空 `bash_id`；`bg.Manager` 能查到该任务 |

### 1.3 `tool/taskgraph/` 包（TodoV2 任务图）

| ID | Task | 目标文件 | 规模 | 依赖 | 验收 |
|---|---|---|---|---|---|
| **S1-T08** | Store 数据结构 + 并发安全 | `tool/taskgraph/store.go` | M | — | `{id,title,status,parent_id,deps,payload}`；Add/Get/Update/List/Remove；`-race` 通过 |
| **S1-T09** | 5 个工具实现 | `tool/taskgraph/tools.go` | L | T08 | `TaskCreate/Get/Update/List/Stop` 全部实现 tool.Tool |
| **S1-T10** | `taskgraph` 单测 | `tool/taskgraph/store_test.go`、`tools_test.go` | M | T08-T09 | 状态转移、拓扑查询、依赖 cycle 检测 |

### 1.4 兼容与集成

| ID | Task | 目标文件 | 规模 | 依赖 | 验收 |
|---|---|---|---|---|---|
| **S1-T11** | `ToolUseContext` 字段调整 | `agents/tool_use_context.go` | S | T01, T08 | `TaskManager interface{}` 注释指向 `*bg.Manager`；新增 `TaskGraph interface{}` |
| **S1-T12** | 旧 `tool/task/` 转发 | `tool/task/*.go` | S | T01-T10 | 原 API 保留并标 `// Deprecated`，内部调用新包；`tools_integration_ported_test.go` 原断言继续过 |

---

## Sprint 2 — 补齐缺失工具（14 tasks, 3 天）

### 2.1 `tool/skill/`

| ID | Task | 目标文件 | 规模 | 依赖 | 验收 |
|---|---|---|---|---|---|
| **S2-T01** | `SkillProvider` 接口 | `agents/tool_use_context.go` | S | — | 新字段 `SkillProvider func(name string) (string, error)` |
| **S2-T02** | `SkillTool` 实现 | `tool/skill/skill.go` | M | T01 | Input `{skill_name}` → Output 技能 markdown；未找到返回错误 |
| **S2-T03** | `skill` 单测 | `tool/skill/skill_test.go` | S | T02 | mock provider；命中/未命中两条路径 |

### 2.2 `EnterPlanMode`

| ID | Task | 目标文件 | 规模 | 依赖 | 验收 |
|---|---|---|---|---|---|
| **S2-T04** | `EnterPlanModeTool` 实现 | `tool/planmode/enter.go` | S | — | 与 `Exit` 对称；设 `toolCtx.PlanMode=ModePlan`；emit `EventPlanModeChange`；只允许从 normal→plan |
| **S2-T05** | `Enter` 单测 | `tool/planmode/enter_test.go` | S | T04 | 成功切换 + 重复进入报错 |

### 2.3 `tool/brief/`

| ID | Task | 目标文件 | 规模 | 依赖 | 验收 |
|---|---|---|---|---|---|
| **S2-T06** | `BriefTool` 实现 | `tool/brief/brief.go` | M | — | Input `{summary, attachments}`；发送 `EventAttachment`，内容为 brief payload |
| **S2-T07** | `brief` 单测 | `tool/brief/brief_test.go` | S | T06 | 断言 `EmitEvent` 被触发且数据形状正确 |

### 2.4 `tool/mcp/` 资源工具 + mock

| ID | Task | 目标文件 | 规模 | 依赖 | 验收 |
|---|---|---|---|---|---|
| **S2-T08** | `MCPResourceProvider` 接口定义 | `agents/types.go` | S | — | 方法：`ListResources(server string) ([]Resource, error)`、`ReadResource(server, uri string) (string, error)` |
| **S2-T09** | `ToolUseContext.MCPResources` 字段 | `agents/tool_use_context.go` | S | T08 | 新增字段 |
| **S2-T10** | `ListMcpResourcesTool` 实现 | `tool/mcp/list.go` | M | T08-T09 | 遍历 `toolCtx.Options.McpClients`；汇总结果 |
| **S2-T11** | `ReadMcpResourceTool` 实现 | `tool/mcp/read.go` | M | T08-T09 | 委托 `MCPResources.ReadResource` |
| **S2-T12** | Mock MCP 服务器 | `tool/mcp/mock.go` | M | T08 | `NewMockProvider(resources map[string]string) MCPResourceProvider`，用于测试注入 |
| **S2-T13** | `mcp` 单测 | `tool/mcp/mcp_test.go` | M | T10-T12 | List 覆盖 0/1/多服务器；Read 覆盖命中/未命中 |

### 2.5 Sprint 2 文档更新

| ID | Task | 目标文件 | 规模 | 依赖 | 验收 |
|---|---|---|---|---|---|
| **S2-T14** | `agents/tool/README.md` 补新工具条目 | `agents/tool/README.md` | S | T02/T04/T06/T10-T11 | 表格包含 `Skill/EnterPlanMode/Brief/MCP*/TaskOutput/TaskGraph` 条目 |

---

## Sprint 3 — 注册策略 + 过滤链对齐（8 tasks, 1 天）

| ID | Task | 目标文件 | 规模 | 依赖 | 验收 |
|---|---|---|---|---|---|
| **S3-T01** | `Options` 扩展 | `tool/builtin/builtin.go` | S | S1, S2 | 新增 `EnableTodoV2`、`EnableSkillTool`、`EnableMCPResourceTools`、`DisabledTools []string` |
| **S3-T02** | `AllTools` 重写对齐 `getAllBaseTools` | `tool/builtin/builtin.go` | M | T01 | 工具顺序/条件分支镜像 TS 源；`AllTools()` 的名字集合 ⊇ 核心条目 100% |
| **S3-T03** | `FilterForModel` 三重过滤 | `tool/builtin/filter.go` | M | T02 | 1) denyRules 2) isEnabled 3) 排除 `ListMcpResources/ReadMcpResource/SyntheticOutput` |
| **S3-T04** | `RegisterAll` 用新过滤链 | `tool/builtin/builtin.go` | S | T03 | 调用顺序：AllTools → FilterForModel → Registry.Register |
| **S3-T05** | 保持 `Tools()` 向后兼容 | `tool/builtin/builtin.go` | S | T02 | 老 API 仍返回 2 个工具集，现有 `builtin_test.go` 不改 |
| **S3-T06** | `builtin` 单测 | `tool/builtin/builtin_test.go` 追加 | M | T02-T04 | 新测："核心条目 100% 覆盖"、"deny rule 过滤生效"、"特殊工具排除" |
| **S3-T07** | `AllTools` 覆盖率断言测试 | `tool/builtin/coverage_test.go` | S | T02 | 硬编码 claude-code 核心清单字符串数组，断言全部存在 |
| **S3-T08** | `builtin/README.md` 更新 | `tool/builtin/README.md`（新建） | S | T01-T04 | claude-code ↔ Go 工具一一对应表；双通道 Task 模型说明 |

---

## Sprint 4 — 真实 LLM 端到端场景 + 差距报告（12 tasks, 2 天）

### 4.1 E2E 测试框架

| ID | Task | 目标文件 | 规模 | 依赖 | 验收 |
|---|---|---|---|---|---|
| **S4-T01** | 场景测试骨架 | `agents/tools_e2e_scenarios_test.go` | M | S3 完成 | INTEGRATION=1 门控；复用 `realDeps`+`toolsDeps`；`INTEGRATION_SCENARIOS` env 可选跑 |
| **S4-T02** | 断言辅助工具 | 同上文件内 helper | S | T01 | `assertToolCalled(log, name, minCount)`、`assertEventEmitted(events, type)` |

### 4.2 6 个场景（各一个 Task）

| ID | Task | 规模 | 触发工具 | 硬断言 | 软断言 |
|---|---|---|---|---|---|
| **S4-T03** | 场景 1：文件工程 | M | Read/Edit/Write/Glob/Grep/NotebookEdit | 各工具插件路径可达 | LLM 调用 ≥3 个不同文件工具 |
| **S4-T04** | 场景 2：Shell + 后台 | M | Bash/PowerShell/TaskOutput/TaskStop | `run_in_background` 分支生效；bg Manager 产出非空 id | LLM 调 TaskOutput ≥1 次 |
| **S4-T05** | 场景 3：子代理 + 任务图 | M | AgentTool/TaskCreate/Get/Update/List | Store 最终包含 ≥3 节点且至少 1 个 completed | AgentTool 被调 ≥1 |
| **S4-T06** | 场景 4：计划模式 + 交互 | M | EnterPlanMode/AskUser/ExitPlanMode/TodoWrite | `plan_mode_change` 事件 ≥2；`ask_user` 事件 ≥1 | TodoWrite 被调 |
| **S4-T07** | 场景 5：网络 + 技能 + Brief | M | WebFetch/WebSearch/Skill/Brief | Skill provider 被调；Brief 发出 attachment 事件 | WebFetch 或 WebSearch 被调 |
| **S4-T08** | 场景 6：MCP 资源 | M | ListMcpResources/ReadMcpResource | Mock provider 收到 List + Read 各 ≥1 | 返回内容在最终回答中出现 |

### 4.3 差距报告 + 收尾文档

| ID | Task | 目标文件 | 规模 | 依赖 | 验收 |
|---|---|---|---|---|---|
| **S4-T09** | `GAP_ANALYSIS.md` | `agents/tool/GAP_ANALYSIS.md` | M | S3 | 完整清单 + 已移植/故意跳过/待定 三分类 + 每条 skip 理由 |
| **S4-T10** | `QUERYENGINE_MIGRATION_PLAN.md §2.6.1` 更新 | 同名文件 | S | S3 | 新增 Sprint 1~3 产出的全部工具到映射表 |
| **S4-T11** | `agents/README.md` 包表更新 | `agents/README.md` | S | S3 | 条目更新含 `bg/taskgraph/skill/brief/mcp` |
| **S4-T12** | 集成测试整体跑通 | — | — | T01-T11 | `go build ./...` → `go test ./agents/...` → `INTEGRATION=1 go test ./agents/ -run E2E_Scenarios` 三者全绿 |

---

## 总览

| Sprint | Task 数 | 规模估算 | 新增文件 | 变更文件 |
|---|---|---|---|---|
| S1 | 12 | ~900 行 | 7 | 4 |
| S2 | 14 | ~1100 行 | 10 | 3 |
| S3 | 8 | ~400 行 | 3 | 2 |
| S4 | 12 | ~800 行 | 2 | 3 |
| **合计** | **46** | **~3200 行** | **22** | **12** |

---

## 依赖拓扑（关键路径）

```
S1-T01 ─┬─ T02 ─┐
        ├─ T03 ─┤
        ├─ T04 ─┤
        ├─ T05 ─┤
        ├─ T06 ─┤
        └─ T11 ─┤
S1-T08 ─┬─ T09 ─┤
        └─ T10 ─┤
                └→ T12 ──────→ [S1 done]
S2 全部并行（以 S1 done 为前置，各子任务内部有小依赖）────→ [S2 done]
S3 全部串行（T01→T02→T03→T04→T06→T07→T08），T05 与 T03 并行 ────→ [S3 done]
S4 T01→T02 先行；T03~T08 可并行；T09~T11 并行；T12 终点
```

---

## 推进节奏建议

- **第 1 个开发日**：S1-T01~T07（bg 包 + bash/ps 后台分支）
- **第 2 个开发日**：S1-T08~T12（taskgraph + 兼容转发）
- **第 3 个开发日**：S2-T01~T07（skill + planmode enter + brief）
- **第 4 个开发日**：S2-T08~T14（mcp 全部 + 文档）
- **第 5 个开发日**：S3 全部 + S4-T01~T02（测试骨架）
- **第 6 个开发日**：S4-T03~T08（6 场景）
- **第 7 个开发日**：S4-T09~T12（差距报告 + 文档 + 联调）

---

## 验收口径（完整执行完毕后）

1. `go build ./...` ✅
2. `go test ./agents/... -count=1` ✅（全绿，含 `-race` 选项）
3. `INTEGRATION=1 go test ./agents/ -run E2E_Scenarios -timeout 20m` ✅（6 场景软断言至少 4/6 通过，硬断言全部通过）
4. `agents/tool/GAP_ANALYSIS.md` 明确列出每个未移植 claude-code 工具的理由
5. `builtin.AllTools()` 名字集合 ⊇ claude-code `getAllBaseTools()` 非 feature-flag 核心条目（通过 `coverage_test.go` 自动断言）
