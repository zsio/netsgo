# Batch 7：nginx / caddy E2E 验证

> 状态：待实现
> 所属阶段：阶段 5（Client + E2E）
> 前置条件：Batch 6 完成
> 估计影响文件：`test/e2e/` 目录下的测试配置和脚本

## 目标

验证在 nginx 和 caddy 作为前置反向代理时，HTTP 域名隧道的关键头（`Host`、`Sec-WebSocket-Protocol`）能正确透传，NetsGo 内部通道和 HTTP 业务隧道都能正常工作。

## 背景

实际部署中，NetsGo 往往运行在 nginx 或 caddy 后面（处理 TLS 终止、80/443 监听等）。前置代理可能会：

- 改写 `Host` 头
- 过滤或改写 `Sec-WebSocket-Protocol` 头
- 缓冲响应导致 SSE 不实时
- 不透传 WebSocket Upgrade

本批次验证这些场景，确认部署路径可用。

> 实现原则：优先复用现有的
> [proxy_e2e_test.go](/Users/dyy/projects/code/netsgo/test/e2e/proxy_e2e_test.go)
> 和
> [compose_stack_e2e_test.go](/Users/dyy/projects/code/netsgo/test/e2e/compose_stack_e2e_test.go)，
> 不要另起一套平行的 E2E 方案。

## 需要验证的场景

### 场景 1：直连（补充基线）

直连链路不再单独起一套新的 E2E 工程。  
它可以由阶段 4/5 的 server/client 回归与人工检查覆盖；本批次的自动化验收聚焦：

- nginx 反代
- caddy 反代
- compose stack 下的恢复与重启链路

### 场景 2：nginx 前置代理

nginx 配置要点：

```nginx
server {
    listen 443 ssl;
    server_name _; # 通配，让所有域名都转发到 NetsGo

    # TLS 配置...

    location / {
        proxy_pass http://netsgo_backend;
        proxy_http_version 1.1;

        # WebSocket 支持
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";

        # 透传 Host（关键！）
        proxy_set_header Host $host;

        # 透传 WebSocket 子协议（关键！）
        proxy_set_header Sec-WebSocket-Protocol $http_sec_websocket_protocol;

        # 转发头
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;

        # SSE 支持
        proxy_buffering off;
        proxy_cache off;
    }
}
```

需要验证：

```
✓ Client 控制通道能在 nginx 后面连接成功
  （Sec-WebSocket-Protocol: netsgo-control.v1 能透传）
✓ Client 数据通道能连接
  （Sec-WebSocket-Protocol: netsgo-data.v1 能透传）
✓ HTTP 域名隧道：Host 头正确透传（不被改写为 upstream 地址）
✓ HTTP 域名隧道：WebSocket Upgrade 可以打通
✓ HTTP 域名隧道：SSE 不因 proxy_buffering 而延迟
✓ 管理面通过 nginx 访问正常
```

### 场景 3：caddy 前置代理

Caddyfile 配置要点：

```caddyfile
*.example.com {
    reverse_proxy netsgo_backend:port {
        header_up Host {host}
        header_up X-Forwarded-For {remote_host}
        header_up X-Forwarded-Proto {scheme}
        # caddy 默认会透传 Sec-WebSocket-Protocol
    }
}
```

需要验证（与 nginx 场景相同的检查点）。

## 测试实现方式

### 查看现有 E2E 测试结构

在实施前先查看：

```bash
ls test/e2e/
cat Makefile | grep -A 5 'e2e\|nginx\|caddy'
```

根据现有结构决定：

- 如果已有 docker-compose 测试框架，扩展现有 compose stack
- 如果没有，新建最小化的 compose stack

### 建议的 E2E compose stack 结构

```
test/e2e/http-tunnel/
  docker-compose.yml        # 包含 netsgo-server、nginx、caddy、mock-upstream
  nginx.conf                # nginx 透传配置
  Caddyfile                 # caddy 配置
  mock-upstream/            # 简单的 HTTP 服务，支持普通请求、WebSocket、SSE
    main.go
  run-test.sh               # 验证脚本
```

### `mock-upstream` 服务

一个简单的 Go HTTP 服务，实现以下端点：

```
GET /         -> 返回 "hello from upstream"
GET /headers  -> 返回所有收到的请求头（用于验证 Host、X-Forwarded-* 是否正确）
GET /sse      -> SSE 流，每秒发一条事件
GET /ws       -> WebSocket echo 服务
```

### `run-test.sh` 验证脚本

```bash
#!/bin/bash
set -e

# 等待服务就绪
wait_for_service() { ... }

# 验证 Host 头透传
curl -H "Host: tunnel.example.com" http://localhost/headers | jq '.Host'
# 期望: "tunnel.example.com"

# 验证 X-Forwarded-Host
curl -H "Host: tunnel.example.com" http://localhost/headers | jq '.["X-Forwarded-Host"]'
# 期望: "tunnel.example.com"

# 验证 SSE 即时推送（最多等 3 秒收到第一条事件）
timeout 3 curl -N -H "Host: tunnel.example.com" http://localhost/sse | head -2

# 验证 WebSocket
# 使用 websocat 或类似工具
echo "ping" | timeout 3 websocat ws://localhost/ws -H "Host: tunnel.example.com"
# 期望: 收到 "ping" echo
```

## 实现步骤

1. 查看 `test/e2e/` 现有结构和 `Makefile` 中相关命令
2. 优先复用现有 `proxy_e2e_test.go` 与 `compose_stack_e2e_test.go`；只有现有覆盖明显不足时才补充
3. 编写 nginx.conf，重点确保 `Sec-WebSocket-Protocol` 透传
4. 编写 Caddyfile
5. 实现 `mock-upstream` 服务
6. 编写验证脚本
7. 运行三个场景的验证

## 验收标准

```bash
# 反代 E2E
make test-e2e-nginx
make test-e2e-caddy

# compose stack E2E
make test-compose-stack-nginx
make test-compose-stack-caddy
```

### 必须通过的关键检查

自动化验收至少覆盖下面三类结果：

- nginx 反代链路通过
- caddy 反代链路通过
- compose stack 下的重启 / 恢复链路通过

直连基线可作为补充人工检查，但不再要求另起一套专用 E2E 目标。

## 已知注意事项

### nginx 透传 `Sec-WebSocket-Protocol`

nginx 默认**不会**透传 `Sec-WebSocket-Protocol`，必须显式配置：

```nginx
proxy_set_header Sec-WebSocket-Protocol $http_sec_websocket_protocol;
```

缺少这一行，Client 连接会失败（服务端看不到子协议，拒绝进入内部通道）。

### caddy 与 WebSocket 子协议

caddy 的 `reverse_proxy` 默认会透传大多数头，但需要验证 `Sec-WebSocket-Protocol` 是否在透传列表中。如果不在，需要显式配置。

### SSE 缓冲

nginx 需要：

```nginx
proxy_buffering off;
```

caddy 默认流式响应，通常不需要额外配置。

## 不引入的改动

- 不改前端（Batch 8 做）
- 不改核心 Go 代码（只加 E2E 测试配置和脚本）
