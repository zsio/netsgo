import { createRootRoute, Outlet } from '@tanstack/react-router';
import { TopBar } from '@/components/custom/layout/TopBar';
import { useEventStream } from '@/hooks/use-event-stream';

function RootLayout() {
  // 在根布局启动 SSE 连接（全局唯一）
  useEventStream();

  return (
    <div className="flex flex-col h-screen w-full bg-background text-foreground font-sans selection:bg-primary/30 overflow-hidden">
      <TopBar />
      <Outlet />
    </div>
  );
}

export const rootRoute = createRootRoute({
  component: RootLayout,
});
