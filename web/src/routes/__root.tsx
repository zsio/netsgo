import { createRootRoute, Outlet } from '@tanstack/react-router';
import { useEventStream } from '@/hooks/use-event-stream';
import { TooltipProvider } from '@/components/ui/tooltip';

function RootLayout() {
  // 在根布局启动 SSE 连接（全局唯一）
  useEventStream();

  return (
    <TooltipProvider>
      <div className="flex h-dvh min-h-dvh w-full min-w-0 flex-col overflow-hidden bg-background text-foreground font-sans selection:bg-primary/30">
        <Outlet />
      </div>
    </TooltipProvider>
  );
}

export const rootRoute = createRootRoute({
  component: RootLayout,
});
