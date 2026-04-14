import { useMemo } from 'react';
import { ArrowRightLeft, Plus } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { TunnelListTable, type TunnelEntry } from '@/components/custom/tunnel/TunnelListTable';
import { TunnelDialog } from '@/components/custom/tunnel/TunnelDialog';
import { useClientTraffic } from '@/hooks/use-client-traffic';
import type { Client } from '@/types';
import { getClientDisplayName } from '@/lib/client-utils';

interface TunnelTableProps {
  client: Client;
}

export function TunnelTable({ client }: TunnelTableProps) {
  const {
    data: trafficData,
    isLoading: isTraffic24hLoading,
    isError: isTraffic24hError,
  } = useClientTraffic(client.id, '24h');

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

  const tunnels: TunnelEntry[] = (client.proxies ?? []).map((proxy) => ({
    ...proxy,
    clientId: client.id,
    clientName: getClientDisplayName(client),
    clientOnline: client.online,
    traffic24hBytes: trafficData ? (traffic24hByTunnel.get(`${proxy.type}:${proxy.name}`) ?? 0) : undefined,
  }));

  return (
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
      showSearch
      emptyAction={
        <TunnelDialog
          mode="create"
          clientId={client.id}
          trigger={
            <Button variant="outline" className="mt-4">
              <Plus className="h-4 w-4 mr-1" />
              立即创建
            </Button>
          }
        />
      }
    />
  );
}
