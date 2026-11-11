import { lazy, Suspense, useEffect, useMemo, useRef, useState } from 'react';
import { motion } from 'motion/react';
import { Waypoints, Laptop, ArrowRightLeft } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import { Skeleton } from '@/components/ui/skeleton';
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { useClients } from '@/hooks/use-clients';
import { useAuthStore } from '@/stores/auth-store';
import { ServerInfoCard } from './ServerInfoCard';
import { DashboardClientTable } from './DashboardClientTable';
import { DashboardTunnelTable } from './DashboardTunnelTable';
import { buildDashboardTabMetrics, formatDashboardTabCount } from './dashboard-tab-metrics';
import { SELF_RESOURCE_SCOPE, type ResourceScope } from '@/lib/resource-scope';

const LazyNetworkTopology = lazy(() =>
  import('./topology/NetworkTopology').then((m) => ({ default: m.NetworkTopology })),
);

const stagger = {
  hidden: {},
  show: { transition: { staggerChildren: 0.08 } },
};

const fadeUp = {
  hidden: { opacity: 0, y: 12 },
  show: { opacity: 1, y: 0, transition: { duration: 0.35, ease: 'easeOut' as const } },
};

export type DashboardTab = 'topology' | 'clients' | 'tunnels';

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

function TabCountBadge({ label }: { label: string | null }) {
  if (!label) {
    return null;
  }

  return (
    <span className="rounded-full bg-muted px-1.5 py-0.5 font-mono text-[10px] leading-none text-muted-foreground group-data-active/trigger:bg-background/80">
      {label}
    </span>
  );
}

export function OverviewPage({
  scope,
  tab,
  onTabChange,
}: {
  scope: ResourceScope;
  tab?: DashboardTab;
  onTabChange?: (tab: DashboardTab) => void;
}) {
  const { t } = useTranslation();
  const isAdmin = useAuthStore((state) => state.user?.is_admin === true);
  const { data: clients } = useClients(scope);
  const containerRef = useRef<HTMLDivElement>(null);
  const [showTopologyTab, setShowTopologyTab] = useState(canInitiallyShowTopologyTab);
  const [activeTab, setActiveTab] = useState<DashboardTab>(() => (
    canInitiallyShowTopologyTab() ? 'topology' : 'clients'
  ));
  const selectedTab = tab ?? activeTab;
  const currentTab = showTopologyTab || selectedTab !== 'topology' ? selectedTab : 'clients';
  const shouldRenderDashboardTabs = clients === undefined || clients.length > 0;

  const tabMetrics = useMemo(() => buildDashboardTabMetrics(clients), [clients]);
  const clientTabCount = formatDashboardTabCount(tabMetrics.clients);
  const tunnelTabCount = formatDashboardTabCount(tabMetrics.tunnels);

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
      {isAdmin && scope.kind === 'self' ? (
        <motion.div variants={fadeUp}>
          <ServerInfoCard scope={scope} />
        </motion.div>
      ) : null}
      <motion.div variants={fadeUp}>
        {shouldRenderDashboardTabs ? (
          <Tabs
            value={currentTab}
            onValueChange={(value) => {
              if (isDashboardTab(value)) {
                setActiveTab(value);
                onTabChange?.(value);
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
                <TabCountBadge label={clientTabCount} />
              </TabsTrigger>
              <TabsTrigger value="tunnels" className="group/trigger gap-1.5 px-3">
                <ArrowRightLeft className="h-4 w-4" />
                {t('dashboard.tabTunnels')}
                <TabCountBadge label={tunnelTabCount} />
              </TabsTrigger>
            </TabsList>
            {showTopologyTab && (
              <TabsContent value="topology" forceMount className="data-[state=inactive]:hidden">
                <Suspense fallback={<Skeleton className="h-[460px] w-full rounded-xl" />}>
                  <LazyNetworkTopology scope={scope} />
                </Suspense>
              </TabsContent>
            )}
            <TabsContent value="clients"><DashboardClientTable scope={scope} /></TabsContent>
            <TabsContent value="tunnels"><DashboardTunnelTable scope={scope} /></TabsContent>
          </Tabs>
        ) : (
          <DashboardClientTable scope={scope} />
        )}
      </motion.div>
    </motion.div>
  );
}

export function SelfOverviewPage() {
  return <OverviewPage scope={SELF_RESOURCE_SCOPE} />;
}
