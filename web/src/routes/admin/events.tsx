import { createRoute } from '@tanstack/react-router';
import { adminRoute } from '../admin';
import { useAdminEvents } from '@/hooks/use-admin-events';
import { Skeleton } from '@/components/ui/skeleton';

export const adminEventsRoute = createRoute({
  getParentRoute: () => adminRoute,
  path: '/events',
  component: AdminEventsPage,
});

function AdminEventsPage() {
  const { data: events = [], isLoading } = useAdminEvents();

  return (
    <div className="flex flex-col gap-6 max-w-4xl mx-auto h-[calc(100vh-140px)]">
      <div className="flex items-center justify-between shrink-0">
        <h2 className="text-2xl font-bold tracking-tight">审计与系统事件时间线</h2>
      </div>

      <div className="rounded-xl border border-border/40 bg-card/50 backdrop-blur-sm p-6 overflow-hidden flex-1 flex flex-col min-h-0 relative">
        <div className="flex-1 overflow-y-auto pr-4">
          {isLoading ? (
            <div className="space-y-4">
              <Skeleton className="h-16 w-full opacity-50" />
              <Skeleton className="h-16 w-full opacity-30" />
              <Skeleton className="h-16 w-full opacity-10" />
            </div>
          ) : events.length === 0 ? (
            <div className="h-full flex items-center justify-center text-muted-foreground">暂无事件记录</div>
          ) : (
            <div className="relative border-l border-border/50 ml-3 space-y-6 pb-4">
              {events.map((evt) => (
                <div key={evt.id} className="relative pl-6">
                  {/* Timeline dot */}
                  <div className="absolute top-1.5 -left-1.5 w-3 h-3 bg-card border-2 border-primary rounded-full z-10" />
                  
                  <div className="flex flex-col gap-1">
                    <div className="flex items-center gap-2">
                      <span className="text-sm font-medium text-foreground">{evt.type}</span>
                      <span className="text-xs text-muted-foreground font-mono bg-muted/50 px-1.5 py-0.5 rounded">
                        {new Date(evt.timestamp).toLocaleString()}
                      </span>
                    </div>
                    <div className="text-sm text-foreground bg-muted/20 p-3 rounded-md border border-border/30 break-words font-mono">
                      {evt.data}
                    </div>
                  </div>
                </div>
              ))}
            </div>
          )}
        </div>
      </div>
    </div>
  );
}
