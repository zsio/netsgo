import { useId, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Area, AreaChart, CartesianGrid, XAxis, YAxis } from 'recharts';
import { AlertCircle, Activity } from 'lucide-react';

import { computeTrafficAxisTicks } from '@/components/custom/dashboard/dashboard-traffic-summary';
import { cn } from '@/lib/utils';
import {
  type ChartConfig,
  ChartContainer,
  ChartTooltip,
  ChartTooltipContent,
  ChartLegend,
  ChartLegendContent,
} from '@/components/ui/chart';
import { useClientTraffic } from '@/hooks/use-client-traffic';
import { formatBytes, formatTrafficRate } from '@/lib/format';
import { getTrafficSeriesKey, getTunnelSeriesKey } from '@/lib/tunnel-traffic-keys';
import type { ResourceScope } from '@/lib/resource-scope';
import type { ClientTrafficRange, ClientTrafficResponse, ProxyConfig, ProxyType } from '@/types';

interface TrafficChartProps {
  scope: ResourceScope;
  clientId: string;
  tunnels: ProxyConfig[];
}

type TunnelMeta = {
  key: string;
  gradId: string;
  name: string;
  type: ProxyType;
  color: string;
};

type ChartRow = {
  timestamp: number;
  [key: string]: number;
};

const RANGE_OPTIONS: Array<{ value: ClientTrafficRange; label: string }> = [
  { value: '60s', label: '60s' },
  { value: '24h', label: '24h' },
  { value: '7d', label: '7d' },
];

const PALETTE = [
  '#10b981', // emerald
  '#0ea5e9', // sky
  '#8b5cf6', // violet
  '#f59e0b', // amber
  '#ec4899', // pink
  '#14b8a6', // teal
] as const;

const RANGE_CHART_CONFIG: Partial<
  Record<
    ClientTrafficRange,
    {
      bucketSeconds: number;
      gridBucketMs: number;
      gridPointCount: number;
    }
  >
> = {
  '60s': { bucketSeconds: 1, gridBucketMs: 1_000, gridPointCount: 60 },
  '24h': { bucketSeconds: 300, gridBucketMs: 300_000, gridPointCount: 24 * 12 },
  '7d': { bucketSeconds: 3_600, gridBucketMs: 3_600_000, gridPointCount: 7 * 24 },
};

function getTunnelColor(index: number) {
  return PALETTE[index % PALETTE.length]!;
}

function getTrafficSeriesName(
  item: ClientTrafficResponse['items'][number],
  t: ReturnType<typeof useTranslation>['t'],
) {
  return item.tunnel_name ?? t('traffic.deletedTunnel', { id: item.tunnel_id ?? 'unknown' });
}

function formatXAxisLabel(timestamp: number, range: ClientTrafficRange, language: string) {
  const date = new Date(timestamp);
  if (range === '60s') {
    return date.toLocaleTimeString(language, {
      hour: '2-digit',
      minute: '2-digit',
      second: '2-digit',
      hour12: false,
    });
  }
  return date.toLocaleString(
    language,
    range === '24h'
      ? { hour: '2-digit', minute: '2-digit', hour12: false }
      : { month: 'numeric', day: 'numeric', hour: '2-digit', hour12: false },
  );
}

function formatTooltipLabel(timestamp: number, range: ClientTrafficRange, language: string) {
  const date = new Date(timestamp);
  if (range === '60s') {
    return date.toLocaleTimeString(language, {
      hour: '2-digit',
      minute: '2-digit',
      second: '2-digit',
      hour12: false,
    });
  }
  return date.toLocaleString(
    language,
    range === '24h'
      ? { month: 'numeric', day: 'numeric', hour: '2-digit', minute: '2-digit', hour12: false }
      : { year: 'numeric', month: 'numeric', day: 'numeric', hour: '2-digit', hour12: false },
  );
}

function getRangeSummary(range: ClientTrafficRange, t: ReturnType<typeof useTranslation>['t']) {
  switch (range) {
    case '60s':
      return t('traffic.range60s');
    case '24h':
      return t('traffic.range24h');
    case '7d':
      return t('traffic.range7d');
    default:
      return t('traffic.rangeDefault');
  }
}

function getErrorMessage(error: unknown, t: ReturnType<typeof useTranslation>['t']) {
  if (error instanceof Error && error.message) {
    return error.message;
  }
  return t('traffic.loadFailed');
}

function buildGridTimestamps(range: ClientTrafficRange, nowMs = Date.now()) {
  const config = RANGE_CHART_CONFIG[range];
  if (!config) return [];
  const { gridBucketMs, gridPointCount } = config;
  const endTimestamp = Math.floor(nowMs / gridBucketMs) * gridBucketMs;
  return Array.from({ length: gridPointCount }, (_, index) => (
    endTimestamp - (gridPointCount - index - 1) * gridBucketMs
  ));
}

function buildTrafficTrendChartState(
  data: ClientTrafficResponse | undefined,
  tunnels: (Pick<ProxyConfig, 'name' | 'type'> & Partial<Pick<ProxyConfig, 'id'>>)[],
  range: ClientTrafficRange,
  gradPrefix: string,
  t: ReturnType<typeof useTranslation>['t'],
) {
  const knownTunnels = new Map<string, Pick<TunnelMeta, 'key' | 'name' | 'type'>>();

  for (const tunnel of tunnels) {
    const seriesKey = getTunnelSeriesKey(tunnel);
    knownTunnels.set(seriesKey, {
      key: seriesKey,
      name: tunnel.name,
      type: tunnel.type,
    });
  }

  for (const item of data?.items ?? []) {
    const seriesKey = getTrafficSeriesKey(item);
    if (!knownTunnels.has(seriesKey)) {
      knownTunnels.set(seriesKey, {
        key: seriesKey,
        name: getTrafficSeriesName(item, t),
        type: item.tunnel_type ?? 'tcp',
      });
    }
  }

  const tunnelSeries: TunnelMeta[] = Array.from(knownTunnels.values())
    .sort((left, right) => {
      if (left.name !== right.name) {
        return left.name.localeCompare(right.name);
      }
      return left.type.localeCompare(right.type);
    })
    .map((tunnel, index) => ({
      key: tunnel.key,
      gradId: `tun-grad-${gradPrefix}-${index}`,
      name: tunnel.name,
      type: tunnel.type,
      color: getTunnelColor(index),
    }));

  const chartConfig = tunnelSeries.reduce<ChartConfig>((config, tunnel) => {
    config[tunnel.key] = {
      label: `${tunnel.name} · ${tunnel.type.toUpperCase()}`,
      color: tunnel.color,
    };
    return config;
  }, {});

  const { bucketSeconds = 1, gridBucketMs = 1_000 } = RANGE_CHART_CONFIG[range] ?? {};
  const pointsByTunnel = new Map<string, Map<number, number>>();
  const timestamps = new Set<number>(buildGridTimestamps(range));
  let totalBytes = 0;

  for (const item of data?.items ?? []) {
    const seriesKey = getTrafficSeriesKey(item);
    let pointMap = pointsByTunnel.get(seriesKey);
    if (!pointMap) {
      pointMap = new Map<number, number>();
      pointsByTunnel.set(seriesKey, pointMap);
    }

    for (const point of item.points) {
      const rawTimestamp = new Date(point.bucket_start).getTime();
      const bucketTimestamp = Math.floor(rawTimestamp / gridBucketMs) * gridBucketMs;
      pointMap.set(bucketTimestamp, (pointMap.get(bucketTimestamp) ?? 0) + point.total_bytes);
      totalBytes += point.total_bytes;
      timestamps.add(bucketTimestamp);
    }
  }

  let peakRate = 0;
  const chartData = Array.from(timestamps)
    .sort((a, b) => a - b)
    .map<ChartRow>((timestamp) => {
      const row: ChartRow = { timestamp };
      for (const tunnel of tunnelSeries) {
        const bytes = pointsByTunnel.get(tunnel.key)?.get(timestamp) ?? 0;
        const rate = bytes / bucketSeconds;
        if (rate > peakRate) peakRate = rate;
        row[tunnel.key] = rate;
      }
      return row;
    });

  return {
    chartConfig,
    chartData,
    tunnelSeries,
    peakRate,
    totalBytes,
  };
}

export function TrafficChart({ scope, clientId, tunnels }: TrafficChartProps) {
  const { t, i18n } = useTranslation();
  const gradPrefix = useId().replace(/:/g, '');
  const [range, setRange] = useState<ClientTrafficRange>('60s');
  const { data, isLoading, isError, error } = useClientTraffic(scope, clientId, range);

  const { chartConfig, chartData, tunnelSeries, peakRate, totalBytes } = useMemo(
    () => buildTrafficTrendChartState(data, tunnels, range, gradPrefix, t),
    [data, gradPrefix, tunnels, range, t],
  );

  const yAxisTicks = useMemo(() => computeTrafficAxisTicks(peakRate), [peakRate]);
  const hasTunnels = tunnelSeries.length > 0;

  return (
    <div className="overflow-hidden rounded-xl border border-border/60 bg-card shadow-2xs">
      <div className="flex flex-wrap items-center justify-between gap-3 border-b border-border/50 px-5 py-3.5">
        <div className="flex flex-wrap items-center gap-2.5">
          <span className="flex size-7 shrink-0 items-center justify-center rounded-lg border border-border/70 bg-muted/60 text-foreground">
            <Activity className="size-4" />
          </span>
          <div>
            <div className="flex items-center gap-2">
              <h3 className="text-sm font-semibold text-foreground">{t('traffic.trendTitle')}</h3>
              <span className="rounded-full border border-border/60 bg-muted/45 px-2 py-0.5 font-mono text-[10.5px] text-muted-foreground">
                {t('traffic.tunnelCount', { count: tunnelSeries.length })}
              </span>
            </div>
            <p className="mt-0.5 text-xs text-muted-foreground">{getRangeSummary(range, t)}</p>
          </div>
        </div>

        <div className="flex flex-wrap items-center gap-3">
          {hasTunnels ? (
            <div className="hidden items-center gap-3 font-mono text-xs sm:flex">
              <span className="rounded-md border border-border/60 bg-muted/30 px-2.5 py-1 text-muted-foreground">
                {t('dashboard.realtimePeakShort', { value: formatTrafficRate(peakRate) })}
              </span>
              <span className="rounded-md border border-border/60 bg-muted/30 px-2.5 py-1 text-muted-foreground">
                {t('dashboard.realtimeTotalShort', { value: formatBytes(totalBytes) })}
              </span>
            </div>
          ) : null}

          <div className="inline-flex items-center rounded-lg border border-border/65 bg-muted/50 p-0.5">
            {RANGE_OPTIONS.map((option) => {
              const active = option.value === range;
              return (
                <button
                  key={option.value}
                  type="button"
                  aria-pressed={active}
                  onClick={() => setRange(option.value)}
                  className={cn(
                    'rounded-md px-2.5 py-1 font-mono text-xs font-medium transition-all',
                    active
                      ? 'bg-background text-foreground shadow-2xs'
                      : 'text-muted-foreground hover:text-foreground',
                  )}
                >
                  {option.label}
                </button>
              );
            })}
          </div>
        </div>
      </div>

      <div className="p-4 sm:p-5">
        {!hasTunnels ? (
          <div className="flex h-64 flex-col items-center justify-center rounded-lg border border-dashed border-border/60 bg-muted/15 text-center">
            <p className="text-sm font-medium text-foreground">{t('traffic.noClientTunnels')}</p>
            <p className="mt-1 text-xs text-muted-foreground">{t('traffic.noClientTunnelsHelp')}</p>
          </div>
        ) : isLoading ? (
          <div className="h-72 animate-pulse rounded-lg border border-border/60 bg-muted/20" />
        ) : isError ? (
          <div className="flex h-64 flex-col items-center justify-center rounded-lg border border-dashed border-destructive/30 bg-destructive/5 text-center">
            <AlertCircle className="mb-3 h-5 w-5 text-destructive" />
            <p className="text-sm font-medium text-foreground">{t('traffic.loadFailed')}</p>
            <p className="mt-1 max-w-md text-xs text-muted-foreground">
              {getErrorMessage(error, t)}
            </p>
          </div>
        ) : (
          <div className="h-72 w-full">
            <ChartContainer config={chartConfig} className="aspect-auto h-full w-full">
              <AreaChart data={chartData} margin={{ top: 10, right: 14, left: 2, bottom: 4 }}>
                <defs>
                  {tunnelSeries.map((tunnel) => (
                    <linearGradient key={tunnel.gradId} id={tunnel.gradId} x1="0" y1="0" x2="0" y2="1">
                      <stop offset="0%" stopColor={tunnel.color} stopOpacity={0.22} />
                      <stop offset="85%" stopColor={tunnel.color} stopOpacity={0.02} />
                      <stop offset="100%" stopColor={tunnel.color} stopOpacity={0.0} />
                    </linearGradient>
                  ))}
                </defs>
                <CartesianGrid
                  vertical={false}
                  stroke="var(--border)"
                  strokeOpacity={0.55}
                />
                <XAxis
                  dataKey="timestamp"
                  axisLine={false}
                  tickLine={false}
                  tickMargin={8}
                  minTickGap={34}
                  tick={{ fontSize: 10.5 }}
                  tickFormatter={(value) => formatXAxisLabel(Number(value), range, i18n.language)}
                />
                <YAxis
                  axisLine={false}
                  tickLine={false}
                  tickMargin={8}
                  width={68}
                  tick={{ fontSize: 10.5 }}
                  domain={[0, yAxisTicks[3]]}
                  ticks={yAxisTicks}
                  tickFormatter={(value) => formatTrafficRate(Number(value))}
                />
                <ChartTooltip
                  cursor={{
                    stroke: 'var(--foreground)',
                    strokeOpacity: 0.22,
                    strokeWidth: 1,
                    strokeDasharray: '3 3',
                  }}
                  content={(
                    <ChartTooltipContent
                      indicator="dot"
                      labelFormatter={(_, payload) => {
                        const timestamp = payload?.[0]?.payload?.timestamp;
                        return typeof timestamp === 'number'
                          ? formatTooltipLabel(timestamp, range, i18n.language)
                          : '';
                      }}
                      formatter={(value, name) => (
                        <>
                          <span className="text-muted-foreground">
                            {chartConfig[String(name)]?.label ?? String(name)}
                          </span>
                          <span className="font-mono font-medium text-foreground tabular-nums">
                            {formatTrafficRate(Number(value))}
                          </span>
                        </>
                      )}
                    />
                  )}
                />
                {tunnelSeries.map((tunnel) => (
                  <Area
                    key={tunnel.key}
                    type="monotone"
                    dataKey={tunnel.key}
                    name={tunnel.key}
                    stroke={tunnel.color}
                    fill={`url(#${tunnel.gradId})`}
                    strokeWidth={1.85}
                    dot={false}
                    activeDot={{ r: 3.5 }}
                    isAnimationActive={false}
                    connectNulls
                  />
                ))}
                <ChartLegend content={<ChartLegendContent className="flex-wrap gap-x-4 gap-y-1 pt-2" />} />
              </AreaChart>
            </ChartContainer>
          </div>
        )}
      </div>
    </div>
  );
}
