import { ArrowRightLeft } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { TunnelListTable, type TunnelEntry } from '@/components/custom/tunnel/TunnelListTable';
import type { Agent } from '@/types';

interface TunnelTableProps {
  agent: Agent;
}

export function TunnelTable({ agent }: TunnelTableProps) {
  const tunnels: TunnelEntry[] = (agent.proxies ?? []).map(proxy => ({
    ...proxy,
    agentId: agent.id,
    agentName: agent.info.hostname,
  }));

  return (
    <TunnelListTable
      tunnels={tunnels}
      title="下属隧道"
      icon={<ArrowRightLeft className="h-5 w-5 text-primary" />}
      showAgent={false}
      showActions
      showSearch
      emptyAction={
        <Button variant="outline" className="mt-4">
          + 立即创建
        </Button>
      }
    />
  );
}
