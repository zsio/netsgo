# NetsGo Client Desktop (Rust + GPUI + Go Sidecar)

基于纯 Rust + `gpui-kit` (`gpui-component`) 构建的原生桌面客户端，数据面与控制面协议 100% 复用 `netsgo client` Go 边车。

## 目录与 Crate 分层

- `crates/netsgo-sidecar-proto`：Rust ↔ Go Sidecar 的控制指令（`--control-stdio`）与结构化运行态快照（`client.runtime_snapshot`）契约及错误码映射。
- `crates/netsgo-client-core`：零 UI 依赖的客户端核心层（`ClientSupervisor` 有限状态机、多节点 Profile 隔离目录、Lifeline 防孤儿进程守护）。
- `crates/netsgo-gpui`：GPUI 原生界面与系统托盘常驻壳（`gpui-kit` + `tray-icon`）。
- `crates/netsgo-desktop-updater`：独立的签名清单、双源下载与整包安装协调。
- `crates/netsgo-desktop-service`：系统服务宿主、受认证的本机控制接口、配置迁移与自启动管理。

## 本地开发与构建

在仓库根目录执行：

```bash
# 启动开发窗口（自动编译当前架构的 Go sidecar）
make dev-client-desktop

# 运行 Rust 单元测试
make test-client-desktop

# 构建 Release 桌面客户端产物
make build-client-desktop
```

## 检查更新与整包升级

左下角显示桌面程序的构建版本，点击打开“版本与更新”；系统托盘也有“检查更新”。发布版启动 8 秒后检查，此后每天检查一次，失败 5 分钟后重试。开发版、dirty 构建显示真实构建标识，不自动升级。

更新检查与 Server 连接无关。客户端并发读取 GitHub / CNB 的签名清单，选择当前平台和通道的最高版本；一个源失败或滞后不会隐藏另一个源的新版本。下载优先采用成功返回新版本的源，超时、损坏或校验失败会尝试另一源。下载不影响隧道，用户选择“重启并更新”后才退出。重启仅恢复更新前正在运行的连接，已有配置和凭证保留在应用数据目录中。

桌面程序、Go sidecar 和后台宿主作为一个 Velopack 包更新。安装使用固定版本的 Velopack Rust SDK / `vpk`（1.2.161），关闭其启动时自动应用缓存更新的默认行为。清单为 Ed25519 签名信封，签名覆盖平台、版本、更新说明、下载地址和 SHA-256；客户端公钥在构建时从 `scripts/common-update.sh` 提取，与现有发布签名密钥一致。更新包缓存也会在使用前重新校验。

静态清单位于 `release-index` 分支的 `updates/desktop-v1/<target>.json`，独立于现有 CLI 的 `updates/index-v1/`。不需要部署更新 API 或额外服务器。

### 构建安装包

当前发布目标为 `osx-arm64`、`osx-x64`、`win-x64`，分别在目标系统构建。准备 Go、Bun、项目 Rust toolchain、.NET 8 SDK，然后安装与 SDK 相同版本的打包器：

```bash
dotnet tool install --global vpk --version 1.2.161
make package-client-desktop CLIENT_DESKTOP_TARGET=osx-arm64 VERSION=v0.2.0
```

`v0.2.0` 仅为示例。产物位于 `client-desktop/dist/<target>/`，包含首次安装包或便携包、完整更新包、Velopack 元数据及安装说明。输出目录必须为空，避免混入旧产物。`VPK` 可指定打包器路径，`DESKTOP_DIST_DIR` 可指定输出根目录。

默认使用 `unsigned` 模式，不需要付费证书或开发者账号：macOS 使用免费的 ad-hoc 签名封装完整 `.app`，输出 ZIP；Windows 输出未签名的安装程序和 ZIP。ad-hoc 签名不代表 Apple 认可的开发者身份，也不包含公证。首次安装和后续升级可能遇到系统确认或拦截，具体见 [安装说明](INSTALL.md)。应用不会自动关闭或绕过系统保护。

### 选择是否发布桌面版

每次需要发布桌面时，将 `client-desktop/release.json` 的 `tag` 设置为**即将推送的确切 Git tag**，选择 `targets` 并更新 `RELEASE_NOTES.md`。提交后按仓库既有流程推送该 tag。版本和通道仍以 Git tag 为唯一来源，不需要修改 Cargo 包版本。

```json
{
  "tag": "v0.2.0",
  "signing": "unsigned",
  "targets": ["osx-arm64", "osx-x64", "win-x64"]
}
```

若下次只发布服务端/CLI `v0.2.1`，保留该文件即可：tag 不匹配会跳过全部桌面任务，桌面清单继续指向 `v0.2.0`。初始空 tag 表示尚未安排桌面发布。稳定版仅检查 stable；beta 检查 stable 和 beta 的最高 SemVer。各平台清单独立保存两条通道的最新版本。

### CI 配置与发布顺序

沿用现有 `NETSGO_RELEASE_SIGNING_KEY_PEM`、`CNB_REPO_URL`、`CNB_USERNAME`、`CNB_PASSWORD`。自有的更新清单签名是免费的，与 Apple / Microsoft 系统签名无关，`unsigned` 模式也始终要求并校验它。桌面发布要求 CNB 配置齐全，并验证 GitHub、CNB 的产物均可匿名下载且哈希正确，然后才公布清单。任意源上传失败不会公布只有半边可下载的新版本；修复后可重跑任务。

### 将来启用系统签名（可选）

当前无需配置下列证书。以后取得证书时，把 `release.json` 的 `signing` 改为 `required`，再配置对应 GitHub Actions secrets。此模式会在缺少证书时终止，不会悄悄降级为未签名发布。本地可用 `NETSGO_DESKTOP_SIGNING=required` 选择同样的策略。

| 平台 | Secrets |
| --- | --- |
| macOS 证书 | `APPLE_CERTIFICATE_BASE64`（包含 Developer ID Application / Installer 的 p12）、`APPLE_CERTIFICATE_PASSWORD` |
| macOS 签名身份 | `APPLE_SIGN_APP_IDENTITY`、`APPLE_SIGN_INSTALL_IDENTITY` |
| macOS 公证 | `APPLE_ID`、`APPLE_TEAM_ID`、`APPLE_APP_PASSWORD`（App 专用密码） |
| Windows 签名 | `WINDOWS_CERTIFICATE_BASE64`（代码签名 pfx）、`WINDOWS_CERTIFICATE_PASSWORD` |

启用系统签名时，证书仅导入临时 CI runner；私钥不会打进客户端。请按顺序发布 tag，等待上一轮索引发布完成。CLI 和桌面索引发布共用 CI 串行锁，更新保留其他产品、平台和通道的条目；旧任务也不会把通道版本倒退。同版本但内容不同的桌面包禁止覆盖，应使用新的 tag。

如果安装包已发布、仅 CNB 清单镜像同步失败，可以在 Release workflow 中手动运行 `repair_desktop_tag`，填入已发布 tag。此恢复操作会验证现有清单签名和两个下载源的更新包，再用完整 Git 历史同步 CNB；不会构建新版本、重新签名或改变已发布文件。正常版本发布仍必须由 Git tag 触发。

测试入口：`make test-client-desktop`。真实升级验收还应在各平台使用前后两个安装包测试：检查、镜像故障切换、下载、退出、整包替换和恢复连接。首次安装应使用浏览器下载的文件测试系统提示；本地编译启动不能证明 Gatekeeper / SmartScreen 会放行。系统签名/公证效果需要使用正式签名产物验证。

macOS 可先用下述命令验证真实安装器。测试只操作临时目录，检查损坏包不会破坏旧版本、桌面和 sidecar 一同升级、旧文件清理、数据目录保留：

```bash
bun scripts/test-desktop-upgrade.mjs /path/to/old-Portable.zip /path/to/new-full.nupkg v0.2.0
```

## 启动与后台运行

左下角“设置”提供两个独立开关：登录后启动托盘界面，以及未登录操作系统时运行后台连接。后一项通过 macOS LaunchDaemon / Windows Service 实现，使用独立宿主进程持有 Go sidecar；界面通过本机认证接口管理它，退出或注销不会断开隧道。详细使用条件见 [安装说明](INSTALL.md#自启动与免登录运行)，实现约束见 [服务设计](SERVICE_DESIGN.md)。

完整更新包同时包含界面、Go client 和后台宿主。启用服务后，更新先请求系统管理员授权替换后台程序并确认启动成功，再交给 Velopack 替换界面。升级失败会恢复原后台程序；停用服务则把配置和凭证迁回桌面数据目录。

真实系统服务安装、升级、失败回滚、自动重连和停用验证只在一次性的 GitHub Actions runner 执行：手动运行 Release workflow 并勾选 `validate_startup`。桌面版 tag 发布时同样执行，验证通过后才公布桌面更新。本地 Rust 测试只使用临时目录，不修改真实启动项。

## 边车与核心约束

- 所有认证、重连退避、yamux 数据通道、P2P 打洞以及未来扩展的 `TunnelSpec` 端点（如串口隧道等）均由 `netsgo client` 统一承载，保证 CLI 与桌面端行为严格一致。
- 边车启动携带 `--control-stdio --log-format json`：当连接宿主（桌面程序或后台服务）退出或崩溃导致 `stdin` 关闭时，Go 边车自动优雅释放控制连接与 `locks/client.lock`。
- 详情中的持续运行时长表示当前边车进程的运行时间：以 Go 快照为基准，在两次快照之间由本地单调时钟推进；同一进程自动重连时继续累计，重新启动边车时重新计时。
