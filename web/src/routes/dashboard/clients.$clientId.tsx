import { createRoute, useParams, useNavigate } from '@tanstack/react-router';
import { useEffect } from 'react';
import { motion } from 'motion/react';
import { dashboardRoute } from '@/routes/dashboard';
import { ClientHeader } from '@/components/custom/client/ClientHeader';
import { ClientInfoCard } from '@/components/custom/client/ClientInfoCard';
import { TunnelTable } from '@/components/custom/tunnel/TunnelTable';
import { TrafficChart } from '@/components/custom/chart/TrafficChart';
import { useClients } from '@/hooks/use-clients';
import { Skeleton } from '@/components/ui/skeleton';

const stagger = {
  hidden: {},
  show: { transition: { staggerChildren: 0.08 } },
};

const fadeUp = {
  hidden: { opacity: 0, y: 12 },
  show: { opacity: 1, y: 0, transition: { duration: 0.35, ease: 'easeOut' as const } },
};

function ClientDetailPage() {
  const { clientId } = useParams({ from: '/dashboard/clients/$clientId' });
  const navigate = useNavigate();
  const { data: clients, isLoading, isFetching } = useClients();

  const client = clients?.find((a) => a.id === clientId);

  useEffect(() => {
    if (!isLoading && !isFetching && clients && !client) {
      navigate({ to: '/dashboard' });
    }
  }, [isLoading, isFetching, clients, client, navigate]);

  if (isLoading) {
    return (
      <div className="z-10 mx-auto flex w-full max-w-6xl flex-col gap-8 p-8">
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
      className="z-10 mx-auto flex w-full max-w-6xl flex-col gap-8 p-8"
      variants={stagger}
      initial="hidden"
      animate="show"
    >
      <motion.div variants={fadeUp}><ClientHeader client={client} /></motion.div>
      <motion.div variants={fadeUp}><ClientInfoCard client={client} /></motion.div>
      <motion.div variants={fadeUp}><TunnelTable client={client} /></motion.div>
      <motion.div variants={fadeUp}>
        <TrafficChart clientId={clientId} />
      </motion.div>
    </motion.div>
  );
}

export const dashboardClientRoute = createRoute({
  getParentRoute: () => dashboardRoute,
  path: '/clients/$clientId',
  component: ClientDetailPage,
});
