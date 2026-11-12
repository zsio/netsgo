import { formatNetSpeed } from '@/lib/format';
import type { TunnelStatusPresentation } from '@/lib/tunnel-model';

import {
  EMPTY_TOPOLOGY_TRAFFIC_RATE,
  type TopologyTrafficRate,
} from './topology-model';

export type StatusKey = TunnelStatusPresentation['key'];

export const STATUS_DOT: Record<StatusKey, string> = {
  exposed: 'bg-emerald-500',
  pending: 'bg-sky-500',
  offline: 'bg-amber-500',
  stopped: 'bg-zinc-400 dark:bg-zinc-500',
  error: 'bg-rose-500',
};

export const STATUS_TEXT: Record<StatusKey, string> = {
  exposed: 'text-emerald-600 dark:text-emerald-400',
  pending: 'text-sky-600 dark:text-sky-400',
  offline: 'text-amber-600 dark:text-amber-400',
  stopped: 'text-muted-foreground',
  error: 'text-rose-600 dark:text-rose-400',
};

export const STATUS_PORT_BORDER: Record<StatusKey, string> = {
  exposed: 'border-emerald-500',
  pending: 'border-sky-500',
  offline: 'border-amber-500',
  stopped: 'border-zinc-400 dark:border-zinc-500',
  error: 'border-rose-500',
};

export const STATUS_WIRE_BASE: Record<StatusKey, string> = {
  exposed: 'stroke-emerald-500/45 dark:stroke-emerald-500/40',
  pending: 'stroke-sky-500/50',
  offline: 'stroke-amber-500/55',
  stopped: 'stroke-zinc-400/45 dark:stroke-zinc-600/60',
  error: 'stroke-rose-500/60',
};

export const STATUS_WIRE_FLOW: Record<StatusKey, string> = {
  exposed: 'stroke-emerald-500 dark:stroke-emerald-400',
  pending: 'stroke-sky-500 dark:stroke-sky-400',
  offline: 'stroke-amber-500',
  stopped: 'stroke-zinc-400',
  error: 'stroke-rose-500 dark:stroke-rose-400',
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
