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

但对于直接下载二进制、希望像普通 Linux 服务一样安装和管理的用户来说，当前体验仍然偏底层：

- 需要自己记住启动参数
- 需要自己写 systemd unit
- 需要自己处理安装、启动、停止、重启、日志、卸载
- 首次启动 server 还要额外经过 Web setup

本期目标是补齐这一层终端用户体验，并统一初始化模型：

1. 新增 `netsgo install` 作为交互式安装入口
2. 新增 `netsgo manage` 作为交互式服务管理入口
3. 新增 `netsgo update` 作为升级占位命令
4. 同时支持 **server** 与 **client** 的交互式安装与管理
5. 保留现有 `netsgo server` / `netsgo client` 直跑模式
6. 取消 Web setup，并在实现上删除对应页面 / API / token / flag，改为在终端交互 / flags / env 中完成初始化
7. 统一 data dir、受管服务权限模型、日志模型、卸载边界与单实例保护语义

---

## 2. 范围与非目标

## 2.1 本期范围

本期包含：

- `netsgo install`
- `netsgo manage`
- `netsgo update` 占位命令
- server / client 的 systemd 服务安装与管理
- 统一 data dir 设计
- 基于内核级文件锁的本地单实例保护
- 取消 Web setup 后的初始化前置设计
- 统一二进制安装到 `/usr/local/bin/netsgo`
- 全模式统一使用 stdout/stderr 输出日志

## 2.2 非目标

本期不包含：

- macOS launchd / Windows service 支持
- 多实例 server 管理
- 多实例 client 管理
- install/manage 内的在线重配置
- Web UI 发起安装 / 管理
- Web setup / setup token
- 真实自更新流程（下载、校验、替换、重启服务）
- 升级回滚能力

说明：

- `netsgo update` 本期只保留命令入口，不实现真实升级逻辑
- 如果用户想修改受管服务的启动参数，做法是：**卸载后重新 install**
- 这条规则仅针对 install/manage 管理的受管服务；Docker / 直跑场景仍按 flags / env 的常规方式使用

---

## 3. 最终命令面

## 3.1 交互式命令

```bash
netsgo install
netsgo manage
netsgo update
```

### `netsgo install`

用于交互式安装受管服务：

- 仅支持 Linux + systemd
- 需要 root 权限；非 root 时整体 `re-exec sudo`
- 选择安装 server 或 client
- 采集必要参数
- 写入 env / unit
- 启动服务并给出后续提示

### `netsgo manage`

用于交互式管理已经安装的受管服务：

- 自动发现已安装的 server / client 服务
- 统一以 root 权限运行；非 root 时整体 `re-exec sudo`
- 执行 status / inspect / logs / start / stop / restart / uninstall

### `netsgo update`

本期只保留占位行为：

```bash
$ netsgo update
自动更新功能尚未实现，请访问 https://github.com/zsio/netsgo
```

它：

- 不下载版本
- 不替换二进制
- 不重启服务
- 不要求 root

## 3.2 保留的直跑命令

```bash
netsgo server ...
netsgo client ...
```

它们继续用于：

- Docker
- CI / 自动化脚本
- shell 启动方式
- 手工调试

这条线与 `install` / `manage` 分离：

- 不依赖交互式菜单
- 不要求 root（除非绑定低位端口或访问受限路径）
- 不受“受管服务必须 root 查看”的产品限制

---

## 4. 核心产品决策

## 4.1 保留直跑，新增受管模式

NetsGo 保留两条使用路径：

### 直跑路径

- `netsgo server`
- `netsgo client`

适合：

- Docker
- CI
- 脚本化启动
- 手工调试

### 受管路径

- `netsgo install`
- `netsgo manage`

适合：

- 终端用户在 Linux 主机上把 NetsGo 当作 systemd 服务使用

两条路径共用同一个二进制，但不共享交互流程。

## 4.2 取消 Web setup

本期明确取消：

- Web setup 页面
- setup token
- “服务先启动，再在浏览器里做首次初始化” 这条流程

删除范围：

- 删除 Web 端 `/setup` 初始化页面
- 删除后端 `/api/setup/status` 与 `/api/setup/init`
- 删除 server 未初始化时输出 setup token banner 的行为
- 删除 `--setup-token` / `NETSGO_SETUP_TOKEN` 这类仅用于 Web setup 的入口
- 删除“未初始化时先启动服务、再由浏览器完成首配”的产品语义

替代方案：

- `netsgo install` 的交互问答
- `netsgo server` 的 init flags
- `netsgo server` 的 init env

也就是说，server 的首次初始化必须在进程启动前就把必需参数准备好；不能再依赖浏览器补完。

实现边界：

- 这是产品规划，不要求“兼容保留旧 setup 路径”
- 文档层面应直接把 Web setup 当作将被删除的旧方案
- 新方案里，未初始化 server 只有两种结果：
  - init 参数齐全：完成初始化后启动
  - init 参数不齐：直接失败退出

## 4.3 初始化前置，但仅作用于未初始化的 server

server 初始化参数只在 **目标 data dir 尚未初始化** 时生效。

行为规则：

- 未初始化 + 参数齐全：启动前完成一次性初始化
- 未初始化 + 参数不齐：直接报错退出
- 已初始化 + 再次提供 init 参数：忽略，不重复初始化

这样可以兼容：

- `netsgo install server` 先初始化再启动服务
- Docker / CLI 在容器或脚本里长期保留 `NETSGO_INIT_*` 环境变量

这也意味着：

- 交互式终端程序负责询问并收集初始化参数
- 非交互式场景通过 flags / env 提供同样的信息
- 不再有“Web 初始化是第三条入口”的设计

## 4.4 交互式受管模式固定为 root 安装与 root 管理

这是本期明确的产品策略，而不是“能不能技术上放开”的问题。

规则：

- `netsgo install` 必须以 root 执行
- `netsgo manage` 必须以 root 执行
- 受管服务的 status / inspect / logs 也必须通过 root 路径查看
- 如果用户以普通身份执行 `install` / `manage`，程序应整体 `re-exec sudo`

原因：

- 受管服务是 root 安装到系统级路径中的 systemd system service
- `/etc/netsgo/`、`/etc/systemd/system/`、`/var/lib/netsgo/` 都属于系统级目录
- 受管服务的 env / unit / journald 查看统一按 root 管理，避免普通用户获得额外查看面

补充说明：

- 这是 **受管模式的管理权限策略**
- 它不要求服务进程以 root 身份运行
- 也不影响 Docker / 直跑模式

## 4.5 受管服务默认以低权限用户运行

虽然 install/manage 要求 root，但受管服务进程本身不应默认跑在 root 下。

本期默认模型：

- 安装时创建系统用户 `netsgo`（若不存在）
- `netsgo-server.service` 与 `netsgo-client.service` 默认使用：
  - `User=netsgo`
  - `Group=netsgo`

原因：

- server 暴露 Web / API / WebSocket 面
- client 持有连接密钥和本地状态
- 默认端口不需要 root 权限
- 最小权限更符合受管服务的安全边界

## 4.6 每角色固定单实例

本期按“每角色单实例”设计：

- 一个 `server`
- 一个 `client`

也就是说：

- 本机只允许一个受管 server 服务
- 本机只允许一个受管 client 服务
- `install/manage` 不支持创建第二个 server 或第二个 client

这不是指“所有环境里全世界只能有一个”，而是指：

- 在同一台主机上的受管模式里，只维护一套 `netsgo-server.service`
- 在同一台主机上的受管模式里，只维护一套 `netsgo-client.service`

## 4.7 全模式统一只用 stdout/stderr

本期统一日志模型：

- direct-run：stdout/stderr
- Docker：stdout/stderr
- systemd：stdout/stderr，由 journald 接管

因此：

- 不再设计 `logs/` 目录
- 不再设计应用自己的日志文件
- 不再设计 `--log-dir`

## 4.8 共享二进制与角色卸载解耦

受管模式统一把二进制安装到：

```text
/usr/local/bin/netsgo
```

这个二进制由 server / client 共享。

因此必须明确：

- 卸载某个角色 ≠ 删除共享二进制
- 只有当 **没有任何受管角色剩余** 时，才允许额外询问是否删除 `/usr/local/bin/netsgo`

## 4.9 交互式终端技术选型

本期 `netsgo install` / `netsgo manage` 的交互式终端，采用：

- **Cobra / Viper + 自建轻量交互层**

不采用：

- 全屏 TUI 框架作为第一版基础
- 重型状态机式终端界面
- 为了“看起来更炫”而引入额外复杂度

### 选型原因

当前交互需求本质上是：

- 菜单选择
- 文本输入
- 密码输入
- yes / no 确认
- 最终摘要展示

真正复杂的是 install/manage 的系统操作与状态边界，而不是终端渲染本身。

因此第一版交互层应刻意保持简单：

- 基于标准输入输出
- 基于 `golang.org/x/term` 做 TTY 检测与密码不回显
- 不做全屏界面
- 不做实时刷新
- 不做多面板
- 不做内嵌日志视图

### 交互层允许提供的能力

第一版只允许提供以下几类原语：

- `Select`
- `Input`
- `Password`
- `Confirm`
- `PrintSummary`

要求：

- 交互层只负责采集输入和展示摘要
- install/manage 的真正业务逻辑、权限处理、文件写入、systemd 操作不得写在交互组件内部
- 交互层返回结构化数据，业务层根据数据执行动作

### 为什么不用更重的 TUI 框架

本期不需要：

- 常驻式终端控制台
- 实时刷新状态面板
- 多栏 inspect
- 内嵌 journald 流
- 键盘快捷键驱动的复杂界面

如果未来 `manage` 演化成真正的终端管理面板，再单独评估是否升级到更重的 TUI 框架。

---

## 5. Server 初始化模型

这是取消 Web setup 之后最关键的设计。

## 5.1 初始化必填项

server 首次初始化至少需要：

- 管理员用户名
- 管理员密码
- 服务对外地址 / 域名
- 允许端口范围

说明：

- 管理员密码是敏感项
- 允许端口范围是业务安全边界的一部分
- 这些值属于“首次初始化输入”，不再通过 Web setup 提交

## 5.2 交互式安装时的初始化方式

`netsgo install server` 必须在安装过程中完成初始化信息采集。

安装流程里，程序应：

1. 询问 server 运行参数
2. 询问初始化参数
3. 在启动受管服务前，先把 server 初始状态写入 `<data-dir>/server/`
4. 然后再安装并启动 systemd 服务

这样做的结果是：

- 受管 server 首次启动时，已经是“已初始化”状态
- 不存在 setup token
- 不存在 `/setup` 引导页
- 不需要把管理员明文密码持久化到 `/etc/netsgo/services/server.env`

### 保留 server 数据后的重装语义

`uninstall server` 如果选择“保留运行数据”，则后续 `install server` 必须按 **恢复安装** 处理，而不是按全新初始化处理。

规则：

- 若发现 `<data-dir>/server/` 中存在有效且完整的 server 数据：
  - 视为“未安装但存在可恢复历史数据”
  - `install server` 在交互式 service-mode 下，必须先明确询问用户是否沿用现有数据
  - 若用户确认沿用：只重新写 unit / env 并启动服务
  - 若用户拒绝沿用：本期不支持在保留旧数据前提下直接改成全新初始化；应提示用户先清理旧 server 数据后再重新 install
  - **不得覆盖**现有管理员、server addr、allowed ports、tunnels、其他 server 状态
  - **不再要求**重新输入初始化参数
- 若发现 `<data-dir>/server/` 存在，但状态不完整、损坏或无法识别：
  - 必须拒绝安装
  - 清晰提示用户：先清理该目录，或回到卸载流程选择删除 server 数据后再重新安装

也就是说：

- `server` 的“已安装”与“保留了历史数据”不是同一件事
- 保留 server 数据的目的，是为了支持卸载服务定义后再恢复受管安装，而不是重新初始化一份全新的 server

## 5.3 直跑 / Docker 时的初始化方式

对于 `netsgo server` 直跑路径，允许通过 flags 或 env 在首次启动时完成初始化。

这些入口同时服务于：

- 本地 direct-run
- Docker 容器启动
- 其他非交互式脚本化场景

推荐支持：

| 初始化项 | flag | env | 说明 |
|---|---|---|---|
| 管理员用户名 | `--init-admin-username` | `NETSGO_INIT_ADMIN_USERNAME` | 仅首次初始化使用 |
| 管理员密码 | `--init-admin-password` | `NETSGO_INIT_ADMIN_PASSWORD` | 建议优先用 env |
| 服务对外地址 | `--init-server-addr` | `NETSGO_INIT_SERVER_ADDR` | 仅首次初始化使用 |
| 允许端口范围 | `--init-allowed-ports` | `NETSGO_INIT_ALLOWED_PORTS` | 例如 `80,443,10000-20000` |

规则：

- 如果 data dir 尚未初始化，以上参数必须齐全
- 缺失任意必填项，`netsgo server` 必须直接报错退出
- 如果 data dir 已初始化，以上参数应被忽略，而不是触发重复初始化

输入模式规则：

- **仅当执行 `netsgo server` 且目标 data dir 未初始化时**，才允许初始化补全问答
- 若当前进程的 stdin/stdout 均为 TTY：
  - 先读取 flags / env
  - 对缺失的初始化必填项，逐项交互询问
  - 补齐后再执行初始化与启动
- 若当前进程不是 TTY（例如 Docker 非交互启动、systemd、脚本重定向场景）：
  - 不得进入交互问答
  - 任一必填项缺失都必须直接失败退出
- 若目标 data dir 已初始化：
  - 不进入初始化问答
  - 忽略所有 `init-*` flags / env

安全建议：

- 管理员密码推荐使用交互输入或环境变量
- 不推荐把管理员密码长期写在明文命令行里

实现说明：

- 交互式 install 需要把上表中的值通过安装向导采集
- 非交互式 direct-run / Docker 需要把上表中的值通过 flags 或 env 注入
- 这两条路径最终都应落到同一套 server 初始化逻辑，而不是再保留一条 Web setup 特例

## 5.4 初始化范围

本期这套初始化前置设计只覆盖 **首次初始化**。

它不等于：

- install/manage 提供在线重配置
- install/manage 接管所有 Web 端业务设置

这个文档只规定：

- 不再使用 Web setup
- 首次初始化必须由 install / flags / env 完成

失败路径要求：

- 对于未初始化或初始化状态损坏的 server，进程必须在**绑定任何监听端口之前**完成初始化检查
- 若检查失败，必须直接退出，退出码非 0
- **不得**启动半初始化状态的服务
- **不得**再暴露 `/setup`
- service 模式下若发生这种失败，应让 unit 进入 failed，并在日志中明确指出问题出在 `<data-dir>/server/` 的初始化状态或缺失初始化参数

---

## 6. 统一路径模型

## 6.1 设计原则

引入统一概念：

# **data dir（运行数据根目录）**

所有运行时文件都从这个目录派生。

统一结构：

```text
<data-dir>/
  server/
  client/
  locks/
```

注意：

- 不再有 `logs/`
- 所有日志统一走 stdout/stderr

## 6.2 不同模式的默认 data dir

### 直跑模式默认 data dir

```text
$HOME/.local/state/netsgo
```

### systemd 服务模式默认 data dir

```text
/var/lib/netsgo
```

### Docker / 容器模式默认 data dir

```text
/var/lib/netsgo
```

说明：

- 这是“目录布局默认值”，不是“rootless 下总能直接写入”的保证
- 对 rootless 容器部署，更推荐显式指定可写路径

补充要求：

- 程序启动时如果目标目录不存在，必须自动创建
- `server/`、`client/`、`locks/` 子目录也应在首次使用时自动创建

## 6.3 固定派生目录

### Server

```text
<data-dir>/server/
```

建议承载：

- tunnels
- admin state
- traffic state
- TLS auto certs
- server 相关其他持久化文件

### Client

```text
<data-dir>/client/
```

建议承载：

- client state
- install id
- token / fingerprint 相关状态
- client 相关其他持久化文件

### Locks

```text
<data-dir>/locks/server.lock
<data-dir>/locks/client.lock
```

## 6.4 `--data-dir`

这里的 `data dir` 指的是：

- **运行数据根目录**

它与以下概念无关：

- root 用户
- sudo 权限
- systemd 的 root 管理策略

也就是说：

- `data dir` 是“数据放哪里”
- root / non-root 是“进程以什么权限运行”

为保持 direct-run 与 service-run 的结构一致，本期建议新增：

```bash
--data-dir <path>
```

适用于：

```bash
netsgo server --data-dir ...
netsgo client --data-dir ...
```

环境变量命名建议：

```bash
NETSGO_DATA_DIR=/some/path
```

这个值始终有默认值；只有在用户想覆盖默认目录时，才需要显式指定。

用途：

- 不传时使用各模式默认 data dir
- direct-run 可显式指定运行根目录
- Docker 可挂载该目录以持久化数据

本期 `install/manage` 不在交互菜单里暴露自定义受管 data dir；受管模式固定使用：

```text
/var/lib/netsgo
```

补充说明：

- **只有 service 模式的 install/manage 必须以 root 执行**
- direct-run 与 Docker 仍然支持非 root 运行
- 只要运行用户对目标 data dir 有写权限，就可以使用该目录

## 6.5 Docker rootless 支持

本期保留 Docker / 容器场景的 rootless 支持。

规则：

- 容器内可以使用非 root 用户运行 `netsgo server` 或 `netsgo client`
- 但运行用户必须对 data dir 可写

默认策略：

- Docker 默认 data dir 仍可使用 `/var/lib/netsgo`
- 但镜像必须确保该目录对容器运行用户可写
- 对 rootless 示例，文档应优先推荐显式设置，例如：`NETSGO_DATA_DIR=/data/netsgo`

如果镜像或部署方式不满足这个条件，则应显式指定：

- `--data-dir <writable-path>`
- 或 `NETSGO_DATA_DIR=<writable-path>`

因此：

- service 模式要求 root 管理
- Docker rootless 不受此限制
- 两者不要混为一谈

---

## 7. 本地单实例保护

## 7.1 保护目标

尽量阻止以下冲突：

- 已经有受管 server 在运行，又手动 `netsgo server`
- 已经有受管 client 在运行，又手动 `netsgo client`
- 同一 root 下重复拉起相同角色实例

## 7.2 机制

采用 **基于 data dir 的角色锁文件 + 内核级文件锁**：

```text
<data-dir>/locks/server.lock
<data-dir>/locks/client.lock
```

实现要求：

- 启动时打开对应 lock 文件
- 对该文件加排他锁（Linux 下使用 `flock`）
- 以非阻塞方式尝试获取
- 获取成功后，进程必须一直持有该文件描述符直到退出
- 获取失败则拒绝启动，并提示已有实例运行

关键点：

- **锁有效性由内核保证，而不是由“锁文件是否存在”保证**
- 即使进程崩溃，内核也会释放锁
- 不需要人工清理“残留锁文件”才能恢复启动

## 7.3 结果行为

### 同一 root 下

- 同角色重复启动会失败
- 这同时适用于：
  - direct-run
  - 受管 systemd 服务

### 不同 root 下

- technically 可以并存
- 但 `install/manage` 不暴露这种能力

### Docker / 容器场景

- 容器自己的 data dir 独立
- 锁不会影响其他容器
- 属于合理并存场景

---

## 8. systemd 受管安装模型

## 8.1 二进制安装路径

统一安装到：

```text
/usr/local/bin/netsgo
```

`netsgo install` 执行时：

- 若不存在：将当前运行的二进制 copy 到该路径
- 若已存在且与当前运行二进制为同一文件：跳过
- 若已存在但路径不同：覆盖，并在 UI 中告知用户

systemd unit 的 `ExecStart` 始终固定引用：

```text
/usr/local/bin/netsgo
```

## 8.2 运行用户与权限模型

### 安装 / 管理者

- `root`

负责：

- 写 `/usr/local/bin/netsgo`
- 写 `/etc/systemd/system/*.service`
- 写 `/etc/netsgo/services/*`
- 创建和删除 `/var/lib/netsgo`
- `systemctl daemon-reload / enable / start / stop / restart / disable`
- journald 查看与受管服务 inspect

### 服务进程用户

- `netsgo`

要求：

- 安装时自动创建系统用户 `netsgo`（若不存在）
- 无登录 shell
- 可写 `/var/lib/netsgo`

### 目录与文件权限

建议：

- `/var/lib/netsgo/`：`netsgo:netsgo`
- `/etc/netsgo/services/server.env`：`root:root`，`0600`
- `/etc/netsgo/services/client.env`：`root:root`，`0600`

说明：

- NetsGo 不维护单独的受管服务 JSON manifest；`/etc/netsgo/services/` 只保存运行环境文件。
- env 文件包含 server/client 运行参数，统一按 root-only 处理。

## 8.3 install env

建议放在：

```text
/etc/netsgo/services/server.env
/etc/netsgo/services/client.env
```

其中：

- `*.env` 用于保存运行时需要注入的环境变量，例如：
  - server：`NETSGO_PORT`、`NETSGO_TLS_MODE`、`NETSGO_TLS_CERT`、`NETSGO_TLS_KEY`、`NETSGO_TRUSTED_PROXIES`
  - client：`NETSGO_SERVER`、`NETSGO_KEY`、`NETSGO_TLS_SKIP_VERIFY`、`NETSGO_TLS_FINGERPRINT`

重要要求：

- `server.env` 里不应存放初始化用的管理员明文密码
- `netsgo manage` 从 systemd unit、env 文件、固定受管路径、binary、角色 runtime dir、server runtime DB（仅 server 强制要求）、systemd active/enabled 状态推导服务状态

## 8.4 unit 文件

```text
/etc/systemd/system/netsgo-server.service
/etc/systemd/system/netsgo-client.service
```

### server

```ini
[Unit]
Description=NetsGo Server
After=network-online.target
Wants=network-online.target

[Service]
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

### client

```ini
[Unit]
Description=NetsGo Client
After=network-online.target
Wants=network-online.target

[Service]
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

关键字段说明：

- `After=network-online.target`：确保网络就绪后再启动，对 client 建立出站连接尤其重要
- `Restart=on-failure`：异常退出后自动重启；正常 `stop` 不触发重启
- `RestartSec=5s`：重启间隔，避免快速循环崩溃
- `User=netsgo`：受管服务默认以低权限用户运行
- `NoNewPrivileges=true`：避免服务进程获得额外提权能力

## 8.5 日志模型

systemd 受管模式默认日志模型：

- 应用只写 stdout/stderr
- systemd / journald 接管日志
- 不写 `<data-dir>/logs/*.log`

查看方式统一为：

```bash
sudo journalctl -u netsgo-server.service -n 100 -f
sudo journalctl -u netsgo-client.service -n 100 -f
```

---

## 9. `netsgo install` 交互流程

## 9.0 权限检查

`install` 命令必须要求 root 权限。

另外，`install` 的交互问答只在 TTY 中受支持。

启动时行为：

- 检测当前是否 root
  - 若是：继续
  - 若否：以 `exec sudo <binary> install` 的方式整体重新以 root 启动当前进程
- 若无法获取 root 权限：
  - 输出清晰错误
  - 直接退出，退出码非 0
- 完成提权后，必须检查 stdin/stdout 是否为 TTY：
  - 若是：继续进入安装向导
  - 若否：直接报错退出，提示 `install` 只支持交互式终端，不支持在非 TTY 下运行

## 9.1 首屏

选择安装对象：

- 安装服务端
- 安装客户端
- 退出

## 9.2 安装前检查

程序必须先检查：

- 当前是否为 Linux + systemd 环境
- `/usr/local/bin/netsgo` 是否可写
- `/etc/systemd/system/` 是否可写
- `/etc/netsgo/services/` 是否可创建
- `/var/lib/netsgo/` 是否可创建
- 目标角色是否已安装

角色状态判定规则：

- **已安装**：对应 role 的 unit、env、runtime dir 存在，unit 内容匹配固定 layout，binary 可执行；server 角色额外要求 server runtime DB 存在
- **未安装但存在历史数据**：仅 server 支持；无 unit/env，但 server runtime DB 仍存在
- **损坏安装**：unit、env、binary、runtime dir 的组合不一致；server 角色还包括 server runtime DB 缺失或状态不完整

若目标角色已安装：

- 直接提示「已安装，请先卸载后重装」
- 不做覆盖式 install

若检测到“未安装但存在历史数据”：

- server：按“恢复安装”规则处理；在交互式 service-mode install 中必须询问是否沿用现有数据
- client：视为异常状态，默认拒绝安装，并提示用户先清理 `/var/lib/netsgo/client/` 后再重新 install；重新 install 必须重新认证，不使用残留旧数据

若检测到“损坏安装”：

- 不自动猜测修复
- 应明确提示用户进入 inspect / uninstall 处理

## 9.4 服务端安装表单

### 运行参数

- 监听端口（默认 `9527`）
- TLS 模式：
  - `off`
  - `auto`
  - `custom`
- 若为 `custom`：
  - 证书路径
  - 私钥路径
- 若为 `off` 且需要反代识别客户端地址：
  - trusted proxies（可选）

约束：

- 这里的“监听端口”是 NetsGo server 自身 Web / API / WebSocket 的监听端口
- 它与“允许端口范围”是两个不同概念
- 监听端口**不要求**属于允许端口范围
- 由于受管服务默认以 `User=netsgo` 运行，本期 `install server` 仅接受 `>=1024` 的监听端口
- 若用户需要 `80/443`，文档建议使用反向代理前置，而不是把受管服务改为 root 运行

TLS `custom` 模式额外要求：

- 证书路径和私钥路径必须在安装阶段就校验通过
- 安装程序必须验证：文件存在、是普通文件、且受管运行用户 `netsgo` 可读
- 若不满足，必须直接拒绝安装
- 本期不设计“自动把 custom 证书复制进受管目录再重写权限”的额外流程

### 初始化参数

- 管理员用户名（可默认 `admin`）
- 管理员密码（必须二次确认，不回显）
- 服务对外地址 / 域名
- 允许端口范围

允许端口范围建议支持：

- 单端口：`80`
- 多个端口：`80,443`
- 区间：`10000-20000`
- 混合：`80,443,10000-20000`

要求：

- 不提供 Web setup 兜底
- 不提供 setup token
- install 阶段必须把上述初始化信息处理完毕
- install 不得把管理员明文密码持久化到 `server.env` 或任何本地 manifest

## 9.5 客户端安装表单

字段：

- 服务端地址
- key
- TLS 跳过校验（可选）
- TLS 证书指纹（可选）

## 9.6 实际安装动作

### server

`install server` 应顺序执行：

1. 创建系统用户 `netsgo`（若不存在）
2. 创建 `/var/lib/netsgo/server`、`/var/lib/netsgo/locks`
3. 先完成 server 初始化，写入 `<data-dir>/server/` 中的初始状态
4. 调整 `/var/lib/netsgo` 属主为 `netsgo:netsgo`
5. 写入 `/etc/netsgo/services/server.env`
6. 安装 `/etc/systemd/system/netsgo-server.service`
7. `systemctl daemon-reload`
8. `systemctl enable --now netsgo-server.service`

### client

`install client` 应顺序执行：

1. 创建系统用户 `netsgo`（若不存在）
2. 创建 `/var/lib/netsgo/client`、`/var/lib/netsgo/locks`
3. 调整 `/var/lib/netsgo` 属主为 `netsgo:netsgo`
4. 写入 `/etc/netsgo/services/client.env`
5. 安装 `/etc/systemd/system/netsgo-client.service`
6. `systemctl daemon-reload`
7. `systemctl enable --now netsgo-client.service`

## 9.7 安装成功后输出

### server

输出至少包含：

- 服务名：`netsgo-server.service`
- 当前状态
- 管理面地址
- 运行用户：`netsgo`
- 查看日志命令：

```bash
sudo journalctl -u netsgo-server.service -n 100 -f
```

- 下一步提示：

```bash
sudo netsgo manage
```

### client

输出至少包含：

- 服务名：`netsgo-client.service`
- 当前状态
- server 地址
- 运行用户：`netsgo`
- 查看日志命令：

```bash
sudo journalctl -u netsgo-client.service -n 100 -f
```

- 下一步提示：

```bash
sudo netsgo manage
```

---

## 10. `netsgo manage` 交互流程

## 10.1 权限要求

`manage` 命令整体要求 root 权限。

另外，`manage` 的交互菜单只在 TTY 中受支持。

启动时行为：

- 检测当前是否 root
  - 若是：继续
  - 若否：以 `exec sudo <binary> manage` 的方式整体重新以 root 启动当前进程
- 若无法获取 root 权限：
  - 输出清晰错误
  - 直接退出，退出码非 0
- 完成提权后，必须检查 stdin/stdout 是否为 TTY：
  - 若是：继续进入管理菜单
  - 若否：直接报错退出，提示 `manage` 只支持交互式终端，不支持在非 TTY 下运行

注意：

- 这里不区分“只读操作”和“写操作”
- status / inspect / logs / uninstall 全部走 root 路径
- 这是受管模式的**产品规则**，不是当前实现细节

## 10.2 状态发现与损坏安装处理

`manage` 必须能区分以下状态：

- 已安装
- 未安装
- 未安装但存在 server 历史数据
- 损坏安装

处理规则：

- 已安装：进入正常管理菜单
- 未安装：提示去 `install`
- 未安装但存在 server 历史数据：
  - 允许 inspect
  - 明确提示这是“可恢复安装候选状态”
- 损坏安装：
  - 不自动修复
  - 明确标记为 broken
  - 至少允许 inspect 与 uninstall / cleanup

## 10.3 入口逻辑

### 情况 A：server 与 client 都已安装

先选择：

- 管理服务端
- 管理客户端
- 卸载全部受管服务
- 退出

### 情况 B：只安装了一个

直接进入该服务的管理菜单。

### 情况 C：一个都没安装

提示未检测到已安装服务，并提供：

- 进入 `netsgo install`
- 退出

## 10.4 管理菜单

进入后提供：

- status
- inspect
- logs
- start
- stop
- restart
- uninstall
- back

## 10.5 inspect 输出

### server inspect

至少包含：

- service name
- role
- installed / running / enabled
- binary path
- data dir
- data path
- lock path
- log target（固定为 `journald`）
- unit path
- env path
- run as user
- listen port
- TLS mode

### client inspect

至少包含：

- service name
- role
- installed / running / enabled
- binary path
- data dir
- data path
- lock path
- log target（固定为 `journald`）
- unit path
- env path
- run as user
- server url
- client identity state 摘要

要求：

- 不输出敏感明文
- 不输出 client key 明文
- 不输出管理员密码明文

## 10.6 logs 行为

`manage -> logs` 应直接进入对应 journald 查看命令，例如：

```bash
journalctl -u netsgo-server.service -n 100 -f
```

或：

```bash
journalctl -u netsgo-client.service -n 100 -f
```

因为 `manage` 已经在 root 下执行，所以这里不需要再次提示 sudo。

---

## 11. uninstall 语义

## 11.1 总原则

卸载语义按角色区分，不能再简单用“一刀切都删数据”的方式表达。

## 11.2 卸载 server

server 的运行数据具有保留价值，因此 `uninstall server` 必须给出显式二级选择：

### 选项 1

**移除服务，但保留运行数据**

行为：

- stop service
- disable service
- remove unit
- remove env
- 保留 `<data-dir>/server/`
- 保留共享二进制

### 选项 2

**移除服务并删除全部运行数据（不可恢复）**

行为：

- stop service
- disable service
- remove unit
- remove env
- remove `<data-dir>/server/`
- 保留共享二进制

## 11.3 卸载 client

client 的本地数据代表本地身份与状态，因此 `uninstall client` 采用更强语义：

- stop service
- disable service
- remove unit
- remove env
- remove `<data-dir>/client/`
- 保留共享二进制

必须明确提示：

- 重新 install 将视为新的 client 身份
- uninstall client 不负责通知服务端清理历史记录
- 如果未来又检测到“未安装但仍残留 client 数据”，应把它视为异常状态，而不是受支持的保留数据路径
- install client 遇到残留旧数据时，不得尝试恢复旧 token / 旧认证状态；必须要求用户按新的 client 安装流程重新认证

## 11.4 卸载全部受管服务

当 server 与 client 都已安装时，可以提供：

**卸载全部受管服务**

行为：

- 先分别执行 server / client 的卸载确认
- server 仍需要用户选择“保留数据”还是“删除数据”
- client 仍按“删除本地身份与状态”语义执行

## 11.5 共享二进制删除语义

`/usr/local/bin/netsgo` 是共享二进制，不属于某个单独角色。

因此：

- 删除 server 时，如果 client 仍安装：**不得删除二进制**
- 删除 client 时，如果 server 仍安装：**不得删除二进制**
- 只有当 **没有任何受管角色剩余** 时，才可以额外询问：

```text
未检测到其他受管角色。
是否同时删除共享二进制 /usr/local/bin/netsgo ?
```

## 11.6 删除前确认要求

删除前必须明确展示将删除的路径。

例如：

- `/etc/systemd/system/netsgo-server.service`
- `/etc/netsgo/services/server.env`
- `/var/lib/netsgo/server/`
- `/usr/local/bin/netsgo`（仅最后一个角色卸载时才可能出现）

要求：

- 删除范围必须是角色可解释、路径明确、不可歧义的
- 不允许基于模糊 home 推导去删除不确定路径

---

## 12. 直跑模式与受管模式的关系

## 12.1 不同职责

### 直跑模式

- 给 Docker / 自动化 / shell 使用
- 保留 flags / env 的常规能力
- 不依赖交互
- server 首次初始化由 flags / env 完成
- 支持非 root 运行

### install/manage 模式

- 给终端用户使用
- 封装 systemd 服务安装与管理
- 使用 root 统一管理
- server 首次初始化在 install 阶段完成
- 不用于 rootless 容器入口

## 12.2 路径关系

- 结构统一
- 默认 root 不同
- 都支持同一套角色目录结构和锁语义

## 12.3 冲突关系

- 若 direct-run 与 service-run 最终落到同一个 data dir 且同角色，则内核锁应阻止重复启动
- 若 root 不同，则技术上可共存，但这不属于 `install/manage` 的支持路径

## 12.4 日志关系

- 两种模式都只用 stdout/stderr
- service 模式通过 journald 查看
- Docker 通过容器日志查看
- 直跑通过当前终端、重定向或外部日志系统查看

---

## 13. 建议的默认路径

## 13.1 用户直跑默认

```text
$HOME/.local/state/netsgo/
```

例如：

```text
~/.local/state/netsgo/
  server/
  client/
  locks/
```

## 13.2 systemd / docker 默认

```text
/var/lib/netsgo/
```

例如：

```text
/var/lib/netsgo/
  server/
  client/
  locks/
```

选择理由：

- 放弃 `~/.netsgo` 这种旧式 home dot-dir 方案
- direct-run 走更规范的 XDG state 路径
- service / container 使用固定系统路径
- 目录结构保持一致
- 不引入日志文件目录

---

## 14. `netsgo update` 设计

本期 `netsgo update` 只保留占位命令，不提供真实自更新。

行为固定为输出提示：

```bash
自动更新功能尚未实现，请访问 https://github.com/zsio/netsgo
```

固定行为要求：

- 输出这段提示后直接 `exit 0`
- 不联网
- 不检查版本
- 不提权
- 不读取安装状态
- 不读写任何受管文件
- 不重启任何服务

本期明确不做：

- 下载源选择
- 版本比较
- checksum 校验
- 下载二进制
- 备份旧二进制
- 替换 `/usr/local/bin/netsgo`
- 自动重启已安装服务

也就是说：

- `update` 只是命令面占位
- 它不应让用户误以为已经支持自更新

---

## 15. 实施分期

## Phase 1：移除文件日志，统一 stdout/stderr

- 去掉应用日志文件模型
- direct-run / Docker / systemd 统一只写 stdout/stderr
- systemd 文档与提示统一改为 journald

## Phase 2：统一 data dir

- 引入 `--data-dir`
- server/client 改为从 data dir 派生数据和锁路径
- 移除运行时对 `~/.netsgo` 的硬编码依赖

## Phase 3：单实例保护

- 增加 `server.lock`
- 增加 `client.lock`
- 启动前用 `flock` 阻止同 root 下同角色重复启动

## Phase 4：取消 Web setup，改为初始化前置

- 移除 setup token 概念
- 移除 Web setup 流程依赖
- 为 `netsgo server` 增加 init flags / env
- 未初始化且参数不齐时直接失败退出

## Phase 5：systemd 受管运行模型

- 创建 `netsgo` 系统用户
- `/usr/local/bin/netsgo` 安装模型
- `/etc/netsgo/services/*.env`
- `netsgo-server.service` / `netsgo-client.service`
- journald 查看模型

## Phase 6：`netsgo install`

- 菜单与表单
- server 安装
- client 安装
- server 初始化前置到 install

## Phase 7：`netsgo manage`

- 自动发现
- status / inspect / logs / start / stop / restart / uninstall
- 删除路径确认提示
- server / client 差异化卸载语义

## Phase 8：`netsgo update` 占位命令与文档

- `netsgo update` 输出占位提示
- README / 下载文档更新

---

## 16. 验收要点

至少应覆盖以下验证：

### 16.1 安装与权限

- 普通用户执行 `netsgo install` 时会整体提权到 sudo
- 安装后 systemd unit、env、data dir 都在正确路径
- 受管服务以 `netsgo` 用户运行，而不是 root

### 16.2 初始化

- `install server` 不依赖 Web setup 即可完成首启
- `netsgo server` 在未初始化时，如果缺少 `init-*` 参数会失败退出
- `netsgo server` 在已初始化后，即使保留 `NETSGO_INIT_*` 也不会重复初始化
- Web setup 页面、API、setup token 与相关 flag/env 均被移除

### 16.3 日志

- 不生成应用日志文件
- systemd 模式下可通过 `journalctl -u ...` 查看日志
- Docker / 直跑模式仍只走 stdout/stderr

### 16.4 运行权限与 rootless

- service 模式下，`install/manage` 必须通过 root 路径执行
- service 进程本身以 `netsgo` 用户运行
- direct-run 支持非 root 运行
- Docker 支持 rootless 运行，只要 data dir 对运行用户可写

### 16.5 锁

- 同一 data dir 下不能同时跑两个同角色实例
- 进程被异常终止后锁会自动释放

### 16.6 卸载

- 卸载 server 时可以选择保留数据或删除数据
- 卸载 client 时会删除本地身份与状态
- 只卸载一个角色时不会误删共享二进制
- 只有最后一个角色卸载时才询问是否删除 `/usr/local/bin/netsgo`

### 16.7 manage

- `netsgo manage` 整体要求 root
- `status / inspect / logs / uninstall` 都通过 root 路径执行
- inspect 不输出敏感明文

---

## 17. 本期定稿结论

本期规划结论如下：

1. 采用 `netsgo install` + `netsgo manage` + `netsgo update` 三入口
2. 保留 `netsgo server` / `netsgo client` 直跑模式
3. server 与 client 均纳入交互式安装/管理范围
4. 取消 Web setup 与 setup token
5. server 首次初始化统一改为 install / flags / env 前置输入
6. 受管模式固定为 root 安装与 root 管理
7. 受管服务默认以低权限用户 `netsgo` 运行
8. 引入统一的 data dir 结构；它表示运行数据根目录，而不是 root 用户权限
9. 不再设计日志文件目录；全模式统一使用 stdout/stderr
10. systemd 受管模式默认通过 journald 查看日志
11. 通过内核级文件锁尽量防止本地重复实例
12. server 与 client 的卸载语义不同：
    - server：可选保留数据
    - client：默认删除本地身份与状态
13. 共享二进制统一安装到 `/usr/local/bin/netsgo`
14. 只有最后一个角色卸载时，才允许额外选择删除共享二进制
15. `install` / `manage` 非 root 时整体 `re-exec sudo`
16. Docker / direct-run 继续支持非 root 运行；service 模式才要求 root 管理
17. `update` 本期只保留占位提示，不实现真实升级逻辑
