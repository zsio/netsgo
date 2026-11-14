import { formatNetSpeed } from '@/lib/format';
import type { TunnelStatusPresentation } from '@/lib/tunnel-model';

import {
  EMPTY_TOPOLOGY_TRAFFIC_RATE,
  type TopologyTrafficRate,
} from './topology-model';

export type StatusKey = TunnelStatusPresentation['key'];

export const STATUS_DOT: Record<StatusKey, string> = {
  exposed: 'bg-status-online',
  pending: 'bg-status-pending',
  offline: 'bg-status-warning',
  stopped: 'bg-muted-foreground/55',
  error: 'bg-status-danger',
};

export const STATUS_TEXT: Record<StatusKey, string> = {
  exposed: 'text-status-online-fg',
  pending: 'text-status-pending-fg',
  offline: 'text-status-warning-fg',
  stopped: 'text-muted-foreground',
  error: 'text-status-danger-fg',
};

export const STATUS_PORT_BORDER: Record<StatusKey, string> = {
  exposed: 'border-status-online',
  pending: 'border-status-pending',
  offline: 'border-status-warning',
  stopped: 'border-muted-foreground/55',
  error: 'border-status-danger',
};

export const STATUS_WIRE_BASE: Record<StatusKey, string> = {
  exposed: 'stroke-status-online/35',
  pending: 'stroke-status-pending/45',
  offline: 'stroke-status-warning/50',
  stopped: 'stroke-muted-foreground/35',
  error: 'stroke-status-danger/55',
};

export const STATUS_WIRE_FLOW: Record<StatusKey, string> = {
  exposed: 'stroke-status-online',
  pending: 'stroke-status-pending',
  offline: 'stroke-status-warning',
  stopped: 'stroke-muted-foreground/50',
  error: 'stroke-status-danger',
};

export const STATUS_WIRE_STOP_COLOR: Record<StatusKey, string> = {
  exposed: 'var(--status-online)',
  pending: 'var(--status-pending)',
  offline: 'var(--status-warning)',
  stopped: 'var(--muted-foreground)',
  error: 'var(--status-danger)',
};

export function statusLabel(
  t: (key: string, options?: Record<string, unknown>) => string,
  status: TunnelStatusPresentation,
) {
  return t(`tunnels.status${status.key[0].toUpperCase()}${status.key.slice(1)}`, {
    defaultValue: status.label,
  });
}

export function rateOrZero(rate: TopologyTrafficRate | undefined) {
  return rate ?? EMPTY_TOPOLOGY_TRAFFIC_RATE;
}

export function hasTraffic(rate: TopologyTrafficRate | undefined) {
  return rateOrZero(rate).totalBps > 0;
}

export function trafficIntensity(rate: TopologyTrafficRate | undefined) {
  const totalBps = rateOrZero(rate).totalBps;
  if (totalBps <= 0) return 0;
  return Math.min(1, Math.log10(totalBps + 1) / 6);
}

export function tunnelStreamDuration(rate: TopologyTrafficRate | undefined) {
  return `${(1.2 - trafficIntensity(rate) * 0.5).toFixed(2)}s`;
}

export function formatTrafficPair(rate: TopologyTrafficRate | undefined) {
  const value = rateOrZero(rate);
  return `↓ ${formatNetSpeed(value.ingressBps)} · ↑ ${formatNetSpeed(value.egressBps)}`;
}
