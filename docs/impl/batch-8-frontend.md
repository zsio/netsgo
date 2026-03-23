# Batch 8：前端联调与展示收口

> 状态：待实现  
> 所属阶段：阶段 6（Frontend 收尾）  
> 前置条件：阶段 4 完成  
> 估计影响文件：`web/src/types/index.ts`、`web/src/lib/tunnel-model.ts`（新建或同类文件）、`web/src/hooks/use-tunnel-mutations.ts`、`web/src/components/custom/tunnel/`、`web/src/components/custom/dashboard/`、`web/src/routes/dashboard/clients.$clientId.tsx`、`web/src/routes/admin/config.tsx`、`web/src/components/custom/client/AddClientDialog.tsx`

## 目标

在后端语义稳定后，把前端所有与 tunnel 相关的展示和交互收成一套统一解释：

1. HTTP 隧道表单可创建 / 编辑
2. 列表、详情、概览对状态的解释一致
3. `server_addr` / `effective_server_addr` / `server_addr_locked` 展示清楚
4. Add Client 文案不再把管理地址误写成唯一连接地址

## 核心原则

### 1. 先统一 view model，再改页面

不要让：

- 列表页自己判断一套状态
- 详情页自己判断一套状态
- 概览页再判断一套状态

应先有统一的派生模型，再由页面消费。

### 2. 不扩展前端状态源

继续使用：

- `/api/clients`
- SSE
- TanStack Query

不要为了 HTTP 隧道再造一套并行 store。

### 3. 不把 offline 伪装成 paused

前端必须区分：

- tunnel 配置状态：`pending / active / paused / stopped / error`
- 当前可服务性：例如 client offline

## 需要完成的部分

### 一、类型与 view model

建议补齐：

- `ProxyStatus` 的 `pending`
- HTTP 冲突返回类型
- AdminConfig 的新增字段类型
- 统一 tunnel view model helper

### 二、TunnelDialog + mutations

要求：

- `type=http` 时显示 `domain + local_ip + local_port`
- 不再把 `remote_port` 当成 HTTP 的用户输入
- 处理后端 `409` 的结构化冲突错误

### 三、列表 / 详情 / 概览统一

至少覆盖：

- tunnel 列表
- dashboard 概览
- client 详情页

要求：

- 都复用同一套 view model
- HTTP 隧道展示为 `domain -> local_ip:local_port`
- active 但 client offline 时展示“不可服务”，不是“已暂停”

### 四、AdminConfig

要求：

- 显示 `server_addr`
- 显示 `effective_server_addr`
- `server_addr_locked=true` 时只读
- dry-run 展示 `conflicting_http_tunnels`

### 五、Add Client 文案

要求：

- 明确 `server_addr` 是默认推荐值
- 不把它写成唯一允许连接地址
- 若存在锁定的生效地址，明确说明来源

## 实现步骤

1. 先补类型定义和 tunnel view model
2. 再改 `use-tunnel-mutations.ts` 与 `TunnelDialog`
3. 再统一列表 / 概览 / 详情页展示
4. 再改 `AdminConfig`
5. 最后改 `AddClientDialog` 文案

## 验收标准

```bash
cd web && bun run build
bun run lint
```

## 功能检查点

1. TCP / UDP 表单行为不回归
2. 选择 HTTP 类型后显示 domain，隐藏公网端口输入
3. HTTP 隧道创建成功后，列表 / 详情 / 概览展示一致
4. `409` 冲突时提示具体原因
5. `server_addr_locked=true` 时配置页不可编辑
6. dry-run 的 HTTP 域名冲突会阻止保存
7. Add Client 文案不会暗示“只能连接管理 Host”

## 不引入的改动

- 不切换到 History Router
- 不新增前端测试框架
- 不改后端 API 语义
- 不改 nginx/caddy 配置
