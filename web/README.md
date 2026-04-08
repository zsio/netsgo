# NetsGo Web Console

前端基于 Vite + React + TanStack Router + shadcn/ui。

## 开发

```bash
bun install
bun run dev
```

默认开发地址为 `http://localhost:5173`。

如果后端不跑在 `http://127.0.0.1:9527`，启动前端前先指定代理目标：

```bash
VITE_DEV_PROXY_TARGET=http://127.0.0.1:9090 bun run dev
```

## 检查与构建

```bash
bun run lint
bun run build
```

## 目录约定

- `src/routes/`: 页面路由
- `src/components/custom/`: 业务组件
- `src/components/ui/`: shadcn/ui 组件源码
- `src/hooks/`: 查询、事件流、状态相关 hooks
- `src/lib/`: API、路由、工具函数

前端构建产物会被 Go 服务端通过 `go:embed` 嵌入到单文件二进制。
