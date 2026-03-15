import { createRoute, useParams, useNavigate } from '@tanstack/react-router';
import { useEffect } from 'react';
import { dashboardRoute } from '@/routes/dashboard';
import { ClientHeader } from '@/components/custom/client/ClientHeader';
import { ClientStatsGrid } from '@/components/custom/client/ClientStatsGrid';
import { TunnelTable } from '@/components/custom/tunnel/TunnelTable';
import { TrafficChart } from '@/components/custom/chart/TrafficChart';
import { useClients } from '@/hooks/use-clients';
import { Skeleton } from '@/components/ui/skeleton';

function ClientDetailPage() {
  const { clientId } = useParams({ from: '/dashboard/clients/$clientId' });
  const navigate = useNavigate();
  const { data: clients, isLoading, isFetching } = useClients();

  const client = clients?.find((a) => a.id === clientId);

  // 如果加载完成但 client 不存在，回到 dashboard 概览
  useEffect(() => {
    if (!isLoading && !isFetching && clients && !client) {
      navigate({ to: '/dashboard' });
    }
  }, [isLoading, isFetching, clients, client, navigate]);

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

  if (!client) {
    return null; // will redirect via useEffect
  }

  return (
    <div className="p-8 max-w-6xl mx-auto w-full flex flex-col gap-8 z-10">
      <ClientHeader client={client} />
      <ClientStatsGrid client={client} />
      <TunnelTable client={client} />
      <TrafficChart />
    </div>
  );
}

export const dashboardClientRoute = createRoute({
  getParentRoute: () => dashboardRoute,
  path: '/clients/$clientId',
  component: ClientDetailPage,
});
