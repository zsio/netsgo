import { createRoute, useParams, useNavigate } from '@tanstack/react-router';
import { useEffect } from 'react';
import { dashboardRoute } from '@/routes/dashboard';
import { AgentHeader } from '@/components/custom/agent/AgentHeader';
import { AgentStatsGrid } from '@/components/custom/agent/AgentStatsGrid';
import { TunnelTable } from '@/components/custom/tunnel/TunnelTable';
import { TrafficChart } from '@/components/custom/chart/TrafficChart';
import { useAgents } from '@/hooks/use-agents';
import { Skeleton } from '@/components/ui/skeleton';

function AgentDetailPage() {
  const { agentId } = useParams({ from: '/dashboard/agents/$agentId' });
  const navigate = useNavigate();
  const { data: agents, isLoading } = useAgents();

  const agent = agents?.find((a) => a.id === agentId);

  // 如果加载完成但 agent 不存在，回到 dashboard 概览
  useEffect(() => {
    if (!isLoading && agents && !agent) {
      navigate({ to: '/dashboard' });
    }
  }, [isLoading, agents, agent, navigate]);

  if (isLoading) {
    return (
      <div className="p-8 max-w-6xl mx-auto w-full flex flex-col gap-8 z-10">
        <Skeleton className="h-20 w-full rounded-xl" />
        <div className="grid grid-cols-4 gap-4">
          {[1, 2, 3, 4].map((i) => (
            <Skeleton key={i} className="h-32 rounded-xl" />
          ))}
        </div>
        <Skeleton className="h-64 w-full rounded-xl" />
      </div>
    );
  }

  if (!agent) {
    return null; // will redirect via useEffect
  }

  return (
    <div className="p-8 max-w-6xl mx-auto w-full flex flex-col gap-8 z-10">
      <AgentHeader agent={agent} />
      <AgentStatsGrid agent={agent} />
      <TunnelTable agent={agent} />
      <TrafficChart />
    </div>
  );
}

export const dashboardAgentRoute = createRoute({
  getParentRoute: () => dashboardRoute,
  path: '/agents/$agentId',
  component: AgentDetailPage,
});
