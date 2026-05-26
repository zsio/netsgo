import { useMemo, useState } from 'react';
import { ArrowRightLeft, GitBranchPlus } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { TunnelListTable, type TunnelEntry } from '@/components/custom/tunnel/TunnelListTable';
import { TunnelDialog } from '@/components/custom/tunnel/TunnelDialog';
import { useClientTraffic } from '@/hooks/use-client-traffic';
import { useClientTunnelsByRole } from '@/hooks/use-tunnel-mutations';
import type { Client } from '@/types';
import { getClientDisplayName } from '@/lib/client-utils';

interface TunnelTableProps {
  client: Client;
  clients?: Client[];
}

export function TunnelTable({ client, clients = [client] }: TunnelTableProps) {
  const [createOpen, setCreateOpen] = useState(false);
  const {
    data: trafficData,
    isLoading: isTraffic24hLoading,
    isError: isTraffic24hError,
  } = useClientTraffic(client.id, '24h');
  const { data: relatedTunnels } = useClientTunnelsByRole(client.id, 'related');

  const traffic24hByTunnel = useMemo(() => {
    const totals = new Map<string, number>();

    for (const item of trafficData?.items ?? []) {
      totals.set(
        `${item.tunnel_type}:${item.tunnel_name}`,
        item.points.reduce((sum, point) => sum + point.total_bytes, 0),
      );
    }

    return totals;
  }, [trafficData?.items]);

  const tunnelSource = relatedTunnels ?? client.proxies ?? [];
  const tunnels: TunnelEntry[] = tunnelSource.map((proxy) => ({
    ...proxy,
    clientId: client.id,
    clientName: getClientDisplayName(client),
    clientOnline: client.online,
    traffic24hBytes: trafficData ? (traffic24hByTunnel.get(`${proxy.type}:${proxy.name}`) ?? 0) : undefined,
  }));

  return (
    <>
      <TunnelListTable
        tunnels={tunnels}
        title="下属隧道"
        icon={<ArrowRightLeft className="h-5 w-5 text-primary" />}
        showClient={false}
        showTraffic24h
        traffic24hState={
          isTraffic24hError
            ? 'error'
            : isTraffic24hLoading
              ? 'loading'
              : 'ready'
        }
        showActions
        showSearch={false}
        headerAction={
          <Button
            type="button"
            variant="outline"
            onClick={() => setCreateOpen(true)}
          >
            <GitBranchPlus className="h-4 w-4 mr-1" />
            添加隧道
          </Button>
        }
        emptyAction={
          <Button
            type="button"
            variant="outline"
            className="mt-4"
            onClick={() => setCreateOpen(true)}
          >
            <GitBranchPlus className="h-4 w-4 mr-1" />
            立即创建
          </Button>
        }
      />
      <TunnelDialog
        mode="create"
        clientId={client.id}
        clients={clients}
        open={createOpen}
        onOpenChange={setCreateOpen}
        hideTrigger
      />
    </>
  );
}
