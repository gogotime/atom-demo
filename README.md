# Atoms-Lite

> 一个用 AI 帮你写前端小应用的工具。描述你想要什么，模型自己改 HTML/CSS/JS，预览实时刷新。

![architecture](https://img.shields.io/badge/stack-Go%201.x%20%2B%20React%2018-blue)

## 它能做什么

- 在网页里描述需求（中文/英文都行），AI 调用工具直接改代码
- 支持**整体替换**（`update_files`）和**精准 patch**（`patch_files`）两种工具路径
- 每次改动的中间过程通过 SSE 流式推送，UI 实时更新预览
- 改完后可一键 **Publish**，生成一个独立的公开链接分享给他人
- 完整的本地文件系统存档（每个 turn 一个 JSON），方便回溯和 debug

## 技术栈

| 层 | 技术 |
| --- | --- |
| 后端 | Go 1.x + chi 路由 + modernc.org/sqlite（纯 Go，无 CGO） |
| 前端 | React 18 + Vite + Tailwind + marked + DOMPurify |
| 存储 | SQLite（项目元数据 + 用户）+ 文件系统（代码 + turn 历史 + SSE 日志） |
| LLM | Anthropic Messages API |
| 鉴权 | Cookie + JWT（HMAC-SHA256） |

## 仓库结构

```
atoms-demo/
├── backend/                Go 单体后端
│   ├── main.go             路由 + 启动
│   ├── handlers.go         HTTP handler
│   ├── auth.go             注册/登录/JWT
│   ├── agent.go            agent 主循环 + 压缩 + 工具执行
│   ├── llm.go              LLM 客户端 + 系统 prompt + 工具定义
│   ├── db.go               SQLite schema
│   ├── stream*.go          SSE 流持久化
│   ├── code_storage.go     代码文件读写
│   └── dist/               前端构建产物（//go:embed 嵌入二进制）
├── frontend/               React + Vite
│   ├── src/
│   │   ├── pages/          Dashboard / Builder / Public
│   │   ├── components/     ChatPanel / Preview
│   │   └── api.js          fetch 封装
│   └── vite.config.js
├── deploy/
│   └── atoms-demo.service  systemd 单元（生产部署用）
├── scripts/
│   └── e2e.py              端到端黑盒测试
├── data/                   SQLite + 项目文件（运行时生成）
└── .env.example            环境变量模板
```

## 快速开始（本地开发）

### 0. 前置条件

- Go 1.22+
- Node.js 18+（带 npm）
- 一个 LLM API key（Anthropic / MiniMax 代理 / OpenAI 兼容端点都支持）

### 1. 克隆并装前端依赖

```bash
git clone <this-repo>
cd atoms-demo/frontend && npm install
```

### 2. 配 backend 环境变量

```bash
cd ../backend
cp ../.env.example .env
# 编辑 .env，至少填上 JWT_SECRET 和 LLM_API_KEY
```

最小 `.env`：

```bash
LISTEN_ADDR=:8080
JWT_SECRET=$(openssl rand -hex 32)         # 必须 >= 16 字符，生产里 32 字符以上
DB_PATH=./data/atoms.db
LLM_PROVIDER=claude                        # claude | openai | mock
LLM_API_KEY=sk-ant-...
LLM_BASE_URL=https://api.anthropic.com    # 或你的代理地址
LLM_MODEL=
```

### 3. 起前端 dev server（一个终端）

```bash
cd frontend
npm run dev
# → http://localhost:5173
```

Vite 会把 `/api/*` 自动代理到 `localhost:8080`。

### 4. 起后端（另一个终端）

```bash
cd backend
go run .
# → listening on :8080
```

后端在 dev 模式下如果找不到 `dist/` 会回退到一个占位 HTML 页面，提示你用前端 dev server。

### 5. 注册账号并开始

## 配置项

所有配置通过环境变量注入（也可用 `backend/.env` 文件，被 [godotenv](https://github.com/joho/godotenv) 加载）：

| 变量 | 必填 | 默认 | 说明 |
| --- | :-: | --- | --- |
| `LISTEN_ADDR` | | `:8080` | HTTP 监听地址 |
| `JWT_SECRET` | ✅ | dev 模式会临时生成一个 | session token 签名密钥，**生产必须设且 ≥ 16 字符** |
| `DB_PATH` | | `./data/atoms.db` | SQLite 文件路径 |
| `LLM_PROVIDER` | | 自动 | `claude` / `openai` / `mock`，留空会自动检测 |
| `LLM_API_KEY` | ✅ | | 兼容新旧命名 `OPENAI_API_KEY` 也会被读 |
| `LLM_BASE_URL` | | `https://api.anthropic.com` | 自定义端点（MiniMax 代理：`https://api.minimaxi.com/anthropic`） |
| `LLM_MODEL` | | `claude-3-5-sonnet-20241022` | 模型名 |
| `ENV=production` | | | 设为 `production` 后缺 `JWT_SECRET` 会 fatal exit |


## API 一览

所有 API 都在 `/api` 前缀下，除 `/healthz` 和 `/api/register` / `/api/login` 外都需要 session cookie。

| Method | Path | 说明 |
| --- | --- | --- |
| `GET`  | `/healthz` | 健康检查，返回 `ok` |
| `POST` | `/api/register` | 注册（email + password） |
| `POST` | `/api/login` | 登录 |
| `POST` | `/api/logout` | 登出 |
| `GET`  | `/api/me` | 当前 session 用户 |
| `GET`  | `/api/public/{slug}` | 公开项目（无需鉴权） |
| `GET`  | `/api/projects` | 我的项目列表 |
| `POST` | `/api/projects` | 建项目 |
| `GET`  | `/api/projects/{id}` | 单个项目（含消息历史） |
| `PATCH`| `/api/projects/{id}` | 改名 / 更新 prompt |
| `DELETE`| `/api/projects/{id}` | 删除 |
| `GET`  | `/api/projects/{id}/code` | 当前 HTML 内容 |
| `GET`  | `/api/projects/{id}/stream/info` | 流偏移 + 是否在生成 |
| `GET`  | `/api/projects/{id}/stream?from=N` | **SSE** 增量事件流 |
| `POST` | `/api/projects/{id}/generate` | 发起一轮生成（SSE 流式返回） |
| `POST` | `/api/projects/{id}/publish` | 发布 / 取消发布（body: `{"is_published":true}`） |

SSE 事件格式：

```
event: <type>
data: {"i":<index>,"t":"<type>","d":<data>}

```

事件类型：`text` / `tool_call` / `tool_result` / `code` / `error` / `done`。

## LLM 工具集

后端向模型暴露三个工具：

| 工具 | 用途 | 模型何时该用 |
| --- | --- | --- |
| `read_files` | 读取项目当前 HTML | 不确定现状时 |
| `patch_files` | 精准字符串替换（`old_text` → `new_text`，可批量） | 小改动（几行到几十行） |
| `update_files` | 整体替换整个 HTML | 新建项目 / 大改 / 重写 |

模型被 prompt 强制要求"任何改动必须调工具，纯文本说'完成'无效"。

### 防御层：拒绝伪 tool call

部分代理（如 MiniMax）在模型生成大段 tool_use 内容时会退化成把 JSON 文本写进 `text_delta`。后端检测到 `[tool_call:` 开头的 assistant 文本会：

1. **立即丢弃**该 chunk 及之后所有文本（不进 SSE、不进持久化）
2. 把丢弃的内容**回喂给模型**并附提醒，要求下次用 `tool_use` 通道

这样前端 chat 气泡永远不会出现 `[tool_call: name id=xxx] {...}` 这种伪调用。

## 数据布局

```
data/
├── atoms.db                                  SQLite（WAL 模式）
└── projects/
    └── {id}/
        ├── code.html                         当前 HTML（写入即用 atomic rename）
        ├── stream.ndjson                     追加式 SSE 事件日志
        ├── compaction.json                   最新一次历史压缩的摘要
        ├── turns/
        │   ├── 0001.json                     第一轮
        │   └── 0002.json                     第二轮
        └── backups/                          （可选，备份脚本用）
```

每个 `turns/NNNN.json` 包含用户消息、工具调用记录、Assistant 文本、用量、stop_reason 等。


## 已知限制

- **没有内置限流**：单用户可以同时发起多轮生成，靠 `lockForProject` 串行化。如果开放注册请加 nginx 限速 / fail2ban
- **SQLite 单文件**：适合 demo / 小团队。> 50 并发用户或 > 1000 个项目时建议迁 PostgreSQL（schema 已经能直接移植）
- **没有文件上传**：当前只支持纯文本描述。如果将来要支持图片上传要加 multipart 中间件 + 对象存储
- **前端假设 LLM 用 tool_use**：如果换成完全不兼容 function calling 的模型会失败。OpenAI 路径已经留了 stub（`backend/llm.go` 里 `provOpenAI`），但没实现
- **没有协作文战/多用户**：每个项目只有 owner

## 开发常用命令

```bash
# 后端 vet + build
cd backend && go vet ./... && go build

# 跨平台构建（无 CGO，纯 Go SQLite）
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o atoms-demo .

# 前端 dev
cd frontend && npm run dev

# 前端 build
cd frontend && npm run build   # 产物在 ../backend/dist

# 看后端日志（生产）
journalctl -u atoms-demo -f
```