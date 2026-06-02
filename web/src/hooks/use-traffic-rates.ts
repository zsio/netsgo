import { useMemo } from 'react';
import { getTrafficSeriesKey, getTunnelSeriesKey } from '@/lib/tunnel-traffic-keys';
import type { ClientTrafficResponse, ClientTrafficRange, ProxyConfig } from '@/types';

export interface RatePoint {
  timestamp: number;
  inRate: number;
  outRate: number;
}

const RANGE_WINDOW_CONFIG: Record<ClientTrafficRange, { pointCount: number; bucketMs: number; divisor: number }> = {
  '60s': {
    pointCount: 60,
    bucketMs: 1_000,
    divisor: 1,
  },
  '1h': {
    pointCount: 60,
    bucketMs: 60_000,
    divisor: 60,
  },
  '24h': {
    pointCount: 24 * 60,
    bucketMs: 60_000,
    divisor: 60,
  },
  '7d': {
    pointCount: 7 * 24,
    bucketMs: 3_600_000,
    divisor: 3_600,
  },
};

type TunnelTrafficFilter = Pick<ProxyConfig, 'name' | 'type'> & Partial<Pick<ProxyConfig, 'id'>>;

function createAllowedSet(tunnels?: TunnelTrafficFilter[]) {
  if (!tunnels || tunnels.length === 0) {
    return null;
  }

  return new Set(tunnels.map(getTunnelSeriesKey));
}

export function hasTrafficSamples(
  data: ClientTrafficResponse | undefined,
  tunnels?: TunnelTrafficFilter[],
) {
  if (!data) {
    return false;
  }

  const allowedSet = createAllowedSet(tunnels);

  return data.items.some((item) => {
    if (allowedSet && !allowedSet.has(getTrafficSeriesKey(item))) {
      return false;
    }

    return item.points.length > 0;
  });
}

export function buildAggregatedTrafficRates(
  data: ClientTrafficResponse | undefined,
  range: ClientTrafficRange,
  tunnels?: TunnelTrafficFilter[],
  nowMs = Date.now(),
): RatePoint[] {
  if (!data) {
    return [];
  }

  const config = RANGE_WINDOW_CONFIG[range];
  const pointsMap = new Map<number, { in: number; out: number }>();
  const allowedSet = createAllowedSet(tunnels);

  for (const item of data.items) {
    if (allowedSet && !allowedSet.has(getTrafficSeriesKey(item))) {
      continue;
    }

    for (const point of item.points) {
      const timestamp = new Date(point.bucket_start).getTime();
      const existing = pointsMap.get(timestamp) ?? { in: 0, out: 0 };

      existing.in += point.ingress_bytes;
      existing.out += point.egress_bytes;
      pointsMap.set(timestamp, existing);
    }
  }

  const endTimestamp = Math.floor(nowMs / config.bucketMs) * config.bucketMs - config.bucketMs;

  return Array.from({ length: config.pointCount }, (_, index) => {
    const timestamp = endTimestamp - (config.pointCount - index - 1) * config.bucketMs;
    const bytes = pointsMap.get(timestamp) ?? { in: 0, out: 0 };

    return {
      timestamp,
      inRate: bytes.in / config.divisor,
      outRate: bytes.out / config.divisor,
    };
  });
}

export function useAggregatedTrafficRates(
  data: ClientTrafficResponse | undefined,
  range: ClientTrafficRange,
  tunnels?: TunnelTrafficFilter[],
): RatePoint[] {
  return useMemo(() => buildAggregatedTrafficRates(data, range, tunnels), [data, range, tunnels]);
}
