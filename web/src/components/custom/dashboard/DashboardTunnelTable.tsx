import { useAgents } from '@/hooks/use-agents';
import { useNavigate } from '@tanstack/react-router';
import { Skeleton } from '@/components/ui/skeleton';
import { ArrowRightLeft } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { TunnelListTable, type TunnelEntry } from '@/components/custom/tunnel/TunnelListTable';

export function DashboardTunnelTable() {
  const { data: agents, isLoading } = useAgents();
  const navigate = useNavigate();

  if (isLoading) {
    return <Skeleton className="h-64 rounded-xl" />;
  }

  // 聚合所有隧道的列表
  const allTunnels: TunnelEntry[] = agents?.flatMap(agent => 
    (agent.proxies || []).map(proxy => ({
      ...proxy,
      agentId: agent.id,
      agentName: agent.info.hostname,
    }))
  ) || [];

  return (
    <TunnelListTable
      tunnels={allTunnels}
      title="全部隧道列表"
      icon={<ArrowRightLeft className="h-5 w-5 text-primary" />}
      showAgent
      showActions={false}
      showSearch
      renderRowAction={(tunnel) => (
        <Button
          variant="ghost"
          size="sm"
          onClick={() => navigate({ to: '/dashboard/agents/$agentId', params: { agentId: tunnel.agentId } })}
        >
          管理
        </Button>
      )}
    />
  );
}
