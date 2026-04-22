# NetsGo `manage update` / `upgrade` 设计文档

## 文档状态

- 状态：定稿
- 日期：2026-04-22
- 适用范围：Linux + systemd（受管模式）
- 前置依赖：`internal/svcmgr` 已实现（Phase 5 完成）

---

## 1. 问题与目标

### 1.1 背景

当前 `netsgo update` 只是一个占位命令，输出提示后直接 `exit 0`，没有实际功能。

`netsgo manage` 已提供 `status / inspect / logs / start / stop / restart / uninstall`，但缺少升级能力。

### 1.2 目标

在 `netsgo manage` 交互菜单中新增 `update` 选项，同时在 root 命令下新增 `upgrade` 命令，覆盖两种互补的升级场景：

1. **manage → update**：旧版本二进制主动检查、下载、替换、重启
2. **upgrade**：用户已下载新版本二进制，用它替换系统安装的旧版本

两种命令**仅适用于 systemd 受管模式**。其他模式（direct-run、Docker）运行时提示不支持。

---

## 2. 设计概述

### 2.1 命令面

| 命令 | 入口 | 用途 | 适用模式 |
|------|------|------|----------|
| `update` | `manage` 交互菜单 | 自动从 Release 下载最新版本并替换 | service 模式 |
| `upgrade` | root 独立命令 | 用当前运行的二进制替换系统安装的旧版本 | service 模式 |

### 2.2 语义区分

- `update` = 我去找新版本 → 拉取 → 安装（pull and install）
- `upgrade` = 我已经在跑了 → 去替换系统里的旧版（replace installed）

---

## 3. `netsgo update` 详细设计

### 3.1 入口路径

```
netsgo manage → 选择 server/client → 选择 update
```

### 3.2 前置检查

1. 是否有已安装的 service？否 → 提示无可升级内容，退出
2. 当前运行二进制版本号是否能获取？否 → 提示无法检查版本，退出

### 3.3 版本检查流程

**方法：HTTP 302 重定向追踪法**

不需要 GitHub API（`api.github.com` 国内经常不可达），只依赖普通 HTTP：

1. 发送 `GET https://github.com/zsio/netsgo/releases/latest`
2. GitHub 返回 302 重定向到 `/releases/tag/v{version}`
3. 从最终 URL 路径中提取版本号
4. 与当前 `pkg/version.Current` 比较

### 3.4 通道选择

获取最新版本时，让用户选择下载通道：

```
1. GitHub（默认）
2. ghproxy（国内镜像）
```

**通道 URL 模板：**

```
GitHub:   https://github.com/zsio/netsgo/releases/download/v{version}/netsgo_{os}_{arch}.tar.gz
ghproxy:  https://ghproxy.com/https://github.com/zsio/netsgo/releases/download/v{version}/netsgo_{os}_{arch}.tar.gz
```

（具体 asset 名称以 `.goreleaser.yml` 实际配置为准）

### 3.5 版本比较结果处理

| 情况 | 行为 |
|------|------|
| 已是最新版 | 提示"当前已是最新版本 vX.Y.Z"，退出 |
| 有新版本 | 展示版本号，让用户确认下载 |
| GitHub 不可达 | 提示网络问题，询问是否切换 ghproxy |
| ghproxy 也不可达 | 提示手动下载 + 使用 `netsgo upgrade` |
| 无法解析版本号 | 提示无法比较版本，退出 |

### 3.6 下载与替换流程

1. 从选定通道下载对应平台的 release asset（`.tar.gz`）
2. 解压获取二进制
3. **停止所有已安装的受管服务**（server 和/或 client）
4. 验证新二进制可执行
5. 替换 `/usr/local/bin/netsgo`
6. **启动所有已安装的受管服务**
7. 输出升级结果

### 3.7 文案

- 菜单项：`update   - 自动下载最新版本`
- 网络不可达提示：`
  无法连接到下载源获取最新版本。
  您可以：
  1. 检查网络连接或配置代理
  2. 手动下载：https://github.com/zsio/netsgo/releases
  3. 或运行：netsgo upgrade（如果您已下载新版本）
  `

---

## 4. `netsgo upgrade` 详细设计

### 4.1 入口

```bash
netsgo upgrade
```

### 4.2 前置检查（严格顺序）

```
1. 是否 root？
   否 → re-exec sudo

2. 是否有已安装的 service？
   否 → 提示"没有可升级的内容"，退出

3. 当前运行路径 == /usr/local/bin/netsgo？
   是 → 提示"当前运行的是系统安装版本，无需操作"，退出

4. 版本比较：
   a. 当前版本为 dev（非语义化）→ 提示风险，默认取消，用户确认后继续
   b. 当前 < 已安装 → 提示降级风险，默认取消，用户确认后继续
   c. 当前 == 已安装 → 提示"已是最新版本，无需升级"，退出
   d. 当前 > 已安装 → 正常继续
```

### 4.3 升级流程

1. 简洁提示将要替换 `/usr/local/bin/netsgo`
2. **停止所有已安装的受管服务**
3. 将当前运行二进制复制/替换到 `/usr/local/bin/netsgo`
4. **启动所有已安装的受管服务**
5. 输出升级结果（显示新旧版本号、重启的服务列表）

### 4.4 文案

- 命令描述：`用当前运行中的二进制替换系统安装版本`
- dev 版本确认提示：`当前运行的是开发版本，升级结果不可预期。是否继续？默认取消`
- 降级确认提示：`当前版本低于已安装版本，升级将造成降级。是否继续？默认取消`

---

## 5. 共享机制

### 5.1 版本比较

- 版本号格式：语义化版本（`v1.2.3` 或 `1.2.3`）
- 使用标准 semver 比较（解析 major/minor/patch）
- dev 版本：版本号非语义化时，走确认流程

### 5.2 二进制替换协议

**必须先停止服务，再替换二进制，最后再启动。**

原因：Linux 下如果二进制正被服务进程持有，直接覆盖会报 `text file busy` 错误。

```
stop server → stop client → 替换 /usr/local/bin/netsgo → start server → start client
```

只操作已安装的角色。通过 `svcmgr` 的 `ServiceSpec` 和 systemd 状态判断。

### 5.3 版本信息来源

当前运行二进制的版本号通过 `pkg/version` 包获取（编译时通过 `-ldflags -X netsgo/pkg/version.Current=...` 注入）。

已安装版本号通过以下方式获取：
- 读取 `/usr/local/bin/netsgo --version` 的输出
- 或读取 spec 文件中的版本信息（如果 spec 已包含）

**优先做法：** 直接执行 `/usr/local/bin/netsgo --version` 获取，不依赖 spec 中的版本字段。

---

## 6. 错误处理

| 错误场景 | 行为 |
|---------|------|
| 下载失败（网络/超时） | 提示具体错误，给出手动下载和 `upgrade` 备选方案 |
| 下载成功但校验失败 | 拒绝替换，提示重新下载 |
| 替换二进制后服务无法启动 | 回滚到旧版本（如果备份可用）或提示手动修复 |
| 权限不足（非 root） | `upgrade` 自动 re-exec sudo；`update` 在 manage 中已保证 root |
| 当前版本 == 已安装版本 | 直接退出，提示无需操作 |

---

## 7. 安全考虑

1. **只操作 `/usr/local/bin/netsgo`**，不修改其他路径
2. **替换前必须先停止服务**，避免 `text file busy`
3. **下载后验证二进制可执行**，不是直接覆盖
4. **不自动降级**（除非用户明确确认）
5. **dev 版本必须用户确认**，防止开发误操作
6. **不写入敏感信息**到任何文件
7. **使用临时目录下载和解压**，完成后清理

---

## 8. 与现有系统的集成

### 8.1 依赖

- `internal/svcmgr`：用于检测已安装服务、获取 service 状态、操作 systemd
- `pkg/version`：用于获取当前二进制版本
- `internal/tui`：用于交互式确认（如果 manage 的交互层需要）

### 8.2 修改点

| 文件 | 修改内容 |
|------|----------|
| `internal/manage/server.go` / `client.go` | 在管理菜单中新增 `update` 选项 |
| `internal/manage/update.go`（新增） | `update` 的核心逻辑：版本检查、下载、替换、重启 |
| `cmd/netsgo/cmd_upgrade.go`（新增） | `upgrade` 命令的 CLI 入口 |
| `internal/upgrade/upgrade.go`（新增） | `upgrade` 的核心逻辑：版本检查、替换、重启 |

### 8.3 不修改的内容

- 不改变 `cmd/netsgo/cmd_update.go` 的结构（它仍作为 `rootCmd` 的独立命令注册，`manage` 的 `update` 通过 `internal/manage` 实现）
- 不修改 `.goreleaser.yml` 或 Release 流程
- 不新增 CI 上传步骤

---

## 9. 验收标准

### 9.1 `update` 验收

- [ ] `manage` 菜单中新增 `update` 选项
- [ ] 选择 `update` 后能正确获取最新版本号
- [ ] 版本比较逻辑正确（等于/大于/小于/dev）
- [ ] 通道选择（GitHub / ghproxy）可用
- [ ] 下载并替换二进制成功
- [ ] 替换后自动重启已安装的受管服务
- [ ] 网络不可达时给出清晰提示和备选方案
- [ ] 非 service 模式提示不支持

### 9.2 `upgrade` 验收

- [ ] `netsgo upgrade` 命令可执行
- [ ] 非 root 时自动 re-exec sudo
- [ ] 无已安装 service 时正确提示并退出
- [ ] 当前运行路径 == `/usr/local/bin/netsgo` 时正确提示并退出
- [ ] 版本比较正确（等于/大于/小于/dev）
- [ ] dev 版本和降级场景要求用户确认
- [ ] 替换二进制成功
- [ ] 替换后自动重启已安装的受管服务
- [ ] 非 service 模式提示不支持

### 9.3 通用验收

- [ ] 替换前停止服务，替换后启动服务
- [ ] 不输出敏感信息
- [ ] 测试通过：`go test -tags dev ./...`
- [ ] 构建通过：`make build`

---

## 10. 非目标

本期明确不做：

- macOS / Windows 的升级支持
- Docker 容器内的自动升级
- direct-run 模式的升级
- 升级回滚能力
- 版本自动检测定时器/守护进程
- 差分更新（只下载变更部分）
- 多架构交叉升级检测（如 arm64 机器下载 amd64 二进制）
- Gitee / 其他国内镜像（仅 GitHub + ghproxy）
