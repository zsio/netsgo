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
  <img src="https://img.shields.io/badge/go-1.25.5-00ADD8?logo=go" alt="Go 1.25.5">
  <img src="https://img.shields.io/badge/react-19-61DAFB?logo=react" alt="React">
  <img src="https://img.shields.io/badge/platform-linux-lightgrey" alt="Platform">
</p>

---

## 📖 简介

**NetsGo** 是一款开箱即用、高性能的内网穿透与边缘管控平台。它将强大的服务端 Web 面板、REST API 和底层网络隧道能力完美地打包在**一个单文件二进制**中。无论是个人开发者远程访问内网服务，还是企业管理海量边缘设备，NetsGo 都能为您提供极简且安全的体验。

## ✨ 核心特性

- 📦 **单文件交付** — 将前端 Web 面板与 Go 后端编译为单个二进制文件。**零外部依赖，一条命令即可启动**。
- 🔌 **单端口多路复用** — Web 面板、REST API、控制通道与数据隧道**共享同一个监听端口**，极大降低防火墙与反向代理的配置成本。
- 🔐 **端到端安全** — 全链路 TLS 加密。支持自动生成自签证书 (TOFU)、使用自定义证书，或无缝接入 Nginx / Caddy 等反向代理。
- 📊 **内置系统探针** — 客户端自带轻量级探针，在 Web 控制台上实时展示所有边缘节点的 CPU、内存、磁盘与网络指标。
- 🧬 **统一可执行程序** — 服务端与客户端同体。使用 `netsgo server` 启动管控中心，使用 `netsgo client` 接入边缘节点。

---

## 🚀 安装指南

### 方式一：下载预编译程序（推荐）

前往项目的 **[Releases](https://github.com/zsio/netsgo/releases)** 页面，下载对应 Linux 平台的最新版本二进制文件，解压后即可直接使用。

### 方式二：使用 Docker

```bash
docker run -d \
  --name netsgo-server \
  --restart unless-stopped \
  -p 8080:8080 \
  -e NETSGO_INIT_ADMIN_USERNAME="admin" \
  -e NETSGO_INIT_ADMIN_PASSWORD="Password123" \
  -e NETSGO_INIT_SERVER_ADDR="https://your-netsgo-domain.com" \
  -e NETSGO_INIT_ALLOWED_PORTS="1-65535" \
  -v netsgo-data:/root/.local/state/netsgo \
  ghcr.io/zsio/netsgo:latest server
```

*(注：NetsGo 默认将配置和数据存储在 `~/.local/state/netsgo`；Docker 容器内对应 `/root/.local/state/netsgo`。强烈建议挂载该目录以实现数据持久化。)*

<details>
<summary>点击查看 Docker Compose 推荐配置 (docker-compose.yml)</summary>

```yaml
services:
  netsgo-server:
    image: ghcr.io/zsio/netsgo:latest
    container_name: netsgo-server
    restart: unless-stopped
    ports:
      - "8080:8080"
    environment:
      - NETSGO_INIT_ADMIN_USERNAME=admin
      - NETSGO_INIT_ADMIN_PASSWORD=Password123
      - NETSGO_INIT_SERVER_ADDR=https://your-netsgo-domain.com
      - NETSGO_INIT_ALLOWED_PORTS=1-65535
    volumes:
      - netsgo-data:/root/.local/state/netsgo
    command: server

volumes:
  netsgo-data:
```

</details>

---

## 🧰 本地构建与开发要求

当前仓库的实际工具链要求以仓库文件和 CI 为准：

- **Go：`1.25.5`**（见 `go.mod`）
- **前端工具：Bun**（见 `web/bun.lock`、`Makefile` 与 `.github/workflows/ci.yml`）

常用本地检查命令：

```bash
# Go 测试
go test ./...

# 前端依赖安装 / lint / 构建
cd web
bun install --frozen-lockfile
bun run lint
bun run build
```

如果 README 其他位置与 `go.mod`、`web/bun.lock` 或 CI 工作流不一致，应以后者为准并同步修正文档。

## 💡 快速开始

### 1. 启动服务端 (Server)

NetsGo 默认监听 `8080` 端口。最基础的启动方式如下：

```bash
# 首次启动时通过 init 参数完成一次性初始化
./netsgo server \
  --port 8080 \
  --init-admin-username admin \
  --init-admin-password Password123 \
  --init-server-addr https://your-netsgo-domain.com \
  --init-allowed-ports 1-65535
```

**🔑 初始化：**
服务端**首次启动**时，必须通过 `init-*` 参数或对应的 `NETSGO_INIT_*` 环境变量完成一次性初始化，设置管理员账号、密码、管理面地址和允许端口范围。
初始化成功后，直接访问 Web 面板并使用该管理员账号登录即可；同一 data dir 后续再次启动时，这些 `init-*` 参数会被自动忽略。

### 2. 启动客户端 (Client)

1. 使用刚才创建的管理员账号登录 Web 面板。
2. 进入“客户端管理”页面，点击新建，获取一个 `Client Key` (格式如 `sk-...`)。
3. 在需要穿透或被管控的内网机器上运行客户端：

```bash
./netsgo client --server ws://<您的服务端IP>:8080 --key <Your-Client-Key>
```
*(注：如果服务端开启了 TLS 或在 HTTPS 反代后，请使用 `wss://` 协议前缀。)*

---

## 🌟 最佳实践与推荐配置

在生产环境中，我们**强烈推荐将 NetsGo 服务端放置在标准的 Web 反向代理（如 Nginx 或 Caddy）之后**。这不仅能利用反代工具自动管理 HTTPS 证书（如 Let's Encrypt），还能提供更强的网络防护。

### 反向代理配置建议

因为 NetsGo 的数据通道和控制通道重度依赖于 **WebSocket** 以及**长时间保持的长连接**，所以配置反向代理时，**务必开启 WebSocket 升级支持，并调大超时时间（避免隧道经常断开重连）**。

#### 👉 Nginx 推荐配置

<details>
<summary>点击查看 Nginx 完整配置</summary>

```nginx
server {
    listen 80;
    # 推荐开启 SSL 
    # listen 443 ssl;
    server_name your-netsgo-domain.com;

    # 调大超时时间，确保长连接/数据流不会被 Nginx 强行掐断
    proxy_read_timeout 3600s;
    proxy_send_timeout 3600s;

    location / {
        # WebSocket 必备支持
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
        
        # 传递真实 IP 与协议
        proxy_set_header Host $http_host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        
        # 针对部分特定 subprotocol 的兼容
        proxy_set_header Sec-WebSocket-Protocol $http_sec_websocket_protocol;
        
        # 关闭缓冲，降低数据通道延迟
        proxy_buffering off;
        proxy_cache off;
        
        proxy_pass http://127.0.0.1:8080;
    }
}
```

</details>

#### 👉 Caddy 推荐配置

Caddy 默认原生支持 WebSocket，配置极为简单，只需透传必要的头部信息即可：

<details>
<summary>点击查看 Caddy 完整配置</summary>

```caddyfile
your-netsgo-domain.com {
    reverse_proxy 127.0.0.1:8080 {
        header_up Host {host}
        header_up X-Forwarded-For {remote_host}
        header_up X-Forwarded-Proto {scheme}
    }
}
```

</details>

### 环境变量与守护进程 (Systemd)

为了方便结合 Docker Compose 或 Systemd 部署，NetsGo 所有的配置项均支持通过 `NETSGO_` 前缀的环境变量注入，推荐使用环境变量管理敏感信息。

推荐的受管部署方式是先运行 `netsgo install`，让它在 `/etc/netsgo/services/`、`/etc/systemd/system/` 和 `/var/lib/netsgo/` 下生成完整配置；后续日常操作统一走 `netsgo manage`。

常见受管运维动作：

- `sudo ./netsgo install`：交互式写入 spec / env / systemd unit，并完成首次初始化
- `sudo ./netsgo manage`：查看状态、inspect、启停、卸载
- 查看日志：`netsgo manage` 会直接转交到 `journalctl -u netsgo-server.service -n 100 -f` 或对应的 client unit

**服务端 (Server) 常用环境变量：**

| 环境变量 | 命令行参数 | 说明 |
|------|---------|------|
| `NETSGO_PORT` | `--port` | 监听端口 (默认: 8080) |
| `NETSGO_SERVER_ADDR` | `--server-addr` | 强制配置服务端的外网访问地址或域名 (推荐以此方式配置，设置后 Web 端配置将被锁定并失效) |
| `NETSGO_INIT_ADMIN_USERNAME` | `--init-admin-username` | 首次初始化管理员用户名 |
| `NETSGO_INIT_ADMIN_PASSWORD` | `--init-admin-password` | 首次初始化管理员密码 |
| `NETSGO_INIT_SERVER_ADDR` | `--init-server-addr` | 首次初始化写入的管理面地址 |
| `NETSGO_INIT_ALLOWED_PORTS` | `--init-allowed-ports` | 首次初始化允许的端口范围，例如 `1-65535` |

**客户端 (Client) 常用环境变量：**

| 环境变量 | 命令行参数 | 说明 |
|------|---------|------|
| `NETSGO_SERVER` | `--server` | 需连接的服务端地址 (例如: `wss://your-netsgo-domain.com`) |
| `NETSGO_KEY` | `--key` | 客户端认证密钥 (格式如 `sk-...`) |

**服务端 Systemd 服务示例 (`/etc/systemd/system/netsgo-server.service`)：**

<details>
<summary>点击查看服务端 Systemd 配置</summary>

```ini
[Unit]
Description=NetsGo Server
After=network.target

[Service]
Type=simple
User=netsgo
Group=netsgo
EnvironmentFile=/etc/netsgo/services/server.env
ExecStart=/usr/local/bin/netsgo server --data-dir /var/lib/netsgo
Restart=on-failure
RestartSec=5s
NoNewPrivileges=true

[Install]
WantedBy=multi-user.target
```

</details>

**客户端 Systemd 服务示例 (`/etc/systemd/system/netsgo-client.service`)：**

<details>
<summary>点击查看客户端 Systemd 配置</summary>

```ini
[Unit]
Description=NetsGo Client Agent
After=network.target

[Service]
Type=simple
User=netsgo
Group=netsgo
EnvironmentFile=/etc/netsgo/services/client.env
ExecStart=/usr/local/bin/netsgo client --data-dir /var/lib/netsgo
Restart=on-failure
RestartSec=5s
NoNewPrivileges=true

[Install]
WantedBy=multi-user.target
```

</details>

---

## 🛡️ 架构与安全

NetsGo 采用极简的单端口路由架构，底层基于高性能流复用技术，确保管控界面与网络隧道之间互不干扰：

```text
                       ┌───────────────────────────────────────────────┐
                       │            NetsGo Server (:8080)              │
                       │                                               │
                       │   ┌─────────────────┴────────────────────┐    │
                       │   │         单端口协议分流                  │   │
                       │   │                                      │    │
                       │   │  数据通道              HTTP 请求       │    │
                       │   │  (yamux 流复用)            │          │    │
                       │   │                     ┌──────┼──────┐  │    │
                       │   │                   /      /api   /ws  │    │
                       │   │                  Web    REST  控制通道 │   │
                       │   │                  面板    API  (WS)    │    │
                       │   └──────────────────────────────────────┘    │
                       └───────────────────────┬───────────────────────┘
                                               │
                        ┌──────────────────────┼──────────────────────┐
                 ┌──────┴──────┐        ┌──────┴──────┐        ┌─────┴──────┐
                 │   Client A  │        │   Client B  │        │  Client C  │
                 │ 探针 + 隧道  │        │ 探针 + 隧道  │        │ 探针 + 隧道 │
                 └─────────────┘        └─────────────┘        └────────────┘
```

**核心安全机制：**
- **严密的准入体系：** 首次启动必须显式完成一次性初始化；Web 界面采用严格的 HttpOnly Cookie JWT 机制，从根源上防范 XSS 与会话劫持。
- **数据通道深度绑定 (DataToken)：** 底层数据传输与已通过强鉴权的控制通道进行深度密码学绑定，彻底杜绝任何未授权的流量接入或端口扫描探测。
- **全方位防爆破保护：** 对登录、系统初始化、边缘节点接入等敏感入口均内置了智能速率限制 (Rate Limiting)，有效抵御暴力破解攻击。

---

## 📄 License

[Apache-2.0](LICENSE)
