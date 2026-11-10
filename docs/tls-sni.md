# HTTPS / TLS SNI 透传

TLS 入口按客户端 ClientHello 中的 SNI 域名选择隧道，将原始 TLS 字节转发到 Client 所在网络的 TCP 服务。业务证书、私钥和 TLS 握手均由目标服务处理，适合将宝塔、nginx 等已有 HTTPS 服务直接暴露到公网。

```text
浏览器 -- TLS --> NetsGo 同一监听端口 -- 数据通道 --> Client -- TCP --> 内网 HTTPS 服务
                 根据 SNI 选择隧道                         在这里完成业务 TLS 握手
```

## 配置

1. 将业务域名解析到 NetsGo 的公网入口。
2. 创建隧道，访问方式选择「公网访问」，协议选择「TLS」。
3. 填写域名、目标 Client、目标地址与 HTTPS 端口，例如 `127.0.0.1:443`。
4. 在目标服务上配置该域名的证书和 HTTPS 站点。
5. 使用 `https://业务域名:入口端口` 访问。公网入口为 443 时可省略端口。

入口复用 Server 的 TCP 监听端口，不额外分配隧道端口。Server 自身的 `--tls-mode` 控制管理面和 Client 通道的 TLS；业务透传也可与明文 HTTP 管理入口共用监听器。

API 使用 `POST /api/tunnels`，入口为 `tls_sni`、目标为 `tcp_service`。下面的 `client-id` 替换为真实 Client ID，认证方式与其他隧道 API 一致：

```json
{
  "name": "private-https",
  "topology": "server_expose",
  "ingress": {
    "location": "server",
    "type": "tls_sni",
    "config": {
      "domain": "app.example.com",
      "allowed_source_cidrs": ["0.0.0.0/0", "::/0"]
    }
  },
  "target": {
    "location": "client",
    "client_id": "client-id",
    "type": "tcp_service",
    "config": {"host": "127.0.0.1", "port": 443}
  },
  "transport_policy": "server_relay_only"
}
```

## 域名与访问行为

- 沿用 HTTP 入口的精确域名和泛域名规则。HTTP 与 TLS 共用域名占用规则；同一条规则不能重复使用，不同用户不能创建相互重叠的规则。
- 同一用户的重叠规则按最具体规则选择，HTTP 与 TLS 之间也遵守此顺序。选中隧道停用、离线或来源不允许时，连接失败，不回退到较宽的规则。
- 管理地址的主机名始终保留给管理服务，即使某个 TLS 泛域名覆盖它。不能创建与管理主机名相同的 TLS 入口；管理端口不同也不能绕过。
- 来源 CIDR 使用 TCP 对端 IP。经过四层代理后看到的是代理 IP；本入口不解析 PROXY protocol，也不使用 HTTP 转发头。需要按真实访问者 IP 限制时，在前置代理上配置访问控制。
- 不解密业务 HTTP，因而不提供入口 Basic Auth、请求头修改、路径路由或自动签发业务证书。业务 TLS 配置和应用认证由目标服务处理。
- 需要客户端发送可见 SNI。无 SNI 的连接交给原管理监听器；ECH 隐藏的真实业务域名无法用于选路。此入口承载 TCP，不支持 HTTP/3、QUIC 或 UDP。
- 运行状态表示 NetsGo 链路和隧道运行态，不代表目标业务健康；NetsGo 不主动探测目标服务。

## 前置代理

前置代理必须保留业务 TLS 字节。原有 HTTP 反向代理配置可继续用于管理和 Client 通道；业务透传需要四层转发路径。

### nginx stream

示例将公网 443 原样转发到本机 NetsGo 的 9527 端口，需要 nginx 编译/加载 stream 和 ssl_preread 模块：

```nginx
events {}

stream {
    server {
        listen 443;
        ssl_preread on;
        proxy_pass 127.0.0.1:9527;
    }
}
```

这是完整配置的最小示例；已有配置只需在顶层合并 `stream`。这里不添加 `ssl` 监听参数或 PROXY protocol。若管理服务也走这条透传路径，需要在 NetsGo 为管理面配置 TLS。

### Caddy layer4

需要包含 [caddy-l4](https://github.com/mholt/caddy-l4) 扩展的 Caddy，普通 `reverse_proxy` 是 HTTP 代理。可使用 `xcaddy build --with github.com/mholt/caddy-l4` 构建，再以如下原生 JSON 配置启动：

```json
{
  "apps": {
    "layer4": {
      "servers": {
        "netsgo": {
          "listen": [":443"],
          "routes": [{
            "handle": [{
              "handler": "proxy",
              "upstreams": [{"dial": ["127.0.0.1:9527"]}]
            }]
          }]
        }
      }
    }
  }
}
```

此处不配置 TLS 终止处理器，管理 TLS 仍在 NetsGo 完成，业务 TLS 在内网目标服务完成。

## 验证与升级

常规 `go test ./internal/server` 包含真实 Client 的 TLS 1.2/1.3 大报文、目标证书、混合管理端口、来源限制、域名优先级及迁移测试。

可选集成测试需要自行准备 nginx Docker 镜像、包含 layer4 的 Caddy，以及旧版 NetsGo 二进制；全部都指向独立测试数据目录：

```bash
NETSGO_TEST_SNI_NGINX=1 \
NETSGO_TEST_SNI_CADDY_BINARY=/path/to/caddy \
NETSGO_TEST_SNI_CLIENT_BINARY=/path/to/netsgo-v0.1.8 \
go test ./internal/server -run '^TestTLSSNIPassthrough' -count=1
```

nginx 集成测试使用 `nginx:1.27-alpine`，通过 `host.docker.internal` 访问测试 Server；本地 Docker 需要支持该主机地址。

此功能新增 SQLite 迁移 017。升级前应备份数据；旧 Server 不认识新 schema，回滚 Server 需要恢复相应版本的数据备份。Client 数据通道沿用现有 TCP 目标协议。
