import { useEffect, useMemo, useRef, useState } from 'react';
import { motion } from 'motion/react';
import { Waypoints, Laptop, ArrowRightLeft } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { useClients } from '@/hooks/use-clients';
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

type DashboardTab = 'topology' | 'clients' | 'tunnels';

const TOPOLOGY_TAB_MIN_AVAILABLE_WIDTH = 820;

function canShowTopologyTabForWidth(width: number) {
  return width >= TOPOLOGY_TAB_MIN_AVAILABLE_WIDTH;
}

function canInitiallyShowTopologyTab() {
  return typeof window === 'undefined' || window.innerWidth >= 1024;
}

function isDashboardTab(value: string): value is DashboardTab {
  return value === 'topology' || value === 'clients' || value === 'tunnels';
}

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
  const containerRef = useRef<HTMLDivElement>(null);
  const [showTopologyTab, setShowTopologyTab] = useState(canInitiallyShowTopologyTab);
  const [activeTab, setActiveTab] = useState<DashboardTab>(() => (
    canInitiallyShowTopologyTab() ? 'topology' : 'clients'
  ));
  const currentTab = showTopologyTab || activeTab !== 'topology' ? activeTab : 'clients';

  const tunnelCount = useMemo(() => {
    const ids = new Set<string>();
    for (const client of clients ?? []) {
      for (const proxy of client.proxies ?? []) {
        ids.add(proxy.id);
      }
    }
    return ids.size;
  }, [clients]);

  useEffect(() => {
    const element = containerRef.current;
    if (!element) return;

    const observer = new ResizeObserver((entries) => {
      const width = entries[0]?.contentRect.width ?? 0;
      const nextShowTopologyTab = canShowTopologyTabForWidth(width);
      setShowTopologyTab(nextShowTopologyTab);
      if (!nextShowTopologyTab) {
        setActiveTab((currentTab) => (currentTab === 'topology' ? 'clients' : currentTab));
      }
    });

    observer.observe(element);
    return () => observer.disconnect();
  }, []);

  return (
    <motion.div
      ref={containerRef}
      className="z-10 mx-auto flex w-full max-w-6xl flex-col gap-5 p-4 sm:gap-6 sm:p-6 lg:gap-8 lg:p-8"
      variants={stagger}
      initial="hidden"
      animate="show"
    >
      <motion.div variants={fadeUp}><ServerInfoCard /></motion.div>
      <motion.div variants={fadeUp}>
        <Tabs
          value={currentTab}
          onValueChange={(value) => {
            if (isDashboardTab(value)) {
              setActiveTab(value);
            }
          }}
          className="gap-4"
        >
          <TabsList className="h-9">
            {showTopologyTab && (
              <TabsTrigger value="topology" className="group/trigger gap-1.5 px-3">
                <Waypoints className="h-4 w-4" />
                {t('dashboard.tabTopology')}
              </TabsTrigger>
            )}
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
          {showTopologyTab && (
            <TabsContent value="topology" forceMount className="data-[state=inactive]:hidden">
              <NetworkTopology />
            </TabsContent>
          )}
          <TabsContent value="clients"><DashboardClientTable /></TabsContent>
          <TabsContent value="tunnels"><DashboardTunnelTable /></TabsContent>
        </Tabs>
      </motion.div>
    </motion.div>
  );
}
