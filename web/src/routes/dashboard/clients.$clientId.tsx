import { createRoute, useParams, useNavigate } from '@tanstack/react-router';
import { useEffect, useState } from 'react';
import { motion } from 'motion/react';
import { dashboardRoute } from '@/routes/dashboard';
import { ClientHeader } from '@/components/custom/client/ClientHeader';
import { ClientInfoCard } from '@/components/custom/client/ClientInfoCard';
import { TunnelTable } from '@/components/custom/tunnel/TunnelTable';
import { ClientActivitySummary } from '@/components/custom/activity/ClientActivitySummary';
import { TrafficChart } from '@/components/custom/chart/TrafficChart';
import { useClients, useDeleteClient } from '@/hooks/use-clients';
import { Skeleton } from '@/components/ui/skeleton';
import { ConfirmDialog } from '@/components/custom/common/ConfirmDialog';
import type { Client } from '@/types';
import { getClientDisplayName } from '@/lib/client-utils';
import toast from 'react-hot-toast';
import { useTranslation } from 'react-i18next';

const stagger = {
  hidden: {},
  show: { transition: { staggerChildren: 0.08 } },
};

const fadeUp = {
  hidden: { opacity: 0, y: 12 },
  show: { opacity: 1, y: 0, transition: { duration: 0.35, ease: 'easeOut' as const } },
};

function ClientDetailPage() {
  const { t } = useTranslation();
  const { clientId } = useParams({ from: '/dashboard/clients/$clientId' });
  const navigate = useNavigate();
  const { data: clients, isLoading, isFetching } = useClients();
  const deleteClient = useDeleteClient();
  const [deleteTarget, setDeleteTarget] = useState<Client | null>(null);

  const client = clients?.find((a) => a.id === clientId);

  useEffect(() => {
    if (!isLoading && !isFetching && clients && !client) {
      navigate({ to: '/dashboard' });
    }
  }, [isLoading, isFetching, clients, client, navigate]);

  if (isLoading) {
    return (
      <div className="z-10 mx-auto flex w-full max-w-6xl flex-col gap-5 p-4 sm:gap-6 sm:p-6 lg:gap-8 lg:p-8">
        <Skeleton className="h-20 w-full rounded-xl" />
        <Skeleton className="h-[200px] w-full rounded-xl" />
        <Skeleton className="h-64 w-full rounded-xl" />
      </div>
    );
  }

  if (!client) {
    return null;
  }

  return (
    <motion.div
      key={clientId}
      className="z-10 mx-auto flex w-full max-w-6xl flex-col gap-5 p-4 sm:gap-6 sm:p-6 lg:gap-8 lg:p-8"
      variants={stagger}
      initial="hidden"
      animate="show"
    >
      <motion.div variants={fadeUp}><ClientHeader client={client} /></motion.div>
      <motion.div variants={fadeUp}><ClientInfoCard client={client} onRequestDelete={setDeleteTarget} /></motion.div>
      <motion.div variants={fadeUp}><ClientActivitySummary clientId={clientId} /></motion.div>
      <motion.div variants={fadeUp}><TunnelTable client={client} clients={clients ?? []} /></motion.div>
      <motion.div variants={fadeUp}>
        <TrafficChart clientId={clientId} tunnels={client.proxies ?? []} />
      </motion.div>
      <ConfirmDialog
        open={deleteTarget !== null}
        title={t('dashboard.deleteOfflineNode')}
        description={t('dashboard.deleteOfflineNodeDescription', { name: deleteTarget ? getClientDisplayName(deleteTarget) : '' })}
        confirmLabel={t('common.delete')}
        variant="destructive"
        onConfirm={() => {
          if (!deleteTarget) return;
          const target = deleteTarget;
          deleteClient.mutate(target.id, {
            onSuccess: () => {
              toast.success(t('dashboard.nodeDeleted', { name: getClientDisplayName(target) }));
              navigate({ to: '/dashboard' });
            },
            onError: (err) => toast.error((err as Error).message),
          });
          setDeleteTarget(null);
        }}
        onCancel={() => setDeleteTarget(null)}
      />
    </motion.div>
  );
}

export const dashboardClientRoute = createRoute({
  getParentRoute: () => dashboardRoute,
  path: '/clients/$clientId',
  component: ClientDetailPage,
});
