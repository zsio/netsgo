<p align="center">
  <img src="web/public/logo.svg" width="80" height="80" alt="NetsGo Logo" />
</p>

<h1 align="center">NetsGo</h1>
<p align="center">
  <strong>内网穿透与节点管理平台</strong><br/>
  内置 Web 控制台 · 单端口接入 · 单文件部署
</p>

<p align="center">
  <a href="README.md">English</a> | <strong>简体中文</strong>
</p>

<p align="center">
  <a href="https://github.com/zsio/netsgo/actions/workflows/ci.yml"><img src="https://github.com/zsio/netsgo/actions/workflows/ci.yml/badge.svg" alt="CI"></a>
  <a href="https://github.com/zsio/netsgo/releases"><img src="https://img.shields.io/github/v/release/zsio/netsgo?include_prereleases&label=release" alt="Release"></a>
  <a href="https://hub.docker.com/r/zsio/netsgo"><img src="https://img.shields.io/badge/docker-zsio%2Fnetsgo-blue?logo=docker" alt="Docker"></a>
</p>

<p align="center">
  <img src="https://img.shields.io/badge/Go-1.25.13-00ADD8?logo=go&logoColor=white" alt="Go">
  <img src="https://img.shields.io/badge/React-19-61DAFB?logo=react&logoColor=black" alt="React">
  <img src="https://img.shields.io/badge/deploy-single--binary-brightgreen" alt="Single Binary">
  <img src="https://img.shields.io/badge/platform-linux%20%7C%20macOS%20%7C%20Windows-lightgrey" alt="Platform">
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-Apache--2.0-blue.svg" alt="License"></a>
</p>

---

**NetsGo** 是一个开箱即用的内网穿透与节点管理平台。它将 Web 控制台、REST API、客户端接入和底层网络隧道整合进一个单文件二进制中，让部署更简单、接入更统一、运维更省心。你可以用它远程访问内网服务，也可以统一管理分散在各处的远程节点。

---

## 目录

- [界面预览](#界面预览)
- [快速开始](#快速开始)
- [为什么用 NetsGo](#为什么用-netsgo)
- [和常见方案的区别](#和常见方案的区别)
- [License](#license)

---

## 界面预览

<p align="center">
  <img src=".github/assets/dashboard.webp" alt="NetsGo Web 控制台总览" width="92%" />
  <br/>
  <sub><strong>Web 控制台总览</strong></sub>
</p>

---

## 快速开始

更多文档和使用说明见官网：[https://netsgo.zs.uy](https://netsgo.zs.uy)。

### 一键安装

```bash
curl -fsSL https://netsgo.zs.uy/install.sh | sh
```

### 一键更新

```bash
curl -fsSL https://netsgo.zs.uy/upgrade.sh | sh -s -- -y
```

安装完成后，按交互提示初始化 Server 或 Client。Client 在线后，即可在 Web 面板里创建和管理 tunnel。

---

### 转发连接与环路保护

Server 和 Client 各自默认最多同时处理 **10000** 条转发连接/UDP 关联，所有隧道共用该实例的额度。超过上限会关闭新增连接并记录日志，已有传输继续；关闭连接后额度自动归还。Server 额度耗尽时，HTTP 入口返回 503。

可通过 `netsgo server --max-connections 20000`、`netsgo client --max-connections 20000` 或环境变量 `NETSGO_MAX_CONNECTIONS=20000` 调整。systemd 部署可在对应的 `/etc/netsgo/services/server.env` 或 `client.env` 中设置，再重启对应服务。配置必须为正整数；这是连接数量限制，与带宽限速分别配置。

Linux 上，新版 Server/Client 通过本次系统启动与网络命名空间标识识别同机 TCP 自回连。确认目标指向当前 TCP 隧道自己的监听地址时，创建/修改配置或下发配置会被拒绝；域名在真实业务拨号解析后、建立目标连接前再次检查，不主动探测目标服务。不同机器或独立容器网络使用相同端口仍然允许。

该环路检查需要两端支持；旧版对端、无法取得网络标识的平台，以及跨多个隧道/外部代理形成的间接环路，仍由连接额度限制资源增长。10000 不是内存安全承诺，请按实例可用内存和实际负载调整。

## 为什么用 NetsGo

如果你只是想尽快把服务端跑起来、把内网机器连上来、然后开始建隧道，NetsGo 的设计重点就是这几件事：

- **一个二进制就能跑**：`./netsgo server` 启服务端，`./netsgo client` 连远程节点。
- **一个端口就够**：Web 控制台、控制通道和数据通道共用同一个入口，防火墙和反向代理只配一处就够。（TCP/UDP 隧道按需使用额外端口。）
- **默认就带控制台**：连上客户端后，直接在 Web 面板里管理节点、查看状态、配置隧道。
- **HTTPS 证书留在内网**：[TLS SNI 透传](docs/tls-sni.md) 按域名将同一服务端端口上的业务 TLS 转发到已有的内网 HTTPS 服务。

## 和常见方案的区别

这张表只做能力清单，不做高低评价。单元格尽量用短词：

- `内置`：产品自己带
- `平台`：由托管平台提供
- `插件`：常见做法可通过插件/面板实现
- `配置`：主要通过配置文件或命令完成
- `外接`：通常要接第三方监控、日志或运维系统

| 对比项 | **NetsGo** | **frp** | **ngrok** | **cloudflared** | **rathole** |
|---|---|---|---|---|---|
| 产品定位 | 自建管理平台 | 自建穿透工具 | 托管平台 | Cloudflare 隧道 | 自建轻量隧道 |
| 服务端自建 | ✅ | ✅ | ❌ | 平台 | ✅ |
| Web 界面 | 内置 | 内置/插件 | 平台 | 平台 | 外接 |
| API / 自动化 | 内置 REST | 支持/插件 | 平台 | 平台 | 配置 |
| 客户端管理 | 内置 | 配置/插件 | 平台 | 平台 | 配置 |
| 隧道增删改 | Web/API | 配置/插件 | 平台 | 平台 | 配置 |
| Web/API/客户端共用入口 | ✅ | 多入口 | 平台 | 平台 | 配置 |
| HTTP 隧道 | ✅ | ✅ | ✅ | ✅ | TCP 承载 |
| TCP 隧道 | ✅ | ✅ | ✅ | ✅ | ✅ |
| UDP 隧道 | ✅ | ✅ | 视套餐/场景 | 视场景 | ✅ |
| 登录/密钥 | 管理员 + Key | Token/OIDC | 账号/Token | 账号/策略 | Token |
| 在线状态 | 内置 | 内置/插件 | 平台 | 平台 | 日志/外接 |
| 流量统计 | 内置 | 内置/插件 | 平台 | 平台 | 外接 |
| 断线重连 | ✅ | ✅ | ✅ | ✅ | ✅ |
| 限速 | 内置 | 内置 | 平台/套餐 | 平台策略 | 外接 |

### 适用建议

- **NetsGo**：适合想把“客户端、隧道、状态、流量”放在一个自建 Web 面板里管理的场景。
- **frp / rathole**：适合偏配置驱动、轻量部署、自己组合运维工具的场景。
- **ngrok / cloudflared**：适合偏托管平台、希望直接使用平台账号和边缘网络能力的场景。

## License

[Apache-2.0](LICENSE)

首发于 [linux.do](https://linux.do) 论坛
