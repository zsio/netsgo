import { createRootRoute, Outlet } from '@tanstack/react-router';
import { useEventStream } from '@/hooks/use-event-stream';
import { TooltipProvider } from '@/components/ui/tooltip';
import { useDocumentLanguage } from '@/i18n/use-document-language';

function RootLayout() {
  // 在根布局启动 SSE 连接（全局唯一）
  useEventStream();
  useDocumentLanguage();

  return (
    <TooltipProvider>
      <div className="relative flex h-dvh min-h-dvh w-full min-w-0 flex-col overflow-hidden bg-background text-foreground font-sans selection:bg-primary/30 dark:bg-black">
        <div
          className="pointer-events-none absolute inset-0 z-0 opacity-55 dark:hidden"
          style={{
            backgroundImage: `
              linear-gradient(to right, #e7e5e4 1px, transparent 1px),
              linear-gradient(to bottom, #e7e5e4 1px, transparent 1px)
            `,
            backgroundSize: '20px 20px',
            backgroundPosition: '0 0, 0 0',
            maskImage: `
              repeating-linear-gradient(
                to right,
                black 0px,
                black 3px,
                transparent 3px,
                transparent 8px
              ),
              repeating-linear-gradient(
                to bottom,
                black 0px,
                black 3px,
                transparent 3px,
                transparent 8px
              )
            `,
            WebkitMaskImage: `
              repeating-linear-gradient(
                to right,
                black 0px,
                black 3px,
                transparent 3px,
                transparent 8px
              ),
              repeating-linear-gradient(
                to bottom,
                black 0px,
                black 3px,
                transparent 3px,
                transparent 8px
              )
            `,
            maskComposite: 'intersect',
            WebkitMaskComposite: 'source-in',
          }}
        />
        <div
          className="pointer-events-none absolute inset-0 z-0 hidden opacity-55 dark:block"
          style={{
            background: '#000000',
            backgroundImage: `
              linear-gradient(to right, rgba(75, 85, 99, 0.4) 1px, transparent 1px),
              linear-gradient(to bottom, rgba(75, 85, 99, 0.4) 1px, transparent 1px)
            `,
            backgroundSize: '40px 40px',
          }}
        />
        <div className="relative z-10 flex min-h-0 flex-1 flex-col">
          <Outlet />
        </div>
      </div>
    </TooltipProvider>
  );
}

export const rootRoute = createRootRoute({
  component: RootLayout,
});
