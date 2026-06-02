import { useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Line, LineChart, CartesianGrid, XAxis, YAxis } from 'recharts';
import { AlertCircle, Activity } from 'lucide-react';

import { Button } from '@/components/ui/button';
import {
  type ChartConfig,
  ChartContainer,
  ChartTooltip,
  ChartTooltipContent,
} from '@/components/ui/chart';
import { useClientTraffic } from '@/hooks/use-client-traffic';
import { formatBytes } from '@/lib/format';
import { getTrafficSeriesKey, getTunnelSeriesKey } from '@/lib/tunnel-traffic-keys';
import type { ClientTrafficRange, ClientTrafficResponse, ProxyConfig, ProxyType } from '@/types';

interface TrafficChartProps {
  clientId: string;
  tunnels: ProxyConfig[];
}

type TunnelMeta = {
  key: string;
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

const CHART_COLORS = [
  'var(--chart-1)',
  'var(--chart-2)',
  'var(--chart-3)',
  'var(--chart-4)',
  'var(--chart-5)',
] as const;

const ZERO_FILLED_RANGE_CONFIG: Partial<Record<ClientTrafficRange, { pointCount: number; bucketMs: number }>> = {
  '24h': { pointCount: 24 * 60, bucketMs: 60_000 },
  '7d': { pointCount: 7 * 24, bucketMs: 3_600_000 },
};

function getTunnelColor(index: number) {
  return CHART_COLORS[index] ?? `hsl(${(index * 67) % 360} 72% 58%)`;
}

function getTrafficSeriesName(item: ClientTrafficResponse['items'][number], t: ReturnType<typeof useTranslation>['t']) {
  return item.tunnel_name ?? t('traffic.deletedTunnel', { id: item.tunnel_id ?? 'unknown' });
}

function formatTrafficValue(value: number, range?: ClientTrafficRange) {
  const formatted = formatBytes(value).replace('.0 ', ' ');
  return range === '60s' ? `${formatted}/s` : formatted;
}

function formatXAxisLabel(timestamp: number, range: ClientTrafficRange, language: string) {
  const date = new Date(timestamp);
  if (range === '60s') {
    return date.toLocaleString(language, { minute: '2-digit', second: '2-digit' });
  }
  return date.toLocaleString(language, range === '24h'
    ? { hour: '2-digit', minute: '2-digit' }
    : { month: 'numeric', day: 'numeric', hour: '2-digit' });
}

function formatTooltipLabel(timestamp: number, range: ClientTrafficRange, language: string) {
  const date = new Date(timestamp);
  if (range === '60s') {
    return date.toLocaleString(language, { hour: '2-digit', minute: '2-digit', second: '2-digit' });
  }
  return date.toLocaleString(language, range === '24h'
    ? { month: 'numeric', day: 'numeric', hour: '2-digit', minute: '2-digit' }
    : { year: 'numeric', month: 'numeric', day: 'numeric', hour: '2-digit' });
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

function buildZeroFilledTimestamps(range: ClientTrafficRange, nowMs = Date.now()) {
  const config = ZERO_FILLED_RANGE_CONFIG[range];
  if (!config) {
    return [];
  }

  const endTimestamp = Math.floor(nowMs / config.bucketMs) * config.bucketMs;
  return Array.from({ length: config.pointCount }, (_, index) => (
    endTimestamp - (config.pointCount - index - 1) * config.bucketMs
  ));
}

function buildTrafficTrendChartState(
  data: ClientTrafficResponse | undefined,
  tunnels: (Pick<ProxyConfig, 'name' | 'type'> & Partial<Pick<ProxyConfig, 'id'>>)[],
  range: ClientTrafficRange,
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

  const pointsByTunnel = new Map<string, Map<number, number>>();
  const timestamps = new Set<number>(buildZeroFilledTimestamps(range));

  for (const item of data?.items ?? []) {
    const pointMap = new Map<number, number>();

    for (const point of item.points) {
      const timestamp = new Date(point.bucket_start).getTime();
      pointMap.set(timestamp, point.total_bytes);
      timestamps.add(timestamp);
    }

    const seriesKey = getTrafficSeriesKey(item);
    pointsByTunnel.set(seriesKey, pointMap);
  }

  const chartData = Array.from(timestamps)
    .sort((a, b) => a - b)
    .map<ChartRow>((timestamp) => {
      const row: ChartRow = { timestamp };
      for (const tunnel of tunnelSeries) {
        row[tunnel.key] = pointsByTunnel.get(tunnel.key)?.get(timestamp) ?? 0;
      }
      return row;
    });

  return {
    chartConfig,
    chartData,
    tunnelSeries,
  };
}

export function TrafficChart({ clientId, tunnels }: TrafficChartProps) {
  const { t, i18n } = useTranslation();
  const [range, setRange] = useState<ClientTrafficRange>('60s');
  const { data, isLoading, isError, error } = useClientTraffic(clientId, range);

  const { chartConfig, chartData, tunnelSeries } = useMemo(
    () => buildTrafficTrendChartState(data, tunnels, range, t),
    [data, tunnels, range, t],
  );

  const hasTunnels = tunnelSeries.length > 0;
  const hasTrafficData = chartData.length > 0;

  return (
    <div className="rounded-xl border border-border/40 bg-card/30 p-6 shadow-sm backdrop-blur-sm">
      <div className="mb-5 flex flex-col gap-4 lg:flex-row lg:items-start lg:justify-between">
        <div className="space-y-1">
          <div className="flex items-center gap-2 text-lg font-semibold text-foreground">
            <Activity className="h-5 w-5 text-primary" />
            <h3>{t('traffic.trendTitle')}</h3>
          </div>
          <p className="text-sm text-muted-foreground">
            {getRangeSummary(range, t)} · {t('traffic.tunnelCount', { count: tunnelSeries.length })}
          </p>
        </div>

        <div className="flex items-center gap-2">
          {RANGE_OPTIONS.map((option) => {
            const active = option.value === range;
            return (
              <Button
                key={option.value}
                type="button"
                size="sm"
                variant={active ? 'default' : 'outline'}
                onClick={() => setRange(option.value)}
              >
                {option.label}
              </Button>
            );
          })}
        </div>
      </div>

      {!hasTunnels ? (
        <div className="flex h-72 flex-col items-center justify-center rounded-xl border border-dashed border-border/60 bg-background/30 text-center">
          <p className="text-sm font-medium text-foreground">{t('traffic.noClientTunnels')}</p>
          <p className="mt-1 text-sm text-muted-foreground">{t('traffic.noClientTunnelsHelp')}</p>
        </div>
      ) : isLoading ? (
        <div className="h-72 animate-pulse rounded-xl border border-border/60 bg-background/30" />
      ) : isError ? (
        <div className="flex h-72 flex-col items-center justify-center rounded-xl border border-dashed border-destructive/30 bg-destructive/5 text-center">
          <AlertCircle className="mb-3 h-5 w-5 text-destructive" />
          <p className="text-sm font-medium text-foreground">{t('traffic.loadFailed')}</p>
          <p className="mt-1 max-w-md text-sm text-muted-foreground">{getErrorMessage(error, t)}</p>
        </div>
      ) : !hasTrafficData ? (
        <div className="flex h-72 flex-col items-center justify-center rounded-xl border border-dashed border-border/60 bg-background/30 text-center">
          <p className="text-sm font-medium text-foreground">{t('traffic.emptyRange')}</p>
        </div>
      ) : (
        <div className="h-80 w-full">
          <ChartContainer config={chartConfig} className="h-full w-full">
            <LineChart data={chartData} margin={{ top: 12, right: 12, left: 0, bottom: 4 }}>
              <CartesianGrid vertical={false} stroke="var(--border)" strokeDasharray="3 3" strokeOpacity={0.45} />
              <XAxis
                dataKey="timestamp"
                axisLine={false}
                tickLine={false}
                tickMargin={12}
                minTickGap={28}
                tickFormatter={(value) => formatXAxisLabel(Number(value), range, i18n.language)}
              />
              <YAxis
                axisLine={false}
                tickLine={false}
                tickMargin={10}
                width="auto"
                tickFormatter={(value) => formatTrafficValue(Number(value), range)}
              />
              <ChartTooltip
                content={(
                  <ChartTooltipContent
                    indicator="line"
                    labelFormatter={(_, payload) => {
                      const timestamp = payload?.[0]?.payload?.timestamp;
                      return typeof timestamp === 'number'
                        ? formatTooltipLabel(timestamp, range, i18n.language)
                        : '';
                    }}
                    formatter={(value, name) => (
                      <>
                        <span className="text-muted-foreground">{chartConfig[String(name)]?.label ?? String(name)}</span>
                        <span className="font-mono font-medium text-foreground tabular-nums">
                          {formatTrafficValue(Number(value), range)}
                        </span>
                      </>
                    )}
                  />
                )}
              />
              {tunnelSeries.map((tunnel) => (
                <Line
                  key={tunnel.key}
                  type="monotone"
                  dataKey={tunnel.key}
                  name={tunnel.key}
                  stroke={tunnel.color}
                  strokeWidth={2}
                  dot={false}
                  activeDot={{ r: 4 }}
                  isAnimationActive={false}
                  connectNulls
                />
              ))}
            </LineChart>
          </ChartContainer>
        </div>
      )}
    </div>
  );
}
