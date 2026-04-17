<p align="center">
  <img src="web/public/logo.svg" width="80" height="80" alt="NetsGo Logo" />
</p>

<h1 align="center">NetsGo</h1>
<p align="center">
  <strong>单文件内网穿透工具</strong><br/>
  单文件交付 · 单端口接入 · Web 控制台统一管理
</p>

<p align="center">
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-Apache--2.0-blue.svg" alt="License"></a>
  <img src="https://img.shields.io/badge/platform-linux%20%7C%20macOS%20%7C%20Windows-lightgrey" alt="Platform">
</p>

---

## 为什么用 NetsGo

如果你只是想尽快把服务端跑起来、把内网机器连上来、然后开始建隧道，NetsGo 的设计重点就是这三件事：

- **一个二进制就能跑**：`netsgo server` 启服务端，`netsgo client` 连边缘节点。
- **一个端口就够**：对外只需要一个入口，部署和过反代都更省事。
- **部署路径清晰**：容器或直接运行都可以；Linux 长驻场景可用 `netsgo install` + `netsgo manage`。
- **默认就带控制台**：连上 client 后，直接在 Web 面板里管 client、看状态、配 tunnel。

---

## 快速开始

> 下文示例默认 `netsgo` 已在 `PATH` 中；如果你只是临时解压在当前目录，也可以把命令里的 `netsgo` 替换成 `./netsgo`。

### 1. 启动服务端

#### 推荐方式：Linux Service（systemd）

长期运行最省事的方案。下载 `netsgo` 二进制后执行：

```bash
sudo netsgo install
```

按交互提示选择 **Server**，并填入管理员账号、访问地址、允许端口范围等信息。安装完成后：

```bash
sudo netsgo manage
```

`manage` 提供查看状态、启停、看日志、卸载等入口。

> 数据目录默认位于 `/var/lib/netsgo`。

#### 方式二：Docker Compose 示例

下面是一份 `docker-compose.yml` 示例配置，可按你的域名和目录自行调整：

```yaml
services:
  netsgo-server:
    image: ghcr.io/zsio/netsgo:latest
    restart: unless-stopped
    ports:
      - "9527:9527"
    environment:
      NETSGO_INIT_ADMIN_USERNAME: "admin"
      NETSGO_INIT_ADMIN_PASSWORD: "Password123"
      NETSGO_INIT_SERVER_ADDR: "https://your-netsgo-domain.com"
      NETSGO_INIT_ALLOWED_PORTS: "10000-11000"
    volumes:
      - netsgo-data:/var/lib/netsgo
    command:
      - "server"
      - "--data-dir"
      - "/var/lib/netsgo"
      - "--tls-mode"
      - "off"

volumes:
  netsgo-data:
```

如果你不走反向代理、准备直接对外暴露 NetsGo，请把 `--tls-mode off` 改成 `--tls-mode auto` 或 `--tls-mode custom`。

#### 方式三：直接运行（仅试用/调试）

<details>
<summary>点击展开 CLI 直跑命令</summary>

首次启动必须提供完整的 `--init-*` 参数：

```bash
./netsgo server \
  --port 9527 \
  --init-admin-username admin \
  --init-admin-password Password123 \
  --init-server-addr https://your-netsgo-domain.com \
  --init-allowed-ports 10000-11000
```

初始化完成后，同一数据目录后续启动可省略 `init-*` 参数。

</details>

---

### 2. 登录 Web 面板并创建 Client Key

1. 浏览器访问服务端地址（如 `https://your-netsgo-domain.com`）。
2. 用初始化时设置的管理员账号登录。
3. 进入“客户端管理”，创建一个 client，复制生成的 `Client Key`（格式类似 `sk-...`）。

---

### 3. 启动客户端

#### 推荐方式：Linux Service（systemd）

```bash
sudo netsgo install
```

按交互提示选择 **Client**，并填入服务端地址与 Client Key。

> **注意**：交互安装时要求填写的服务端地址应使用 WebSocket 口径 `ws://` 或 `wss://`，不要写成 `https://`。例如：
> - `wss://your-netsgo-domain.com`
> - `ws://192.168.1.10:9527`

之后用 `sudo netsgo manage` 查看状态和日志。

#### 方式二：直接运行（仅试用/调试）

```bash
./netsgo client --server https://your-netsgo-domain.com --key <your-client-key>
```

服务端地址支持以下格式，客户端会自动处理：

- `ws://host:port`
- `wss://host:port`
- `http://host:port`
- `https://host:port`

---

### 4. 创建 Tunnel 并验证

client 在线后，在 Web 面板里直接添加、修改或删除 tunnel，无需再登录 client 机器改配置。

创建完成后，直接从公网入口访问或连接对应 tunnel，确认流量已经正确转发。

---

## 生产部署建议

生产环境推荐把 NetsGo 放在 **Nginx** 或 **Caddy** 反向代理之后。只需要注意两点：

- **开启 WebSocket Upgrade**
- **把超时时间调大**，避免长连接被代理提前断开

#### Nginx 示例

<details>
<summary>点击展开</summary>

```nginx
server {
    listen 80;
    server_name your-netsgo-domain.com;

    proxy_read_timeout 3600s;
    proxy_send_timeout 3600s;

    location / {
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
        proxy_set_header Host $http_host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header Sec-WebSocket-Protocol $http_sec_websocket_protocol;
        proxy_buffering off;
        proxy_cache off;
        proxy_pass http://127.0.0.1:9527;
    }
}
```

</details>

#### Caddy 示例

<details>
<summary>点击展开</summary>

```caddyfile
your-netsgo-domain.com {
    reverse_proxy 127.0.0.1:9527 {
        header_up Host {host}
        header_up X-Forwarded-For {remote_host}
        header_up X-Forwarded-Proto {scheme}
    }
}
```

</details>

---

## 附录

<details>
<summary>常用命令参考</summary>

### `netsgo server`

除“首次初始化”外，下面示例都默认你使用的是**已初始化过的数据目录**。

```bash
# 首次初始化
netsgo server \
  --init-admin-username admin \
  --init-admin-password Password123 \
  --init-server-addr https://panel.example.com \
  --init-allowed-ports 10000-11000

# 已初始化数据目录的后续启动
netsgo server --data-dir ~/.local/state/netsgo

# 自动生成自签 TLS 证书
netsgo server --tls-mode auto

# 使用自定义证书
netsgo server --tls-mode custom --tls-cert /path/to/cert.pem --tls-key /path/to/key.pem

# 由反向代理处理 TLS
netsgo server --tls-mode off --trusted-proxies 127.0.0.1/32,10.0.0.0/8
```

### `netsgo client`

```bash
# 连接远端 HTTPS / WSS 服务端
netsgo client --server https://1.2.3.4:9527 --key mykey

# 交互式 install 时使用 WebSocket 地址
netsgo client --server wss://1.2.3.4:9527 --key mykey

# 仅用于测试：跳过 TLS 校验
netsgo client --server wss://1.2.3.4:9527 --key mykey --tls-skip-verify
```

### `netsgo install` / `netsgo manage`

```bash
sudo netsgo install   # 交互式安装为 systemd 服务
sudo netsgo manage    # 查看状态、启停、日志、卸载
```

</details>

<details>
<summary>常用环境变量</summary>

所有命令都支持 `NETSGO_` 前缀环境变量。

### 服务端

| 环境变量 | 对应参数 | 说明 |
|---|---|---|
| `NETSGO_PORT` | `--port` | 监听端口，默认 `9527` |
| `NETSGO_SERVER_ADDR` | `--server-addr` | 强制指定对外访问地址 |
| `NETSGO_INIT_ADMIN_USERNAME` | `--init-admin-username` | 首次初始化管理员用户名 |
| `NETSGO_INIT_ADMIN_PASSWORD` | `--init-admin-password` | 首次初始化管理员密码 |
| `NETSGO_INIT_SERVER_ADDR` | `--init-server-addr` | 首次初始化写入的管理面地址 |
| `NETSGO_INIT_ALLOWED_PORTS` | `--init-allowed-ports` | 首次初始化允许端口范围 |

### 客户端

| 环境变量 | 对应参数 | 说明 |
|---|---|---|
| `NETSGO_SERVER` | `--server` | 服务端地址，例如 `wss://netsgo.example.com` |
| `NETSGO_KEY` | `--key` | client 鉴权 key |

</details>

---

## License

[Apache-2.0](LICENSE)
