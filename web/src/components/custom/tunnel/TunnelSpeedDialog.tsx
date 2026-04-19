import { Activity } from 'lucide-react';
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { TrafficChart } from '@/components/custom/chart/TrafficChart';
import type { ProxyConfig } from '@/types';

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
  if (!tunnel) return null;

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-3xl">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <Activity className="h-5 w-5 text-primary" />
            <span>{tunnel.name} 的 24 小时速率</span>
          </DialogTitle>
        </DialogHeader>
        <div className="py-4">
          <TrafficChart clientId={clientId} tunnelFilter={[tunnel]} />
        </div>
      </DialogContent>
    </Dialog>
  );
}
