# NetsGo `install` / `manage` 实施文档

## 文档状态

- 状态：可直接实施
- 规划来源：`docs/setup-manage-plan.md`
- 适用范围：Linux + systemd
- 目标读者：直接编码实施的人类工程师或 AI agent
- 文档目的：把已定稿规划翻译成**文件级执行说明**；执行者拿到本文后不再需要做新的架构决策

---

## 一、文档说明

### 1.1 使用方式

1. 先按本文的 **Phase 依赖顺序** 实施，不要跳 Phase。
2. 每个 Phase 完成后，先跑该 Phase 的验收命令，再进入下一阶段。
3. 如果实现中发现当前代码与本文快照不一致，先重新读取对应代码，再继续；不要靠猜。
4. 本项目尚未发布，不做 `~/.netsgo` → 新路径的兼容迁移，也不要保留旧 setup 路径兼容层。

### 1.2 执行约束

- 不新增“过渡态”接口；旧方案直接删除。
- 不把管理员明文密码写入任何持久化文件。
- 不为 Phase 4 保留 `/setup`、`/api/setup/*`、`setup token` 兼容壳。
- `install` / `manage` 的新 Cobra 子命令必须沿用当前仓库的文件布局：**每个命令一个 `cmd_*.go` 文件，通过 `init()` 自注册**，不要把注册逻辑堆到 `main.go`。
- 如果本地没有 `web/dist`，Go 构建/测试前先执行 `cd web && bun run build`，或在只验证后端逻辑时使用 `-tags dev`。

---

## 二、现状快照（基于当前代码）

### 2.1 CLI 入口现状

- `cmd/netsgo/main.go`
  - 只负责 `rootCmd.Execute()`。
  - 设置 `NETSGO_` 环境变量前缀与 `-` → `_` 的 Viper 映射。
- 现有子命令采用“**单命令单文件，自身 `init()` 注册**”模式：
  - `cmd/netsgo/cmd_server.go`
  - `cmd/netsgo/cmd_client.go`
  - `cmd/netsgo/cmd_docs.go`
  - `cmd/netsgo/cmd_benchmark.go`
- 当前**还没有** `install` / `manage` / `update` 命令文件。

### 2.2 当前运行路径模型

当前运行时仍然把数据路径硬编码到 `~/.netsgo`：

- `pkg/logger/logger.go`
  - `DefaultDir()` → `~/.netsgo/logs`
- `internal/server/server_bootstrap.go`
  - `initStore()`
    - `tunnels.json` 默认在 `~/.netsgo/tunnels.json`
    - `admin.json` 默认在 `~/.netsgo/admin.json`
    - `traffic.json` 默认在 `s.getDataDir()/traffic.json`
  - `getDataDir()` → 默认返回 `~/.netsgo`
- `internal/client/state.go`
  - `defaultStatePath()` → `~/.netsgo/client.json`
- `internal/server/tls.go`
  - `TLS auto` 默认目录是 `filepath.Join(dataDir, "tls")`
  - 目前 `dataDir` 来自 `server_bootstrap.go` 的 `s.getDataDir()`，因此实际落点是 `~/.netsgo/tls`

### 2.3 当前核心结构体

当前 `Server` 与 `Client` 仍是“具体文件路径”模型，还没有统一的 `DataDir` 字段：

```go
// internal/server/server.go
type Server struct {
    Port                        int
    StorePath                   string
    SetupToken                  string
    AllowLoopbackManagementHost bool
    TLS                         *TLSConfig
    TLSFingerprint              string
    // ...
}

// internal/client/client.go
type Client struct {
    ServerAddr     string
    Key            string
    Token          string
    InstallID      string
    StatePath      string
    TLSSkipVerify  bool
    TLSFingerprint string
    // ...
}
```

目标状态不要继续保留 `StorePath` / `StatePath` 作为运行入口；Phase 2 统一改成 `DataDir` 根目录模型，再由内部派生具体文件路径。

### 2.4 当前日志模型

- `cmd/netsgo/cmd_server.go`
  - 启动时调用 `logger.DefaultDir()` → `logger.Init("server", logDir)` → `defer logger.Close()`
- `cmd/netsgo/cmd_client.go`
  - 启动时调用 `logger.DefaultDir()` → `logger.Init("client", logDir)` → `defer logger.Close()`
- `pkg/logger/logger.go`
  - 提供按日期 + 大小轮转的文件日志
  - 同时 `stderr + 文件` 双写
- `internal/server/server_logging_test.go`
  - 当前还在验证“setup token 不进入文件日志”

这套日志模型在 Phase 1 整体删除。

### 2.5 当前 Web setup 模型

后端仍保留完整的 setup 流程：

- `internal/server/admin_api.go`
  - `handleSetupStatus`
  - `handleSetupInit`
- `internal/server/server_http.go`
  - 注册 `GET /api/setup/status`
  - 注册 `POST /api/setup/init`
- `internal/server/http_tunnel_proxy.go`
  - `allowSetupRequest()` 允许未初始化时放行 `/`、`/assets/*`、`/api/setup/*`
- `internal/server/server_bootstrap.go`
  - 在 `!IsInitialized()` 时生成/使用 `SetupToken`
  - `emitSetupTokenBanner(os.Stderr)` 输出初始化 token
- `internal/server/auth_service.go`
  - 还持有 `setupLimiter` 与 `setupToken`

前端仍保留 setup 页面与类型：

- `web/src/routes/setup.tsx`
- `web/src/lib/auth.ts`
  - `fetchSetupStatus()`
  - 多处未初始化时 `redirect({ to: '/setup' })`
- `web/src/lib/router.ts`
  - 仍注册 `setupRoute`
- `web/src/types/index.ts`
  - `SetupStatus`
  - `SetupRequest`
  - `SetupResponse`
- `web/src/hooks/use-event-stream.ts`
  - `pathname !== '/setup' && pathname !== '/login'`

README、e2e 脚本与测试仍依赖 setup token / setup API：

- `README.md`
- `test/e2e/scripts/bootstrap.sh`
- `test/e2e/docker-compose.stack.yml`
- `test/e2e/proxy_e2e_test.go`
- `test/e2e/compose_stack_e2e_test.go`
- 多个 `internal/server/*_test.go`

### 2.6 `IsInitialized()` 现状

- 定义位置：`internal/server/admin_store.go` 的 `func (s *AdminStore) IsInitialized() bool`
- 生产调用点包括：
  - `internal/server/server_bootstrap.go`
  - `internal/server/auth_middleware.go`
  - `internal/server/control_auth.go`
  - `internal/server/proxy.go`
  - `internal/server/http_tunnel_proxy.go`
  - `internal/server/tunnel_restore.go`

Phase 4 **保留** `IsInitialized()` 及其业务语义，只删除 Web setup 与 setup token 流程。

---

## 三、目标状态与新增包设计

### 3.1 目标目录结构

统一的运行数据根目录是 `data dir`，最终布局固定为：

```text
<data-dir>/
  server/
    tunnels.json
    admin.json
    traffic.json
    tls/
  client/
    client.json
  locks/
    server.lock
    client.lock
```

说明：

- `TLS auto` 证书目录最终必须是 `<data-dir>/server/tls/`，不是 `<data-dir>/tls/`。
- `StorePath` / `StatePath` 不再作为外部配置入口；运行时代码统一只接收 `DataDir`。
- 受管服务 spec / env 不放在 data dir，固定放在 `/etc/netsgo/services/`。

### 3.2 新增文件与包结构

按当前仓库风格，新增文件布局固定如下：

```text
cmd/netsgo/
  cmd_install.go
  cmd_manage.go
  cmd_update.go

pkg/
  datadir/
    datadir.go
  flock/
    flock.go
    flock_stub.go

internal/
  server/
    init.go
  svcmgr/
    spec.go
    env.go
    unit.go
    systemd.go
    user.go
    binary.go
    state.go
  tui/
    tui.go
  install/
    install.go
    server.go
    client.go
  manage/
    manage.go
    server.go
    client.go
```

### 3.3 新增包职责

- `pkg/datadir`
  - 只负责“默认 data dir 的选择”
  - 不在这里堆大量业务路径 helper
- `pkg/flock`
  - 只负责 Linux 文件锁与非 Linux stub
- `internal/server/init.go`
  - 只负责“未初始化 server 的一次性初始化”
- `internal/svcmgr`
  - 负责受管服务的 spec/env/unit/systemd/user/binary/state
- `internal/tui`
  - 负责轻量交互原语，不承担业务逻辑
- `internal/install`
  - 负责交互式安装流程
- `internal/manage`
  - 负责交互式管理流程

### 3.4 关键数据结构（固定）

```go
// internal/server/init.go
type InitParams struct {
    AdminUsername string
    AdminPassword string
    ServerAddr    string
    AllowedPorts  string
}

func (p InitParams) IsComplete() bool {
    return p.AdminUsername != "" &&
        p.AdminPassword != "" &&
        p.ServerAddr != "" &&
        p.AllowedPorts != ""
}
```

```go
// internal/svcmgr/spec.go
type ServiceSpec struct {
    Role        string `json:"role"`
    ServiceName string `json:"service_name"`
    BinaryPath  string `json:"binary_path"`
    DataDir     string `json:"data_dir"`
    UnitPath    string `json:"unit_path"`
    EnvPath     string `json:"env_path"`
    SpecPath    string `json:"spec_path"`
    RunAsUser   string `json:"run_as_user"`
    InstalledAt string `json:"installed_at"`
    ListenPort  int    `json:"listen_port,omitempty"`
    TLSMode     string `json:"tls_mode,omitempty"`
    ServerURL   string `json:"server_url,omitempty"`
}
```

要求：

- `Role` 固定为 `server` / `client`
- `InstalledAt` 固定写 RFC3339 字符串
- `SpecPath` 固定指向 `/etc/netsgo/services/server.json` 或 `/etc/netsgo/services/client.json`

```go
// internal/svcmgr/state.go
type InstallState int

const (
    StateNotInstalled InstallState = iota
    StateInstalled
    StateHistoricalDataOnly
    StateBroken
)
```

状态解释：

- `StateInstalled`：unit + spec + env 一致存在，且 data dir 状态匹配
- `StateHistoricalDataOnly`：仅 `server` 允许；表示未安装但保留了可恢复的 server 数据
- `StateBroken`：文件组合不一致，或出现不支持的残留状态

---

## 四、Phase 依赖关系与执行顺序

推荐执行顺序：

```text
Phase 1  日志统一 stdout/stderr
    ↓
Phase 2  统一 data dir 与路径派生
    ↓
Phase 3  文件锁单实例保护
    ↓
Phase 4  删除 Web setup，改为初始化前置
    ↓
Phase 5  systemd 受管运行模型（svcmgr）
    ↓
Phase 6  netsgo install
    ↓
Phase 7  netsgo manage
    ↓
Phase 8  netsgo update + README / e2e 文档收尾
```

依赖说明：

- Phase 3 依赖 Phase 2：锁文件路径来自 `<data-dir>/locks/`
- Phase 4 依赖 Phase 2：初始化必须写入 `<data-dir>/server/`
- Phase 5 依赖 Phase 2 与 Phase 3：受管服务固定跑在 `/var/lib/netsgo` 且需要锁语义
- Phase 6 依赖 Phase 4 与 Phase 5：安装 server 时需要先 `ApplyInit`，再写 systemd 资源
- Phase 7 依赖 Phase 5：管理功能建立在 `svcmgr` 之上
- Phase 8 功能代码独立，但 README / e2e 的收尾最好在 Phase 4 之后统一完成

---

## 五、分 Phase 变更地图

### Phase 1：移除文件日志，统一 stdout/stderr

**新增文件**

- 无

**修改文件**

- `cmd/netsgo/cmd_server.go`
  - 删除 `netsgo/pkg/logger` import
  - 删除 `logger.DefaultDir()`、`logger.Init()`、`logger.Close()`
  - 启动最前面改为只初始化 stderr 日志输出，例如：
    - `slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)))`
  - 保留现有 `log.Printf` 调用；标准库 `log` 继续通过默认 logger 走 stderr
- `cmd/netsgo/cmd_client.go`
  - 同上

**删除文件 / 删除代码**

- 删除整个 `pkg/logger/`
  - `pkg/logger/logger.go`
  - `pkg/logger/logger_test.go`
- 删除 `internal/server/server_logging_test.go`
  - 该测试只服务于“setup token 不进入文件日志”，文件日志删除后它已失去意义

**跟随修改**

- 清理所有 `pkg/logger` import
- 删除任何对 `DefaultDir()` / `logger.Init()` / `logger.Close()` 的引用

**验收命令**

```bash
go test -tags dev ./cmd/netsgo/... ./internal/server/...
grep -R "pkg/logger\|logger.Init\|logger.Close\|DefaultDir()" .
```

`grep` 最终不应再返回运行时代码引用。

### Phase 2：统一 data dir

**新增文件**

- `pkg/datadir/datadir.go`
  - 暴露 `func DefaultDataDir() string`
  - 规则固定为：
    1. 正常 direct-run 默认 `$HOME/.local/state/netsgo`
    2. `INVOCATION_ID` 非空时返回 `/var/lib/netsgo`
  - Docker / rootless 场景通过显式 `--data-dir` / `NETSGO_DATA_DIR` 覆盖；不要额外做容器探测分支

**修改文件**

- `cmd/netsgo/cmd_server.go`
  - 新增 `--data-dir`
  - 绑定 `NETSGO_DATA_DIR`
  - 创建 `server.New(port)` 后写入 `s.DataDir`
- `cmd/netsgo/cmd_client.go`
  - 新增 `--data-dir`
  - 绑定 `NETSGO_DATA_DIR`
  - 创建 `client.New(serverAddr, key)` 后写入 `c.DataDir`
- `internal/server/server.go`
  - 删除 `StorePath string`
  - 新增 `DataDir string`
- `internal/client/client.go`
  - 删除 `StatePath string`
  - 新增 `DataDir string`
- `internal/server/server_bootstrap.go`
  - 删除旧的 `StorePath` 派生逻辑
  - 把当前 `getDataDir()` 重写为返回统一 data dir 根目录
  - 新增内部路径派生逻辑：
    - `<data-dir>/server/tunnels.json`
    - `<data-dir>/server/admin.json`
    - `<data-dir>/server/traffic.json`
    - `<data-dir>/server/tls/`
  - 启动时自动创建 `server/` 子目录
- `internal/server/tls.go`
  - `auto` 模式默认目录改为 `<data-dir>/server/tls/`
- `internal/client/state.go`
  - 用 `c.DataDir` 派生 `<data-dir>/client/client.json`
  - 自动创建 `client/` 子目录
- `internal/server/console_api.go`
  - `getStorePath()` 改为返回派生后的 `tunnels.json` 实际路径
  - 依赖 `filepath.Dir(s.getStorePath())` 的磁盘统计逻辑同步指向新的 server 数据目录
- 测试与 e2e 跟随更新：
  - `e2e_test.go`
  - `test/e2e/proxy_e2e_test.go`
  - `internal/server/server_test.go`
  - `internal/client/client_tls_test.go`
  - 其他直接写 `StorePath` / `StatePath` 的测试

**删除文件 / 删除代码**

- 删除运行时对 `~/.netsgo` 的硬编码

**固定目标路径**

```text
<data-dir>/server/tunnels.json
<data-dir>/server/admin.json
<data-dir>/server/traffic.json
<data-dir>/server/tls/
<data-dir>/client/client.json
<data-dir>/locks/server.lock
<data-dir>/locks/client.lock
```

**验收命令**

```bash
go test -tags dev ./internal/server/... ./internal/client/...
NETSGO_DATA_DIR=/tmp/netsgo-phase2 go run -tags dev ./cmd/netsgo server --port 18080
```

手工确认 `/tmp/netsgo-phase2/server/` 下出现 `tunnels.json`、`admin.json`、`traffic.json`，且没有新的 `~/.netsgo` 运行时写入。

### Phase 3：本地单实例保护

**新增文件**

- `pkg/flock/flock.go`
  - `//go:build linux`
  - `func TryLock(path string) (unlock func(), err error)`
  - 语义：非阻塞获取排他锁，失败时返回错误
- `pkg/flock/flock_stub.go`
  - `//go:build !linux`
  - 返回 no-op `unlock`

**修改文件**

- `cmd/netsgo/cmd_server.go`
  - 在真正启动前：
    1. 创建 `<data-dir>/locks/`
    2. 获取 `<data-dir>/locks/server.lock`
    3. 失败则打印清晰错误并退出非 0
  - `unlock` 生命周期保持到进程退出
- `cmd/netsgo/cmd_client.go`
  - 同理，对 `<data-dir>/locks/client.lock` 加锁

**删除文件 / 删除代码**

- 无

**验收命令**

```bash
go test -tags dev ./pkg/flock/...
NETSGO_DATA_DIR=/tmp/netsgo-lock go run -tags dev ./cmd/netsgo server --port 18080 &
NETSGO_DATA_DIR=/tmp/netsgo-lock go run -tags dev ./cmd/netsgo server --port 18081
```

第二个同角色实例必须直接失败，并提示已有实例运行。

### Phase 4：取消 Web setup，改为初始化前置

**新增文件**

- `internal/server/init.go`
  - `InitParams`
  - `func (p InitParams) IsComplete() bool`
  - `func ApplyInit(dataDir string, params InitParams) error`

`ApplyInit` 的固定职责：

1. 确保 `<data-dir>/server/` 存在
2. 打开 `<data-dir>/server/admin.json`
3. 复用 `AdminStore.Initialize(...)` 完成一次性初始化
4. 解析 `AllowedPorts string` 为现有 `[]PortRange`
5. 已初始化时静默忽略，不重复初始化

**修改文件**

- `cmd/netsgo/cmd_server.go`
  - 删除 `--setup-token`
  - 保留现有 `--server-addr` / `NETSGO_SERVER_ADDR` 运行时覆盖能力
  - 新增初始化 flags / env：
    - `--init-admin-username` / `NETSGO_INIT_ADMIN_USERNAME`
    - `--init-admin-password` / `NETSGO_INIT_ADMIN_PASSWORD`
    - `--init-server-addr` / `NETSGO_INIT_SERVER_ADDR`
    - `--init-allowed-ports` / `NETSGO_INIT_ALLOWED_PORTS`
  - 启动顺序改为：
    1. 先解析 data dir
    2. 先检查是否已初始化
    3. 若未初始化：
       - 先读 flags / env
       - 若 stdin/stdout 是 TTY，则对缺失项做交互补全
       - 若不是 TTY 且缺字段，直接失败退出
       - 参数齐全后先调用 `ApplyInit`
    4. 只有初始化成功后才允许继续 `s.Start()`
  - **必须在绑定监听端口之前完成以上检查**
- `internal/server/server_bootstrap.go`
  - 删除 `SetupToken` 生成逻辑
  - 删除 `emitSetupTokenBanner()` 与调用点
- `internal/server/auth_service.go`
  - 删除 `setupLimiter`
  - 删除 `setupToken`
- `internal/server/admin_api.go`
  - 删除 `handleSetupStatus`
  - 删除 `handleSetupInit`
- `internal/server/server_http.go`
  - 删除 `/api/setup/status`
  - 删除 `/api/setup/init`
- `internal/server/http_tunnel_proxy.go`
  - 删除 `allowSetupRequest()`
  - 删除对 `/api/setup/*` 与 `/setup` 的放行语义
- 前端删除与跟随修改：
  - 删除 `web/src/routes/setup.tsx`
  - 修改 `web/src/lib/router.ts`，移除 `setupRoute`
  - 修改 `web/src/lib/auth.ts`，删除 `fetchSetupStatus()` 与所有 `/setup` redirect
  - 修改 `web/src/hooks/use-event-stream.ts`，移除 `pathname !== '/setup'` 条件
  - 修改 `web/src/types/index.ts`，删除 `SetupStatus` / `SetupRequest` / `SetupResponse`
- 测试与 e2e 跟随修改：
  - 删除或重写所有 `/api/setup/*` 相关测试
  - `internal/server/admin_api_test.go`
  - `internal/server/security_fix_test.go`
  - `internal/server/rate_limit_integration_test.go`
  - `test/e2e/proxy_e2e_test.go`
  - `test/e2e/compose_stack_e2e_test.go`
  - `test/e2e/scripts/bootstrap.sh`
  - `test/e2e/docker-compose.stack.yml`

**删除文件 / 删除代码**

- 删除 setup token 概念
- 删除 Web setup 页面
- 删除 setup API
- 删除 setup 速率限制器

**固定行为**

- `IsInitialized()` 保留不动
- 未初始化 + 参数不齐：直接失败退出
- 已初始化 + 再给 `init-*`：忽略，不重复初始化
- 不新增新的公开 setup/status 兼容端点给前端或 e2e 使用

**验收命令**

```bash
go test -tags dev ./internal/server/...
NETSGO_INIT_ADMIN_USERNAME=admin \
NETSGO_INIT_ADMIN_PASSWORD=Password123 \
NETSGO_INIT_SERVER_ADDR=http://127.0.0.1:18080 \
NETSGO_INIT_ALLOWED_PORTS=10000-10010 \
go run -tags dev ./cmd/netsgo server --port 18080 --data-dir /tmp/netsgo-phase4
curl -i http://127.0.0.1:18080/api/setup/status
```

`/api/setup/status` 必须返回 `404`；首次启动后应直接进入正常登录流，而不是 setup 流。

### Phase 5：systemd 受管运行模型（`internal/svcmgr`）

**新增文件**

- `internal/svcmgr/spec.go`
- `internal/svcmgr/env.go`
- `internal/svcmgr/unit.go`
- `internal/svcmgr/systemd.go`
- `internal/svcmgr/user.go`
- `internal/svcmgr/binary.go`
- `internal/svcmgr/state.go`

**修改文件**

- 无业务入口修改；本 Phase 先把通用库落好

**固定输出路径**

- server spec：`/etc/netsgo/services/server.json`
- server env：`/etc/netsgo/services/server.env`
- client spec：`/etc/netsgo/services/client.json`
- client env：`/etc/netsgo/services/client.env`
- server unit：`/etc/systemd/system/netsgo-server.service`
- client unit：`/etc/systemd/system/netsgo-client.service`
- binary：`/usr/local/bin/netsgo`

**固定 unit 内容要求**

- server：`ExecStart=/usr/local/bin/netsgo server --data-dir /var/lib/netsgo`
- client：`ExecStart=/usr/local/bin/netsgo client --data-dir /var/lib/netsgo`
- `User=netsgo`
- `Group=netsgo`
- `Restart=on-failure`
- `RestartSec=5s`
- `NoNewPrivileges=true`

**固定 env 内容要求**

- `server.env` 只保存运行参数，如：
  - `NETSGO_PORT`
  - `NETSGO_TLS_MODE`
  - `NETSGO_TLS_CERT`
  - `NETSGO_TLS_KEY`
  - `NETSGO_TRUSTED_PROXIES`
  - `NETSGO_SERVER_ADDR`
  - `NETSGO_ALLOW_LOOPBACK_MANAGEMENT_HOST`
- `client.env` 保存：
  - `NETSGO_SERVER`
  - `NETSGO_KEY`
  - `NETSGO_TLS_SKIP_VERIFY`
  - `NETSGO_TLS_FINGERPRINT`
- **禁止**把 `NETSGO_INIT_ADMIN_PASSWORD` 或其他初始化明文持久化进去

**删除文件 / 删除代码**

- 无

**验收命令**

```bash
go test -tags dev ./internal/svcmgr/...
```

### Phase 6：新增 `netsgo install`

**新增文件**

- `cmd/netsgo/cmd_install.go`
- `internal/tui/tui.go`
- `internal/install/install.go`
- `internal/install/server.go`
- `internal/install/client.go`

**修改文件**

- `cmd/netsgo/cmd_install.go`
  - 沿用现有 Cobra 风格，在 `init()` 中 `rootCmd.AddCommand(installCmd)`
- `internal/tui/tui.go`
  - 提供固定 5 个原语：
    - `Select`
    - `Input`
    - `Password`
    - `Confirm`
    - `PrintSummary`
  - `Password` 与 TTY 检测使用 `golang.org/x/term`
- `internal/install/install.go`
  - 做安装入口、角色选择、root / TTY / Linux + systemd 检查
- `internal/install/server.go`
  - server 安装固定顺序：
    1. root 检查，不满足则 `syscall.Exec("sudo", ...)`
    2. TTY 检查
    3. 采集 server 运行参数 + 初始化参数
    4. 调用 `svcmgr.EnsureUser("netsgo")`
    5. 创建 `/var/lib/netsgo/server` 与 `/var/lib/netsgo/locks`
    6. 先 `ApplyInit("/var/lib/netsgo", params)`
    7. 再写 `server.json` / `server.env` / unit
    8. `systemctl daemon-reload`
    9. `systemctl enable --now netsgo-server.service`
- `internal/install/client.go`
  - client 安装固定顺序：
    1. root 检查，不满足则 `syscall.Exec("sudo", ...)`
    2. TTY 检查
    3. 采集 client 运行参数
    4. 创建用户与目录
    5. 写 `client.json` / `client.env` / unit
    6. `systemctl daemon-reload`
    7. `systemctl enable --now netsgo-client.service`

**删除文件 / 删除代码**

- 无

**固定行为**

- `install` 只支持交互式 TTY
- 非 root 时必须整体 `re-exec sudo`
- `server` 的初始化在 install 阶段直接完成，不依赖 service 首次启动
- 检测到 server 历史数据且完整时，按“恢复安装”处理；在交互式 service-mode install 中必须先询问用户是否沿用现有数据；若用户拒绝，则提示其先清理旧 server 数据后再重新 install
- client 遇到残留旧数据时，一律按异常状态处理；不得恢复旧 token / 旧认证状态，重新 install 必须重新认证

**验收命令**

```bash
go build -tags dev ./cmd/netsgo
sudo ./netsgo install
```

手工验收点：

- 进入安装角色选择菜单
- 非 root 执行时会整体提权
- server 安装完成后不需要 `/setup`

### Phase 7：新增 `netsgo manage`

**新增文件**

- `cmd/netsgo/cmd_manage.go`
- `internal/manage/manage.go`
- `internal/manage/server.go`
- `internal/manage/client.go`

**修改文件**

- `cmd/netsgo/cmd_manage.go`
  - 沿用 `init()` 自注册风格
- `internal/manage/manage.go`
  - 做 root / TTY 检查
  - 调用 `svcmgr` 检测 server/client 状态
- `internal/manage/server.go`
  - 提供 `status / inspect / logs / start / stop / restart / uninstall`
  - uninstall 必须支持：
    - 仅移除服务，保留 `<data-dir>/server/`
    - 移除服务并删除 `<data-dir>/server/`
- `internal/manage/client.go`
  - 提供同样的管理动作
  - uninstall 固定删除 `<data-dir>/client/`

**删除文件 / 删除代码**

- 无

**固定行为**

- `manage` 整体要求 root
- `logs` 不做内嵌 TUI，直接转给 `journalctl -u ... -n 100 -f`
- 最后一个角色卸载时，才允许额外询问是否删除 `/usr/local/bin/netsgo`
- 本期不删除 `netsgo` 系统用户

**验收命令**

```bash
go build -tags dev ./cmd/netsgo
sudo ./netsgo manage
```

手工验收点：

- 未安装时提示去 `install`
- 已安装时可进入 `status / inspect / logs / uninstall`
- inspect 不输出管理员密码或 client key 明文

### Phase 8：新增 `netsgo update` 占位命令，并收尾文档 / e2e

**新增文件**

- `cmd/netsgo/cmd_update.go`

**修改文件**

- `cmd/netsgo/cmd_update.go`
  - 输出固定提示并 `exit 0`
- `README.md`
  - 删除 setup token、`/setup`、`~/.netsgo`、旧 Docker 初始化说明
  - 改成 `NETSGO_INIT_*` 与新 data dir / journald / install/manage 说明
- `test/e2e/scripts/bootstrap.sh`
  - 改成通过 `NETSGO_INIT_*` 启动 server
  - 不再依赖 `/api/setup/status` 与 `/api/setup/init`
- `test/e2e/docker-compose.stack.yml`
  - 改用 `NETSGO_INIT_*`
  - 改用 `/var/lib/netsgo`
- 其他 README / 文档 / e2e 里对旧 setup 与旧数据路径的引用全部替换

**删除文件 / 删除代码**

- 无额外删除；本 Phase 的重点是收尾与对外文档一致性

**固定提示文案**

```text
自动更新功能尚未实现，请访问 https://github.com/zsio/netsgo
```

**验收命令**

```bash
go run -tags dev ./cmd/netsgo update
```

必须输出固定提示并返回 0。

---

## 六、固定路径速查表

| 路径 | 用途 |
|---|---|
| `$HOME/.local/state/netsgo/` | direct-run 默认 data dir |
| `/var/lib/netsgo/` | systemd 受管模式固定 data dir |
| `<data-dir>/server/` | server 运行数据目录 |
| `<data-dir>/server/tunnels.json` | 隧道配置 |
| `<data-dir>/server/admin.json` | 管理员与初始化状态 |
| `<data-dir>/server/traffic.json` | 流量历史 |
| `<data-dir>/server/tls/` | auto TLS 证书目录 |
| `<data-dir>/client/client.json` | client 本地身份与状态 |
| `<data-dir>/locks/server.lock` | server 单实例锁 |
| `<data-dir>/locks/client.lock` | client 单实例锁 |
| `/usr/local/bin/netsgo` | 共享二进制安装路径 |
| `/etc/netsgo/services/server.json` | server 受管 spec |
| `/etc/netsgo/services/server.env` | server 受管 env |
| `/etc/netsgo/services/client.json` | client 受管 spec |
| `/etc/netsgo/services/client.env` | client 受管 env |
| `/etc/systemd/system/netsgo-server.service` | server unit |
| `/etc/systemd/system/netsgo-client.service` | client unit |

---

## 七、已知风险与注意事项

1. **`IsInitialized()` 不删除。** Phase 4 只删除 Web setup 与 setup token；现有业务依赖的初始化判定仍然保留。
2. **不要保留 `StorePath` / `StatePath` 入口。** Phase 2 直接切到 `DataDir` 根目录模型，避免新旧路径语义并存。
3. **`TLS auto` 目录要跟着新结构走。** 最终目录是 `<data-dir>/server/tls/`，不是 `<data-dir>/tls/`。
4. **`server-addr` 与 `init-server-addr` 不是一回事。** 前者是现有运行时配置覆盖；后者只服务于未初始化 data dir 的一次性初始化。
5. **`ApplyInit` 必须复用现有 `AdminStore.Initialize(...)`。** 不要新写一套 bcrypt / JWT secret / admin.json 持久化逻辑。
6. **未初始化失败必须发生在 `net.Listen` 之前。** 不能再启动“半初始化”的 server，更不能再暴露 `/setup`。
7. **`server.env` / `server.json` 不得写入明文管理员密码。** install server 必须先执行 `ApplyInit`，再写 env/spec。
8. **`pkg/flock` 必须有 Linux build tag 与非 Linux stub。** 不要把 Linux `flock` 逻辑直接塞进通用文件。
9. **`install` / `manage` 的提权必须用 `syscall.Exec`。** 不要用 `exec.Command("sudo", ...).Run()` 再回到原进程。
10. **CLI 新命令文件要遵循当前模式。** 新增 `cmd_install.go` / `cmd_manage.go` / `cmd_update.go`，不要把命令逻辑直接写进 `main.go`。
11. **删除 setup 时，后端放行逻辑也要一起删。** `http_tunnel_proxy.go` 的 `allowSetupRequest()` 不能遗漏。
12. **前端 setup 类型与路由要一起删。** 仅删 `setup.tsx` 不够，`router.ts`、`auth.ts`、`types/index.ts`、`use-event-stream.ts` 都要同步清理。
13. **README 与 e2e 现在仍引用旧流程。** Phase 4/8 后必须同步改掉，否则文档与测试会继续把人带回 setup token 路径。
14. **spec 文件固定放 `/etc/netsgo/services/*.json`。** 不要把 `service.json` 放到 data dir 里。
15. **client 的残留数据不是“历史数据恢复”能力。** 只有 server 支持 `StateHistoricalDataOnly`；client 残留数据默认视为 broken / cleanup required，且重新 install 必须重新认证。
16. **本期不删除共享二进制的默认条件很严格。** 只有最后一个角色卸载时才允许询问是否删除 `/usr/local/bin/netsgo`。
17. **本期不删除 `netsgo` 系统用户。** 规划没有要求回收系统用户，避免把卸载语义扩大化。

---

## 八、各 Phase 验收命令速查

### 8.1 分阶段命令

```bash
# Phase 1
go test -tags dev ./cmd/netsgo/... ./internal/server/...

# Phase 2
go test -tags dev ./internal/server/... ./internal/client/...

# Phase 3
go test -tags dev ./pkg/flock/...

# Phase 4
go test -tags dev ./internal/server/...

# Phase 5
go test -tags dev ./internal/svcmgr/...

# Phase 6
go build -tags dev ./cmd/netsgo
sudo ./netsgo install

# Phase 7
go build -tags dev ./cmd/netsgo
sudo ./netsgo manage

# Phase 8
go run -tags dev ./cmd/netsgo update
```

### 8.2 最终集成验收

后端完成后：

```bash
go test -tags dev ./...
```

前端完成后：

```bash
cd web && bun run build
```

完整构建：

```bash
make build
```

手工集成冒烟建议：

1. direct-run：
   - 用 `NETSGO_INIT_*` + `--data-dir /tmp/...` 首次启动 server
   - 确认不再出现 `/setup`
2. 锁验证：
   - 同一 `data dir` 同角色第二实例直接失败
3. systemd 安装：
   - 在 disposable Linux VM 内执行 `sudo ./netsgo install`
   - 确认 unit/spec/env/data dir 路径正确
4. systemd 管理：
   - 执行 `sudo ./netsgo manage`
   - 验证 `status / inspect / logs / uninstall`
5. 文档一致性：
   - README、e2e 脚本、Compose 示例中不再出现 `setup token`、`/api/setup/*`、`~/.netsgo`

---

## 收尾标准

只有在满足以下全部条件后，本实施文档对应的工作才算完成：

1. 运行时代码中不再出现 setup token / setup API / setup 页面。
2. 运行时代码中不再硬编码 `~/.netsgo`。
3. `server` / `client` 都以 `DataDir` 根目录派生路径。
4. `install` / `manage` / `update` 三个命令都已接入 CLI。
5. systemd 受管模式能生成正确的 spec/env/unit，并使用 journald。
6. server 安装不再依赖 Web setup。
7. README 与 e2e 资料已同步到新模型。
