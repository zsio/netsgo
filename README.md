# 🚀 NetsGo

新一代内网穿透与边缘管控平台 — 轻量级管控中心 (C2) + 高性能网络隧道。

## 特性

- **单端口架构** — Web 面板、API、控制通道、数据通道共用一个端口
- **探针监控** — 实时采集 Agent 所在机器的 CPU、内存、磁盘、网络状态
- **Monorepo** — 服务端与客户端共享协议定义，杜绝联调 Bug
- **单文件交付** — 基于 `go:embed`，服务端内嵌 Web 面板，双击即用

## 快速开始

### 编译

```bash
# 编译全部
make build-all

# 或单独编译
go build -o bin/server.exe ./cmd/server/
go build -o bin/client.exe ./cmd/client/
```

### 运行

```bash
# 启动服务端（默认端口 8080）
./bin/server.exe -port 8080

# 启动客户端连接到服务端
./bin/client.exe -server ws://your-server-ip:8080
```

### 开发模式

```bash
# 启动服务端
go run ./cmd/server/ -port 8080

# 另一个终端，启动客户端
go run ./cmd/client/ -server ws://localhost:8080
```

## 项目结构

```
netsgo/
├── cmd/
│   ├── server/          # 服务端入口
│   └── client/          # 客户端入口
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
│         Agent (Client)              │
│  探针采集（CPU/内存/磁盘/网络）        │
│  本地隧道管理                        │
└─────────────────────────────────────┘
```

## 开发路线

- **Phase 1 (MVP)** — 控制通道、心跳、探针上报、基础 TCP 转发
- **Phase 2** — yamux 多路复用、go:embed 前端、SQLite 持久化
- **Phase 3** — 可视化监控大屏、HTTP 智能路由、流量限速

## 文档

- `docs/设想和方案.md` — 项目提案与目标架构
- `docs/架构审查.md` — 当前实现的架构审查结论与改进建议
- `docs/已知问题.md` — 已识别的缺陷与修复方向
