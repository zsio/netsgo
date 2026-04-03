# NetsGo `install` / `manage` 交互式服务管理规划

## 文档状态

- 状态：本期定稿
- 适用范围：Linux + systemd
- 目标：在保留现有 `netsgo server` / `netsgo client` 直跑模式的前提下，新增面向终端用户的交互式安装与服务管理体验

---

## 1. 背景与目标

NetsGo 现在已经支持：

- `netsgo server`：直接启动服务端
- `netsgo client`：直接启动客户端
- 通过环境变量或命令行参数完成自动化 / Docker / 脚本化部署

但对于直接下载二进制、在机器上手动操作的用户来说，当前体验仍然偏底层：

- 需要自己记住参数
- 需要自己写 systemd unit
- 需要自己处理安装、启动、停止、日志、卸载

本期目标是补齐这一层终端用户体验：

1. 新增 `netsgo install` 作为交互式安装入口
2. 新增 `netsgo manage` 作为交互式服务管理入口
3. 同时支持 **server** 与 **client** 的交互式安装与管理
4. 保留现有直跑模式，不破坏 Docker / 自动化场景
5. 统一数据目录、日志目录、锁文件目录的规划模型
6. 在同一台机器上尽量避免 server / client 的重复实例冲突

---

## 2. 范围与非目标

## 2.1 本期范围

本期包含：

- `netsgo install`
- `netsgo manage`
- `netsgo update`
- server / client 的 systemd 服务安装与管理
- 统一 runtime root 设计
- 单机本地单实例保护
- install token（Web 端 setup 用）自动生成或手动输入
- 卸载时删除运行数据
- 二进制统一安装到 `/usr/local/bin/netsgo`

## 2.2 非目标

本期不包含：

- macOS launchd / Windows service 支持
- 多实例 server 管理
- 多实例 client 管理
- 在线”重配置”能力
- 程序内置下载源选择逻辑（下载源选择由 `netsgo update` 的交互菜单负责）
- Web UI 发起安装 / 管理
- 升级版本的回滚能力

说明：

- 如果用户想修改参数，做法是：**卸载后重新走 `install`**
- 下载脚本只负责下载二进制并启动 `netsgo install`，网络优化留在脚本层完成

---

## 3. 最终命令面

## 3.1 交互式命令

```bash
netsgo install
netsgo manage
netsgo update
```

### `netsgo install`

用于交互式安装服务（仅 root 或 sudo 可执行）：

- 选择语言
- 选择安装 server 或 client
- 检查目标角色是否已安装，若已安装则提示「已安装，请先卸载后重装」并退出
- 采集必要参数
- 写入配置
- 生成/安装 systemd unit
- 启动服务并给出后续提示

默认端口统一为 `9527`，包括开发环境与文档示例。

### `netsgo manage`

用于交互式管理已经安装的服务：

- 自动发现已安装的 server / client 服务
- 进入管理菜单
- 执行 status / start / stop / restart / inspect / uninstall

### `netsgo update`

用于升级已安装的 `netsgo` 二进制：

- 选择下载源（默认使用 GitHub）
- 下载最新版本并替换 `/usr/local/bin/netsgo`
- 替换后自动重启已安装的服务
- 需要 root 权限

## 3.2 保留的直跑命令

```bash
netsgo server ...
netsgo client ...
```

它们继续用于：

- Docker
- CI / 自动化脚本
- 传统 shell 启动方式
- 手工调试

这条线与 `install` / `manage` 分离，不要求交互。

---

## 4. 核心产品决策

## 4.1 交互式模式不暴露 `role` 参数

用户不需要输入：

```bash
--role server
--role client
```

而是在菜单中选择：

- 安装服务端
- 安装客户端
- 管理服务端
- 管理客户端

但在程序内部，仍然保留角色概念：

- `server`
- `client`

用于路径派生、unit 命名、锁文件、状态判断。

## 4.2 无重配置模式

本期明确不做：

- `reconfigure`
- 在线修改已安装服务参数

参数变更流程统一为：

1. 进入 `netsgo manage`
2. 卸载当前服务
3. 重新执行 `netsgo install`

## 4.3 每个角色固定单实例

本期按“每角色单实例”设计：

- 一个 `server`
- 一个 `client`

也就是说：

- 本机只允许一个受管 server 服务
- 本机只允许一个受管 client 服务
- `install/manage` 不支持创建第二个 server 或第二个 client

这不是指“整个世界只能有一个”，而是指：

- 在同一运行环境、同一路径根下，不允许重复同角色实例
- Docker 容器中的副本因为 root 路径与命名空间隔离，不受此限制

---

## 5. 统一路径模型

这是本期最关键的基础设计。

## 5.1 设计原则

不再继续依赖零散的 `~/.netsgo/...` 默认路径，而是引入一个统一概念：

# **runtime root**

所有运行时文件都从这个 root 派生。

统一结构：

```text
<runtime-root>/
  server/
  client/
  logs/
  locks/
```

含义：

- `server/`：服务端运行数据
- `client/`：客户端运行数据
- `logs/`：日志
- `locks/`：本地单实例锁

## 5.2 不同模式的默认 root

目录结构统一，但默认 root 按模式区分。

### 直跑模式默认 root

```text
$HOME/.local/state/netsgo
```

### systemd 服务模式默认 root

```text
/var/lib/netsgo
```

### Docker / 容器模式默认 root

```text
/var/lib/netsgo
```

补充要求：

- 程序启动时如果目标目录不存在，必须自动创建
- `server/`、`client/`、`logs/`、`locks/` 等子目录也应在首次使用时自动创建

说明：

- 这样实现的是“结构统一”，不是“所有模式共享同一个物理目录”
- 这是为了兼顾：
  - 用户态直跑安全性
  - systemd 规范化部署
  - 容器内路径可预测性

## 5.3 固定派生目录

### Server

```text
<runtime-root>/server/
```

建议承载：

- tunnels
- admin state
- traffic state
- TLS auto certs
- server 相关其他持久化文件

### Client

```text
<runtime-root>/client/
```

建议承载：

- client state
- install id
- token / fingerprint 相关状态
- client 相关其他持久化文件

### Logs

```text
<runtime-root>/logs/server.log
<runtime-root>/logs/client.log
```

### Locks

```text
<runtime-root>/locks/server.lock
<runtime-root>/locks/client.lock
```

---

## 6. 运行参数设计

为保持 direct-run 与 service-run 统一，本期建议新增一个可选参数：

```bash
--runtime-root <path>
```

适用于：

```bash
netsgo server --runtime-root ...
netsgo client --runtime-root ...
```

用途：

- 不传时使用各模式的默认 root
- direct-run 可显式指定运行根目录
- install/manage 安装 systemd 服务时会把它固化进 unit
- Docker 可挂载该目录以持久化数据

本期不优先暴露零散的：

- `--data-dir`
- `--log-dir`

而是优先统一到一个 root 上，由程序内部派生子目录。

---

## 7. 本地单实例保护

## 7.1 保护目标

尽量阻止以下冲突：

- 已经有受管 server 在运行，又手动 `netsgo server`
- 已经有受管 client 在运行，又手动 `netsgo client`
- 同一 root 下重复拉起相同角色实例

## 7.2 机制

采用 **基于 runtime root 的角色锁文件**：

```text
<runtime-root>/locks/server.lock
<runtime-root>/locks/client.lock
```

在启动时：

- `server` 获取 `server.lock`
- `client` 获取 `client.lock`
- 获取失败则拒绝启动，并提示已有实例运行

## 7.3 结果行为

### 同一 root 下

- 同角色重复启动会失败
- 这同时适用于：
  - direct-run
  - install/manage 安装出来的 systemd 服务

### 不同 root 下

- technically 可以并存
- 但本期产品不鼓励，也不在 `install/manage` 中暴露这种能力

### Docker / 容器场景

- 容器自己的 root 独立
- 锁不会影响其他容器
- 属于合理并存场景

---

## 8. `netsgo install` 交互流程

## 8.0 权限检查

`install` 命令需要 root 权限来写入 systemd unit、/etc 配置和 /var/lib 数据目录。

启动时行为：

- 检测当前是否 root
  - 若是：继续
  - 若否：以 `exec sudo <binary> install` 的方式整体重新以 root 启动当前进程（re-exec）
    - 这样整个 install 流程都以 root 身份运行，不存在"部分操作提权"的问题
- 若无法获取 root 权限（非 root 且无 sudo，或用户拒绝输入密码）：
  - 输出清晰错误：「安装需要 root 权限，请使用 sudo 运行」
  - 直接退出，退出码非 0

## 8.1 首屏

选择语言：

- 中文
- English

## 8.2 第二屏

选择安装对象：

- 安装服务端
- 安装客户端
- 退出

## 8.3 服务端安装表单

字段：

- 监听端口（默认 `9527`）
- 服务地址 / 域名
- install token（可留空，用于 Web 端初始化）

### install token 规则

- 用户填写：直接使用，但长度必须 **大于等于 6**
- 用户留空：程序自动生成 **16 位随机值**

要求：

- 生成值应便于人工复制
- 不沿用当前 64 字符 hex 的默认生成方式

## 8.4 客户端安装表单

字段：

- 服务端地址
- key
- 可选 TLS 校验选项

## 8.5 安装成功后输出

### server

输出：

- 服务名：`netsgo-server`
- 当前状态
- 管理面地址
- install token 来源：手填 / 自动生成
- 查看日志命令
- 下一步提示：

```bash
netsgo manage
```

### client

输出：

- 服务名：`netsgo-client`
- 当前状态
- server 地址
- 查看日志命令
- 下一步提示：

```bash
netsgo manage
```

---

## 9. `netsgo manage` 交互流程

## 9.1 入口逻辑

### 情况 A：server 与 client 都已安装

先选择：

- 管理服务端
- 管理客户端

### 情况 B：只安装了一个

直接进入该服务的管理菜单。

### 情况 C：一个都没安装

提示未检测到已安装服务，并提供跳转：

- 进入 `netsgo install`（需 root/sudo）
- 退出

## 9.2 管理菜单

进入后提供：

- status
- start
- stop
- restart
- inspect
- uninstall
- back

## 9.3 inspect 输出

### server inspect

至少包含：

- service name
- role
- installed / running / enabled
- binary path
- runtime root
- data path
- log path
- unit path
- env/spec path
- server addr
- port

### client inspect

至少包含：

- service name
- role
- installed / running / enabled
- binary path
- runtime root
- data path
- log path
- unit path
- env/spec path
- server url
- client identity state 摘要

---

## 10. uninstall 语义

## 10.1 总原则

无论用户选择哪种 uninstall 分支：

- **运行数据都删除**
- **配置都删除**

因为两种典型场景都是：

1. 全面卸载
2. 删除旧配置并重新 install

## 10.2 菜单分支

建议文案：

### 选项 1

**移除服务并清空运行数据**

行为：

- stop service
- disable service
- remove unit
- remove spec/env
- remove runtime root 下该角色的数据、日志、锁
- 保留二进制

### 选项 2

**移除服务、二进制并清空运行数据**

行为：

- 包含选项 1 的所有动作
- 额外删除安装的二进制（若当前二进制由 install 管理）

## 10.3 重要要求

因为 uninstall 会删数据，所以必须满足：

- 删除范围是 **角色/实例可解释的、路径明确的**
- 不能再使用模糊的 home 目录推导
- 删除前在 UI 中明确展示将删除的路径

---

## 11. systemd 安装模型

## 11.1 install spec / env

建议放在：

```text
/etc/netsgo/services/server.json
/etc/netsgo/services/server.env

/etc/netsgo/services/client.json
/etc/netsgo/services/client.env
```

其中：

- `*.json` 用于保存安装规格，例如角色、service 名称、binary 路径、runtime root、端口/地址等非敏感信息
- `*.env` 用于保存运行时需要注入的敏感或运行配置，例如 `NETSGO_SETUP_TOKEN`、`NETSGO_SERVER`、`NETSGO_KEY` 等
- systemd unit 通过 `EnvironmentFile=` 引用这些变量，避免把敏感信息硬编码进 unit 内容
- `*.env` 文件由 install 写入时必须设置权限为 `0600`，归属 root 或专用运行用户，防止其他用户读取敏感配置

## 11.2 unit 文件

```text
/etc/systemd/system/netsgo-server.service
/etc/systemd/system/netsgo-client.service
```

## 11.3 unit 文件内容规范

### server

```ini
[Unit]
Description=NetsGo Server
After=network-online.target
Wants=network-online.target

[Service]
ExecStart=/usr/local/bin/netsgo server --runtime-root /var/lib/netsgo
EnvironmentFile=/etc/netsgo/services/server.env
Restart=on-failure
RestartSec=5s
User=root

[Install]
WantedBy=multi-user.target
```

### client

```ini
[Unit]
Description=NetsGo Client
After=network-online.target
Wants=network-online.target

[Service]
ExecStart=/usr/local/bin/netsgo client --runtime-root /var/lib/netsgo
EnvironmentFile=/etc/netsgo/services/client.env
Restart=on-failure
RestartSec=5s
User=root

[Install]
WantedBy=multi-user.target
```

关键字段说明：

- `After=network-online.target`：确保网络就绪后再启动，对 client 建立出站连接尤其重要
- `Restart=on-failure`：异常退出后自动重启；正常 `stop` 不触发重启
- `RestartSec=5s`：重启间隔，避免快速循环崩溃
- `User=root`：本期以 root 运行，简化权限模型

---

## 12. 直跑模式与 service 模式的关系

## 12.1 不同职责

### 直跑模式

- 给 Docker / 自动化 / shell 使用
- 保留 flags / env 的现有能力
- 不依赖交互

### install/manage 模式

- 给终端用户使用
- 封装服务安装与管理
- 不逼用户手写参数

## 12.2 路径关系

- 结构统一
- 默认 root 不同
- 允许通过 `--runtime-root` 实现定制

## 12.3 冲突关系

- 若 direct-run 与 service-run 最终落到同一个 runtime root 且同角色，则锁文件应阻止重复启动
- 若 root 不同，则技术上可共存，但这不属于本期鼓励路径

---

## 13. client 身份模型约束

这是完整版里最需要明确提醒的地方。

当前 client 并不只是保存一个 `key`，还涉及：

- install id
- token
- 本地 state

因此本期规划中明确采用以下约束：

### 决策

- `install` 安装 client 时，视为创建一份新的受管 client 运行实例
- `uninstall` 删除 client 运行数据后，再次 `install` 视为新的 client 身份
- direct-run client 若使用不同 runtime root，也视为不同本地运行实例

### 结果

- “卸载再 install” = 身份重建
- 不做旧身份迁移
- 不做 client 重配置

这能让 install/manage 的完整体验成立，但也意味着：

- client reinstall 不是”无痕替换参数”
- 而是新实例生命周期

---

## 14. 建议的默认路径

## 14.1 用户直跑默认

```text
$HOME/.local/state/netsgo/
```

例如：

```text
~/.local/state/netsgo/
  server/
  client/
  logs/
  locks/
```

## 14.2 systemd / docker 默认

```text
/var/lib/netsgo/
```

例如：

```text
/var/lib/netsgo/
  server/
  client/
  logs/
  locks/
```

选择理由：

- 放弃 `~/.netsgo` 这种旧式 home dot-dir 方案
- direct-run 走更规范的 XDG state 路径
- service / container 使用固定系统路径
- 目录结构保持一致

---

## 15. 实施分期

## Phase 1：统一 runtime root

- 引入 `--runtime-root`
- server/client 改为从 runtime root 派生数据/日志/锁路径
- 移除运行时对 `~/.netsgo` 的硬编码依赖

## Phase 2：单实例保护

- 增加 `server.lock`
- 增加 `client.lock`
- 启动前检测并阻止同 root 下重复启动

## Phase 3：`netsgo install`

- 菜单与表单
- server 安装
- client 安装
- install token（Web 端 setup 用）自动生成逻辑
- systemd unit/spec/env 生成

## Phase 4：`netsgo manage`

- 自动发现
- status/start/stop/restart/inspect/uninstall
- 删除路径确认提示

## Phase 5：`netsgo update`

- 下载源选择菜单
- 下载并替换二进制
- 重启已安装服务

## Phase 6：文档与下载脚本

- 更新 README
- 提供下载脚本
- 脚本仅负责下载 + 运行 `netsgo install`

---

## 16. 本期定稿结论

本期规划结论如下：

1. 采用 `netsgo install` + `netsgo manage` + `netsgo update` 的交互式三入口
2. 保留 `netsgo server` / `netsgo client` 直跑模式
3. server 与 client 均纳入交互式安装/管理范围
4. 不做重配置；改配置即卸载重装
5. 引入统一的 runtime root 结构
6. 结构统一，默认 root 按模式区分
7. 通过角色锁文件尽量防止本地重复实例
8. uninstall 两个分支都删除运行数据，区别只在于是否删除二进制
9. install token（Web 端 setup 用）支持手填；留空时自动生成 16 位随机值
10. client reinstall 视为新身份生命周期
11. 二进制统一安装到 `/usr/local/bin/netsgo`
12. `install` / `update` 非 root 时整体 re-exec sudo，不做部分提权
13. `manage` 的 start/stop/restart/uninstall 操作需要 root；status/inspect 普通用户可用
14. systemd unit 统一使用 `Restart=on-failure` + `After=network-online.target`

---

## 17. 二进制安装路径

## 17.1 安装位置

```text
/usr/local/bin/netsgo
```

## 17.2 install 的处理方式

`netsgo install` 执行时：

- 检查 `/usr/local/bin/netsgo` 是否已存在
  - 若不存在：将当前运行的二进制 copy 到该路径
  - 若已存在且与当前二进制为同一文件（inode 相同）：跳过，不重复 copy
  - 若已存在但路径不同：覆盖，并在 UI 中告知用户
- systemd unit 的 `ExecStart` 始终硬编码为 `/usr/local/bin/netsgo`

## 17.3 uninstall 行为

- 选项 1（保留二进制）：不删除 `/usr/local/bin/netsgo`
- 选项 2（删除二进制）：删除 `/usr/local/bin/netsgo`

---

## 18. `netsgo manage` 权限要求

`manage` 不像 `install` 那样整体要求 root，因为部分操作普通用户也可以执行。

## 18.1 各操作权限

| 操作 | 是否需要 root |
|---|---|
| status | 否（读 systemctl is-active） |
| inspect | 否（读 spec JSON 和 systemctl 状态） |
| start | 是（systemctl start） |
| stop | 是（systemctl stop） |
| restart | 是（systemctl restart） |
| uninstall | 是（删文件、disable、remove unit） |

## 18.2 提权方式

与 `install` 保持一致：对需要 root 的操作，整体 re-exec sudo 后重新进入 manage，而不是逐操作提权。

具体行为：

- 用户以普通身份运行 `netsgo manage`
- 选择 status / inspect：正常执行
- 选择需要 root 的操作（start/stop/restart/uninstall）：
  - 若已是 root：继续
  - 若非 root：re-exec `sudo netsgo manage`，整体重启为 root 进程
  - 无法提权则报错退出

---

## 19. `netsgo update` 升级命令设计

## 19.1 功能

下载最新版本二进制并替换 `/usr/local/bin/netsgo`，然后重启已安装的服务。

需要 root 权限，提权方式与 `install` 相同（整体 re-exec sudo）。

## 19.2 交互流程

**第一屏：选择下载源**

```
请选择下载源：
  1. 默认（GitHub Releases）
  2. 中国镜像源
  3. 退出
```

- 用户不选择时默认 GitHub
- 超时或直接回车也默认 GitHub

**第二屏：确认版本**

- 展示当前版本和最新版本
- 若已是最新版本，提示「当前已是最新版本」并提供选项继续或退出
- 若有新版本，确认后开始下载

**第三屏：下载与替换**

- 下载进度展示
- 校验 checksum
- 替换 `/usr/local/bin/netsgo`

**第四屏：重启服务**

- 自动检测并重启已安装的 server / client 服务
- 展示重启结果

## 19.3 下载源地址

### GitHub（默认）

从 GitHub Releases 下载，根据当前系统架构（amd64 / arm64）自动选择对应资产。

### 中国镜像源

```text
# TODO: 补充国内镜像地址，待后期确认具体镜像仓库后填入
```

## 19.4 注意事项

- 替换二进制前先备份原文件到 `/usr/local/bin/netsgo.bak`（本期不提供回滚命令，但保留文件方便手动恢复）
- 替换完成后通过 `systemctl restart` 重启服务，不做进程内热更新
- 本期不支持版本回滚命令

---

