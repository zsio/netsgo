import { useMemo, useState } from 'react';
import { ArrowRightLeft, GitBranchPlus } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { TunnelListTable, type TunnelEntry } from '@/components/custom/tunnel/TunnelListTable';
import { TunnelDialog } from '@/components/custom/tunnel/TunnelDialog';
import { useClientTraffic } from '@/hooks/use-client-traffic';
import { useClientTunnelsByRole } from '@/hooks/use-tunnel-mutations';
import type { Client } from '@/types';
import { getClientDisplayName } from '@/lib/client-utils';
import { getTrafficSeriesKey, getTunnelSeriesKey } from '@/lib/tunnel-traffic-keys';
import { useTranslation } from 'react-i18next';
import {
  CLIENT_DETAIL_TUNNEL_ROLE,
  getClientOwnedTunnelSource,
  resolveTunnelOwnerClientId,
} from '@/components/custom/tunnel/TunnelTable.helpers';

interface TunnelTableProps {
  client: Client;
  clients?: Client[];
}

export function TunnelTable({ client, clients = [client] }: TunnelTableProps) {
  const { t } = useTranslation();
  const [createOpen, setCreateOpen] = useState(false);
  const {
    data: trafficData,
    isLoading: isTraffic24hLoading,
    isError: isTraffic24hError,
  } = useClientTraffic(client.id, '24h');
  const { data: ownerTunnels } = useClientTunnelsByRole(client.id, CLIENT_DETAIL_TUNNEL_ROLE);

  const traffic24hByTunnel = useMemo(() => {
    const totals = new Map<string, number>();

    for (const item of trafficData?.items ?? []) {
      totals.set(
        getTrafficSeriesKey(item),
        item.points.reduce((sum, point) => sum + point.total_bytes, 0),
      );
    }

    return totals;
  }, [trafficData?.items]);

  const tunnelSource = getClientOwnedTunnelSource(ownerTunnels, client.proxies, client.id);
  const tunnels: TunnelEntry[] = tunnelSource.map((proxy) => ({
    ...proxy,
    clientId: resolveTunnelOwnerClientId(proxy, client.id),
    clientName: getClientDisplayName(client),
    clientOnline: client.online,
    traffic24hBytes: trafficData ? (traffic24hByTunnel.get(getTunnelSeriesKey(proxy)) ?? 0) : undefined,
  }));

  return (
    <>
      <TunnelListTable
        tunnels={tunnels}
        clients={clients}
        title={t('dashboard.childTunnels')}
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
            {t('tunnels.addTunnel')}
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
            {t('dashboard.createNow')}
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
