# Version Update Notice and Trusted Command Upgrade Plan

本文档是 NetsGo 版本更新提醒、可信下载、首次安装脚本和命令式升级的实现规格。

NetsGo 仍处于发布前开发阶段。本方案以正确、安全、可解释为优先。旧的 Web 托管下载、Web 托管替换二进制、Web 托管重启服务、server 向 client 分发更新包等设计不再作为本轮目标。

## 总体结论

本轮目标收敛为：

```text
server 统一发现更新 / 脚本可信下载 / 本机命令式安装或升级
```

核心职责边界：

- Web 面板只负责提醒更新，并展示明确的人工执行入口。
- Server 统一负责拉取 release index、缓存版本发现结果，并根据 server/client 当前上报信息计算是否有更新。
- Client 不主动检查 release index，不接收更新控制消息，不执行远程自更新。
- `scripts/install.sh` 负责首次安装场景的可信下载，然后执行临时二进制的 `netsgo install`，后续进入 TUI 交互。
- `scripts/upgrade.sh` 负责托管服务升级场景的可信下载，然后执行临时二进制的 `netsgo upgrade`。
- `netsgo upgrade` 只负责本机托管服务检测、权限提升、二进制替换、服务停止/启动和失败回滚；它不负责联网下载。

本轮不实现 Web 内下载、Web 内 apply、Web 内重启、client 远程自更新、Docker 镜像自动更新、privileged updater helper、systemd socket、sudoers 白名单、持久化更新任务或审计表。

## 版本表示规则

NetsGo 对外版本值统一使用带 `v` 的 SemVer tag。

必须带 `v` 的位置：

- `pkg/version.Current`。
- `netsgo --version` 输出中可提取的版本。
- server status 的 `version`。
- client 上报的 `info.version`。
- release index 的 `version`、`latest`。
- version check API 的 `current_version`、`latest_version`。
- 文档示例中的版本字段。

不带 `v` 的位置：

- release artifact 文件名仍保持 GoReleaser 风格，例如 `netsgo_0.1.0_linux_amd64.tar.gz`。
- 脚本或 release index 生成器如需从 tag 得到 artifact 文件名中的版本，可以内部使用 `${version#v}` 或等价逻辑，但不得在对外 JSON 中暴露 `normalized_version` 之类的无 `v` 版本字段。

版本比较必须使用标准 SemVer 工具。Go 侧推荐使用 `golang.org/x/mod/semver`，它要求输入带 `v`，正好符合本方案。前端不得实现 SemVer 比较逻辑，只展示 server 计算后的结果。

合法发布 tag 只允许：

```text
stable: vMAJOR.MINOR.PATCH
beta:   vMAJOR.MINOR.PATCH-beta.N
```

`N` 必须是正整数。同一 `MAJOR.MINOR.PATCH` 下，`beta.N` 必须递增。其他 prerelease 形态，例如 `alpha`、`rc`、`beta`、`beta.0`，不得进入 release index，release workflow 应直接失败。

只有 `vMAJOR.MINOR.PATCH-beta.N` 算 beta 通道。`dev`、`snapshot`、`dirty`、`rc` 都不算 beta 通道。

## 更新通道选择

NetsGo 只有两个发布通道：

- `stable`
- `beta`

通道不需要持久化，完全由当前版本判断：

- 当前版本是 `vMAJOR.MINOR.PATCH`：当前用户在 stable 轨道。
- 当前版本是 `vMAJOR.MINOR.PATCH-beta.N`：当前用户在 beta 轨道。
- 当前版本是 dev/snapshot/dirty 或不可比较版本：默认按 stable 轨道处理，但必须能提取可比较的基准 SemVer 后才允许提示更新。

默认版本提醒规则：

- stable 用户只检查 stable 候选。
- beta 用户同时检查 stable 和 beta 候选，并推荐 SemVer 最高者。
- dev/snapshot/dirty 用户默认检查 stable，但只有能提取当前基准 SemVer 且 stable latest 更高时才显示更新。
- 纯 `dev`、纯 `snapshot`、纯 `dirty` 等不可比较版本不显示更新提醒。手动检查时可返回 `reason=current_version_uncomparable`。

可比较基准 SemVer 提取规则：

- 当前版本本身是合法带 `v` 的 stable 或 beta tag 时，直接用当前版本比较。
- 当前版本是 `git describe` 形态时，可以提取前缀 tag 作为基准，例如 `v0.1.0-3-gabc123`、`v0.1.0-3-gabc123-dirty` 的基准是 `v0.1.0`，`v0.1.0-beta.5-3-gabc123` 的基准是 `v0.1.0-beta.5`。
- 只有前缀 tag 符合本方案允许的 stable 或 beta tag 时才算可比较。
- 纯 commit hash、`dev`、`snapshot`、`dirty`、`test` 或无法提取合法前缀 tag 的字符串都不可比较。
- 提取出的基准只用于判断是否需要提示更新；对外响应中的 `current_version` 仍返回原始当前版本字符串。
- 后文所有“当前版本/已安装版本是否可比较”的判断都复用这套规则。目标 release 版本必须是精确合法 tag，不允许用基准提取规则宽松解析。

示例：

```text
current:       v0.1.0-beta.5
stable latest: v0.1.0
beta latest:   v0.1.0-beta.6
recommended:   v0.1.0
```

因为 SemVer 中正式版 `v0.1.0` 高于同基线 prerelease。

```text
current:       v0.2.0-beta.5
stable latest: v0.2.0
beta latest:   v0.2.1-beta.1
recommended:   v0.2.1-beta.1
```

beta 用户不做同基线 stable 特判，直接选 stable/beta 候选中的 SemVer 最高版本。

beta 用户候选缺失语义：

- stable 或 beta 某一个通道缺失时，不必直接失败。
- 只要另一个通道存在可比较候选，就用该候选参与比较。
- 如果存在候选但都不高于当前版本，结果是无更新，不是检查失败。
- 如果 stable 和 beta 都缺失或都不可解析，才返回 `reason=no_matching_candidate` 或 `reason=channel_unavailable`。

脚本规则：

- `install.sh` 默认安装 stable，可显式 `--channel stable|beta`。
- `upgrade.sh` 默认 `--channel auto`。
- `upgrade.sh --channel auto`：
  - 当前 stable：只看 stable。
  - 当前 beta：stable/beta 候选中选 SemVer 最高。
  - 当前 dev/snapshot/dirty：默认 stable。
- `upgrade.sh --channel stable`：只看 stable。
- `upgrade.sh --channel beta`：只看 beta。该参数允许用户主动从 stable 切到更高的 beta，但非 `-f` 时仍必须满足目标版本高于当前版本。
- Web/API 返回的一键升级命令必须显式带 `--channel <recommended_channel>`，不得依赖脚本默认 auto。

## 安装方式语义

安装方式只定义三类：

```text
service
docker
binary
```

`binary` 是正常兜底分类，不表示“精确确认用户手动下载了裸二进制”。凡不是确认的 service，也不是确认的容器运行，都归为 `binary`。

推荐动作：

- `service`：有更新时展示 `upgrade.sh` 一键升级命令。
- `docker`：有更新时提示镜像/文档入口，不展示脚本升级命令。
- `binary`：有更新时提示 GitHub Releases 手动下载，不展示脚本升级命令。

检测优先级：

```text
docker > 当前角色 service > binary
```

容器环境统一返回 `docker`，不细分 Docker、Kubernetes、Podman。检测可使用 `/.dockerenv`、`/run/.containerenv`、`/proc/1/cgroup` 等特征。

进程自检返回 `service` 必须同时满足：

- 当前不是容器环境。
- 当前系统是 Linux。
- systemd 可用。
- 当前角色 unit 已安装。
- 当前运行二进制路径是 `svcmgr.BinaryPath`，即 `/usr/local/bin/netsgo`。
- 当前角色 unit 的 `MainPID` 等于当前进程 PID。

如果用户手动运行 `/usr/local/bin/netsgo server`，即使路径匹配，只要 `MainPID` 不匹配，也必须降级为 `binary`。

`upgrade.sh` 检测本机是否可升级时不要求 `MainPID` 匹配。脚本是在目标机器终端里执行，它只需要确认本机存在 NetsGo 托管 unit；服务可以正在运行，也可以已停止。

## Client 上报模型

client 不主动检查更新。server 只使用 client 已上报的信息做版本比较。

client 更新判断所需的静态信息在认证/重连时上报，不进入高频 `probe_report`。

`protocol.ClientInfo` 应扩展：

```json
{
  "hostname": "client-1",
  "os": "linux",
  "arch": "amd64",
  "ip": "10.0.0.2",
  "version": "v0.1.0-beta.5",
  "update_capability": {
    "install_method": "service"
  }
}
```

`update_capability` 本轮只包含 `install_method`。不新增 platform 字段。运行态上报的 `arch` 保持 Go 原始 `runtime.GOARCH`，例如 `arm`、`amd64`、`arm64`。release artifact 匹配所需的 canonical arch 由本地脚本通过 `uname`/环境推导。

老版本 client 缺失 `update_capability.install_method` 时，server 按 `binary` 降级，不展示脚本命令。

离线 client：

- 不触发版本检查。
- 不显示更新控件。
- 不复用前端或 server 之前缓存过的“可更新”提醒。

## Server 统一版本检查

版本检查由 server 统一完成。

Web 入口：

- Dashboard server 版本旁：检查当前 server 进程版本。
- 在线 client detail 版本旁：检查该 client 当前在线连接中上报的版本。

手动检查：

- server 手动检查只刷新 server 侧 release index 缓存。
- client 手动检查也只刷新 server 侧 release index 缓存，不向 client 发控制消息。

server 维护全局 release index cache，而不是按 target/version 缓存远端请求结果。

推荐缓存策略：

- 缓存对象：`latest.json`。
- TTL：12 小时。
- `force=true` 绕过 TTL。
- 强制刷新受 10 秒 cooldown 保护。
- 并发刷新通过 `singleflight` 或等价锁合并。
- cache 过期但刷新失败时，如果有旧 cache，使用 stale cache 继续计算。
- 无 cache 且刷新失败时，API 返回 HTTP 200 结构化失败。

外部查询失败不是 API 失败。HTTP 5xx 只表示 API 自身、认证、请求处理失败。版本检查失败通过响应字段表达。

## Version Check API

server 检查：

```text
GET /api/version/check?force=false
```

该接口不再要求前端传 `version`。server 使用当前进程版本。

client 检查：

```text
GET /api/clients/{id}/version/check?force=false
```

只对在线 client 有意义。离线 client 应返回明确不可检查状态，或前端不发请求。

响应字段建议：

```json
{
  "target": "server",
  "target_id": "server",
  "current_version": "v0.1.0-beta.5",
  "latest_version": "v0.1.0",
  "update_available": true,
  "checked_at": "2026-05-10T12:00:00Z",
  "install_method": "service",
  "recommended_channel": "stable",
  "recommended_action": "run_script",
  "commands": {
    "domestic": "curl -fsSL https://cnb.cool/zsio/netsgo/-/raw/main/scripts/upgrade.sh | sh -s -- --source cnb --channel stable -y",
    "global": "curl -fsSL https://raw.githubusercontent.com/zsio/netsgo/main/scripts/upgrade.sh | sh -s -- --source github --channel stable -y"
  },
  "release_url": "https://github.com/zsio/netsgo/releases",
  "check_failed": false,
  "refresh_failed": false,
  "cache_source": "fresh",
  "reason": ""
}
```

`recommended_action` 建议值：

```text
none
run_script
github_release
docker_docs
```

`cache_source` 表示本次计算使用的 release index cache 状态，建议值：

```text
fresh
cache
stale_cache
none
```

`reason` 使用稳定字符串，前端映射中文文案。建议值：

```text
release_index_unavailable
channel_unavailable
current_version_uncomparable
no_matching_candidate
client_offline
```

无更新示例：

```json
{
  "target": "server",
  "target_id": "server",
  "current_version": "v0.1.0",
  "latest_version": "v0.1.0",
  "update_available": false,
  "checked_at": "2026-05-10T12:00:00Z",
  "install_method": "service",
  "recommended_channel": "stable",
  "recommended_action": "none",
  "commands": null,
  "release_url": "https://github.com/zsio/netsgo/releases",
  "check_failed": false,
  "refresh_failed": false,
  "cache_source": "cache",
  "reason": ""
}
```

检查失败且无 cache 示例：

```json
{
  "target": "server",
  "target_id": "server",
  "current_version": "v0.1.0",
  "latest_version": "",
  "update_available": false,
  "checked_at": "2026-05-10T12:00:00Z",
  "install_method": "service",
  "recommended_channel": "",
  "recommended_action": "github_release",
  "commands": null,
  "release_url": "https://github.com/zsio/netsgo/releases",
  "check_failed": true,
  "refresh_failed": true,
  "cache_source": "none",
  "reason": "release_index_unavailable"
}
```

stale cache 规则：

- stale cache 算出有更新：正常显示更新，不额外提示“无法获取最新数据”。
- stale cache 算出无更新，且本次是手动检查且刷新失败：前端显示“检查失败”，不得提示“已是最新”。
- API 应返回 `refresh_failed=true`，供前端区分。

commands 规则：

- 只有 `install_method=service` 且 `update_available=true` 时返回 `commands`。
- 命令必须使用 `scripts/upgrade.sh`。
- 命令必须显式带 `--channel <recommended_channel>`。
- 国内源命令必须显式带 `--source cnb`。
- 国外源命令必须显式带 `--source github`。
- Web/API 返回的命令默认带 `-y`。
- Web/API 不得返回 `-f` 命令。
- `check_failed=true` 时 `commands=null`。

## 状态接口扩展

server status 应暴露本机更新能力：

```json
{
  "version": "v0.1.0",
  "update_capability": {
    "install_method": "service"
  }
}
```

client 列表/detail 通过 `ClientInfo.update_capability` 暴露 client 上报的安装方式。

状态接口表达事实，版本检查接口表达本次推荐动作。两者都需要包含安装方式相关信息，方便前端构造 TanStack Query key 和展示运行方式。

## Frontend UX

前端不得自己比较 SemVer。

前端不得手写 localStorage 版本检查缓存。版本检查使用 TanStack Query：

- 自动检查使用 query。
- 手动检查使用 mutation 调用 `force=true`。
- mutation 成功后写回对应 query cache。
- `staleTime` 建议 10 分钟。
- query key 至少包含 target kind、target id、current version、install method。client 场景可额外包含已上报的 os、arch；server 场景不要求为了 query key 额外暴露 os、arch。

自动检查：

- Dashboard server 版本旁自动检查一次。
- 在线 client detail 版本旁自动检查一次。
- 自动检查失败静默。

手动检查：

- 用户点击版本旁的检查/刷新入口时，调用 `force=true`。
- 手动检查失败时提示检查失败，并提供 GitHub Releases 链接。
- 手动检查无更新时短暂提示“已是最新版本”。
- 如果手动检查使用 stale cache 且 stale 结果无更新，同时 `refresh_failed=true`，提示检查失败，不提示已最新。

无更新：

- 默认只显示版本文本。
- 不显示常驻绿色状态。
- hover 或轻量按钮可触发手动检查。

有更新：

- 显示持久黄色更新图标。
- 点击打开更新说明弹窗。

service 更新弹窗必须包含：

- 当前版本。
- 最新版本。
- 推荐通道。
- 国内源升级命令。
- 国外源升级命令。
- 复制按钮。
- GitHub Releases 备用入口。
- 明确说明命令要在目标机器执行。
- 明确说明命令会升级并重启本机所有 NetsGo 托管服务。

server 文案：

```text
请在运行 NetsGo server 的机器上执行以下命令。该命令会下载并验证可信的 NetsGo release，然后升级并重启本机所有 NetsGo 托管服务。
```

client 文案：

```text
请在该 client 所在机器上执行以下命令。不要在 server 机器上执行，除非 server 与该 client 本来就在同一台机器。该命令会下载并验证可信的 NetsGo release，然后升级并重启本机所有 NetsGo 托管服务。
```

binary 更新：

- 显示更新提示。
- 点击后只显示 GitHub Releases 手动下载入口。
- 不显示脚本命令。

docker 更新：

- 显示更新提示。
- 点击后显示镜像/文档入口。
- 不显示脚本命令。

client 更新控件只在 client detail 显示。Dashboard client 列表不显示每个 client 的更新控件。

## Web 命令

Web 只展示 upgrade 命令，不展示 install 命令。

国内源升级命令：

```sh
curl -fsSL https://cnb.cool/zsio/netsgo/-/raw/main/scripts/upgrade.sh | sh -s -- --source cnb --channel stable -y
```

国外源升级命令：

```sh
curl -fsSL https://raw.githubusercontent.com/zsio/netsgo/main/scripts/upgrade.sh | sh -s -- --source github --channel stable -y
```

`--channel` 必须由 API 根据推荐结果填入 `stable` 或 `beta`。上面只是 stable 示例。Web/API 返回的国内源命令必须带 `--source cnb`，国外源命令必须带 `--source github`。

安装命令只放 README/安装文档，不放 Web 更新弹窗。

脚本 URL 使用 `main` 分支。脚本本身是 bootstrap 信任入口，用户通过 HTTPS 从官方仓库获取脚本；脚本内部只能安装或升级 release index 中的正式发布产物，并且必须完成签名、checksum 和版本校验。后续如需调整脚本 allowlist、下载源或兼容逻辑，可以通过更新 `main` 分支脚本生效。

`scripts/install.sh` 与 `scripts/upgrade.sh` 可能会从同一官方 `main` 分支加载 `scripts/common-update.sh`。该 common 脚本也属于 bootstrap 信任入口的一部分，不属于 release 产物验签范围；common 脚本之后下载的 release index、checksum、signature 和 archive 仍必须经过 URL allowlist、签名、checksum 和版本校验。

国内源首次安装：

```sh
curl -fsSL https://cnb.cool/zsio/netsgo/-/raw/main/scripts/install.sh | sh -s -- --source cnb
```

国外源首次安装：

```sh
curl -fsSL https://raw.githubusercontent.com/zsio/netsgo/main/scripts/install.sh | sh -s -- --source github
```

安装 beta：

```sh
curl -fsSL https://cnb.cool/zsio/netsgo/-/raw/main/scripts/install.sh | sh -s -- --source cnb --channel beta
```

## install.sh

`scripts/install.sh` 是首次安装入口。

职责：

1. 检查运行环境。
2. 检查 Linux + systemd。
3. 检查依赖。
4. 如检测到已有 NetsGo 托管服务，提示使用 `upgrade.sh` 或 `netsgo manage`，不下载并退出。
5. 判断 source，默认 `auto`，可显式 `--source auto|cnb|github`。
6. 判断 channel，默认 stable，可显式 `--channel stable|beta`。
7. 查询 release index。
8. 下载 release detail。
9. 选择当前平台 release archive。
10. 下载 `checksums.txt`、`checksums.txt.sig`、`checksums.txt.sshsig` 中可用的签名。
11. 使用 openssl 或 ssh-keygen 任一路径验证 `checksums.txt`。
12. 用已验签的 `checksums.txt` 校验 archive SHA256。
13. 解压临时 `netsgo`。
14. 验证临时 `netsgo --version` 精确等于目标 tag。
15. 执行临时 `./netsgo install`，进入 TUI。

参数语义：

```text
--source auto|cnb|github
  选择下载源优先级。默认 auto。

--channel stable|beta
  选择安装通道。默认 stable。
```

`install.sh` 不支持 `-y`/`--yes`。首次安装需要选择 server/client、填写 server 地址、key、初始化配置等交互信息，不做无人值守安装。

## upgrade.sh

`scripts/upgrade.sh` 是托管服务升级入口。

职责：

1. 检查运行环境。
2. 检查 Linux + systemd。
3. 检查依赖。
4. 检测本机是否存在 NetsGo 托管 unit。没有托管服务时失败，不下载。
5. 读取已安装 `/usr/local/bin/netsgo --version`。
6. 判断 source，默认 `auto`，可显式 `--source auto|cnb|github`。
7. 查询 release index。
8. 按 `--channel auto|stable|beta` 选择目标版本。
9. 非 `-f` 时，如果当前版本不可比较、当前版本等于目标版本、或当前版本高于目标版本，按规则拒绝或跳过。
10. 下载 release detail。
11. 选择当前平台 release archive。
12. 下载并验签 `checksums.txt`。
13. 校验 archive SHA256。
14. 解压临时 `netsgo`。
15. 验证临时 `netsgo --version` 精确等于目标 tag。
16. 调用临时 `./netsgo upgrade`，并透传 `-f`/`-y`。

参数语义：

```text
--source auto|cnb|github
  选择下载源优先级。默认 auto。该参数只影响脚本下载行为，不传给 netsgo upgrade。

--channel auto|stable|beta
  选择升级通道。默认 auto。

-f, --force
  强制下载并尝试替换，即使当前版本不可比较、等于目标版本、或看起来高于目标版本。

-y, --yes
  跳过最终替换确认。脚本自身不额外确认；该参数会传给临时 netsgo upgrade。
```

映射关系：

```text
upgrade.sh
  -> ./netsgo upgrade

upgrade.sh -y
  -> ./netsgo upgrade -y

upgrade.sh -f
  -> ./netsgo upgrade -f

upgrade.sh -f -y
  -> ./netsgo upgrade -f -y
```

非 `-f` 行为：

- 当前版本 < 目标版本：下载并升级。
- 当前版本 == 目标版本：提示已是目标版本，不下载、不替换、不重启。
- 当前版本 > 目标版本：拒绝降级。
- 当前版本不可比较：不做无交互升级；要求人工处理或使用 `-f`。

`-f` 行为：

- 无论版本不可比较、等版本、降级，都重新下载目标 release，并触发完整替换流程。
- 一旦进入替换流程，就必须停止服务、备份旧二进制、替换、启动服务，失败时回滚。

Web 默认命令可以带 `-y`，但不得带 `-f`。

## netsgo upgrade CLI

`netsgo upgrade` 的职责是用当前正在运行的二进制替换系统已安装的托管二进制，并重启托管服务。

它不负责：

- 查询 release index。
- 下载 release archive。
- 验签。
- 选择 channel。

参数语义必须调整为：

```text
-f, --force
  强制允许替换，不因版本相同、降级、不可比较而阻止。

-y, --yes
  跳过最终确认。
```

旧的 `--force` 只表示跳过确认，本轮应改为上面的新语义。项目尚未发布，不需要兼容旧 CLI 语义。

默认安全规则：

- 目标二进制版本必须是合法带 `v` 的 SemVer。
- 已安装版本必须能比较。
- 目标版本必须高于已安装版本。
- 等版本替换默认跳过，不重启。
- 降级默认拒绝。
- 不可比较版本默认拒绝。

`-f` 后：

- 允许不可比较、等版本、降级进入替换流程。
- 等版本 `-f` 也必须执行完整替换和服务重启。

`-y` 只跳过最终确认，不改变版本安全规则。

`-f -y` 表示无交互强制替换。

普通交互确认使用 `yes`/`y`，不再要求输入 `upgrade binary`。卸载、删除数据、清理异常状态等 destructive 操作仍应保留短语确认，不受本方案影响。

同一机器如果同时安装 server 和 client 托管服务，`netsgo upgrade` 一次替换共享二进制，并重启本机所有 NetsGo 托管服务。

## Release Index

release index 是发现源，不是信任根。信任根是脚本内置公钥对 `checksums.txt` 的签名验证。

路径：

```text
updates/index-v1/latest.json
updates/index-v1/releases/<tag>.json
```

国内源 latest：

```text
https://cnb.cool/zsio/netsgo/-/raw/release-index/updates/index-v1/latest.json
```

国外源 latest：

```text
https://raw.githubusercontent.com/zsio/netsgo/release-index/updates/index-v1/latest.json
```

`latest.json` 示例：

```json
{
  "schema": 1,
  "project": "netsgo",
  "generated_at": "2026-05-10T12:00:00Z",
  "channels": {
    "stable": {
      "latest": "v0.1.0",
      "release_urls": [
        {
          "provider": "cnb",
          "url": "https://cnb.cool/zsio/netsgo/-/raw/release-index/updates/index-v1/releases/v0.1.0.json"
        },
        {
          "provider": "github",
          "url": "https://raw.githubusercontent.com/zsio/netsgo/release-index/updates/index-v1/releases/v0.1.0.json"
        }
      ]
    },
    "beta": {
      "latest": "v0.1.0-beta.1",
      "release_urls": [
        {
          "provider": "cnb",
          "url": "https://cnb.cool/zsio/netsgo/-/raw/release-index/updates/index-v1/releases/v0.1.0-beta.1.json"
        },
        {
          "provider": "github",
          "url": "https://raw.githubusercontent.com/zsio/netsgo/release-index/updates/index-v1/releases/v0.1.0-beta.1.json"
        }
      ]
    }
  }
}
```

`latest.json` 自身不需要自描述多个 latest provider。脚本内置 latest 源列表，并根据 `--source auto|cnb|github` 决定优先级。`curl ... | sh` 场景下脚本不能可靠感知自身来自哪个 URL，因此不得根据脚本入口 URL 推断 provider。

release detail 示例：

```json
{
  "schema": 1,
  "project": "netsgo",
  "version": "v0.1.0-beta.1",
  "prerelease": true,
  "generated_at": "2026-05-10T12:00:00Z",
  "checksum_asset": {
    "name": "checksums.txt",
    "urls": [
      {
        "provider": "cnb",
        "url": "https://cnb.cool/zsio/netsgo/-/releases/download/v0.1.0-beta.1/checksums.txt",
        "requires_auth": false
      },
      {
        "provider": "github",
        "url": "https://github.com/zsio/netsgo/releases/download/v0.1.0-beta.1/checksums.txt",
        "requires_auth": false
      }
    ]
  },
  "signature_assets": {
    "ed25519": {
      "name": "checksums.txt.sig",
      "urls": [
        {
          "provider": "cnb",
          "url": "https://cnb.cool/zsio/netsgo/-/releases/download/v0.1.0-beta.1/checksums.txt.sig",
          "requires_auth": false
        },
        {
          "provider": "github",
          "url": "https://github.com/zsio/netsgo/releases/download/v0.1.0-beta.1/checksums.txt.sig",
          "requires_auth": false
        }
      ]
    },
    "sshsig": {
      "name": "checksums.txt.sshsig",
      "urls": [
        {
          "provider": "cnb",
          "url": "https://cnb.cool/zsio/netsgo/-/releases/download/v0.1.0-beta.1/checksums.txt.sshsig",
          "requires_auth": false
        },
        {
          "provider": "github",
          "url": "https://github.com/zsio/netsgo/releases/download/v0.1.0-beta.1/checksums.txt.sshsig",
          "requires_auth": false
        }
      ]
    }
  },
  "assets": [
    {
      "name": "netsgo_0.1.0-beta.1_linux_amd64.tar.gz",
      "os": "linux",
      "arch": "amd64",
      "size": 12345678,
      "sha256": "...",
      "urls": [
        {
          "provider": "cnb",
          "url": "https://cnb.cool/zsio/netsgo/-/releases/download/v0.1.0-beta.1/netsgo_0.1.0-beta.1_linux_amd64.tar.gz",
          "requires_auth": false
        },
        {
          "provider": "github",
          "url": "https://github.com/zsio/netsgo/releases/download/v0.1.0-beta.1/netsgo_0.1.0-beta.1_linux_amd64.tar.gz",
          "requires_auth": false
        }
      ]
    }
  ]
}
```

release detail 不包含 `normalized_version`。

脚本 provider 选择：

- 脚本通过 `--source cnb|github|auto` 决定 provider 优先级，不能依赖 pipe 执行时的脚本来源推断。`curl ... | sh` 场景下脚本无法可靠知道自己来自哪个 URL。
- `--source cnb`：优先选择 `provider=cnb` 的 release detail、checksum、signature、archive URL。
- `--source github`：优先选择 `provider=github`。
- `--source auto`：使用脚本默认顺序，建议 CNB 优先、GitHub fallback。
- 当前 provider 缺失或失败时 fallback 另一个 provider。
- checksum、signature、archive 优先同 provider，但不强制同源。只要最终签名和 checksum 校验通过，可以混用 provider。

通道字段容错：

- 当前目标只需要某个通道时，只校验该通道存在。
- stable 检查不因 beta 缺失失败。
- beta 检查如果需要 stable/beta 候选，则分别使用可用候选；两个都无有效候选才失败。

## Release Workflow

Release workflow 必须：

1. 通过 tag 触发。
2. 强校验 tag 只允许 stable 或 beta.N。
3. beta.N 在同一 `MAJOR.MINOR.PATCH` 下必须递增。
4. 构建 web dist。
5. 构建 Go release artifacts。
6. 生成 `dist/checksums.txt`。
7. 使用 `NETSGO_RELEASE_SIGNING_KEY_PEM` 生成：
   - `dist/checksums.txt.sig`
   - `dist/checksums.txt.sshsig`
8. 上传 archive、`checksums.txt`、两类签名到 GitHub Release。
9. 缺任一签名资产时发布失败。
10. 镜像 archive、`checksums.txt`、两类签名到 CNB Release。
11. CNB 附件上传每个文件首次尝试加 2 次重试。
12. CNB 附件最终失败时，该文件不写 CNB URL，但仍可发布包含 GitHub URL 和成功 CNB URL 的 index。
13. 生成 release index。
14. 推送 release-index 分支到 GitHub。失败则 release workflow 失败。
15. 推送 release-index 分支到 CNB。推送首次尝试加 2 次重试，最终失败则 release workflow 失败。

release index 生成器必须校验：

- `dist/checksums.txt` 存在。
- `dist/checksums.txt.sig` 存在。
- `dist/checksums.txt.sshsig` 存在。
- checksums 中列出的 NetsGo archive 文件存在。
- archive 实际 SHA256 与 checksums 一致。
- release detail 包含 checksum、两类签名、至少一个可安装平台 asset。
- 只把实际上传成功的 CNB 附件写入 CNB provider URL。

生成 release index 时必须保留或重建 stable/beta 两个通道指针，不得发布 beta 覆盖 stable 指针，也不得发布 stable 覆盖 beta 指针。

## 签名与校验

每个可由脚本安装/升级的 release 必须包含：

```text
checksums.txt
checksums.txt.sig
checksums.txt.sshsig
```

`checksums.txt.sig` 是对 `checksums.txt` 原始字节的 Ed25519 签名，给 openssl 路径使用。

`checksums.txt.sshsig` 是 OpenSSH SSHSIG 格式签名，给 `ssh-keygen -Y verify` 路径使用。

CI 只维护一个私钥 secret：

```text
NETSGO_RELEASE_SIGNING_KEY_PEM
```

仓库内新增 release signing Go 工具，由该 PEM Ed25519 私钥生成两种签名。工具必须有测试。

脚本内置两种公钥格式：

- PEM public key，给 openssl 验证 `.sig`。
- OpenSSH allowed signers public key，给 ssh-keygen 验证 `.sshsig`。

正式发布前必须先完成 release signing key 初始化：

```sh
go run ./cmd/netsgo-release-sign keygen --private-out release-signing-key.pem
NETSGO_RELEASE_SIGNING_KEY_PEM="$(cat release-signing-key.pem)" scripts/embed-release-public-keys.sh
```

`release-signing-key.pem` 的内容配置为 GitHub Secret `NETSGO_RELEASE_SIGNING_KEY_PEM`，不得提交到仓库。`scripts/embed-release-public-keys.sh` 只会把由私钥派生出的 PEM public key 与 OpenSSH allowed signers public key 写入 `scripts/common-update.sh`，并会立即执行 `verify-embedded` 反校验。Release workflow 也会在 GoReleaser 发布前执行同样的嵌入公钥校验；未嵌入真实公钥或公钥与 secret 私钥不匹配时，发布必须失败。

用户侧验签规则：

- 如果 openssl 可用且支持所需 Ed25519 验签，尝试验证 `.sig`。
- 如果 ssh-keygen 可用且支持 `-Y verify`，尝试验证 `.sshsig`。
- 任一验签路径成功即可继续。
- 另一条路径失败或不可用时无需提醒用户。
- 两条路径都不可用或都验证失败时，必须终止。
- 不提供任何 `--insecure-skip-signature` 或类似跳过签名校验参数。

校验顺序：

1. 下载 `checksums.txt`。
2. 下载可用签名文件。
3. 至少一种路径验证 `checksums.txt` 成功。
4. 下载 release archive。
5. 使用已验签的 `checksums.txt` 校验 archive SHA256。
6. 解压 `netsgo`。
7. 验证临时二进制可执行。
8. 验证临时 `netsgo --version` 中可提取版本精确等于目标带 `v` tag。
9. 执行 `netsgo install` 或 `netsgo upgrade`。

签名失败、checksum 不匹配、版本不匹配都必须终止。不得降级为仅依赖 HTTPS。

首次安装时本机没有旧版本也可以验签，因为信任根不是旧二进制，而是脚本内置公钥。脚本本身是 bootstrap 信任入口；下载的 release 产物由内置公钥保护。

## URL Allowlist

release index 不是信任根，但脚本仍不得访问 index/release detail 中的任意 URL。所有自动下载 URL 必须匹配官方 HTTPS allowlist。

本轮允许：

```text
https://github.com/zsio/netsgo/releases/download/
https://raw.githubusercontent.com/zsio/netsgo/release-index/
https://raw.githubusercontent.com/zsio/netsgo/main/scripts/common-update.sh
https://cnb.cool/zsio/netsgo/-/releases/download/
https://cnb.cool/zsio/netsgo/-/raw/release-index/
https://cnb.cool/zsio/netsgo/-/raw/main/scripts/common-update.sh
```

不得允许 HTTP、localhost、内网地址、metadata 地址或任意第三方域名。

GitHub Releases 页面可作为人工入口：

```text
https://github.com/zsio/netsgo/releases
```

如果未来增加 `https://netsgo.zs.uy/` 等官方域名，必须修改脚本 allowlist 后再使用。

## Platform Identity

release artifact 和 release detail asset 使用 canonical release platform：

```text
<goos>_<release_arch>
```

示例：

```text
linux_amd64
linux_arm64
linux_armv7
```

特殊规则：

- `GOARCH=arm` 且 `GOARM=7` 映射为 `armv7`。
- release artifact、release detail `asset.arch`、脚本选择逻辑必须使用同一套 canonical arch。
- client/server 上报的 `arch` 可以保持 Go 原始 arch，不用于下载匹配。

脚本只支持 Linux + systemd。非 Linux 或无 systemd 时，应提示前往 GitHub Releases 手动下载，或提示脚本只支持 systemd 托管安装/升级。

## Removed Web-Managed Update APIs

本轮不实现以下 API：

```text
POST /api/version/update/download
GET  /api/version/update/status
POST /api/version/update/cancel
POST /api/version/update/apply

POST /api/clients/{id}/version/update/download
GET  /api/clients/{id}/version/update/status
POST /api/clients/{id}/version/update/cancel
POST /api/clients/{id}/version/update/apply
```

也不新增 client 远程更新控制消息：

```text
version_update_prepare
version_update_progress
version_update_cancel
version_update_apply
version_update_apply_ack
```

如果未来重新引入 Web-managed apply，必须先单独设计 privileged updater executor。

## 实现建议

Backend:

- 使用 `golang.org/x/mod/semver` 做版本比较。
- 收拢 `pkg/version`，保证默认版本、构建注入、`--version` 输出都带 `v`。
- 增加 release index parser。
- 增加 release index cache，包含 TTL、cooldown、singleflight。
- 增加 install method detector。
- 扩展 `protocol.ClientInfo.update_capability.install_method`。
- 新增 client version check API。
- 修改 server version check API，不再要求前端传 version。
- API 失败语义改为 HTTP 200 + `check_failed`。

CLI:

- 修改 `netsgo upgrade` 参数：
  - `-f/--force`：强制允许替换。
  - `-y/--yes`：跳过确认。
- 普通确认改为 yes/no。
- 默认拒绝不可比较、等版本、降级替换。
- `-f` 后允许进入完整替换和服务重启流程。

Scripts:

- 新增 `scripts/install.sh`。
- 新增 `scripts/upgrade.sh`。
- 抽共享下载/验签逻辑，避免 install/upgrade 两套实现漂移。
- 支持 CNB/GitHub 双源 fallback。
- `install.sh` 支持 `--source auto|cnb|github`、`--channel stable|beta`。
- `upgrade.sh` 支持 `--source auto|cnb|github`、`--channel auto|stable|beta`、`-f/--force`、`-y/--yes`。
- 支持 openssl/ssh-keygen 双验签路径。
- 支持 URL allowlist。
- `install.sh` 不支持 `-y`。

Release:

- 新增 Go release signing 工具。
- Release workflow 生成并上传两类签名。
- Release index 结构改为 `updates/index-v1`。
- Release index 保留 stable/beta 双指针。
- CNB 上传和 CNB release-index push 都要重试。

Frontend:

- 删除 `use-version-check.ts` 里的手写 localStorage 缓存。
- 使用 TanStack Query + mutation。
- 更新控件 target-aware。
- Dashboard server 版本旁显示更新控件。
- Client detail 在线时显示更新控件。
- Client 列表本轮不显示更新控件。
- service 弹窗显示 `upgrade.sh` 命令并说明目标机器和本机所有托管服务重启语义。
- binary/docker 不显示脚本命令。

## 验证计划

Go tests:

- SemVer canonical 带 `v` 解析和比较。
- stable/beta tag 校验。
- beta.N 递增校验。
- stable 用户只看 stable。
- beta 用户 stable/beta 候选择高。
- dev/snapshot/dirty 可比较和不可比较场景。
- release index parsing。
- release index cache TTL、force cooldown、singleflight。
- stale cache refresh failure 行为。
- install method detection：
  - docker 优先。
  - service MainPID 匹配。
  - 手动运行 `/usr/local/bin/netsgo` 降级 binary。
  - 缺失 client capability 降级 binary。
- version check API response shape。
- client 离线不检查。
- `netsgo upgrade -f/-y` 语义：
  - 正常升级。
  - 等版本无 `-f` 跳过。
  - 等版本 `-f` 替换并重启。
  - 降级无 `-f` 拒绝。
  - 降级 `-f` 允许。
  - 不可比较无 `-f` 拒绝。
  - `-y` 只跳过确认。
- release signing Go 工具：
  - PEM Ed25519 私钥读取。
  - raw `.sig` 验证。
  - `.sshsig` 生成和验证；本机无 `ssh-keygen` 时可 skip 外部命令测试。
  - key mismatch 失败。
  - 输入字节变化导致验签失败。

Script tests:

- install.sh 参数解析。
- install.sh `--source auto|cnb|github`。
- install.sh `--channel stable|beta`。
- install.sh 检测已有托管服务后退出。
- upgrade.sh 参数解析。
- upgrade.sh 无托管服务失败。
- upgrade.sh `--source auto|cnb|github`。
- upgrade.sh `--channel auto|stable|beta`。
- upgrade.sh `-f/-y` 透传。
- platform mapping，尤其 `linux_armv7`。
- URL allowlist 拒绝非官方 URL。
- openssl 验签成功。
- ssh-keygen 验签成功。
- 两种 verifier 都不可用时失败。
- checksum mismatch 失败。
- unsigned release 失败。
- extracted binary version mismatch 失败。
- source 优先级和 provider fallback。

Frontend tests:

- 自动检查失败静默。
- 手动检查失败提示。
- 手动检查 stale 无更新且 refresh_failed 时提示失败。
- 有更新 service 显示两条 `upgrade.sh ... -y` 命令。
- API 不返回 commands 时不显示脚本。
- 离线 client 不显示更新控件。
- query key 包含 target/version/install method；client 场景额外覆盖 os/arch 变化。

Build/CI:

- `cd web && bun run build`。
- 相关 Go 包测试。
- 条件允许时 `go test ./...`。
- Release workflow dry-run 或等价脚本测试。
- Linux/systemd smoke test：
  - `install.sh` 首次安装并进入 TUI。
  - `upgrade.sh` 正常升级。
  - `upgrade.sh -y` 无交互升级。
  - `upgrade.sh -f -y` 等版本强制替换并重启。
  - 缺依赖错误提示。
  - 非 root with sudo。
  - root without sudo。
