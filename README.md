# 🚀 NetsGo

新一代内网穿透与边缘管控平台 — 轻量级管控中心 (C2) + 高性能网络隧道。

## 特性

- **单端口架构** — Web 面板、API、控制通道、数据通道共用一个端口
- **探针监控** — 实时采集 Agent 所在机器的 CPU、内存、磁盘、网络状态
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
# Dockerfile 依赖已生成的 web/dist
make build-web

docker build -t netsgo:local .
docker run --rm -p 8080:8080 netsgo:local
```

### 运行

```bash
# 启动服务端（默认端口 8080）
./bin/netsgo server --port 8080

# 启动客户端连接到服务端（代理隧道由服务端 Web 面板统一管控）
./bin/netsgo client --server ws://your-server-ip:8080

# 带认证密钥
./bin/netsgo client --server ws://your-server-ip:8080 --key mykey

# 运行性能压测
./bin/netsgo benchmark -c 50 --size 1
```

### 开发模式

```bash
# 启动服务端
go run ./cmd/netsgo/ server -port 8080

# 另一个终端，启动客户端
go run ./cmd/netsgo/ client -server ws://localhost:8080

# 运行压测
go run ./cmd/netsgo/ benchmark
```

### 查看帮助

```bash
# 查看所有子命令
./bin/netsgo help

# 查看子命令选项
./bin/netsgo server -h
./bin/netsgo client -h

# 查看版本
./bin/netsgo version
```

## 自动发布

- CI: GitHub Actions 会在 `push main/master` / `pull_request` 时执行前端构建、`go test ./...` 和跨平台 smoke build
- Release: 推送 `v*` tag（例如 `v0.1.0`）后，会自动发布 Linux / macOS / Windows 二进制，并构建多架构 Docker 镜像
- Docker 镜像: 默认发布到 `ghcr.io/<owner>/netsgo`，如果配置了 `DOCKERHUB_USERNAME` 和 `DOCKERHUB_TOKEN`，也会同步推送到 Docker Hub

## 项目结构

```
netsgo/
├── cmd/netsgo/          # 统一入口 (server / client / benchmark)
├── pkg/protocol/        # 💎 双端共享协议与数据结构
├── internal/
│   ├── server/          # 服务端核心逻辑
│   └── client/          # 客户端核心逻辑（含探针）
├── web/dist/            # 前端构建产物
├── Makefile             # 构建脚本
└── README.md
```

## 架构

```
┌─────────────────────────────────────┐
│         Server (:8080)              │
│  /            → Web 面板             │
│  /api/        → REST API            │
│  /ws/control  → 控制通道 (WebSocket)  │
│  /ws/data     → 数据通道 (WebSocket)  │
└─────────────────┬───────────────────┘
                  │
     ┌────────────┼────────────┐
     │ 控制通道     │ 数据通道    │
     │ (心跳/探针)  │ (流量转发)  │
     └────────────┼────────────┘
                  │
┌─────────────────┴───────────────────┐
│         Client (代理端)              │
│  探针采集（CPU/内存/磁盘/网络）        │
│  本地隧道管理                        │
└─────────────────────────────────────┘
```
