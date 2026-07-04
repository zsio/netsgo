import { formatNetSpeed } from '@/lib/format';
import type { TunnelStatusPresentation } from '@/lib/tunnel-model';

import {
  EMPTY_TOPOLOGY_TRAFFIC_RATE,
  type TopologyLinkEmphasis,
  type TopologyTrafficRate,
} from './topology-model';

export type StatusKey = TunnelStatusPresentation['key'];

export const EDGE_STROKE: Record<StatusKey, string> = {
  exposed: 'stroke-cyan-500/75',
  pending: 'stroke-violet-500/70',
  offline: 'stroke-amber-500/60',
  stopped: 'stroke-muted-foreground/35',
  error: 'stroke-destructive/70',
};

export const STATUS_DOT: Record<StatusKey, string> = {
  exposed: 'bg-emerald-500',
  pending: 'bg-sky-500',
  offline: 'bg-amber-500',
  stopped: 'bg-muted-foreground/60',
  error: 'bg-destructive',
};

export const STATUS_TEXT: Record<StatusKey, string> = {
  exposed: 'text-emerald-600',
  pending: 'text-sky-600',
  offline: 'text-amber-600',
  stopped: 'text-muted-foreground',
  error: 'text-destructive',
};

export const LABEL_HALO = {
  paintOrder: 'stroke',
  stroke: 'var(--color-background)',
  strokeWidth: 3,
  strokeLinejoin: 'round',
} as const;

export function statusLabel(t: (key: string, options?: Record<string, unknown>) => string, status: TunnelStatusPresentation) {
  return t(`tunnels.status${status.key[0].toUpperCase()}${status.key.slice(1)}`, {
    defaultValue: status.label,
  });
}

export function truncateLabel(value: string, max = 16) {
  return value.length > max ? `${value.slice(0, max - 1)}…` : value;
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

export function flowDuration(rate: TopologyTrafficRate | undefined) {
  return `${(1.55 - trafficIntensity(rate) * 0.85).toFixed(2)}s`;
}

/** 渐变流光扫过整条边所需时长；流量越大扫得越快。 */
export function flowSweepDuration(rate: TopologyTrafficRate | undefined) {
  return `${(2.6 - trafficIntensity(rate) * 1.4).toFixed(2)}s`;
}

export function trafficStrokeWidth(rate: TopologyTrafficRate | undefined, base: number, emphasis: TopologyLinkEmphasis) {
  const emphasisBoost = emphasis === 'strong' ? 0.45 : 0;
  return base + trafficIntensity(rate) * 1.3 + emphasisBoost;
}

export function emphasisOpacity(emphasis: TopologyLinkEmphasis) {
  switch (emphasis) {
    case 'strong':
      return 0.92;
    case 'muted':
      return 0.2;
    case 'hidden':
    default:
      return 0;
  }
}

export function formatTrafficPair(rate: TopologyTrafficRate | undefined) {
  const value = rateOrZero(rate);
  return `↓ ${formatNetSpeed(value.ingressBps)}  ↑ ${formatNetSpeed(value.egressBps)}`;
}
