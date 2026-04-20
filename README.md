# uBuilding — Agent Chat Full Stack

全栈智能体对话应用：Next.js 前端 + Go (Gin/GORM/SQLite) 后端 + agents 模块集成。

---

## 项目结构

```
uBuilding/
├── backend/
│   ├── agents/          # 智能体核心引擎 (QueryEngine, QueryLoop 等)
│   └── app/             # Web 服务端
│       ├── main.go
│       ├── config/      # 环境变量配置
│       ├── database/    # GORM + SQLite 初始化
│       ├── models/      # Conversation, Message 数据模型
│       ├── bridge/      # agents 集成层 (SessionPool + simpleDeps)
│       ├── handlers/    # WebSocket 聊天 + REST 会话管理
│       ├── middleware/  # CORS
│       └── routes/      # 路由注册
└── frontend/            # Next.js 15 + TailwindCSS 对话框 UI
    ├── app/chat/        # 主对话页面
    ├── components/      # Sidebar, ChatDialog, MessageList, InputBar
    ├── hooks/           # useWebSocket, useConversations
    └── utils/           # REST API 封装
```

---

## 快速启动

### 1. 配置环境变量

```bash
cp .env.example .env
# 编辑 .env，填写 AGENT_ENGINE_API_KEY 等 LLM 配置
```

### 2. 启动后端

```bash
cd backend/app
$env:GOPROXY="https://goproxy.cn,direct"
go run main.go
# 服务监听 :8080
```

### 3. 启动前端

```bash
cd frontend
npm run dev
# 访问 http://localhost:3000
```

---

## API 接口

| 方式 | 路径 | 说明 |
|------|------|------|
| WS | `/ws` | WebSocket 聊天（流式推流） |
| GET | `/api/conversations` | 会话列表 |
| POST | `/api/conversations` | 新建会话 |
| GET | `/api/conversations/:id` | 获取会话+消息 |
| PATCH | `/api/conversations/:id/title` | 更新标题 |
| DELETE | `/api/conversations/:id` | 删除会话 |
| GET | `/health` | 健康检查 |

## WebSocket 消息协议

**Client → Server**
```json
{"type":"chat","conversation_id":"<uuid>","content":"用户消息"}
{"type":"new_conversation"}
```

**Server → Client**
```json
{"type":"token","content":"..."} 
{"type":"done","message_id":"<uuid>"}
{"type":"error","content":"..."}
{"type":"conversation_id","conversation_id":"<uuid>"}
```

---

## 技术栈

- **后端**: Go 1.22+, Gin v1.12, GORM v1.31, SQLite (modernc 纯Go), Gorilla WebSocket
- **前端**: Next.js 15, React 19, TailwindCSS v4, react-markdown, lucide-react
- **智能体**: backend/agents QueryEngine (支持 OpenAI/Anthropic/vLLM/Ollama)
