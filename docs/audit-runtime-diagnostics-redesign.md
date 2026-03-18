# RFC: 操作审计、运行态事件与诊断日志重构方案

> **状态**: 待审核  
> **作者**: AI Assistant  
> **日期**: 2026-03-18  
> **影响范围**: Server / Web / 管理接口 / 本地存储模型

---

## 1. 摘要

当前 NetsGo 把三类本质不同的信息混在了一起：

1. 面向管理员追责与回溯的“操作审计”
2. 面向运维观察系统状态变化的“运行态事件”
3. 面向开发与排障的“诊断日志”

现有实现的问题不是“展示太简陋”，而是模型本身不成立：

- “系统日志”只是若干手工拼接字符串的内存 ring buffer，无法承担审计职责
- “审计事件”本质上是把 SSE 推送顺手持久化，记录的是运行态变化，不是用户操作
- 页面直接展示原始 JSON 字符串，对用户没有业务意义
- 高价值操作并没有被完整记录，尤其缺少“谁在什么时间对什么对象做了什么、结果如何”
- `admin.json` 同时承担配置、账号、会话、事件历史等多重职责，职责过载

本 RFC 提议一步到位重构为三套边界清晰的能力：

1. **操作审计 Audit**
   面向管理员行为、敏感安全行为、关键配置变更，要求可追责、可检索、可导出
2. **运行态事件 Runtime Events**
   面向 Client、Tunnel、连接、恢复、限流、安全告警等运行时状态变化，要求可观察、可时间线展示
3. **诊断日志 Diagnostics**
   面向开发排障的结构化原始日志，要求可落盘、可轮转、可打包导出，但不作为主 UI 功能

此方案**不考虑向后兼容**。旧接口、旧页面、旧存储字段可以直接删除。

---

## 2. 背景与现状问题

### 2.1 当前前端入口

系统设置下当前存在两个入口：

- `系统日志`
- `审计事件`

这两个入口都在 UI 上被当作一级管理能力，但其数据来源和语义并不可靠。

### 2.2 当前后端实现的实际情况

当前实现存在以下问题：

1. `SystemLogEntry`
   - 只包含 `level / message / source`
   - 仅保存在内存 ring buffer
   - 服务重启后全部丢失
   - 主要通过 `AddSystemLog("INFO", "...", "admin")` 这类手工字符串写入

2. `EventRecord`
   - 只包含 `type / data`
   - `data` 是 JSON 字符串
   - 实际来源是 SSE 事件总线的持久化副产物
   - 记录到的是 `client_online`、`client_offline`、`tunnel_changed` 等运行态事件

3. `admin.json`
   - 同时存放管理员、Key、Client、Token、Session、ServerConfig、Events
   - 把管理配置和观察性历史揉在一起
   - 每次写事件都可能触发整份 JSON 重写

4. 审计覆盖不完整
   - 登录成功会记录，登录失败通常不会
   - Key、服务配置、策略有零散日志
   - Client 改名、隧道创建/编辑/暂停/恢复/停止/删除等高价值操作缺乏结构化审计

5. 展示层缺乏语义
   - 事件页直接展示 `evt.data`
   - 用户只能看到原始 JSON
   - 无法快速回答“是谁做的”“影响了什么”“成功还是失败”“和哪次请求相关”

### 2.3 根因

根因不是缺几个字段，而是三类能力被错误合并：

- 把“用于前端实时刷状态的总线”当成“审计来源”
- 把“调试日志”当成“用户可读记录”
- 把“持久化配置存储”当成“历史事件仓库”

---

## 3. 设计目标

### 3.1 目标

本次重构的目标是：

1. 让“操作审计”真正回答以下问题：
   - 谁做的
   - 在什么时间做的
   - 对什么对象做的
   - 做了什么动作
   - 成功、失败、拒绝还是部分成功
   - 影响范围是什么
   - 请求来源是什么

2. 让“运行态事件”真正回答以下问题：
   - 哪个 Client / Tunnel / 会话发生了状态变化
   - 变化前后是什么
   - 原因是什么
   - 是否与某次人工操作相关

3. 让“诊断日志”退回正确位置：
   - 主要服务于开发与排障
   - 不再作为系统设置下的主业务页面
   - 支持结构化落盘、轮转和导出

4. 让前端页面从“显示原始 JSON”变成“显示用户能读懂的记录”

5. 让后端写入方式从“手写字符串”和“顺手持久化”升级为显式模型、显式接口、显式职责

### 3.2 非目标

本 RFC 不追求：

- 向旧接口或旧存储格式兼容
- 继续保留 `admin.json.events`
- 继续把原始系统日志作为一级导航页面展示
- 继续使用“轮询 + 原始字符串”的页面交互模式

---

## 4. 设计原则

### 4.1 语义先于展示

先定义清楚“记录的是什么”，再决定前端怎么展示。  
禁止继续用“原始 JSON 字符串”充当前端模型。

### 4.2 记录来源必须显式

- 审计记录只能来自显式的审计写入
- 运行态事件只能来自显式的领域事件投影
- 诊断日志只能来自标准化 logger

禁止再出现“某个总线顺手订阅一下就当历史记录”的写法。

### 4.3 面向查询设计

记录结构必须首先满足：

- 过滤
- 搜索
- 排序
- 关联
- 导出

而不是先落一坨 JSON，前端再去猜意义。

### 4.4 单机单实例，但不再受限于 JSON 文件

本项目仍按单机单实例 Server 设计。  
但“单机”不等于“只能用单份 JSON 结构存一切”。  
对于审计与事件查询，单机内嵌数据库是更合理的单机方案。

### 4.5 记录不可丢语义

任何关键记录至少要有：

- 主体 actor
- 动作 action
- 目标 resource
- 结果 result
- 摘要 summary
- 上下文 context

---

## 5. 目标架构

### 5.1 总体分层

```text
                    ┌─────────────────────────────────────┐
                    │         Domain Operations           │
                    │  登录 / Key / 配置 / 隧道 / 会话     │
                    └──────────────┬──────────────────────┘
                                   │
                ┌──────────────────┴──────────────────┐
                │                                     │
         显式审计写入                           领域事件发布
                │                                     │
       ┌────────▼────────┐                 ┌──────────▼──────────┐
       │  Audit Recorder │                 │   Domain Event Bus   │
       └────────┬────────┘                 └──────────┬──────────┘
                │                                     │
       ┌────────▼────────┐              ┌─────────────▼─────────────┐
       │   Audit Store   │              │ Runtime Projectors        │
       │   (SQLite)      │              │ 1. RuntimeEvent Store     │
       └────────┬────────┘              │ 2. Console Stream Project │
                │                       └─────────────┬─────────────┘
                │                                     │
      ┌─────────▼─────────┐               ┌───────────▼───────────┐
      │ /api/admin/audit  │               │ /api/admin/runtime-*  │
      └───────────────────┘               │ /api/console/stream   │
                                          └───────────────────────┘

                    ┌─────────────────────────────────────┐
                    │     Structured Diagnostic Logger    │
                    │   slog + file rotation + export     │
                    └─────────────────────────────────────┘
```

### 5.2 三套能力的边界

#### A. 操作审计 Audit

适用范围：

- 管理员登录成功 / 失败 / 登出
- 初始化完成
- API Key 创建 / 启用 / 禁用 / 删除
- 服务配置修改
- 隧道策略修改
- Client 展示名修改
- 隧道创建 / 编辑 / 暂停 / 恢复 / 停止 / 删除
- 未来的用户管理、会话踢出、权限变更、Token 吊销等

要求：

- 持久化
- 可分页查询
- 可按 actor、action、resource、result、时间搜索
- 可导出
- 可关联请求 ID / session ID / correlation ID

#### B. 运行态事件 Runtime Events

适用范围：

- Client 上线 / 下线
- 数据通道建立 / 断开
- Tunnel 状态变化
- 隧道恢复批次完成
- 限流触发
- 安全告警
- 后台任务异常

要求：

- 结构化持久化
- 允许短中期保留
- 前端展示为人类可读的时间线
- 可与审计记录关联，但不等于审计记录

#### C. 诊断日志 Diagnostics

适用范围：

- 开发排障
- 服务内部错误栈
- 网络细节、协议细节、异常细节

要求：

- 使用结构化 logger
- 落文件而不是混进 UI 业务记录
- 支持轮转
- 支持下载支持包

---

## 6. 存储设计

### 6.1 总体决策

本 RFC 采用以下存储模型：

1. **保留 `admin.json`**
   - 仅用于管理员账号、会话、Key、ServerConfig、Client 元数据等管理状态
   - 删除 `Events` 字段

2. **新增本地嵌入式 SQLite**
   - 文件建议为 `~/.netsgo/observability.db`
   - 用于保存审计记录和运行态事件
   - 推荐使用纯 Go SQLite 驱动，保持单文件交付与无 CGO 依赖

3. **新增诊断日志目录**
   - 路径建议为 `~/.netsgo/logs/`
   - 使用 JSON Lines 或 text+attributes 的结构化文件输出
   - 支持大小与日期双重轮转

### 6.2 为什么选 SQLite

相比继续写 JSON 文件，SQLite 更符合本场景：

- 单机单实例天然适配
- 支持过滤、排序、分页、索引、全文检索
- 适合审计和事件这种追加写、多条件读的模型
- 避免每次更新都重写整份 `admin.json`
- 比自研 append-only 文件 + 索引系统更简单也更可靠

### 6.3 审计表设计

建议表：`audit_records`

核心字段：

| 字段 | 类型 | 说明 |
|------|------|------|
| `id` | TEXT PK | UUID |
| `occurred_at` | DATETIME | 发生时间 |
| `actor_type` | TEXT | `admin_user` / `system` / `anonymous` / `client` |
| `actor_id` | TEXT | 操作者 ID |
| `actor_name` | TEXT | 用户名 / install_id / system |
| `actor_role` | TEXT | admin / viewer 等 |
| `session_id` | TEXT | 管理会话 ID |
| `request_id` | TEXT | 请求级 ID |
| `correlation_id` | TEXT | 关联链路 ID |
| `ip` | TEXT | 来源 IP |
| `user_agent` | TEXT | UA |
| `action` | TEXT | 规范化动作名 |
| `resource_type` | TEXT | `api_key` / `server_config` / `client` / `tunnel` |
| `resource_id` | TEXT | 资源主键 |
| `resource_name` | TEXT | 展示名 |
| `result` | TEXT | `success` / `rejected` / `failed` / `partial` |
| `summary` | TEXT | 人类可读摘要 |
| `details_json` | TEXT | 结构化上下文 |
| `diff_json` | TEXT | 变更前后摘要 |
| `tags_json` | TEXT | 标签 |

索引建议：

- `occurred_at DESC`
- `(actor_type, actor_id, occurred_at DESC)`
- `(action, occurred_at DESC)`
- `(resource_type, resource_id, occurred_at DESC)`
- `(result, occurred_at DESC)`
- `(correlation_id, occurred_at DESC)`

全文检索建议：

- 对 `summary`
- 对 `actor_name`
- 对 `resource_name`
- 对 `details_json` 中可索引的文本影子字段

### 6.4 运行态事件表设计

建议表：`runtime_events`

核心字段：

| 字段 | 类型 | 说明 |
|------|------|------|
| `id` | TEXT PK | UUID |
| `occurred_at` | DATETIME | 发生时间 |
| `category` | TEXT | `client` / `tunnel` / `session` / `security` / `system` |
| `event_type` | TEXT | 规范化事件名 |
| `severity` | TEXT | `info` / `warn` / `error` |
| `subject_type` | TEXT | `client` / `tunnel` / `session` |
| `subject_id` | TEXT | 主体 ID |
| `subject_name` | TEXT | 主体展示名 |
| `summary` | TEXT | 人类可读摘要 |
| `payload_json` | TEXT | 结构化事件详情 |
| `correlation_id` | TEXT | 关联链路 ID |
| `caused_by_audit_id` | TEXT NULL | 若由某次管理操作直接触发，则关联审计记录 |

索引建议：

- `occurred_at DESC`
- `(category, occurred_at DESC)`
- `(event_type, occurred_at DESC)`
- `(subject_type, subject_id, occurred_at DESC)`
- `(severity, occurred_at DESC)`

### 6.5 保留策略

建议默认策略：

- 审计记录：保留 365 天，可配置
- 运行态事件：保留 30 天，可配置
- 诊断日志：保留最近 7 天或最近 N GiB

说明：

- 审计记录按时间删除，不按条数硬裁剪
- 运行态事件允许更短保留期
- 诊断日志只做排障，不做长期归档

---

## 7. 领域模型设计

### 7.1 审计动作模型

审计记录必须来自“动作完成边界”，而不是中间状态。

例如：

- 登录成功
- 登录失败
- 隧道创建成功
- 隧道创建失败
- 隧道删除被拒绝，因为状态不允许

建议动作命名：

| 动作名 | 含义 |
|------|------|
| `auth.login` | 管理员登录 |
| `auth.logout` | 管理员登出 |
| `setup.initialize` | 首次初始化 |
| `api_key.create` | 创建 Key |
| `api_key.enable` | 启用 Key |
| `api_key.disable` | 禁用 Key |
| `api_key.delete` | 删除 Key |
| `server_config.update` | 修改服务配置 |
| `tunnel_policy.update` | 修改隧道策略 |
| `client.display_name.update` | 修改 Client 展示名 |
| `tunnel.create` | 创建隧道 |
| `tunnel.update` | 编辑隧道 |
| `tunnel.pause` | 暂停隧道 |
| `tunnel.resume` | 恢复隧道 |
| `tunnel.stop` | 停止隧道 |
| `tunnel.delete` | 删除隧道 |

### 7.2 审计结果模型

统一结果枚举：

- `success`
- `rejected`
- `failed`
- `partial`

语义要求：

- `success`：动作完成
- `rejected`：系统基于规则拒绝，例如状态不允许、参数不合法、权限不足
- `failed`：预期允许，但执行异常
- `partial`：动作完成但伴随部分副作用，例如配置修改成功但部分隧道被标记异常

### 7.3 运行态事件模型

运行态事件必须是**结构化语义事件**，不能只是 `type + arbitrary JSON blob`。

建议事件命名：

| 事件名 | 类别 | 说明 |
|------|------|------|
| `client.online` | client | Client 进入 live |
| `client.offline` | client | Client 离线 |
| `client.pending_data_timeout` | session | 控制通道已认证但数据通道超时 |
| `data_channel.connected` | session | 数据通道建立 |
| `data_channel.disconnected` | session | 数据通道断开 |
| `tunnel.created` | tunnel | 隧道创建成功 |
| `tunnel.updated` | tunnel | 隧道配置更新 |
| `tunnel.paused` | tunnel | 隧道暂停 |
| `tunnel.resumed` | tunnel | 隧道恢复 |
| `tunnel.stopped` | tunnel | 隧道停止 |
| `tunnel.deleted` | tunnel | 隧道删除 |
| `tunnel.restore_batch.completed` | tunnel | 批量恢复完成 |
| `security.rate_limited` | security | 限流触发 |
| `security.session_mismatch` | security | Session UA 不匹配等安全异常 |

### 7.4 关联模型

需要引入统一的 `request_id` 与 `correlation_id`：

- HTTP 请求入口生成 `request_id`
- 一次用户操作产生的审计记录使用同一个 `correlation_id`
- 该操作引发的运行态事件携带同一个 `correlation_id`

这样前端可以在审计详情中看到：

- 此次操作触发了哪些运行态结果

也可以在运行态事件详情中看到：

- 该事件是否由某次人工操作触发

---

## 8. 后端实现设计

### 8.1 模块划分

建议新增模块：

```text
internal/observability/
├── audit/
│   ├── model.go
│   ├── recorder.go
│   ├── store.go
│   ├── query.go
│   └── export.go
├── runtime/
│   ├── event.go
│   ├── bus.go
│   ├── projector_store.go
│   ├── projector_console.go
│   └── query.go
├── diagnostics/
│   ├── logger.go
│   ├── context.go
│   └── rotate.go
└── sqlite/
    ├── open.go
    ├── audit_repo.go
    └── runtime_repo.go
```

### 8.2 审计写入方式

禁止继续到处调用：

- `AddSystemLog("INFO", "...", "admin")`
- `AddEvent("...", "...")`

改为统一入口：

```go
auditRecorder.Record(ctx, audit.Record{
    Action:   audit.ActionTunnelCreate,
    Actor:    actor,
    Resource: resource,
    Result:   audit.ResultSuccess,
    Summary:  "管理员 alice 为 client edge-01 创建 TCP 隧道 web-80",
    Details:  ...,
    Diff:     ...,
})
```

要求：

1. 审计由 handler 或 service 在“知道最终结果”时写入
2. 失败、拒绝也要写
3. 摘要与结构化字段同时存在
4. 不允许只写自然语言字符串而没有结构化上下文

### 8.3 运行态事件写入方式

领域层通过 typed event 发布：

```go
runtimeBus.Publish(ctx, runtime.Event{
    Type:      runtime.EventClientOnline,
    Category:  runtime.CategoryClient,
    Severity:  runtime.SeverityInfo,
    Subject:   ...,
    Summary:   "Client edge-01 已上线",
    Payload:   ...,
})
```

投影器拆分为两条：

1. `RuntimeEventProjector`
   - 持久化到 `runtime_events`
2. `ConsoleProjector`
   - 转换为前端控制台增量更新

这意味着：

- 运行态历史不再来自 SSE 顺手持久化
- SSE 只负责实时 UI 同步
- 历史与实时共用领域语义，但不是同一存储对象

### 8.4 控制台实时流重构

当前 `/api/events` 名称模糊，既像事件总线，又像审计源。

建议改为：

- `/api/console/stream`

只承担：

- 控制台快照
- Client 在线状态变化
- Tunnel 视图变化
- stats 更新

配套修改：

- `useEventStream` 重命名为 `useConsoleStream`
- SSE 事件类型命名统一化，例如：
  - `console.ready`
  - `console.snapshot`
  - `client.online`
  - `client.offline`
  - `tunnel.changed`
  - `stats.updated`

### 8.5 诊断日志重构

建议把当前 `log.Printf` 整体迁移到 `log/slog`：

- 标准字段：`time`, `level`, `msg`
- 扩展字段：`request_id`, `session_id`, `client_id`, `tunnel`, `remote_ip`, `component`

日志输出策略：

1. 控制台输出：开发模式下人类可读
2. 文件输出：生产模式下 JSON lines
3. 支持 `support bundle` 导出

诊断日志不再通过 `/api/admin/logs` 做主功能查询。  
如需后台查看，可单独提供：

- `GET /api/admin/diagnostics/files`
- `POST /api/admin/diagnostics/export`

但这不是首期主要 UI 能力。

### 8.6 中间件与上下文

新增请求上下文中间件，自动注入：

- `request_id`
- `session_info`
- `remote_ip`
- `user_agent`
- `correlation_id`

审计与日志均从上下文取公共字段，不允许各 handler 重复拼装。

### 8.7 删除项

本 RFC 明确删除以下旧设计：

1. `AdminData.Events`
2. `EventRecord`
3. `SystemLogEntry` 对外查询能力
4. `/api/admin/logs`
5. `/api/admin/events`
6. `persistEventsLoop`
7. “事件页直接显示原始 JSON”
8. “系统日志”作为系统设置一级菜单

---

## 9. 前端设计

### 9.1 菜单重构

系统设置一级菜单调整为：

- `服务配置`
- `Key 管理`
- `隧道策略`
- `操作审计`
- `运行态事件`

`系统日志` 从一级菜单移除。  
若保留诊断能力，放到：

- 调试入口
- 支持包导出弹窗
- 或隐藏管理页

### 9.2 操作审计页面

目标：一眼看懂谁做了什么。

列表主字段：

- 时间
- 操作者
- 动作
- 资源
- 结果
- 摘要

筛选器：

- 时间范围
- 操作者
- 动作
- 资源类型
- 结果
- 关键词

详情抽屉：

- 请求信息
- 资源信息
- 变更前后 diff
- 结构化详情
- 关联运行态事件

### 9.3 运行态事件页面

目标：一眼看懂系统发生了什么变化。

列表主字段：

- 时间
- 类别
- 级别
- 主体对象
- 事件摘要

筛选器：

- 时间范围
- 类别
- 事件类型
- 级别
- Client / Tunnel
- 关键词

详情抽屉：

- 结构化详情
- 关联审计记录
- 关联主体当前状态

### 9.4 展示原则

禁止直接向用户展示原始 `payload_json`。  
所有已知事件类型都必须有明确的前端渲染器：

- Client 相关事件渲染 Client 名称、ID、来源 IP
- Tunnel 相关事件渲染隧道名、协议、端口、状态变化
- 安全事件渲染触发原因和来源

原始 JSON 只能作为折叠后的“原始数据”高级信息存在。

### 9.5 数据获取方式

查询页使用分页拉取：

- 审计页：cursor pagination
- 运行态事件页：cursor pagination

实时刷新方式：

- 默认不开全局 5 秒轮询
- 页面停留时可使用局部轮询或局部流式订阅
- 控制台状态仍通过 `console stream` 全局连接维护

---

## 10. API 设计

### 10.1 审计 API

#### `GET /api/admin/audit-records`

查询参数：

- `cursor`
- `limit`
- `from`
- `to`
- `actor`
- `action`
- `resource_type`
- `resource_id`
- `result`
- `q`

响应：

```json
{
  "items": [
    {
      "id": "aud_01",
      "occurred_at": "2026-03-18T12:34:56Z",
      "actor": {
        "type": "admin_user",
        "id": "u_1",
        "name": "alice",
        "role": "admin"
      },
      "action": "tunnel.create",
      "resource": {
        "type": "tunnel",
        "id": "client-1:web-80",
        "name": "web-80"
      },
      "result": "success",
      "summary": "管理员 alice 为 client edge-01 创建 TCP 隧道 web-80",
      "request_id": "req_123",
      "correlation_id": "corr_123"
    }
  ],
  "next_cursor": "..."
}
```

#### `GET /api/admin/audit-records/{id}`

返回完整详情，包括：

- request context
- details
- diff
- related runtime events

#### `GET /api/admin/audit-records/export`

支持：

- `format=jsonl`
- `format=csv`

### 10.2 运行态事件 API

#### `GET /api/admin/runtime-events`

查询参数：

- `cursor`
- `limit`
- `from`
- `to`
- `category`
- `event_type`
- `severity`
- `subject_type`
- `subject_id`
- `q`

#### `GET /api/admin/runtime-events/{id}`

返回完整详情，包括：

- payload
- caused_by_audit
- related subject snapshot

#### `GET /api/admin/runtime-events/stream`

可选。用于当前页 live tail，不作为全局状态同步入口。

### 10.3 控制台实时流 API

#### `GET /api/console/stream`

用于：

- Dashboard 实时状态同步
- 在线状态更新
- 探针数据更新

不用于：

- 审计历史
- 运行态历史查询

---

## 11. 关键实现约束

### 11.1 审计必须覆盖失败和拒绝

以下情况必须记录：

- 登录失败
- 参数校验失败
- 资源不存在
- 状态不允许
- 权限不足
- 限流触发

否则审计会再次失去意义。

### 11.2 审计记录必须在最终结果边界写入

例如 `tunnel.delete`：

- 资源不存在：`rejected`
- 状态不允许删除：`rejected`
- 调用删除逻辑报错：`failed`
- 删除成功：`success`

禁止在中间过程先写一条“尝试删除”再没有结论。

### 11.3 运行态事件必须有稳定模板

每个 `event_type` 必须定义：

- 类别
- 级别
- 摘要模板
- 允许的 payload 字段

前后端共享同一套类型定义，避免再次退化为自由 JSON。

### 11.4 审计与运行态事件禁止共用同一张表

原因：

- 生命周期不同
- 查询维度不同
- 读者不同
- 是否可删、可导出的要求不同

### 11.5 诊断日志不再做主导航

原始日志是技术工具，不是业务审计。  
它可以存在，但不应继续占据系统设置一级入口。

---

## 12. 权限设计

建议把当前简单角色扩展为能力权限：

- `audit.read`
- `audit.export`
- `runtime.read`
- `diagnostics.read`
- `diagnostics.export`

默认映射建议：

| 角色 | audit.read | audit.export | runtime.read | diagnostics.read | diagnostics.export |
|------|------------|--------------|--------------|------------------|--------------------|
| `admin` | 是 | 是 | 是 | 是 | 是 |
| `viewer` | 是 | 否 | 是 | 否 | 否 |

如果暂时不做细粒度 RBAC，也至少要保证：

- `viewer` 可读运行态事件
- 审计导出仅 `admin` 可用

---

## 13. 实施方案

### 13.1 一步到位替换

由于本次明确不考虑兼容，建议直接采用“新旧不并存”的方式：

1. 引入 `observability.db`
2. 建立 Audit / Runtime / Diagnostics 三套模块
3. 删除 `AdminData.Events`
4. 删除 `/api/admin/logs` 和 `/api/admin/events`
5. 删除对应前端页面、hooks、types
6. 将全局 SSE 更名为 `console stream`
7. 新增审计页与运行态事件页
8. 将现有所有关键 handler 全量接入审计
9. 将运行态变化统一改走 typed runtime events

### 13.2 首批必须接入的后端入口

必须首批完成接入的接口与动作：

- setup init
- auth login success/failure/logout
- API Key create/enable/disable/delete
- server config update
- tunnel policy update
- client display name update
- tunnel create/update/pause/resume/stop/delete

### 13.3 首批必须接入的运行态事件

- client online/offline
- data channel connected/disconnected
- tunnel created/updated/paused/resumed/stopped/deleted
- tunnel restore batch completed
- security rate limited
- session environment mismatch

---

## 14. 验收标准

### 14.1 审计能力验收

以下问题必须可以直接回答：

1. 昨天谁删除了某个 API Key？
2. 某条隧道是谁创建的？后来谁修改过？
3. 某个管理员何时登录失败过？来源 IP 是什么？
4. 某次配置修改影响了哪些资源？
5. 某次操作是成功、失败还是被规则拒绝？

### 14.2 运行态能力验收

以下问题必须可以直接回答：

1. 某个 Client 什么时候上线、什么时候下线？
2. 某条隧道为何从 active 变成 paused / error？
3. 某次人工操作之后，系统运行态发生了哪些后续变化？
4. 某段时间是否有连续限流或安全异常？

### 14.3 UI 能力验收

必须满足：

- 首页导航中不再有“系统日志”和“审计事件”这种混淆命名
- 页面默认展示必须是用户可读摘要
- 原始 JSON 不能成为主视图
- 审计详情与运行态详情都必须支持结构化展开

### 14.4 存储能力验收

必须满足：

- `admin.json` 不再包含事件历史
- 审计与运行态数据可独立查询
- 重启服务后审计与运行态历史仍存在
- 诊断日志可轮转且可导出

---

## 15. 测试策略

### 15.1 单元测试

- 审计记录构造器
- 运行态事件模板
- SQLite repository 查询条件
- retention 清理逻辑
- diagnostics log rotation

### 15.2 集成测试

- 关键 handler 触发审计写入
- 关键运行态变化触发 runtime event 写入
- `/api/admin/audit-records` 查询过滤正确
- `/api/admin/runtime-events` 查询过滤正确
- `/api/console/stream` 仅负责控制台实时状态

### 15.3 前端测试

- 审计页筛选与详情展开
- 运行态事件页渲染器
- 控制台流仍能驱动 Dashboard 状态更新

### 15.4 E2E

至少覆盖：

- 登录失败 -> 登录成功 -> 创建隧道 -> 暂停 -> 恢复 -> 删除
- 对应审计记录完整
- 对应运行态事件完整
- nginx / caddy 路径下控制台流正常

---

## 16. 风险与规避

### 16.1 风险：审计遗漏

规避：

- 统一封装审计 helper
- 对关键 handler 建立测试清单
- Code review 中把“是否写审计”列为固定检查项

### 16.2 风险：运行态事件再次退化为自由 JSON

规避：

- 每个事件类型注册模板与字段 schema
- 前端按 `event_type` 做明确渲染
- 未注册事件不得直接暴露为一级页面记录

### 16.3 风险：日志、事件、审计再次耦合

规避：

- 模块物理拆分
- 存储拆分
- API 拆分
- 导航拆分

---

## 17. 最终决策

本 RFC 的最终结论如下：

1. **废弃当前“系统日志 / 审计事件”的实现与命名**
2. **操作审计、运行态事件、诊断日志必须彻底拆分**
3. **审计与运行态历史改用本地 SQLite 持久化**
4. **原始系统日志退出主导航，改为诊断能力**
5. **SSE 改名并收缩为控制台实时状态流，不再承担历史记录语义**
6. **所有关键管理操作必须有结构化审计记录，且覆盖成功、失败、拒绝三类结果**

这是一次模型纠偏，而不是一次页面美化。

只有先把模型改对，前端展示才会真正有意义。
