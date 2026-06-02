import { Activity } from 'lucide-react';
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { TrafficRateChart } from '@/components/custom/chart/TrafficRateChart';
import type { ProxyConfig } from '@/types';
import { useTranslation } from 'react-i18next';
import { buildTunnelSpeedFilter } from '@/lib/tunnel-speed';

interface TunnelSpeedDialogProps {
  tunnel: ProxyConfig | null;
  clientId: string;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}

export function TunnelSpeedDialog({
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
          <TrafficRateChart clientId={clientId} tunnelFilter={buildTunnelSpeedFilter(tunnel)} />
        </div>
      </DialogContent>
    </Dialog>
  );
}
