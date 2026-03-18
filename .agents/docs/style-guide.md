# 🎨 NetsGo Web 样式规范指南

> 适用于 `web/src/` 下所有 React 组件的样式编写。
> 技术栈：Tailwind CSS v4 + shadcn/ui + cva + cn()

---

## 核心原则

**只用语义化的东西，不用具体的东西。**

- 颜色 → 用 CSS 变量（`bg-card`），不写死（`bg-[#1a1a2e]`）
- 组件 → 用 shadcn（`<Button>`），不造轮子（`<button className="...">`）
- 变体 → 用 cva()，不写一堆三元表达式
- 间距 → 用 Tailwind scale（`p-4`），不写任意值（`p-[13px]`）

**状态转换可以使用motion提高流畅度**

- 使用小的动画效果,可以显著提升用户体验,让界面感觉更生动和响应式。

---

## 规则 1：颜色 — 只用语义变量，禁止硬编码

```tsx
// ✅ 正确 — 使用 shadcn 语义色
<div className="bg-background text-foreground" />
<div className="bg-card border-border" />
<div className="text-muted-foreground" />
<div className="bg-primary text-primary-foreground" />
<div className="bg-destructive/10 text-destructive" />

// ❌ 错误 — 硬编码颜色
<div className="bg-[#1a1a2e] text-[#e0e0e0]" />
<div className="bg-gray-800 text-gray-200" />
<div style={{ color: '#ff5555' }} />
```

### 例外：状态指示色

只有状态指示可以使用固定颜色（不随主题变化）：

```tsx
// ✅ 允许 — 固定语义的状态色
<div className="bg-emerald-500" />   // 在线 / 健康
<div className="text-amber-500" />    // 警告
<div className="text-blue-500" />     // 信息
<div className="text-destructive" />  // 错误（优先用语义变量）
```

---

## 规则 2：组件 — 优先用 shadcn，不重复造轮子

```tsx
// ✅ 正确
import { Button } from '@/components/ui/button'
<Button variant="secondary" size="sm">启动</Button>

// ❌ 错误 — 和 shadcn Button 功能重复
<button className="px-4 py-2 bg-primary rounded-md text-sm">启动</button>
```

### 判断标准

| 情况 | 做法 |
|------|------|
| shadcn 有现成组件 | 直接用 |
| shadcn 没有，但会复用 | 写到 `components/custom/` |
| 只在一个地方用 | 在当前组件内用 `div + Tailwind` 即可 |

---

## 规则 3：自定义组件 — 用 cva() 管理变体

当自定义组件有多种状态/外观时，必须使用 `cva()`，和 shadcn 保持同一套模式：

```tsx
import { cva, type VariantProps } from 'class-variance-authority'
import { cn } from '@/lib/utils'

const statusBadgeVariants = cva(
  'inline-flex items-center gap-1.5 rounded-full px-2.5 py-0.5 text-xs font-medium',
  {
    variants: {
      status: {
        online:  'bg-emerald-500/10 text-emerald-500 border border-emerald-500/20',
        offline: 'bg-muted text-muted-foreground border border-border',
        error:   'bg-destructive/10 text-destructive border border-destructive/20',
      },
    },
    defaultVariants: { status: 'offline' },
  }
)

export function StatusBadge({ status, className, children }: Props) {
  return (
    <span className={cn(statusBadgeVariants({ status }), className)}>
      {children}
    </span>
  )
}
```

---

## 规则 4：间距 — 用 Tailwind scale，禁用任意值

```tsx
// ✅ 正确
<div className="p-4 mb-6 gap-3" />

// ❌ 错误
<div className="p-[13px] mb-[22px]" />
```

### 间距约定表

| 场景 | 推荐值 |
|------|--------|
| 页面内边距 | `p-6` 或 `p-8` |
| 卡片内边距 | `p-4` 或 `p-6` |
| 卡片间距 | `gap-4` 或 `gap-6` |
| 表格单元格 | `px-6 py-4` |
| 紧凑元素间距 | `gap-2` |
| 大区块间距 | `gap-8` |

---

## 规则 5：排版约定

### 字号层级

| 层级 | Class | 使用场景 |
|------|-------|---------|
| 页面标题 | `text-2xl font-bold tracking-tight` | 节点名称等主标题 |
| 区域标题 | `text-lg font-semibold` | "下属隧道"、"流量趋势" |
| 正文 | `text-sm` | 表格内容、一般文字 |
| 辅助文字 | `text-xs text-muted-foreground` | ID、协议标签、时间 |
| 等宽数据 | `text-sm font-mono` | IP 地址、端口号、ID |

### 圆角

| 元素 | Class |
|------|-------|
| 大容器 / 卡片 | `rounded-xl` |
| 小标签 / Badge | `rounded-full` 或 `rounded` |
| 按钮 / 输入框 | 由 shadcn 组件自动管理 |

### 阴影

暗色主题下阴影效果不明显，仅对悬浮元素使用：

| 元素 | Class |
|------|-------|
| 卡片 hover | `shadow-sm` |
| Dialog / Popover | `shadow-lg` |

---

## 规则 6：暗黑模式 — 不写 dark: 前缀

颜色通过 CSS 变量自动切换，不需要手动写 `dark:` 前缀。

```tsx
// ✅ 正确 — 变量自动适配
<div className="bg-card text-card-foreground border-border" />

// ❌ 错误 — 说明用了非语义色
<div className="bg-white dark:bg-gray-900 text-black dark:text-white" />
```

> **判断标准：如果你在写 `dark:` 前缀，说明你用了非语义色。**
> 应该退回来思考：这个颜色应该映射到哪个语义变量？

---

## 规则 7：className 书写顺序

长 className 使用 `cn()` 分行，按以下顺序排列：

```tsx
<div className={cn(
  // 1. 布局 (display, position, flex/grid)
  'flex items-center justify-between',
  // 2. 尺寸 (width, height, padding, margin)
  'h-14 px-4',
  // 3. 外观 (background, border, shadow, rounded)
  'bg-background border-b border-border/40 rounded-xl',
  // 4. 排版 (font, text, color)
  'text-sm font-medium text-foreground',
  // 5. 交互 (hover, focus, cursor, transition)
  'hover:bg-muted cursor-pointer transition-colors',
  // 6. 条件样式
  isActive && 'bg-primary/10 text-primary',
)} />
```

---

## 组件目录结构

```
components/
├── ui/                    # shadcn 管理（勿手动修改）
│   ├── button.tsx
│   ├── card.tsx
│   └── ...
└── custom/                # 业务组件（我们自己写的）
    ├── layout/            #   布局组件
    ├── agent/             #   Agent 相关组件
    ├── tunnel/            #   隧道相关组件
    └── chart/             #   图表组件
```

---

## 速查清单

编写组件时逐项自查：

- [ ] 颜色是否全部来自语义变量？
- [ ] 是否检查过 shadcn 有没有现成组件？
- [ ] 有变体的组件是否用了 `cva()`？
- [ ] 间距是否使用 Tailwind 标准值？
- [ ] 是否避免了 `dark:` 前缀？
- [ ] className 过长时是否用 `cn()` 分行？
