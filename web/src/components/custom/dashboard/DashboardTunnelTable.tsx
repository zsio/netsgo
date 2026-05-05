import { useClients } from '@/hooks/use-clients';
import { useNavigate } from '@tanstack/react-router';
import { Skeleton } from '@/components/ui/skeleton';
import { ArrowRightLeft, Settings } from 'lucide-react';
import { TableActionIconButton } from '@/components/custom/common/TableActionIconButton';
import { TunnelListTable, type TunnelEntry } from '@/components/custom/tunnel/TunnelListTable';
import { getClientDisplayName } from '@/lib/client-utils';

export function DashboardTunnelTable() {
  const { data: clients, isLoading } = useClients();
  const navigate = useNavigate();

  if (isLoading) {
    return <Skeleton className="h-64 rounded-xl" />;
  }

  // 聚合所有隧道的列表
  const allTunnels: TunnelEntry[] = clients?.flatMap(client =>
    (client.proxies || []).map((proxy) => ({
      ...proxy,
      clientId: client.id,
      clientName: getClientDisplayName(client),
      clientOnline: client.online,
    }))
  ).sort((a, b) => {
    if (a.clientOnline !== b.clientOnline) {
      return a.clientOnline ? -1 : 1;
    }
    return (a.clientName ?? '').localeCompare(b.clientName ?? '') || a.name.localeCompare(b.name);
  }) || [];

  return (
    <TunnelListTable
      tunnels={allTunnels}
      title="全部隧道列表"
      icon={<ArrowRightLeft className="h-5 w-5 text-primary" />}
      showClient
      showActions={false}
      showSearch
      renderRowAction={(tunnel) => (
        <TableActionIconButton
          label="管理"
          tone="primary"
          onClick={() => navigate({ to: '/dashboard/clients/$clientId', params: { clientId: tunnel.clientId } })}
        >
          <Settings className="h-4 w-4" />
        </TableActionIconButton>
      )}
    />
  );
}
