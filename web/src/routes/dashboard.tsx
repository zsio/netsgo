import { createRoute } from '@tanstack/react-router';
import { rootRoute } from './__root';
import { AgentSidebar } from '@/components/custom/agent/AgentSidebar';
import { AgentHeader } from '@/components/custom/agent/AgentHeader';
import { AgentStatsGrid } from '@/components/custom/agent/AgentStatsGrid';
import { AgentEmptyState } from '@/components/custom/agent/AgentEmptyState';
import { TunnelTable } from '@/components/custom/tunnel/TunnelTable';
import { TrafficChart } from '@/components/custom/chart/TrafficChart';
import { ErrorFallback } from '@/components/custom/layout/ErrorFallback';
import { useAgents } from '@/hooks/use-agents';
import { useUIStore } from '@/stores/ui-store';
import { Skeleton } from '@/components/ui/skeleton';

function DashboardPage() {
  const { data: agents, isLoading, isError, error, refetch } = useAgents();
  const selectedAgentId = useUIStore((s) => s.selectedAgentId);

  if (isError) {
    return (
      <div className="flex flex-1 overflow-hidden">
        <ErrorFallback error={error as Error} onRetry={() => refetch()} />
      </div>
    );
  }

  const selectedAgent = agents?.find((a) => a.id === selectedAgentId);

  return (
    <div className="flex flex-1 overflow-hidden">
      <AgentSidebar agents={agents ?? []} isLoading={isLoading} />

      <main className="flex-1 flex flex-col overflow-y-auto bg-background/50 relative">
        {/* Subtle background glow */}
        <div className="absolute top-0 left-1/4 w-[500px] h-[500px] bg-primary/10 rounded-full blur-3xl pointer-events-none" />

        {isLoading ? (
          <div className="p-8 max-w-6xl mx-auto w-full flex flex-col gap-8 z-10">
            <Skeleton className="h-20 w-full rounded-xl" />
            <div className="grid grid-cols-4 gap-4">
              {[1, 2, 3, 4].map((i) => (
                <Skeleton key={i} className="h-32 rounded-xl" />
              ))}
            </div>
            <Skeleton className="h-64 w-full rounded-xl" />
          </div>
        ) : selectedAgent ? (
          <div className="p-8 max-w-6xl mx-auto w-full flex flex-col gap-8 z-10">
            <AgentHeader agent={selectedAgent} />
            <AgentStatsGrid agent={selectedAgent} />
            <TunnelTable agent={selectedAgent} />
            <TrafficChart />
          </div>
        ) : (
          <AgentEmptyState />
        )}
      </main>
    </div>
  );
}

export const dashboardRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/dashboard',
  component: DashboardPage,
});
