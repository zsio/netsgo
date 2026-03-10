# NetsGo Web 前端实施计划指南

本文档是 NetsGo 前端架构落地的详细指南。它不仅列出了需要修改的路径，还详细说明了**为什么要这样做**以及**具体应该怎么做**。

无论是人类开发者还是 AI Assistant，在进入下一步开发前，请务必参考此文档以确保架构的一致性。

---

## 阶段概述

整个落地过程分为以下 7 个步骤：

1. **Step 1: 基础设施** - 安装核心依赖，建立与后端同步的 TypeScript 类型。
2. **Step 2: 路由骨架** - 使用 TanStack Router 搭建支持 Hash 模式的页面框架。
3. **Step 3: 组件库拆分** - 拆解 `App.tsx` 原型为符合 `feature` 的业务组件。
4. **Step 4: 客户端状态 (Zustand)** - 管理 UI 折叠、主题、选中项等。
5. **Step 5: 服务端状态 (TanStack Query)** - 接入 REST API。
6. **Step 6: WebSocket 实时同步** - WebSocket 与 Query 缓存结合。
7. **Step 7: 暗黑模式与持久化** - 完善系统体验。

---

## Step 1: 基础设施 (依赖与类型)

### 1.1 为什么要这一步？
巧妇难为无米之炊。由于我们要引入谭叔生态 (TanStack) 和 Zustand，我们必须先将其安装好。此外，NetsGo 采用 Go 后端，为了维持数据的一致性，我们需要将 `protocol/types.go` 翻译成 TS 类型，并在全局共用。

### 1.2 具体怎么做？

**1. 安装依赖**
在 `web` 目录下执行：
```bash
pnpm add @tanstack/react-router @tanstack/react-query zustand
```

**2. 定义类型**
新建文件 `src/types/index.ts`。
你需要参考 Go 层面的数据结构，导出对应的 interface，例如：
- `AgentInfo` (OS, Arch, IP)
- `SystemStats` (CPU, Mem, Disk, Net I/O)
- `ProxyConfig` (隧道配置)
- `Agent` (包含 info, stats, tunnels 的组合结构)

**3. API 与 QueryClient 底座**
- 新建 `src/lib/query-client.ts`: 导出基础的 `QueryClient` 实例，提供默认重试策略。
- 新建 `src/lib/api.ts`: 封装原生的 `fetch` 为统一的 API 请求器，后续业务代码不直接写 `fetch`。

---

## Step 2: 路由骨架搭建 (TanStack Router)

### 2.1 为什么要这一步？
因为最终应用要被打包塞进 Go 二进制，没有服务端的路由支持。我们采用 `createHashHistory` 的 Hash 模式 (#/dashboard)。`App.tsx` 中的代码将被迁移到一个具体的页面组件中，`main.tsx` 则纯粹负责提供 Provider。

### 2.2 具体怎么做？

**1. 建立路由结构定义**
新建 `src/routes/__root.tsx`:
在这里搭建应用的**根外壳**。它将包含 `<TopBar />`（全局的顶部导航）和一个 `<Outlet />`。同时别忘了引入 `<ScrollRestoration>`。

新建 `src/routes/dashboard.tsx`:
这里将是目前原型代码的落脚点。

新建 `src/routes/index.tsx`:
默认根路径`/`，直接用 redirect 重定向到 `/dashboard`。

**2. 组装 Router 实例**
新建 `src/lib/router.ts`:
使用 Code-based routing 组装 `routeTree`，并将其注入 `createRouter`。需要开启 hash history：
```typescript
import { createHashHistory, createRouter } from '@tanstack/react-router';
const hashHistory = createHashHistory();
export const router = createRouter({ routeTree, history: hashHistory });
```

**3. 改写入口点**
将原先的 `App.tsx` 转变成纯粹的 Provider 容器，仅仅用来抛出 `<QueryClientProvider>` 和 `<RouterProvider>`。

---

## Step 3: 业务组件拆分与复用

### 3.1 为什么要这一步？
目前的 `App.tsx` 长达 400 多行，所有状态和 DOM 揉捏在一起的。为了未来的可维护性，我们必须按照**业务域 (domain)** 进行拆分。

### 3.2 具体怎么做？

使用 `components/custom/` 目录组织文件（避开与 shadcn 的 `ui` 目录冲突）。

**1. 布局层提取**
- `src/components/custom/layout/TopBar.tsx`: 抽离出顶部的 Logo + 压测/停止全隧道按钮。

**2. Agent 管控域提取 (`components/custom/agent/`)**
- `AgentSidebar.tsx`: 左侧的大纲视图。包含对 "活跃"、"离线" 分组的管理和搜索框。
- `AgentHeader.tsx`: 右侧详情顶部的服务器标题与基础信息标签。
- `AgentStatsGrid.tsx`: 右侧的四个指标方块 (CPU、内存、磁盘、网络)。
- `AgentEmptyState.tsx`: 没有选中机器时的占位图。

**3. 隧道管控域提取 (`components/custom/tunnel/`)**
- `TunnelTable.tsx`: 右侧的下属隧道表格。提取出来单独接受 `tunnelsData`。

**4. 图表域 (`components/custom/chart/`)**
- `TrafficChart.tsx`: 预留的流量波浪图。

**5. 页面组装**
最后，在 `src/routes/dashboard.tsx` 中将这些拆分后的组件以极简的代码重新拼装，并使得视觉效果与拆分前一模一样。

---

## Step 4: 客户端纯 UI 状态管理 (Zustand)

### 4.1 为什么要这一步？
像 "用户正在查看哪个 Agent (selectedAgentId)"，或者 "左侧的状态树哪些展开了/折叠了" 完全是**不涉及服务端的、只存在于客户端的临时状态**。
如果不使用 Zustand，我们将面临层层钻取 (Props Drilling)，Sidebar 必须把 selectedId 抛回给 Dashboard，再传给右侧内容区。

### 4.2 具体怎么做？

新建 `src/stores/ui-store.ts`。

定义一个极简的 Store：
```typescript
interface UIState {
  selectedAgentId: string | null;
  setSelectedAgentId: (id: string | null) => void;
  // 还可以包含分组折叠状态...
}
```
把原先位于 Dashboard 中的 `useState` 扔掉，Sidebar 组件内部触发 `setSelectedAgentId`，右侧的各类 Content 订阅这个 ID 来决定展示哪个 Agent 的详情。

---

## Step 5: 接入服务端基础数据 (TanStack Query)

### 5.1 为什么要这一步？
现在数据是 Dummy Data，我们要让前端真正运转起来。使用 TanStack Query 作为核心事实来源 (Single Source of Truth)，处理 Loading 态、Error 重试逻辑。

### 5.2 具体怎么做？

新建 `src/hooks/use-agents.ts`。

**1. 封装 Hook**
基于 `api.ts` 中的获取远端列表逻辑，使用 `useQuery` 进行包裹。
```typescript
export function useAgents() {
  return useQuery({
    queryKey: ['agents'],
    queryFn: fetchAgentsFromAPI,
  })
}
```

**2. 组件改造**
打开 `AgentSidebar` 和 Dashboard 主体，删除开头的 `agentsData` 写死的内容，改为使用 `const { data: agents, isLoading } = useAgents()`。增加针对 `isLoading` 状态的 Skeleton UI！

---

## Step 6: WebSocket 赋能 (实时更新)

### 6.1 为什么要这一步？
NetsGo 是一个探针与管理系统。当我们收到探针心跳时，前端数据得每秒跳动（CPU、网络等），靠传统轮询会让系统承受巨大压力。我们需要让 TanStack Query 配合 WebSocket 一起工作。

### 6.2 具体怎么做？

**1. WebSocket 状态控制**
新建 `src/stores/ws-store.ts`。由它管理当前的断连状态、重连次数。（这可以用来驱动 TopBar 旁的绿灯/黄灯/红灯）。

**2. 核心：基于缓存的黑科技**
新建 `src/hooks/use-websocket.ts`。

- 在组件挂载时建立 WebSocket 连接。
- 当 `ws.onmessage` 收到 `probe_report` 时：
  - **不要去更新额外的 state**，这会带来两份数据撕裂。
  - 直接调用 `queryClient.setQueryData(['agents'], updaterFn)` 覆写刚刚在 Step 5 里拿到的基础数据缓存。
  - 只要缓存被写入，TanStack Query 就会自动通知所有的 React 组件去重新 Render 指标！！

这就完美兼得了 "初始可靠性"（GET 请求）与 "增量高性能"（WS 推送）的能力。

---

## Step 7: 体验拔高 (暗黑模式/设置项保存)

### 7.1 为什么要这一步？
我们之前在 `index.html` 强制拿掉了 `class="dark"`，是为了让用户能够自主控制。一个称职的管理工具需要能顺应用户的阅读习惯，并在重新打开浏览器时记住。

### 7.2 具体怎么做？

将暗黑模式的偏好存入 `Zustand` 的持久化中间件（或纯净的 Local Storage）。
```typescript
type Theme = "dark" | "light" | "system";
```
在应用初始化（甚至在 `index.html` 的 head 里的一个即时执行 JS 中）读取偏好，根据它是 `dark` 还是 `system && (prefers-color-scheme: dark)` 给 HTML tag 的 classList 增加或剥离 "dark"。保证这套逻辑生效的同时，避免页面闪烁 (FOUC)。
