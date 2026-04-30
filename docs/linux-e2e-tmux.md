# Linux E2E 测试环境

本项目有一台专属 Linux 主机，用于执行需要真实 systemd、root 管理路径、
交互式服务管理的端到端测试。

## 连接方式

使用共享 tmux 会话，让 AI/agent 可以复用同一个长期终端，避免每次测试都重新建立 SSH 连接。

如果会话已经存在，直接进入：

```bash
tmux attach -t netsgo-e2e-session
```

如果会话不存在，可以自行创建：

```bash
tmux new -s netsgo-e2e-session
```

进入 tmux 会话后，使用下面的命令连接专属 Linux 主机：

```bash
ssh netsgo-e2e-linux
```

目标主机是专门用于 NetsGo E2E 验证的 Debian 13 x86 AMD 系统。

测试时不需要关注该主机的 IP。在本机中（当前系统）已经通过 hosts 将下面的域名映射到
这台专属 Linux主机：

- `netsgo.zsio.dev`
- `*.zsio.dev`

涉及服务访问、回调地址、管理地址、反向代理或浏览器验证时，优先使用这些
域名。`netsgo.zsio.dev` 可作为固定管理平台域名；`*.zsio.dev` 可用于覆盖
HTTP 隧道、子域名路由和泛域名相关场景。

如果 client service 也运行在这台 Linux 主机上，并且使用
`ws://netsgo.zsio.dev:9527` 连接本机 server，需要确保 Linux 主机本身也能
解析 `netsgo.zsio.dev`。否则 client service 会启动成功，但会因为 DNS 解析
失败持续重连。

## 权限

该主机已经配置 passwordless sudo。agent 可以直接执行需要 sudo 的服务管理
命令，不需要等待密码输入。

## 适用测试范围

该环境用于执行 macOS 无法真实覆盖的 Linux 生命周期测试，尤其是：

- `netsgo install`
- `netsgo manage`
- `netsgo upgrade`
- 服务 start、stop、restart、status、inspect、logs
- uninstall 流程与清理行为
- systemd unit 行为
- `/etc/netsgo/`、`/var/lib/netsgo/`、`/etc/systemd/system/` 等受管路径

凡是涉及真实 systemd 集成、root-owned 安装路径、Linux 服务生命周期的测试，
优先使用该环境验证。

## Agent 使用约定

执行破坏性测试前，先查看当前状态：

```bash
systemctl status netsgo-server.service --no-pager
systemctl status netsgo-client.service --no-pager
ls -la /usr/local/bin/netsgo /etc/netsgo /var/lib/netsgo 2>/dev/null
```

生命周期测试结束后，除非当前任务明确要求保留现场用于后续调试，否则应清理已安装服务和遗留状态。
