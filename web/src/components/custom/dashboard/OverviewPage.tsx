import { useMemo } from 'react';
import { motion } from 'motion/react';
import { Waypoints, Laptop, ArrowRightLeft } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { useClients } from '@/hooks/use-clients';
import { OverviewHeader } from './OverviewHeader';
import { ServerInfoCard } from './ServerInfoCard';
import { DashboardClientTable } from './DashboardClientTable';
import { DashboardTunnelTable } from './DashboardTunnelTable';
import { NetworkTopology } from './NetworkTopology';

const stagger = {
  hidden: {},
  show: { transition: { staggerChildren: 0.08 } },
};

const fadeUp = {
  hidden: { opacity: 0, y: 12 },
  show: { opacity: 1, y: 0, transition: { duration: 0.35, ease: 'easeOut' as const } },
};

function TabCountBadge({ count }: { count: number }) {
  return (
    <span className="rounded-full bg-muted px-1.5 py-0.5 font-mono text-[10px] leading-none text-muted-foreground group-data-active/trigger:bg-background/80">
      {count}
    </span>
  );
}

export function OverviewPage() {
  const { t } = useTranslation();
  const { data: clients } = useClients();

  const tunnelCount = useMemo(() => {
    const ids = new Set<string>();
    for (const client of clients ?? []) {
      for (const proxy of client.proxies ?? []) {
        ids.add(proxy.id);
      }
    }
    return ids.size;
  }, [clients]);

  return (
    <motion.div
      className="z-10 mx-auto flex w-full max-w-6xl flex-col gap-5 p-4 sm:gap-6 sm:p-6 lg:gap-8 lg:p-8"
      variants={stagger}
      initial="hidden"
      animate="show"
    >
      <motion.div variants={fadeUp}><OverviewHeader /></motion.div>
      <motion.div variants={fadeUp}><ServerInfoCard /></motion.div>
      <motion.div variants={fadeUp}>
        <Tabs defaultValue="topology" className="gap-4">
          <TabsList className="h-9">
            <TabsTrigger value="topology" className="group/trigger gap-1.5 px-3">
              <Waypoints className="h-4 w-4" />
              {t('dashboard.tabTopology')}
            </TabsTrigger>
            <TabsTrigger value="clients" className="group/trigger gap-1.5 px-3">
              <Laptop className="h-4 w-4" />
              {t('dashboard.tabClients')}
              <TabCountBadge count={clients?.length ?? 0} />
            </TabsTrigger>
            <TabsTrigger value="tunnels" className="group/trigger gap-1.5 px-3">
              <ArrowRightLeft className="h-4 w-4" />
              {t('dashboard.tabTunnels')}
              <TabCountBadge count={tunnelCount} />
            </TabsTrigger>
          </TabsList>
          <TabsContent value="topology"><NetworkTopology /></TabsContent>
          <TabsContent value="clients"><DashboardClientTable /></TabsContent>
          <TabsContent value="tunnels"><DashboardTunnelTable /></TabsContent>
        </Tabs>
      </motion.div>
    </motion.div>
  );
}
