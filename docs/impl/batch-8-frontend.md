# Batch 8：前端重构

> 状态：待实现
> 前置条件：Batch 5 完成（后端 API 稳定后再做前端）
> 估计影响文件：`web/src/components/custom/tunnel/TunnelDialog.tsx`、`web/src/components/custom/tunnel/`、`web/src/lib/api.ts`、`web/src/hooks/`

## 目标

重构前端隧道相关界面，使其支持 HTTP 隧道的创建、编辑、状态展示，并正确处理 `server_addr` / `effective_server_addr` 的区分、`server_addr_locked` 只读模式、域名冲突提示等。

**重要约束**：前端路由继续使用 TanStack Router Hash 模式，不切换为 History Router。

## 要做的改动

### 1. 统一的 Tunnel View Model

**文件**：`web/src/lib/tunnel-model.ts`（新建）

目的：让隧道列表、概览卡片、详情页、表单复用同一套视图模型，避免各处各自映射字段。

```typescript
export type TunnelType = 'tcp' | 'udp' | 'http'
export type TunnelStatus = 'pending' | 'active' | 'paused' | 'stopped' | 'error'

export interface TunnelViewModel {
  clientId: string
  name: string
  type: TunnelType
  status: TunnelStatus
  localIp: string
  localPort: number
  // TCP/UDP 专用
  remotePort?: number
  // HTTP 专用
  domain?: string
  error?: string
}

// 从 API 响应映射到 ViewModel
export function toTunnelViewModel(raw: ApiTunnel): TunnelViewModel

// 判断隧道是否可服务（用于列表状态展示）
export function isTunnelServicable(tunnel: TunnelViewModel, clientOnline: boolean): boolean
```

**原则**：先落 tunnel view model，再让列表 / 概览 / 详情页复用，不要各页面各自做字段映射。

### 2. TunnelDialog 表单重构

**文件**：`web/src/components/custom/tunnel/TunnelDialog.tsx`

当前表单以「公网端口」心智为主，HTTP 隧道需要独立的表单逻辑。

需要改动：

- 类型选择器增加 `http` 选项
- 选择 `http` 时，隐藏「公网端口」字段，显示「域名」字段
- 域名字段前端基础校验：
  - 非空
  - 格式合法（不含 scheme、路径、泛域名）
  - 不等于当前生效管理 Host
- `local_ip` 和 `local_port` 字段对 http 类型同样保留
- 提交时如果后端返回 `409 Conflict`，根据错误码区分显示：
  - `server_addr_conflict`："域名与管理地址冲突"
  - `http_tunnel_conflict`："域名已被其他隧道占用"
  - `client_offline_action_not_allowed`："Client 离线，无法执行此操作"

### 3. 隧道列表与状态展示

**文件**：`web/src/components/custom/tunnel/TunnelList.tsx`（或现有对应组件）

- 复用 `TunnelViewModel` 和 `isTunnelServicable`
- HTTP 隧道显示「域名」而非「公网端口」
- 状态标签统一：pending / active / paused / stopped / error
- active 但 Client 离线时，显示「不可服务」状态

### 4. 管理配置页改动

**文件**：`web/src/components/custom/settings/`（或现有 AdminConfig 相关组件）

#### 4.1 `server_addr` 只读模式

- 调用 `GET /api/admin/config` 后，检查 `server_addr_locked`
- 若 `server_addr_locked == true`，`server_addr` 输入框改为只读展示，不允许提交
- 展示 `effective_server_addr`（实际生效地址）与 `server_addr`（保存值）的区别

#### 4.2 dry-run 冲突展示扩展

- 修改 `server_addr` 时调用 dry-run，解析新增的 `conflicting_http_tunnels` 字段
- 若 `conflicting_http_tunnels` 非空，禁止保存按钮并展示冲突隧道列表
- `affected_tunnels`（端口白名单影响）可二次确认继续，`conflicting_http_tunnels` 必须阻止保存

### 5. Client 接入文案

- 在 Client 接入引导页/弹窗中，区分说明 TCP、UDP、HTTP 三种隧道类型的配置方式
- HTTP 隧道强调：需要域名解析到服务端 IP，不填写公网端口

## 实现步骤

1. 先查看现有 TunnelDialog、列表组件、AdminConfig 组件的实际代码（`web/src/components/custom/tunnel/`）
2. 新建 `web/src/lib/tunnel-model.ts`，定义 TunnelViewModel
3. 改 TunnelDialog：加 http 类型分支，domain 字段，前端校验，409 错误处理
4. 改隧道列表组件：复用 TunnelViewModel，HTTP 隧道展示域名
5. 改 AdminConfig 组件：server_addr_locked 只读，conflicting_http_tunnels 展示
6. 跑前端构建验证

## 验收标准

```bash
# 前端构建通过
cd web && bun run build

# lint 检查
bun run lint
```

### 功能验收检查点

1. 创建 TCP/UDP 隧道表单行为不变（回归）
2. 创建 HTTP 隧道：选择 http 类型后出现域名字段，公网端口隐藏
3. HTTP 隧道创建成功后，列表显示域名而非端口
4. 域名冲突时，toast 显示具体冲突原因
5. 管理配置页：环境变量锁定时 server_addr 不可编辑
6. 管理配置页：dry-run 时 HTTP 域名冲突会阻止保存

## 不引入的改动

- 不切换为 History Router
- 不新增管理面路由
- 不改后端 API
- 不改 nginx/caddy 配置
