# NetsGo Agent Instructions

## 项目概述

NetsGo 是一个内网穿透与边缘管控平台，由 Go 后端 + React 前端组成。
前端最终通过 `go:embed` 嵌入 Go 二进制，单文件交付。

## 技术栈

- **构建工具**: Vite 8.x
- **UI 框架**: React 19 + TypeScript
- **样式**: Tailwind CSS v4 + shadcn/ui (radix-nova 风格)
- **路由**: TanStack Router (Hash 模式)
- **服务端状态**: TanStack Query v5
- **客户端状态**: Zustand v5
- **图标**: Lucide React
- **字体**: JetBrains Mono

## 目录结构

```
web/src/
├── routes/           # TanStack Router 路由定义
├── components/
│   ├── ui/           # shadcn 组件（由 shadcn CLI 管理，勿手动修改）
│   └── custom/       # 业务组件（按业务域分子目录：layout/agent/tunnel/chart）
├── stores/           # Zustand stores（纯客户端状态）
├── hooks/            # 自定义 React Hooks
├── lib/              # 工具函数、API 封装
└── types/            # TypeScript 类型定义
```

## 样式规范

**编写任何前端组件时，必须遵循样式规范指南：**

📄 [.agents/docs/style-guide.md](docs/style-guide.md)

核心要点：
1. **颜色只用语义变量**（`bg-card`, `text-foreground`），禁止硬编码（`bg-[#xxx]`, `bg-gray-800`）
2. **优先使用 shadcn 组件**，不重复造轮子
3. **自定义组件变体用 `cva()` 管理**，和 shadcn 保持同一模式
4. **间距用 Tailwind 标准 scale**（`p-4`, `gap-6`），禁止任意值（`p-[13px]`）
5. **不写 `dark:` 前缀**，颜色通过 CSS 变量自动切换
6. **长 className 用 `cn()` 分行书写**，按布局→尺寸→外观→排版→交互的顺序排列

## 数据流

- REST API (`/api/status`, `/api/agents`) 用于初始数据加载
- WebSocket (`/ws/control`) 用于实时推送（探针数据、隧道状态变更）
- TanStack Query 缓存作为 Single Source of Truth，WebSocket 通过 `queryClient.setQueryData()` 注入更新

## 后端 API 接口

| 端点 | 方法 | 说明 |
|------|------|------|
| `/api/status` | GET | 服务端状态（版本、Agent 数） |
| `/api/agents` | GET | 所有已连接 Agent 列表（含探针数据） |
| `/ws/control` | WebSocket | 控制通道（心跳、探针上报、隧道管理） |
