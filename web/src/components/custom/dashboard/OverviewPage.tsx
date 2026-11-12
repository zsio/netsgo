import { lazy, Suspense, useEffect, useRef, useState } from 'react';
import { motion } from 'motion/react';

import { Skeleton } from '@/components/ui/skeleton';
import { SELF_RESOURCE_SCOPE, type ResourceScope } from '@/lib/resource-scope';
import { useAuthStore } from '@/stores/auth-store';
import { DashboardActivityCard } from './DashboardActivityCard';
import { DashboardClientTable } from './DashboardClientTable';
import { ServerInfoCard } from './ServerInfoCard';

const LazyNetworkTopology = lazy(() =>
  import('./topology/NetworkTopology').then((m) => ({ default: m.NetworkTopology })),
);

const LazyDashboardTrafficCard = lazy(() =>
  import('./DashboardTrafficCard').then((m) => ({ default: m.DashboardTrafficCard })),
);

const stagger = {
  hidden: {},
  show: { transition: { staggerChildren: 0.06 } },
};

const fadeUp = {
  hidden: { opacity: 0, y: 10 },
  show: { opacity: 1, y: 0, transition: { duration: 0.3, ease: 'easeOut' as const } },
};

const TOPOLOGY_MIN_AVAILABLE_WIDTH = 820;

function canShowTopologyForWidth(width: number) {
  return width >= TOPOLOGY_MIN_AVAILABLE_WIDTH;
}

function canInitiallyShowTopology() {
  return typeof window === 'undefined' || window.innerWidth >= 1024;
}

export function OverviewPage({ scope }: { scope: ResourceScope }) {
  const isAdmin = useAuthStore((state) => state.user?.is_admin === true);
  const containerRef = useRef<HTMLDivElement>(null);
  const [showTopology, setShowTopology] = useState(canInitiallyShowTopology);

  useEffect(() => {
    const element = containerRef.current;
    if (!element) return;

    const observer = new ResizeObserver((entries) => {
      const width = entries[0]?.contentRect.width ?? 0;
      setShowTopology(canShowTopologyForWidth(width));
    });

    observer.observe(element);
    return () => observer.disconnect();
  }, []);

  return (
    <motion.div
      ref={containerRef}
      className="z-10 mx-auto flex w-full max-w-6xl flex-col gap-4 p-4 sm:gap-5 sm:p-6 lg:p-8"
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
        {showTopology ? (
          <Suspense fallback={<Skeleton className="h-[360px] w-full rounded-xl" />}>
            <LazyNetworkTopology scope={scope} />
          </Suspense>
        ) : (
          <DashboardClientTable scope={scope} />
        )}
      </motion.div>

      <motion.div variants={fadeUp} className="grid grid-cols-1 gap-4 sm:gap-5 lg:grid-cols-2">
        <Suspense fallback={<Skeleton className="h-[316px] w-full rounded-xl" />}>
          <LazyDashboardTrafficCard scope={scope} />
        </Suspense>
        <DashboardActivityCard scope={scope} />
      </motion.div>
    </motion.div>
  );
}

export function SelfOverviewPage() {
  return <OverviewPage scope={SELF_RESOURCE_SCOPE} />;
}
