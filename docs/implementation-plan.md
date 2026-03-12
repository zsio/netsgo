# NetsGo Dashboard & 系统管理 实施计划

本文档规划两大功能迭代：**第一期 Dashboard 全局仪表盘** 和 **第二期系统管理**。

---

## 第一期：Dashboard 全局仪表盘

### 背景与目标

当前问题：当没有 Agent 连接或未选中 Agent 时，右侧主区域仅显示一个空白引导 (`AgentEmptyState`)，信息量为零。

目标：在左侧 Sidebar 顶部增加一个固定的 **Dashboard** 菜单入口，点击后右侧展示服务端全局概览，包括：
- 统计卡片（Agent 在线/离线数、隧道活跃/停止数）
- 服务端信息卡（版本、端口、运行时长、存储路径）
- Agent 总表（所有 Agent 一览，含在线/离线状态，可点击跳转）
- 隧道总表（所有隧道汇总，按状态筛选）

### 交互方案

采用 **方案 A：Sidebar 顶部固定项**。

- 在 `AgentSidebar` 搜索框下方、Agent 分组上方，增加一个固定的 "📊 Dashboard" 菜单项
- 点击后，右侧主区域切换为 Dashboard 全局概览视图
- 点击具体 Agent 时，切回原有的 Agent 详情视图
- 当无 Agent 连接时，默认展示 Dashboard 视图

---

### 后端改动

#### [MODIFY] [server.go](file:///Users/dyy/projects/code/netsgo/internal/server/server.go)

**扩展 `handleAPIStatus` 响应**，增加以下字段：

```diff
 func (s *Server) handleAPIStatus(w http.ResponseWriter, r *http.Request) {
+    // 计算各类统计
     agentCount := 0
+    tunnelActive := 0
+    tunnelPaused := 0
+    tunnelStopped := 0
     s.agents.Range(func(_, value any) bool {
         agentCount++
+        a := value.(*AgentConn)
+        a.RangeProxies(func(_ string, t *ProxyTunnel) bool {
+            switch t.Config.Status {
+            case protocol.ProxyStatusActive:
+                tunnelActive++
+            case protocol.ProxyStatusPaused:
+                tunnelPaused++
+            case protocol.ProxyStatusStopped:
+                tunnelStopped++
+            }
+            return true
+        })
         return true
     })

     w.Header().Set("Content-Type", "application/json")
     json.NewEncoder(w).Encode(map[string]any{
         "status":        "running",
         "agent_count":   agentCount,
         "version":       "0.1.0",
+        "listen_port":   s.Port,
+        "uptime":        int64(time.Since(s.startTime).Seconds()),
+        "store_path":    s.getStorePath(),
+        "tunnel_active": tunnelActive,
+        "tunnel_paused": tunnelPaused,
+        "tunnel_stopped": tunnelStopped,
     })
 }
```

需要在 `Server` 结构体中增加 `startTime time.Time` 字段，在 `Start()` 方法开头设置 `s.startTime = time.Now()`。增加 `getStorePath()` 辅助方法返回 store 的实际路径。

---

### 前端改动

#### 新增类型

##### [MODIFY] [index.ts](file:///Users/dyy/projects/code/netsgo/web/src/types/index.ts)

扩展 `ServerStatus` 接口以匹配新的 `/api/status` 响应：

```typescript
export interface ServerStatus {
  status: string;
  agent_count: number;
  version: string;
  listen_port: number;
  uptime: number;         // seconds
  store_path: string;
  tunnel_active: number;
  tunnel_paused: number;
  tunnel_stopped: number;
}
```

---

#### UI Store 改动

##### [MODIFY] [ui-store.ts](file:///Users/dyy/projects/code/netsgo/web/src/stores/ui-store.ts)

增加 `activeView` 状态，用于区分 Dashboard 视图和 Agent 详情视图：

```typescript
type ActiveView = 'dashboard' | 'agent';

interface UIState {
  activeView: ActiveView;
  setActiveView: (view: ActiveView) => void;
  selectedAgentId: string | null;
  setSelectedAgentId: (id: string | null) => void;
  // ...existing...
}
```

- `setSelectedAgentId` 被调用时（选中某 Agent），自动切换 `activeView` 为 `'agent'`
- 点击 Dashboard 菜单时，调用 `setActiveView('dashboard')` 并将 `selectedAgentId` 置为 `null`
- 初始值为 `'dashboard'`

---

#### 新增 Hook

##### [NEW] [use-server-status.ts](file:///Users/dyy/projects/code/netsgo/web/src/hooks/use-server-status.ts)

```typescript
export function useServerStatus() {
  return useQuery({
    queryKey: ['server-status'],
    queryFn: () => api.get<ServerStatus>('/api/status'),
    refetchInterval: 10000, // 10s 轮询（status 无 SSE 推送）
  });
}
```

---

#### Sidebar 改动

##### [MODIFY] [AgentSidebar.tsx](file:///Users/dyy/projects/code/netsgo/web/src/components/custom/agent/AgentSidebar.tsx)

在搜索框下方增加固定的 Dashboard 菜单项：

```tsx
{/* Dashboard 固定入口 */}
<div
  className={`flex items-center py-2 px-3 mx-2 rounded-md cursor-pointer text-sm transition-colors ${
    activeView === 'dashboard'
      ? 'bg-primary/10 text-primary font-medium'
      : 'text-muted-foreground hover:bg-muted/50 hover:text-foreground'
  }`}
  onClick={() => setActiveView('dashboard')}
>
  <LayoutDashboard className="h-4 w-4 mr-2" />
  Dashboard
</div>
<div className="mx-3 my-2 border-t border-border/30" />
```

---

#### 新增 Dashboard 组件

##### [NEW] [OverviewPage.tsx](file:///Users/dyy/projects/code/netsgo/web/src/components/custom/dashboard/OverviewPage.tsx)

Dashboard 全局概览页，组装以下子模块：

```tsx
export function OverviewPage() {
  return (
    <div className="p-8 max-w-6xl mx-auto w-full flex flex-col gap-8 z-10">
      <OverviewHeader />
      <OverviewStatsGrid />
      <ServerInfoCard />
      <DashboardAgentTable />
      <DashboardTunnelTable />
    </div>
  );
}
```

##### [NEW] [OverviewHeader.tsx](file:///Users/dyy/projects/code/netsgo/web/src/components/custom/dashboard/OverviewHeader.tsx)

Dashboard 顶部标题区域，包含 "Dashboard" 标题和简要描述。

##### [NEW] [OverviewStatsGrid.tsx](file:///Users/dyy/projects/code/netsgo/web/src/components/custom/dashboard/OverviewStatsGrid.tsx)

四张统计卡片：

| 卡片 | 数据来源 | 图标 |
|------|----------|------|
| 在线 Agent 数 | `useAgents()` 中 `stats !== null` 的数量 | `Monitor` |
| 离线 Agent 数 | `useAgents()` 中 `stats === null` 的数量 | `MonitorOff` |
| 活跃隧道数 | `useServerStatus().tunnel_active` | `Zap` |
| 停止/暂停隧道数 | `useServerStatus().tunnel_paused + tunnel_stopped` | `Pause` |

卡片样式复用现有 `AgentStatsGrid` 的设计语言（glassmorphism 卡片 + 图标着色）。

##### [NEW] [ServerInfoCard.tsx](file:///Users/dyy/projects/code/netsgo/web/src/components/custom/dashboard/ServerInfoCard.tsx)

服务端信息卡，展示：
- 服务端版本号
- 监听端口
- 运行时长（使用 `formatUptime`）
- 隧道配置存储路径

使用横向排列的信息条目，风格类似 Agent 详情的 `AgentHeader`。

##### [NEW] [DashboardAgentTable.tsx](file:///Users/dyy/projects/code/netsgo/web/src/components/custom/dashboard/DashboardAgentTable.tsx)

Agent 总表，列出所有 Agent（在线 + 离线）：

| 列 | 说明 |
|----|------|
| 状态 | 绿色圆点 (在线) / 灰色圆点 (离线) |
| Hostname | Agent 主机名 |
| IP | 地址 |
| OS / Arch | 如 `linux/amd64` |
| 隧道数 | 该 Agent 上的隧道数量 |
| 操作 | "查看详情" 按钮，点击切换到 Agent 视图 |

数据来源：`useAgents()` hook。

##### [NEW] [DashboardTunnelTable.tsx](file:///Users/dyy/projects/code/netsgo/web/src/components/custom/dashboard/DashboardTunnelTable.tsx)

隧道总表，汇总所有 Agent 上的隧道：

| 列 | 说明 |
|----|------|
| 名称 | 隧道名 |
| 类型 | TCP / UDP / HTTP |
| 所属 Agent | hostname（可点击跳转） |
| 映射 | `remote_port → local_ip:local_port` |
| 状态 | active / paused / stopped 状态徽章 |

支持按状态筛选的 Tab 或 Filter。数据来源：`useAgents()` 中聚合所有 `agent.proxies`。

---

#### 路由 / 页面组装改动

##### [MODIFY] [dashboard.tsx](file:///Users/dyy/projects/code/netsgo/web/src/routes/dashboard.tsx)

修改 `DashboardPage` 组件，根据 `activeView` 状态决定右侧渲染内容：

```tsx
// 核心变更逻辑
const activeView = useUIStore((s) => s.activeView);

// 右侧主区域
{activeView === 'dashboard' ? (
  <OverviewPage />
) : selectedAgent ? (
  <div className="p-8 max-w-6xl mx-auto w-full flex flex-col gap-8 z-10">
    <AgentHeader agent={selectedAgent} />
    <AgentStatsGrid agent={selectedAgent} />
    <TunnelTable agent={selectedAgent} />
    <TrafficChart />
  </div>
) : (
  <AgentEmptyState />
)}
```

同时，将自动选中第一个 Agent 的逻辑改为：当 `activeView === 'agent'` 且 `selectedAgentId === null` 时才自动选中。初始加载时 `activeView` 为 `'dashboard'`，不自动选中 Agent。

---

### 新增文件总览 (第一期)

```
web/src/
├── hooks/
│   └── use-server-status.ts          [NEW]
├── components/custom/dashboard/
│   ├── OverviewPage.tsx              [NEW]
│   ├── OverviewHeader.tsx            [NEW]
│   ├── OverviewStatsGrid.tsx         [NEW]
│   ├── ServerInfoCard.tsx            [NEW]
│   ├── DashboardAgentTable.tsx       [NEW]
│   └── DashboardTunnelTable.tsx      [NEW]

internal/server/
└── server.go                         [MODIFY]
```

---

## 第二期：系统管理

### 背景与目标

为 NetsGo 服务端增加完整的管理能力，包括安全认证、访问控制、审计日志和隧道策略。所有管理功能统一放在 `/admin` 路由下。

### 路由结构

```
/dashboard           → 全局仪表盘（第一期，已实现）
/dashboard           → 选中 Agent 时展示 Agent 详情
/admin               → 系统管理（第二期）
  /admin/keys        → Key / Token 管理
  /admin/accounts    → 管理员账号管理
  /admin/logs        → 系统日志查看
  /admin/policies    → 隧道策略（端口范围、白名单）
  /admin/events      → 事件时间线
```

### 功能列表

#### 1. Key 管理 (`/admin/keys`)

**后端**：
- 新增数据模型 `APIKey`：`id`, `name`, `key_hash`, `permissions`, `created_at`, `expires_at`, `is_active`
- 新增 CRUD API：`GET/POST/PUT/DELETE /api/admin/keys`
- 改造 `handleAuth`：验证 Agent 提供的 Key 是否在有效 Key 列表中

**前端**：
- Key 列表表格（名称、权限、创建时间、到期时间、状态）
- 创建/编辑 Key 的 Dialog
- 支持禁用/启用 Key
- 复制 Key 到剪贴板

#### 2. 管理员账号 (`/admin/accounts`)

**后端**：
- 新增数据模型 `AdminUser`：`id`, `username`, `password_hash`, `role`, `created_at`, `last_login`
- 新增认证中间件（JWT 或 Session）
- 新增 API：`POST /api/auth/login`, `GET/POST/PUT/DELETE /api/admin/accounts`
- Web 面板的 API 请求需要携带认证信息

**前端**：
- 登录页面
- 账号列表表格
- 创建/编辑账号的 Dialog
- 角色分配（admin / viewer）

#### 3. 系统日志 (`/admin/logs`)

**后端**：
- 在 Server 中增加 ring buffer（容量 N 条）存储结构化日志
- 新增 API：`GET /api/admin/logs?level=&limit=&offset=`
- 可选：通过 SSE 实时推送新日志

**前端**：
- 日志列表，按时间倒序
- 支持按级别筛选（INFO / WARN / ERROR）
- 自动滚动 + 暂停滚动功能
- 日志条目高亮（ERROR 红色、WARN 黄色）

#### 4. 隧道策略 (`/admin/policies`)

**后端**：
- 新增数据模型 `TunnelPolicy`：允许的端口范围（`min_port`, `max_port`）、端口黑名单、Agent 白名单
- 新增 API：`GET/PUT /api/admin/policies`
- 在 `StartProxy` 中校验策略：拒绝不在范围内的端口

**前端**：
- 端口范围配置（起始-结束）
- 保留端口黑名单编辑器
- Agent 白名单管理
- 配置变更确认提示

#### 5. 事件时间线 (`/admin/events`)

**后端**：
- 新增 `EventStore` 持久化最近 N 条事件（基于现有 `EventBus`）
- 新增 API：`GET /api/admin/events?type=&limit=&since=`

**前端**：
- 时间线组件，展示 agent_online / agent_offline / tunnel_changed 等事件
- 按事件类型筛选
- 时间范围选择

### 前端架构改动 (第二期)

#### 路由扩展

##### [MODIFY] [router.ts](file:///Users/dyy/projects/code/netsgo/web/src/lib/router.ts)

```typescript
const routeTree = rootRoute.addChildren([
  indexRoute,
  dashboardRoute,
  adminRoute.addChildren([
    adminKeysRoute,
    adminAccountsRoute,
    adminLogsRoute,
    adminPoliciesRoute,
    adminEventsRoute,
  ]),
]);
```

##### [NEW] 新增路由文件

- `src/routes/admin.tsx` — Admin 布局（左侧导航 + 右侧内容）
- `src/routes/admin/keys.tsx`
- `src/routes/admin/accounts.tsx`
- `src/routes/admin/logs.tsx`
- `src/routes/admin/policies.tsx`
- `src/routes/admin/events.tsx`

#### 导航变更

在 `TopBar` 中增加管理入口（齿轮图标或菜单），点击跳转到 `/admin`。

---

## 验证计划

### 第一期验证

#### 后端单元测试

在现有 `internal/server/server_test.go` 中新增测试：

```bash
cd /Users/dyy/projects/code/netsgo && go test ./internal/server/ -run TestAPI_Status -v
```

新增测试覆盖 `/api/status` 扩展字段：
- `TestAPI_Status_ExtendedFields`：验证返回值包含 `listen_port`, `uptime`, `store_path`, `tunnel_active`, `tunnel_paused`, `tunnel_stopped`
- `TestAPI_Status_UptimeIncreasing`：验证 `uptime` 随时间递增
- `TestAPI_Status_TunnelCounts`：创建隧道后验证各状态计数正确

#### 前端构建检查

```bash
cd /Users/dyy/projects/code/netsgo/web && npx tsc --noEmit && pnpm run build
```

确保无编译错误。

#### 手动验证

1. 启动后端和前端开发服务器：
   ```bash
   # 终端 1
   cd /Users/dyy/projects/code/netsgo && go run ./cmd/server
   # 终端 2
   cd /Users/dyy/projects/code/netsgo/web && pnpm run dev
   ```
2. 打开浏览器，确认：
   - 左侧 Sidebar 顶部出现 "Dashboard" 菜单项
   - 点击 Dashboard 后，右侧展示全局概览（统计卡片 + 服务端信息 + Agent 表 + 隧道表）
   - 服务端信息显示正确的版本号、端口、运行时长
   - 连接一个 Agent 后，Agent 表和统计卡片实时更新
   - 点击 Agent 表中的 "查看详情" 后，切换到 Agent 详情视图
   - 点击 Sidebar 的 Agent 后，切到 Agent 详情；再点 Dashboard 可切回全局视图

### 第二期验证

> 第二期开始实施时，将根据具体模块制定详细的测试计划。初步预计：
> - 每个后端 API 模块增加对应的 `_test.go` 测试
> - 前端走构建检查 + 手动流程验证
> - 认证相关功能增加安全性测试（无效 Token、过期 Token 等）
