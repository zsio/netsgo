import { useClients } from '@/hooks/use-clients';
import { useNavigate } from '@tanstack/react-router';
import { Skeleton } from '@/components/ui/skeleton';
import { ArrowRightLeft } from 'lucide-react';
import { TunnelListTable, type TunnelEntry } from '@/components/custom/tunnel/TunnelListTable';
import { getClientDisplayName } from '@/lib/client-utils';
import { useTranslation } from 'react-i18next';

export function DashboardTunnelTable() {
  const { t } = useTranslation();
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
      title={t('dashboard.allTunnels')}
      icon={<ArrowRightLeft className="h-5 w-5 text-primary" />}
      clients={clients}
      showClient
      showActions={false}
      showSearch
      onClientClick={(tunnel) => navigate({
        to: '/dashboard/clients/$clientId',
        params: { clientId: tunnel.clientId },
      })}
    />
  );
}
