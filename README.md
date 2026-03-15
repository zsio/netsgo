# 🚀 NetsGo

新一代内网穿透与边缘管控平台 — 轻量级管控中心 + 高性能网络隧道。

## ✨ 特性

- **单端口架构** — Web 面板、REST API、控制通道、数据通道共用一个端口
- **端到端 TLS** — 支持自签证书 (TOFU)、自定义证书、反向代理三种模式
- **探针监控** — 实时采集 Client 的 CPU、内存、磁盘、网络状态
- **Monorepo** — 服务端与客户端共享协议定义，杜绝联调 Bug
- **单文件交付** — 基于 `go:embed`，服务端内嵌 Web 面板，双击即用
- **统一二进制** — 服务端和客户端编译为同一个程序，通过子命令区分

## 快速开始

### 编译

```bash
make build
# 产物: bin/netsgo
```

### Docker 构建

```bash
make build-web
docker build -t netsgo:local .
docker run --rm -p 8080:8080 netsgo:local
```

### 运行

```bash
# 启动服务端（默认端口 8080，无 TLS）
netsgo server --port 8080

# 启动服务端（自动生成自签名证书）
netsgo server --port 8080 --tls-mode auto

# 启动服务端（使用自定义证书）
netsgo server --tls-mode custom --tls-cert cert.pem --tls-key key.pem

# 启动客户端
netsgo client --server https://your-server:8080 --key your-key

# 跳过证书校验（仅测试）
netsgo client --server https://your-server:8080 --key your-key --tls-skip-verify
```

### 开发模式

```bash
# 终端 1: 服务端
make dev-server

# 终端 2: 客户端
make dev-client

# 终端 3: 前端热更新
make dev-web
```

### 查看帮助

```bash
netsgo help
netsgo server -h
netsgo client -h
netsgo version
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
                   │             │        │             │        │            │
                   │ 探针 + 隧道  │        │ 探针 + 隧道  │        │ 探针 + 隧道 │
                   └─────────────┘        └─────────────┘        └────────────┘
```

### 通信架构

| 通道 | 协议 | 用途 |
|------|------|------|
| 控制通道 | WebSocket (`/ws/control`) | 认证、心跳、探针上报、隧道指令下发 |
| 数据通道 | TCP 二进制 (yamux 流复用) | 隧道流量转发（TCP / UDP） |
| 管理 API | REST (`/api/*`) | Web 面板后端、Client Key 管理 |
| 实时事件 | SSE (`/api/events`) | Client 上下线、隧道变更、状态推送 |

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

## 安全特性

| 特性 | 说明 |
|------|------|
| **TLS 加密** | 控制通道和数据通道均支持 TLS，三种模式可选 |
| **httpOnly Cookie** | 管理端 JWT 通过 httpOnly cookie 传递，防 XSS |
| **双认证模式** | 浏览器用 cookie，API 调用用 Authorization header |
| **速率限制** | 登录、Client 认证、初始化接口均有速率限制 |
| **Session 绑定** | Session 绑定 IP 和 User-Agent，防 token 盗用 |
| **单端登录** | 同一管理员同时只有一个活跃 session |
| **数据通道认证** | DataToken 机制绑定数据通道到已认证的控制通道 |
| **Setup Token** | 首次初始化需要一次性 token，防未授权初始化 |
| **安全头** | HSTS、CSP、X-Frame-Options 等安全响应头 |
| **优雅关闭** | 支持 SIGINT/SIGTERM 信号优雅关闭 |

## 项目结构

```text
netsgo/
├── cmd/netsgo/              # CLI 入口 (server / client / benchmark / docs)
│   ├── main.go              # Cobra 根命令
│   ├── cmd_server.go        # server 子命令 (TLS 配置、端口等)
│   ├── cmd_client.go        # client 子命令 (地址、Key、TLS 选项)
│   └── cmd_benchmark.go     # benchmark 子命令
│
├── pkg/                     # 公共可复用包
│   ├── protocol/            # 双端共享协议 (消息类型、探针结构体)
│   ├── mux/                 # yamux 流复用 + UDP 帧封装
│   └── version/             # 版本信息
│
├── internal/
│   ├── server/              # 服务端核心
│   │   ├── server.go        # 主逻辑 (连接管理、API 路由、SSE)
│   │   ├── admin_api.go     # 管理 API (登录、Key CRUD、配置)
│   │   ├── admin_store.go   # 管理数据持久化 (JSON 文件)
│   │   ├── auth_middleware.go # JWT + Session + Cookie 认证
│   │   ├── tls.go           # TLS 配置 (auto/custom/off)
│   │   ├── proxy.go         # TCP 代理隧道
│   │   ├── udp_proxy.go     # UDP 代理隧道
│   │   ├── data.go          # 数据通道 (yamux session 管理)
│   │   ├── peek.go          # PeekListener (首字节分流)
│   │   ├── events.go        # SSE 事件总线
│   │   ├── rate_limiter.go  # 速率限制器
│   │   ├── store.go         # 隧道配置持久化
│   │   └── tunnel_manager.go # 隧道持久化与恢复
│   │
│   └── client/              # 客户端核心
│       ├── client.go        # 主逻辑 (连接、认证、TLS、隧道)
│       ├── probe.go         # 系统探针 (CPU/内存/磁盘/网络)
│       ├── state.go         # 连接状态机
│       └── udp_handler.go   # UDP 隧道处理
│
├── web/                     # 前端 (React + TypeScript + Vite)
│   └── src/
│       ├── routes/          # 页面 (登录、初始化、仪表盘、管理)
│       ├── components/      # UI 组件 (shadcn/ui + 业务组件)
│       ├── hooks/           # 自定义 Hooks (SSE、Client 数据)
│       ├── stores/          # Zustand 状态管理
│       ├── lib/             # 工具 (API 客户端、路由守卫)
│       └── types/           # TypeScript 类型定义
│
├── Makefile                 # 构建脚本
├── Dockerfile               # Docker 镜像
├── .goreleaser.yaml         # GoReleaser 发布配置
└── .github/workflows/       # CI/CD (测试 + 跨平台构建 + 发布)
```

## 技术栈

| 层 | 技术 |
|------|------|
| **后端** | Go、Cobra、Viper、gorilla/websocket、hashicorp/yamux、golang-jwt |
| **前端** | React 19、TypeScript、Vite、TanStack Router/Query、Zustand、shadcn/ui、Motion |
| **构建** | Makefile、GoReleaser、GitHub Actions、Docker 多架构构建 |

## 环境变量

所有命令行参数均可通过 `NETSGO_` 前缀的环境变量配置：

| 变量 | 对应参数 | 说明 |
|------|---------|------|
| `NETSGO_PORT` | `--port` | 监听端口（默认 8080） |
| `NETSGO_TLS_MODE` | `--tls-mode` | TLS 模式：auto / custom / off |
| `NETSGO_TLS_CERT` | `--tls-cert` | TLS 证书路径 |
| `NETSGO_TLS_KEY` | `--tls-key` | TLS 私钥路径 |
| `NETSGO_SERVER` | `--server` | 服务端地址 |
| `NETSGO_KEY` | `--key` | Client 认证密钥 |

## 自动发布

- **CI**: Push / PR 时自动运行前端构建 + `go test ./...` + 跨平台 Smoke Build
- **Release**: 推送 `v*` tag 后自动发布 Linux / macOS / Windows 二进制 + 多架构 Docker 镜像
- **Docker**: 默认发布到 `ghcr.io`，配置 `DOCKERHUB_USERNAME` + `DOCKERHUB_TOKEN` 后同步 Docker Hub

## License

[Apache-2.0](LICENSE)
