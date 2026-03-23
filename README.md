<p align="center">
  <img src="web/public/logo.svg" width="80" height="80" alt="NetsGo Logo" />
</p>

<h1 align="center">NetsGo</h1>
<p align="center">
  <strong>新一代内网穿透与边缘管控平台</strong><br/>
  轻量级管控中心 · 高性能网络隧道 · 单文件交付
</p>

<p align="center">
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-Apache--2.0-blue.svg" alt="License"></a>
  <img src="https://img.shields.io/badge/go-%3E%3D1.23-00ADD8?logo=go" alt="Go">
  <img src="https://img.shields.io/badge/react-19-61DAFB?logo=react" alt="React">
  <img src="https://img.shields.io/badge/platform-linux%20%7C%20macos%20%7C%20windows-lightgrey" alt="Platform">
</p>

---

## ✨ 特性

- 🔌 **单端口架构** — Web 面板、REST API、控制通道、数据通道共用一个端口
- 🔐 **端到端 TLS** — 支持自签证书 (TOFU)、自定义证书、反向代理三种模式
- 📊 **探针监控** — 实时采集 Client 的 CPU、内存、磁盘、网络状态
- 📦 **单文件交付** — 基于 `go:embed`，服务端内嵌 Web 面板，双击即用
- 🧬 **统一二进制** — 服务端和客户端是同一个程序，通过子命令区分
- 🧩 **Monorepo** — 双端共享协议定义，杜绝联调 Bug

## 快速开始

### 编译

```bash
make build        # 前端 + 后端完整构建
# 产物: bin/netsgo
```

### Docker

```bash
make build-web
docker build -t netsgo:local .
docker run --rm -p 8080:8080 netsgo:local
```

### 运行

```bash
# 服务端（无 TLS）
netsgo server --port 8080

# 服务端（自动生成自签证书）
netsgo server --port 8080 --tls-mode auto

# 服务端（自定义证书）
netsgo server --tls-mode custom --tls-cert cert.pem --tls-key key.pem

# 客户端
netsgo client --server https://your-server:8080 --key your-key
```

### 本地开发

三个终端各跑一个：

```bash
make dev-server   # 终端 1 — 服务端 (自动跳过 go:embed)
make dev-client   # 终端 2 — 客户端
make dev-web      # 终端 3 — 前端热更新 (Vite)
```

如果后端开发地址不是默认的 `http://127.0.0.1:8080`，启动前端前可先设置代理目标：

```bash
cd web
VITE_DEV_PROXY_TARGET=http://127.0.0.1:9090 bun run dev
```

## 架构

### 系统架构

```text
                       ┌───────────────────────────────────────────────┐
                       │            NetsGo Server (:8080)              │
                       │                                               │
                       │  ┌─────────────────────────────────────────┐  │
                       │  │          TLS (auto / custom / off)      │  │
                       │  └──────────────────┬──────────────────────┘  │
                       │                     │                         │
                       │   ┌─────────────────┼────────────────────┐   │
                       │   │        单端口协议分流                  │   │
                       │   │                                      │   │
                       │   │  数据通道              HTTP 请求       │   │
                       │   │  (yamux 流复用)            │          │   │
                       │   │                     ┌──────┼──────┐  │   │
                       │   │                   /      /api   /ws  │   │
                       │   │                  Web    REST  控制通道 │   │
                       │   │                  面板    API  (WS)    │   │
                       │   └──────────────────────────────────────┘   │
                       └───────────────────────┬───────────────────────┘
                                               │
                        ┌──────────────────────┼──────────────────────┐
                        │                      │                      │
                 ┌──────┴──────┐        ┌──────┴──────┐        ┌─────┴──────┐
                 │   Client A  │        │   Client B  │        │  Client C  │
                 │  (linux)    │        │  (windows)  │        │  (darwin)  │
                 │ 探针 + 隧道  │        │ 探针 + 隧道  │        │ 探针 + 隧道 │
                 └─────────────┘        └─────────────┘        └────────────┘
```

### 通信协议

| 通道 | 协议 | 用途 |
|------|------|------|
| 控制通道 | WebSocket `/ws/control` | 认证、心跳、探针上报、隧道指令下发 |
| 数据通道 | WebSocket `/ws/data` + yamux | 隧道流量转发（TCP / UDP） |
| 管理 API | REST `/api/*` | Web 面板后端、Key 管理 |
| 实时事件 | SSE `/api/events` | Client 上下线、隧道变更推送 |

### 认证体系

```text
初始化                    Client 认证                管理员认证
┌──────┐               ┌──────────┐              ┌───────────┐
│Setup │               │   Key    │              │ Username  │
│Token │──→ 创建管理员   │  (sk-*)  │──兑换──→ Token│ Password  │
│(一次性)│              │          │   (tk-*)     │           │
└──────┘               └──────────┘              └─────┬─────┘
                            │                          │
                        用于 Client                  JWT Session
                        控制通道认证                  httpOnly Cookie
                                                   (浏览器) 或
                                                   Bearer Header
                                                      (API)
```

## 安全

| 特性 | 说明 |
|------|------|
| TLS 加密 | 控制与数据通道均支持 TLS，三种模式可选 |
| httpOnly Cookie | 管理端 JWT 通过 httpOnly cookie 传递，防 XSS |
| 速率限制 | 登录、Client 认证、初始化接口均有速率限制 |
| Session 绑定 | 绑定 User-Agent，降低 token 被不同终端直接复用的风险 |
| 数据通道认证 | DataToken 机制绑定数据通道到已认证的控制通道 |
| Setup Token | 首次初始化需一次性 token，防未授权初始化 |
| 安全头 | HSTS、CSP、X-Frame-Options 等 |

## 项目结构

```text
netsgo/
├── cmd/netsgo/              # CLI 入口 (server / client / benchmark / docs)
├── pkg/
│   ├── protocol/            # 双端共享协议 (消息类型、探针结构体)
│   ├── mux/                 # yamux 流复用 + UDP 帧封装
│   └── version/             # 版本信息
├── internal/
│   ├── server/              # 服务端 (API、认证、隧道、SSE)
│   └── client/              # 客户端 (连接、探针、隧道)
├── web/                     # 前端 (React + TypeScript + Vite)
│   └── src/
│       ├── routes/          # 页面路由
│       ├── components/
│       │   ├── custom/      # 业务组件
│       │   └── ui/          # shadcn/ui 源码层
│       ├── hooks/           # 自定义 Hooks
│       ├── lib/             # API、路由、工具函数
│       ├── stores/          # Zustand 状态管理
│       └── types/           # 类型定义
├── Makefile
├── Dockerfile
└── .goreleaser.yaml
```

## 技术栈

| 层 | 技术 |
|------|------|
| **后端** | Go · Cobra · gorilla/websocket · hashicorp/yamux · golang-jwt |
| **前端** | React 19 · TypeScript · Vite · TanStack Router/Query · Zustand · shadcn/ui |
| **构建** | Makefile · GoReleaser · GitHub Actions · Docker 多架构 |

## 环境变量

所有命令行参数均可通过 `NETSGO_` 前缀的环境变量配置：

| 变量 | 对应参数 | 说明 |
|------|---------|------|
| `NETSGO_PORT` | `--port` | 监听端口 (默认 8080) |
| `NETSGO_TLS_MODE` | `--tls-mode` | TLS 模式: `auto` / `custom` / `off` |
| `NETSGO_TLS_CERT` | `--tls-cert` | TLS 证书路径 |
| `NETSGO_TLS_KEY` | `--tls-key` | TLS 私钥路径 |
| `NETSGO_SETUP_TOKEN` | `--setup-token` | 显式指定初始化 Setup Token（适合自动化部署 / E2E） |
| `NETSGO_SERVER` | `--server` | 服务端地址 |
| `NETSGO_KEY` | `--key` | Client 认证密钥 |

## 运行说明

- 运行日志默认写入 `~/.netsgo/logs/`。
- 首次启动时生成的 Setup Token 只输出到 stderr / 控制台，不会写入日志文件。
- `/api/events` 仍然是实时状态流接口，用于推送在线状态与隧道变更，不是审计接口。

## CI / CD

- **CI** — Push / PR 时自动运行前端构建 + `go test` + 跨平台构建
- **Release** — 推送 `v*` tag 后自动发布 Linux / macOS / Windows 二进制 + 多架构 Docker 镜像
- **Docker** — 默认发布到 `ghcr.io`，配置 `DOCKERHUB_USERNAME` / `DOCKERHUB_TOKEN` 后同步 Docker Hub

## 验证入口

推荐优先使用项目内置入口：

```bash
make build        # 先构建 web/dist，再构建 Go 二进制
make test         # 运行 go test ./...
go vet ./...

make bench-data
make test-e2e-nginx
make test-e2e-caddy
make test-compose-stack-nginx
make test-compose-stack-caddy
make soak-data STACK_PROXY=nginx
```

如果直接执行 `go build ./...` 或 `go test ./...`，请先确保已经有 `web/dist`（例如先运行一次 `make build-web`），否则非 dev 模式下 `go:embed` 可能因找不到前端构建产物而失败。

`test/e2e/` 下的 nginx / Caddy 验证会把 `/ws/control` 和 `/ws/data` 一起经过反向代理，覆盖首连、空闲存活和代理重启后的自动恢复。

如果需要保留一套可复用的多容器联调环境，可以直接使用 Compose stack：

```bash
make compose-stack-up STACK_PROXY=nginx
make compose-stack-logs STACK_PROXY=nginx
make compose-stack-down STACK_PROXY=nginx
make compose-stack-clean STACK_PROXY=nginx
```

这套 stack 会同时启动 `server`、`client`、`backend`、`bootstrap` 和 `proxy`。其中 `bootstrap` 会自动完成初始化、登录、创建 API key、等待 client 上线并创建测试隧道；`compose-stack-down` 保留卷，便于继续做长时间测试，`compose-stack-clean` 会连卷一起删除。

`make soak-data` 现在会基于这套 Compose stack 做短周期 soak：默认空闲 `45s`（显式跨过 yamux 默认 `30s` keepalive）、验证 live client 计数稳定，并在过程中分别重启 `proxy` 和 `client` 检查整会话恢复。

## License

[Apache-2.0](LICENSE)
