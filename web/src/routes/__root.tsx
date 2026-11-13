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
      <div className="relative flex h-dvh min-h-dvh w-full min-w-0 flex-col overflow-hidden bg-background text-foreground font-sans selection:bg-primary/20 selection:text-foreground">
        <div
          className="pointer-events-none absolute inset-0 z-0 opacity-45 dark:hidden"
          style={{
            backgroundImage: `
              radial-gradient(ellipse 80% 42% at 50% -12%, rgba(244, 129, 32, 0.055), transparent 70%),
              linear-gradient(to right, #e6e1d8 1px, transparent 1px),
              linear-gradient(to bottom, #e6e1d8 1px, transparent 1px)
            `,
            backgroundSize: '100% 100%, 24px 24px, 24px 24px',
            backgroundPosition: '0 0, 0 0, 0 0',
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
          className="pointer-events-none absolute inset-0 z-0 hidden dark:block"
          style={{
            backgroundImage: `
              radial-gradient(ellipse 75% 38% at 50% -10%, rgba(244, 129, 32, 0.065), transparent 72%),
              linear-gradient(to right, rgba(245, 238, 228, 0.038) 1px, transparent 1px),
              linear-gradient(to bottom, rgba(245, 238, 228, 0.038) 1px, transparent 1px)
            `,
            backgroundSize: '100% 100%, 32px 32px, 32px 32px',
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
