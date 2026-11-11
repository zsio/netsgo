import { lazy, Suspense } from 'react';
import { Activity } from 'lucide-react';
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { Skeleton } from '@/components/ui/skeleton';
import type { ProxyConfig } from '@/types';
import type { ResourceScope } from '@/lib/resource-scope';
import { useTranslation } from 'react-i18next';
import { buildTunnelSpeedFilter } from '@/lib/tunnel-speed';

const LazyTrafficRateChart = lazy(() =>
  import('@/components/custom/chart/TrafficRateChart').then((m) => ({
    default: m.TrafficRateChart,
  })),
);

interface TunnelSpeedDialogProps {
  scope: ResourceScope;
  tunnel: ProxyConfig | null;
  clientId: string;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}

export function TunnelSpeedDialog({
  scope,
  tunnel,
  clientId,
  open,
  onOpenChange,
}: TunnelSpeedDialogProps) {
  const { t } = useTranslation();
  if (!tunnel) return null;

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="w-[calc(100vw-2rem)] gap-3 sm:max-w-3xl">
        <DialogHeader>
          <DialogTitle className="flex min-w-0 items-center gap-2 pr-8">
            <Activity className="h-5 w-5 shrink-0 text-primary" />
            <span className="truncate">{t('clients.tunnelRateTitle', { name: tunnel.name })}</span>
          </DialogTitle>
        </DialogHeader>
        <div className="min-w-0">
          {open ? (
            <Suspense fallback={<Skeleton className="h-72 w-full rounded-xl" />}>
              <LazyTrafficRateChart
                scope={scope}
                clientId={clientId}
                tunnelFilter={buildTunnelSpeedFilter(tunnel)}
              />
            </Suspense>
          ) : null}
        </div>
      </DialogContent>
    </Dialog>
  );
}
