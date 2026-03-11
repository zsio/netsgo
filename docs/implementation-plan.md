# NetsGo Web 前端实施计划（全面修订版）

本文档是 NetsGo 前端架构落地的完整指南。基于对现有代码库（`web/src/App.tsx`、`internal/server/server.go`、`pkg/protocol/types.go`）的深度分析，以及对前后端 API 合约缺口的梳理而制定。

无论是人类开发者还是 AI Assistant，在进入下一步开发前，请务必参考此文档以确保架构的一致性。

---

## 阶段概述

整个落地过程分为 **8 个步骤**，按依赖顺序编排：

| Step | 名称 | 领域 | 前置条件 |
|:----:|------|------|:--------:|
| 0 | 开发体验基建 | 工程配置 | 无 |
| 1 | 基础设施（依赖与类型） | 工程配置 | Step 0 |
| 2 | 业务组件拆分 | 前端 UI | Step 1 |
| 3 | 路由骨架搭建 | 前端架构 | Step 2 |
| 4 | 客户端状态管理 | 前端架构 | Step 2 |
| 5 | 接入服务端数据 | 前端 + 后端 | Step 1, 4 |
| 6 | SSE 实时同步 | 前端 + 后端 | Step 5 |
| 7 | 暗黑模式与持久化 | 体验完善 | Step 4 |

> **注意**：Step 3 和 Step 4 可以并行。Step 7 的 CSS 变量部分已在 Step 0 中前置处理（防闪烁），Step 7 只处理交互切换和持久化。

---

## Step 0: 开发体验基建

### 0.1 为什么要这一步？
在写任何业务代码之前，需要先确保开发环境顺畅：能联调后端、有环境变量区分、暗黑模式不闪烁。这些基建看似微小，但如果推迟做，会在后续每一步都造成摩擦。

### 0.2 具体怎么做？

**1. Vite 开发代理**

修改 `web/vite.config.ts`，添加 `server.proxy` 配置，将 `/api` 和 `/ws` 请求代理到 Go 后端，前端开发时不再遇到跨域问题：
```typescript
server: {
  proxy: {
    '/api': 'http://localhost:7900',
    '/ws': {
      target: 'ws://localhost:7900',
      ws: true,
    },
  },
},
```

**2. 暗黑模式防闪烁 (FOUC Prevention)**

修改 `web/index.html`，在 `<head>` 中注入 blocking script，在 DOM 渲染前读取 localStorage 并设置 `dark` class：
```html
<script>
  (function() {
    const stored = localStorage.getItem('netsgo-theme');
    const isDark = stored === 'dark' ||
      (!stored && window.matchMedia('(prefers-color-scheme: dark)').matches);
    if (isDark) document.documentElement.classList.add('dark');
  })();
</script>
```

同时将 `<title>` 从 `web` 改为 `NetsGo Console`。

---

## Step 1: 基础设施（依赖与类型）

### 1.1 为什么要这一步？
引入 TanStack 生态和 Zustand 作为核心依赖。同时，将 Go 后端的数据结构翻译为 TypeScript 类型，确保前后端数据对齐。

### 1.2 具体怎么做？

**1. 安装依赖**
```bash
pnpm add @tanstack/react-router @tanstack/react-query zustand
```

**2. 定义 TypeScript 类型**

新建 `src/types/index.ts`，必须对齐 `pkg/protocol/types.go`：

```typescript
// 对齐 protocol.AgentInfo
export interface AgentInfo {
  hostname: string;
  os: string;        // "windows" | "linux" | "darwin"
  arch: string;      // "amd64" | "arm64"
  ip: string;
  version: string;
}

// 对齐 protocol.SystemStats
export interface SystemStats {
  cpu_usage: number;
  mem_total: number;   // bytes
  mem_used: number;    // bytes
  mem_usage: number;
  disk_total: number;  // bytes
  disk_used: number;   // bytes
  disk_usage: number;
  net_sent: number;    // bytes (cumulative)
  net_recv: number;    // bytes (cumulative)
  uptime: number;      // seconds
  num_cpu: number;
}

// 对齐 /api/agents 响应中的 agentView（server.go handleAPIAgents）
export interface Agent {
  id: string;
  info: AgentInfo;
  stats: SystemStats | null;  // ⚠️ 可能为 null（Agent 刚连接还没上报时）
}

// 对齐 protocol.ProxyConfig
export interface ProxyConfig {
  name: string;
  type: "tcp" | "udp" | "http";
  local_ip: string;
  local_port: number;
  remote_port: number;
  domain: string;
  agent_id: string;
  status: "active" | "stopped" | "error";
}
```

> ⚠️ 当前后端 `/api/agents` **不返回隧道列表 (proxies)**。在 Step 5 实施前，后端需要扩展 API（方案见 Step 5）。

**3. 数据格式化工具**

新建 `src/lib/format.ts`。后端返回的 `mem_total` / `mem_used` 是 `uint64` (bytes)，`uptime` 是秒数。前端需要转换为人类可读格式：

```typescript
export function formatBytes(bytes: number): string { /* 1073741824 → "1.0 GB" */ }
export function formatUptime(seconds: number): string { /* 86400 → "1 天 0 小时" */ }
export function formatPercent(value: number): string { /* 45.23 → "45.2%" */ }
```

**4. API 请求器与 QueryClient**

新建 `src/lib/api.ts`：封装 `fetch` 的请求器，统一错误处理。后续业务代码不直接写 `fetch`。

新建 `src/lib/query-client.ts`：导出 `QueryClient` 实例，配置默认策略：
```typescript
export const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      retry: 2,
      retryDelay: (attempt) => Math.min(1000 * 2 ** attempt, 10000),
      staleTime: 5000,
    },
  },
});
```

---

## Step 2: 业务组件拆分

### 2.1 为什么要这一步？
当前 `App.tsx` 长达 420+ 行，所有状态和 DOM 揉在一起。必须按业务域拆分，为后续路由和状态管理做准备。

> **重要**：这一步**先于路由**进行。先把组件拆干净，再把它们装进路由页面里，逻辑更自然。

### 2.2 具体怎么做？

使用 `components/custom/` 目录（与 shadcn `ui/` 不冲突）。

**1. 布局层** (`components/custom/layout/`)

| 文件 | 来源 | 说明 |
|------|------|------|
| `TopBar.tsx` | App.tsx L41-L66 | Logo、压测/停止按钮、设置、**连接状态灯** |
| `ErrorFallback.tsx` | 新增 | API 不可达时的全局错误回退 |

**2. Agent 管控域** (`components/custom/agent/`)

| 文件 | 来源 | 说明 |
|------|------|------|
| `AgentSidebar.tsx` | App.tsx L72-L171 | 左侧大纲视图：搜索框 + 分组折叠 + Agent 列表 |
| `AgentHeader.tsx` | App.tsx L183-L217 | 右侧顶部：Agent 名称、状态徽章、OS 信息、操作按钮 |
| `AgentStatsGrid.tsx` | App.tsx L220-L278 | 四宫格指标卡片：CPU、内存、磁盘、网络 I/O |
| `AgentEmptyState.tsx` | App.tsx L409-L413 | 未选中 Agent 时的占位引导 |

**3. 隧道管控域** (`components/custom/tunnel/`)

| 文件 | 来源 | 说明 |
|------|------|------|
| `TunnelTable.tsx` | App.tsx L280-L380 | 隧道列表表格（含搜索、状态、操作列） |
| `AddTunnelDialog.tsx` | 新增 | "添加隧道"的 Dialog 表单（名称、类型、端口） |

**4. 图表域** (`components/custom/chart/`)

| 文件 | 来源 | 说明 |
|------|------|------|
| `TrafficChart.tsx` | App.tsx L382-L405 | 流量趋势图（当前是静态 SVG，后续接真实数据） |

**5. 通用交互组件** (`components/custom/common/`)

| 文件 | 说明 |
|------|------|
| `ConfirmDialog.tsx` | 危险操作二次确认（停止全隧道、删除隧道等） |
| `ConnectionIndicator.tsx` | TopBar 中的连接状态灯（绿/黄/红），后续绑定 SSE 状态 |

**6. 页面组装**

将 `App.tsx` 从 420+ 行缩减为 ~30 行的组件拼装：
```tsx
export default function App() {
  return (
    <div className="flex flex-col h-screen ...">
      <TopBar />
      <div className="flex flex-1 overflow-hidden">
        <AgentSidebar />
        <DashboardContent />
      </div>
    </div>
  );
}
```

拆分完成后，视觉效果必须与拆分前**完全一致**（回归验证）。

---

## Step 3: 路由骨架搭建 (TanStack Router)

### 3.1 为什么要这一步？
最终应用要打包嵌进 Go 二进制（`go:embed`），没有服务端路由支持。采用 `createHashHistory` 的 Hash 模式（`#/dashboard`）。为未来的 Web Terminal、Settings 等页面预留路由结构。

### 3.2 具体怎么做？

**1. 路由定义**

| 文件 | 说明 |
|------|------|
| `src/routes/__root.tsx` | 根外壳：`<TopBar />` + `<Outlet />` + `<ScrollRestoration>` |
| `src/routes/dashboard.tsx` | Dashboard 页面：组装 Step 2 拆好的组件 |
| `src/routes/index.tsx` | `/` → redirect 到 `/dashboard` |

**2. Router 实例**

新建 `src/lib/router.ts`：
```typescript
import { createHashHistory, createRouter } from '@tanstack/react-router';
const hashHistory = createHashHistory();
export const router = createRouter({ routeTree, history: hashHistory });
```

**3. 改写入口**

将 `main.tsx` 变成 Provider 容器：
```tsx
<StrictMode>
  <QueryClientProvider client={queryClient}>
    <RouterProvider router={router} />
  </QueryClientProvider>
</StrictMode>
```

App.tsx 的职责被 `__root.tsx` 和 `dashboard.tsx` 接管，可以删除或转为纯 re-export。

---

## Step 4: 客户端纯 UI 状态管理 (Zustand)

### 4.1 为什么要这一步？
`selectedAgentId`、分组折叠状态等是纯客户端 UI 状态，使用 Zustand 避免 Props Drilling。

### 4.2 具体怎么做？

新建 `src/stores/ui-store.ts`：

```typescript
interface UIState {
  selectedAgentId: string | null;
  setSelectedAgentId: (id: string | null) => void;
  expandedGroups: Record<string, boolean>;
  toggleGroup: (group: string) => void;
}
```

删除原先 Dashboard 里的 `useState`，改为 Sidebar 内部触发 `setSelectedAgentId`，右侧内容区订阅这个 ID。

---

## Step 5: 接入服务端基础数据 (TanStack Query)

### 5.1 为什么要这一步？
去掉 Dummy Data，让前端真正连接后端 API。

### 5.2 前后端 API 合约

> ⚠️ **重要**：以下标注了后端需要新增/扩展的端点。前端实施 Step 5 之前，后端必须先完成这些改动。

| 端点 | 方法 | 状态 | 说明 |
|------|:----:|:----:|------|
| `/api/status` | GET | ✅ 已有 | 服务端状态（版本、Agent 数量） |
| `/api/agents` | GET | ⚠️ 需扩展 | 需在 `agentView` 中增加 `proxies []ProxyConfig` 字段 |
| `/api/agents/:id/tunnels` | POST | 🆕 需新增 | 为指定 Agent 创建隧道 |
| `/api/agents/:id/tunnels/:name` | DELETE | 🆕 需新增 | 删除指定隧道 |
| `/api/events` | GET (SSE) | 🆕 需新增 | Step 6 用，实时事件推送 |

### 5.3 具体怎么做？

新建 `src/hooks/use-agents.ts`：
```typescript
export function useAgents() {
  return useQuery({
    queryKey: ['agents'],
    queryFn: () => api.get<Agent[]>('/api/agents'),
  });
}
```

新建 `src/hooks/use-tunnel-mutations.ts`：
```typescript
export function useCreateTunnel() {
  return useMutation({
    mutationFn: (data: CreateTunnelInput) =>
      api.post(`/api/agents/${data.agentId}/tunnels`, data),
    onSuccess: () =>
      queryClient.invalidateQueries({ queryKey: ['agents'] }),
  });
}
```

**组件改造**：
- `AgentSidebar`、`AgentStatsGrid`、`TunnelTable`：删除硬编码数据，改用 `useAgents()` Hook
- 增加 `isLoading` → Skeleton UI
- 增加 `isError` → ErrorFallback
- 增加空数据态 → 引导用户安装 Agent 的空状态界面

---

## Step 6: SSE 赋能（实时更新）

### 6.1 为什么要这一步？
Agent 探针数据每秒变化（CPU、内存、网络 I/O），靠轮询开销太大。使用 **SSE (Server-Sent Events)** 实现服务端 → 前端的实时推送。

为什么选 SSE 而不是 WebSocket？
- 数据流是**单向**的（Server → 前端），不需要双向通信
- `EventSource` API **内置自动重连**，不需要手写断线重连逻辑
- SSE 基于标准 HTTP，同端口复用无冲突（走 PeekListener 的 HTTP 分支）
- 变更操作（创建/停止隧道等）是低频的，REST 即可

> 💡 未来需要 Web Terminal 时，为 Terminal 单独开一条 WebSocket `/ws/terminal/:agentId` 即可。

### 6.2 具体怎么做？

**1. 后端新增 SSE 端点**

后端需新增 `GET /api/events` 端点，当 Server 收到 Agent 的 `probe_report` 时广播给所有前端客户端。事件类型：

| 事件名 | 数据 | 触发时机 |
|--------|------|----------|
| `stats_update` | `{ agent_id, stats: SystemStats }` | Agent 上报探针数据时 |
| `agent_online` | `{ agent_id, info: AgentInfo }` | Agent 认证成功时 |
| `agent_offline` | `{ agent_id }` | Agent 断开连接时 |
| `tunnel_changed` | `{ agent_id, tunnel: ProxyConfig }` | 隧道创建/停止/异常时 |

**2. 连接状态管理**

新建 `src/stores/connection-store.ts`，管理 SSE 连接状态（connected / reconnecting / disconnected），驱动 TopBar 中的 `ConnectionIndicator`。

**3. 事件流 Hook**

新建 `src/hooks/use-event-stream.ts`：

```typescript
export function useEventStream() {
  const queryClient = useQueryClient();

  useEffect(() => {
    const es = new EventSource('/api/events');

    es.addEventListener('stats_update', (e) => {
      const { agent_id, stats } = JSON.parse(e.data);
      queryClient.setQueryData(['agents'], (old: Agent[] | undefined) =>
        old?.map(a => a.id === agent_id ? { ...a, stats } : a)
      );
    });

    es.addEventListener('agent_online', () => {
      queryClient.invalidateQueries({ queryKey: ['agents'] });
    });

    es.addEventListener('agent_offline', () => {
      queryClient.invalidateQueries({ queryKey: ['agents'] });
    });

    es.onerror = () => { /* 更新 connection-store → reconnecting */ };
    es.onopen  = () => { /* 更新 connection-store → connected */ };

    return () => es.close();
  }, [queryClient]);
}
```

核心策略：**SSE 推送写入 TanStack Query 缓存，自动触发组件重渲染**。不维护额外的 state，避免数据撕裂。

---

## Step 7: 暗黑模式与持久化

### 7.1 为什么要这一步？
Step 0 已处理了防闪烁的 CSS 基础，这一步完善交互层面的主题切换和 localStorage 持久化。

### 7.2 具体怎么做？

在 `ui-store.ts` 中扩展（或新建 `src/stores/theme-store.ts`）：

```typescript
type Theme = "dark" | "light" | "system";

interface ThemeState {
  theme: Theme;
  setTheme: (theme: Theme) => void;
}
```

使用 Zustand 的 `persist` 中间件将主题偏好存入 `localStorage`。

在 TopBar 的设置按钮中添加主题切换下拉菜单。

---

## 新增文件总览

```
src/
├── types/
│   └── index.ts                          [Step 1]
├── lib/
│   ├── api.ts                            [Step 1]
│   ├── format.ts                         [Step 1]
│   ├── query-client.ts                   [Step 1]
│   └── router.ts                         [Step 3]
├── stores/
│   ├── ui-store.ts                       [Step 4]
│   ├── connection-store.ts               [Step 6]
│   └── theme-store.ts                    [Step 7]
├── hooks/
│   ├── use-agents.ts                     [Step 5]
│   ├── use-tunnel-mutations.ts           [Step 5]
│   └── use-event-stream.ts              [Step 6]
├── routes/
│   ├── __root.tsx                        [Step 3]
│   ├── index.tsx                         [Step 3]
│   └── dashboard.tsx                     [Step 3]
├── components/custom/
│   ├── layout/
│   │   ├── TopBar.tsx                    [Step 2]
│   │   └── ErrorFallback.tsx             [Step 2]
│   ├── agent/
│   │   ├── AgentSidebar.tsx              [Step 2]
│   │   ├── AgentHeader.tsx               [Step 2]
│   │   ├── AgentStatsGrid.tsx            [Step 2]
│   │   └── AgentEmptyState.tsx           [Step 2]
│   ├── tunnel/
│   │   ├── TunnelTable.tsx               [Step 2]
│   │   └── AddTunnelDialog.tsx           [Step 2]
│   ├── chart/
│   │   └── TrafficChart.tsx              [Step 2]
│   └── common/
│       ├── ConfirmDialog.tsx             [Step 2]
│       └── ConnectionIndicator.tsx       [Step 2]
```

## 后端需配合的改动

| 改动 | 关联 Step | 优先级 |
|------|:---------:|:------:|
| `/api/agents` 响应增加 `proxies` 字段 | Step 5 | 🔴 高 |
| 新增 `POST /api/agents/:id/tunnels` | Step 5 | 🔴 高 |
| 新增 `DELETE /api/agents/:id/tunnels/:name` | Step 5 | 🔴 高 |
| 新增 `GET /api/events` SSE 端点 | Step 6 | 🔴 高 |

---

## Verification Plan

### 每个 Step 完成后的构建检查

在 `web/` 目录下执行，确保无编译错误：
```bash
cd web && npx tsc --noEmit && pnpm run build
```

### Step 2 视觉回归验证

组件拆分后，启动开发服务器，对比拆分前后页面是否视觉一致：
```bash
cd web && pnpm run dev
```
手动检查：
1. 打开 `http://localhost:5173`
2. 确认 TopBar、Sidebar、Stats 卡片、隧道表格、流量图的渲染与拆分前一致
3. 确认点击 Agent 切换、分组折叠/展开交互正常

### Step 5 API 联调验证

启动 Go 后端和前端 dev server，连接一个 Agent：
```bash
# 终端 1: 启动后端
go run ./cmd/server

# 终端 2: 启动前端
cd web && pnpm run dev

# 终端 3: 启动一个 Agent
go run ./cmd/client
```
手动检查：
1. 前端 Sidebar 显示已连接的 Agent（而非 Dummy Data）
2. Agent 的 CPU/内存指标显示真实数据（非 null 时）
3. 隧道列表正确展示

### Step 6 SSE 实时性验证

在 Step 5 的环境基础上：
1. 打开浏览器 DevTools → Network → EventStream，确认 `/api/events` 连接建立
2. 观察 Agent Stats 指标是否每隔几秒自动更新（无需手动刷新）
3. 断开 Agent，确认前端状态更新为离线
4. 断开后端，确认 TopBar 连接状态灯变红，后端恢复后自动重连
