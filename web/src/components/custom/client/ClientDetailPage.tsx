import { useParams, useNavigate } from '@tanstack/react-router';
import { lazy, Suspense, useEffect, useState } from 'react';
import { motion } from 'motion/react';
import { ClientHeader } from '@/components/custom/client/ClientHeader';
import { ClientInfoCard } from '@/components/custom/client/ClientInfoCard';
import { TunnelTable } from '@/components/custom/tunnel/TunnelTable';
import { ClientActivitySheet } from '@/components/custom/activity/ClientActivitySheet';
import { ManagedUserBreadcrumb } from '@/components/custom/users/ManagedUserBreadcrumb';
import { useClients, useDeleteClient } from '@/hooks/use-clients';
import { Skeleton } from '@/components/ui/skeleton';
import { ConfirmDialog } from '@/components/custom/common/ConfirmDialog';
import type { Client } from '@/types';
import { getClientDisplayName } from '@/lib/client-utils';
import { adminUserResourceScope, SELF_RESOURCE_SCOPE, type ResourceScope } from '@/lib/resource-scope';
import toast from 'react-hot-toast';
import { useTranslation } from 'react-i18next';

const LazyTrafficChart = lazy(() =>
  import('@/components/custom/chart/TrafficChart').then((m) => ({ default: m.TrafficChart })),
);

const stagger = {
  hidden: {},
  show: { transition: { staggerChildren: 0.08 } },
};

const fadeUp = {
  hidden: { opacity: 0, y: 12 },
  show: { opacity: 1, y: 0, transition: { duration: 0.35, ease: 'easeOut' as const } },
};

export function ClientDetailPage({ scope, clientId }: { scope: ResourceScope; clientId: string }) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const { data: clients, isLoading, isFetching } = useClients(scope);
  const deleteClient = useDeleteClient(scope);
  const [deleteTarget, setDeleteTarget] = useState<Client | null>(null);
  const [activityOpen, setActivityOpen] = useState(false);

  const client = clients?.find((a) => a.id === clientId);

  useEffect(() => {
    if (!isLoading && !isFetching && clients && !client) {
      if (scope.kind === 'admin-user') {
        navigate({ to: '/dashboard/users/$userId', params: { userId: scope.userId } });
        return;
      }
      navigate({ to: '/dashboard' });
    }
  }, [isLoading, isFetching, clients, client, navigate, scope]);

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
      {scope.kind === 'admin-user' && (
        <motion.div variants={fadeUp}>
          <ManagedUserBreadcrumb userId={scope.userId} clientLabel={getClientDisplayName(client)} />
        </motion.div>
      )}
      <motion.div variants={fadeUp}><ClientHeader scope={scope} client={client} onShowActivity={() => setActivityOpen(true)} /></motion.div>
      <motion.div variants={fadeUp}><ClientInfoCard scope={scope} client={client} onRequestDelete={setDeleteTarget} /></motion.div>
      <motion.div variants={fadeUp}><TunnelTable scope={scope} client={client} clients={clients ?? []} /></motion.div>
      <motion.div variants={fadeUp}>
        <Suspense fallback={<Skeleton className="h-[260px] w-full rounded-xl" />}>
          <LazyTrafficChart scope={scope} clientId={clientId} tunnels={client.proxies ?? []} />
        </Suspense>
      </motion.div>
      <ClientActivitySheet
        scope={scope}
        key={clientId}
        client={client}
        open={activityOpen}
        onOpenChange={setActivityOpen}
      />
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
              if (scope.kind === 'admin-user') {
                navigate({ to: '/dashboard/users/$userId', params: { userId: scope.userId } });
                return;
              }
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

export function SelfClientDetailRoute() {
  const { clientId } = useParams({ from: '/dashboard/clients/$clientId' });
  return <ClientDetailPage scope={SELF_RESOURCE_SCOPE} clientId={clientId} />;
}

export function ManagedUserClientDetailRoute() {
  const { userId, clientId } = useParams({ from: '/dashboard/users/$userId/clients/$clientId' });
  return <ClientDetailPage scope={adminUserResourceScope(userId)} clientId={clientId} />;
}
