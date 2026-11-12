import { getTrafficSeriesKey } from '@/lib/tunnel-traffic-keys';
import type {
  ClientTrafficRange,
  ClientTrafficResponse,
  TrafficRealtimeEvent,
} from '@/types';

export interface RealtimeRatePoint {
  timestamp: number;
  inRate: number;
  outRate: number;
  negInRate: number;
  negOutRate: number;
}

export interface GlobalRealtimeTrafficSummary {
  series: RealtimeRatePoint[];
  currentInRate: number;
  currentOutRate: number;
  headlineInRate: number;
  headlineOutRate: number;
  peakInRate: number;
  peakOutRate: number;
  totalInBytes: number;
  totalOutBytes: number;
  topTunnelName: string | null;
}

const REALTIME_WINDOW_SECONDS = 60;
const SECOND_BUCKET_MS = 1_000;

const HISTORICAL_RANGE_CONFIG: Record<
  Exclude<ClientTrafficRange, '60s'>,
  { pointCount: number; bucketMs: number; bucketSeconds: number; windowSeconds: number }
> = {
  '1h': {
    pointCount: 60,
    bucketMs: 60_000,
    bucketSeconds: 60,
    windowSeconds: 3_600,
  },
  '24h': {
    pointCount: 24 * 12,
    bucketMs: 300_000,
    bucketSeconds: 300,
    windowSeconds: 86_400,
  },
  '7d': {
    pointCount: 7 * 24,
    bucketMs: 3_600_000,
    bucketSeconds: 3_600,
    windowSeconds: 7 * 86_400,
  },
};

export function buildGlobalRealtimeTrafficSummary(
  event: TrafficRealtimeEvent | undefined,
  onlineClientIds: ReadonlySet<string>,
  fallbackNowMs: number,
): GlobalRealtimeTrafficSummary {
  // 同一条 C2C 隧道可能同时出现在两端客户端的列表中，按 seriesKey + 秒级桶取较大值去重
  const perSeriesBuckets = new Map<
    string,
    { name: string | null; buckets: Map<number, { inBytes: number; outBytes: number; totalBytes: number }> }
  >();
  let latestObservedBucketMs = -Infinity;

  for (const client of event?.clients ?? []) {
    if (!onlineClientIds.has(client.client_id) || client.resolution !== 'second') {
      continue;
    }

    for (const item of client.items ?? []) {
      const seriesKey = getTrafficSeriesKey(item);
      let entry = perSeriesBuckets.get(seriesKey);
      if (!entry) {
        entry = { name: item.tunnel_name ?? null, buckets: new Map() };
        perSeriesBuckets.set(seriesKey, entry);
      } else if (!entry.name && item.tunnel_name) {
        entry.name = item.tunnel_name;
      }

      for (const point of item.points ?? []) {
        const rawMs = Date.parse(point.bucket_start);
        if (!Number.isFinite(rawMs)) {
          continue;
        }
        const bucketMs = Math.floor(rawMs / SECOND_BUCKET_MS) * SECOND_BUCKET_MS;
        if (bucketMs > latestObservedBucketMs) {
          latestObservedBucketMs = bucketMs;
        }
        const prev = entry.buckets.get(bucketMs);
        if (!prev || point.total_bytes > prev.totalBytes) {
          entry.buckets.set(bucketMs, {
            inBytes: point.ingress_bytes,
            outBytes: point.egress_bytes,
            totalBytes: point.total_bytes,
          });
        }
      }
    }
  }

  const fallbackBucketMs = Math.floor(fallbackNowMs / SECOND_BUCKET_MS) * SECOND_BUCKET_MS;
  const endBucketMs =
    Number.isFinite(latestObservedBucketMs) && Math.abs(fallbackBucketMs - latestObservedBucketMs) <= 15_000
      ? latestObservedBucketMs
      : fallbackBucketMs;
  const startBucketMs = endBucketMs - (REALTIME_WINDOW_SECONDS - 1) * SECOND_BUCKET_MS;

  const totalByBucket = new Map<number, { inBytes: number; outBytes: number }>();
  let topTunnelName: string | null = null;
  let topTunnelWindowBytes = 0;
  let totalInBytes = 0;
  let totalOutBytes = 0;

  for (const entry of perSeriesBuckets.values()) {
    let seriesWindowBytes = 0;
    for (const [bucketMs, sample] of entry.buckets.entries()) {
      if (bucketMs < startBucketMs || bucketMs > endBucketMs) {
        continue;
      }
      const agg = totalByBucket.get(bucketMs) ?? { inBytes: 0, outBytes: 0 };
      agg.inBytes += sample.inBytes;
      agg.outBytes += sample.outBytes;
      totalByBucket.set(bucketMs, agg);
      totalInBytes += sample.inBytes;
      totalOutBytes += sample.outBytes;
      seriesWindowBytes += sample.totalBytes;
    }

    if (seriesWindowBytes > topTunnelWindowBytes && entry.name) {
      topTunnelWindowBytes = seriesWindowBytes;
      topTunnelName = entry.name;
    }
  }

  let peakInRate = 0;
  let peakOutRate = 0;
  const series: RealtimeRatePoint[] = Array.from({ length: REALTIME_WINDOW_SECONDS }, (_, idx) => {
    const timestamp = startBucketMs + idx * SECOND_BUCKET_MS;
    const agg = totalByBucket.get(timestamp) ?? { inBytes: 0, outBytes: 0 };
    if (agg.inBytes > peakInRate) peakInRate = agg.inBytes;
    if (agg.outBytes > peakOutRate) peakOutRate = agg.outBytes;
    return {
      timestamp,
      inRate: agg.inBytes,
      outRate: agg.outBytes,
      negInRate: agg.inBytes === 0 ? 0 : -agg.inBytes,
      negOutRate: agg.outBytes === 0 ? 0 : -agg.outBytes,
    };
  });

  const last = series[series.length - 1] ?? { inRate: 0, outRate: 0 };

  return {
    series,
    currentInRate: last.inRate,
    currentOutRate: last.outRate,
    headlineInRate: last.inRate,
    headlineOutRate: last.outRate,
    peakInRate,
    peakOutRate,
    totalInBytes,
    totalOutBytes,
    topTunnelName,
  };
}

export function buildGlobalHistoricalTrafficSummary(
  responses: ReadonlyArray<ClientTrafficResponse | undefined>,
  range: Exclude<ClientTrafficRange, '60s'>,
  nowMs: number,
): GlobalRealtimeTrafficSummary {
  const config = HISTORICAL_RANGE_CONFIG[range];
  const endBucketMs = Math.floor(nowMs / config.bucketMs) * config.bucketMs;
  const startBucketMs = endBucketMs - (config.pointCount - 1) * config.bucketMs;

  // 先按隧道 seriesKey + 原始采样时间去重（避免双端 C2C 重复累加），再归入展示时间桶
  const perSeriesRawSamples = new Map<
    string,
    { name: string | null; rawPoints: Map<number, { inBytes: number; outBytes: number; totalBytes: number }> }
  >();

  for (const response of responses) {
    for (const item of response?.items ?? []) {
      const seriesKey = getTrafficSeriesKey(item);
      let entry = perSeriesRawSamples.get(seriesKey);
      if (!entry) {
        entry = { name: item.tunnel_name ?? null, rawPoints: new Map() };
        perSeriesRawSamples.set(seriesKey, entry);
      } else if (!entry.name && item.tunnel_name) {
        entry.name = item.tunnel_name;
      }

      for (const point of item.points ?? []) {
        const rawMs = Date.parse(point.bucket_start);
        if (!Number.isFinite(rawMs)) {
          continue;
        }
        const prev = entry.rawPoints.get(rawMs);
        if (!prev || point.total_bytes > prev.totalBytes) {
          entry.rawPoints.set(rawMs, {
            inBytes: point.ingress_bytes,
            outBytes: point.egress_bytes,
            totalBytes: point.total_bytes,
          });
        }
      }
    }
  }

  const totalByBucket = new Map<number, { inBytes: number; outBytes: number }>();
  let topTunnelName: string | null = null;
  let topTunnelWindowBytes = 0;
  let totalInBytes = 0;
  let totalOutBytes = 0;

  for (const entry of perSeriesRawSamples.values()) {
    let seriesWindowBytes = 0;
    for (const [rawMs, sample] of entry.rawPoints.entries()) {
      const bucketMs = Math.floor(rawMs / config.bucketMs) * config.bucketMs;
      if (bucketMs < startBucketMs || bucketMs > endBucketMs) {
        continue;
      }
      const agg = totalByBucket.get(bucketMs) ?? { inBytes: 0, outBytes: 0 };
      agg.inBytes += sample.inBytes;
      agg.outBytes += sample.outBytes;
      totalByBucket.set(bucketMs, agg);
      totalInBytes += sample.inBytes;
      totalOutBytes += sample.outBytes;
      seriesWindowBytes += sample.totalBytes;
    }

    if (seriesWindowBytes > topTunnelWindowBytes && entry.name) {
      topTunnelWindowBytes = seriesWindowBytes;
      topTunnelName = entry.name;
    }
  }

  let peakInRate = 0;
  let peakOutRate = 0;
  const series: RealtimeRatePoint[] = Array.from({ length: config.pointCount }, (_, idx) => {
    const timestamp = startBucketMs + idx * config.bucketMs;
    const agg = totalByBucket.get(timestamp) ?? { inBytes: 0, outBytes: 0 };
    const inRate = agg.inBytes / config.bucketSeconds;
    const outRate = agg.outBytes / config.bucketSeconds;
    if (inRate > peakInRate) peakInRate = inRate;
    if (outRate > peakOutRate) peakOutRate = outRate;
    return {
      timestamp,
      inRate,
      outRate,
      negInRate: inRate === 0 ? 0 : -inRate,
      negOutRate: outRate === 0 ? 0 : -outRate,
    };
  });

  const last = series[series.length - 1] ?? { inRate: 0, outRate: 0 };

  return {
    series,
    currentInRate: last.inRate,
    currentOutRate: last.outRate,
    headlineInRate: totalInBytes / config.windowSeconds,
    headlineOutRate: totalOutBytes / config.windowSeconds,
    peakInRate,
    peakOutRate,
    totalInBytes,
    totalOutBytes,
    topTunnelName,
  };
}

const NICE_STEP_MULTIPLIERS = [1, 2, 5, 10, 20, 50, 100, 200, 500, 1024];

function computeNiceBinaryStep(rawStep: number): number {
  const safeRaw = Math.max(rawStep, 1);
  const exp = Math.max(0, Math.min(3, Math.floor(Math.log(safeRaw) / Math.log(1024))));
  const unit = 1024 ** exp;
  const normalized = safeRaw / unit;
  const multiplier = NICE_STEP_MULTIPLIERS.find((candidate) => candidate >= normalized) ?? 1024;
  return multiplier * unit;
}

export function computeTrafficAxisTicks(peakRate: number): [number, number, number, number] {
  const minTop = Math.max(peakRate * 1.15, 3 * 1024);
  const step = computeNiceBinaryStep(minTop / 3);
  return [0, step, step * 2, step * 3];
}

export function computeSymmetricTrafficTicks(
  peakRate: number,
): [number, number, number, number, number] {
  const minTop = Math.max(peakRate * 1.18, 2 * 1024);
  const step = computeNiceBinaryStep(minTop / 2);
  return [-step * 2, -step, 0, step, step * 2];
}
