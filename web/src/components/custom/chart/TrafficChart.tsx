import { useMemo, useState } from 'react';
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

function getTunnelColor(index: number) {
  return CHART_COLORS[index] ?? `hsl(${(index * 67) % 360} 72% 58%)`;
}

function getTunnelSeriesKey(name: string, type: ProxyType) {
  return `${type}:${name}`;
}

function formatTrafficValue(value: number) {
  return formatBytes(value).replace('.0 ', ' ');
}

function formatXAxisLabel(timestamp: number, range: ClientTrafficRange) {
  const date = new Date(timestamp);
  return date.toLocaleString('zh-CN', range === '24h'
    ? { hour: '2-digit', minute: '2-digit' }
    : { month: 'numeric', day: 'numeric', hour: '2-digit' });
}

function formatTooltipLabel(timestamp: number, range: ClientTrafficRange) {
  const date = new Date(timestamp);
  return date.toLocaleString('zh-CN', range === '24h'
    ? { month: 'numeric', day: 'numeric', hour: '2-digit', minute: '2-digit' }
    : { year: 'numeric', month: 'numeric', day: 'numeric', hour: '2-digit' });
}

function getRangeSummary(range: ClientTrafficRange) {
  if (range === '24h') {
    return '最近 24 小时 · 按分钟聚合 · 自动刷新';
  }
  return '最近 7 天 · 按小时聚合 · 自动刷新';
}

function getErrorMessage(error: unknown) {
  if (error instanceof Error && error.message) {
    return error.message;
  }
  return '流量数据加载失败';
}

function buildTrafficTrendChartState(
  data: ClientTrafficResponse | undefined,
  tunnels: Pick<ProxyConfig, 'name' | 'type'>[],
) {
  const knownTunnels = new Map<string, Pick<TunnelMeta, 'name' | 'type'>>();

  for (const tunnel of tunnels) {
    knownTunnels.set(getTunnelSeriesKey(tunnel.name, tunnel.type), {
      name: tunnel.name,
      type: tunnel.type,
    });
  }

  for (const item of data?.items ?? []) {
    const seriesKey = getTunnelSeriesKey(item.tunnel_name, item.tunnel_type);
    if (!knownTunnels.has(seriesKey)) {
      knownTunnels.set(seriesKey, {
        name: item.tunnel_name,
        type: item.tunnel_type,
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
      key: getTunnelSeriesKey(tunnel.name, tunnel.type),
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
  const timestamps = new Set<number>();

  for (const item of data?.items ?? []) {
    const pointMap = new Map<number, number>();

    for (const point of item.points) {
      const timestamp = new Date(point.bucket_start).getTime();
      pointMap.set(timestamp, point.total_bytes);
      timestamps.add(timestamp);
    }

    const seriesKey = getTunnelSeriesKey(item.tunnel_name, item.tunnel_type);
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
  const [range, setRange] = useState<ClientTrafficRange>('24h');
  const { data, isLoading, isError, error, isFetching } = useClientTraffic(clientId, range);

  const { chartConfig, chartData, tunnelSeries } = useMemo(
    () => buildTrafficTrendChartState(data, tunnels),
    [data, tunnels],
  );

  const hasTunnels = tunnelSeries.length > 0;
  const hasTrafficData = chartData.length > 0;

  return (
    <div className="rounded-xl border border-border/40 bg-card/30 p-6 shadow-sm backdrop-blur-sm">
      <div className="mb-5 flex flex-col gap-4 lg:flex-row lg:items-start lg:justify-between">
        <div className="space-y-1">
          <div className="flex items-center gap-2 text-lg font-semibold text-foreground">
            <Activity className="h-5 w-5 text-primary" />
            <h3>隧道流量趋势</h3>
          </div>
          <p className="text-sm text-muted-foreground">
            {getRangeSummary(range)} · {tunnelSeries.length} 条隧道
            {isFetching && !isLoading ? ' · 刷新中…' : ''}
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
          <p className="text-sm font-medium text-foreground">当前 Client 暂无隧道</p>
          <p className="mt-1 text-sm text-muted-foreground">创建隧道后，这里会自动展示每条隧道的流量趋势。</p>
        </div>
      ) : isLoading ? (
        <div className="h-72 animate-pulse rounded-xl border border-border/60 bg-background/30" />
      ) : isError ? (
        <div className="flex h-72 flex-col items-center justify-center rounded-xl border border-dashed border-destructive/30 bg-destructive/5 text-center">
          <AlertCircle className="mb-3 h-5 w-5 text-destructive" />
          <p className="text-sm font-medium text-foreground">流量数据加载失败</p>
          <p className="mt-1 max-w-md text-sm text-muted-foreground">{getErrorMessage(error)}</p>
        </div>
      ) : !hasTrafficData ? (
        <div className="flex h-72 flex-col items-center justify-center rounded-xl border border-dashed border-border/60 bg-background/30 text-center">
          <p className="text-sm font-medium text-foreground">该时间范围内暂无流量数据</p>
        </div>
      ) : (
        <div className="h-80 w-full">
          <ChartContainer config={chartConfig} className="h-full w-full">
            <LineChart data={chartData} margin={{ top: 12, right: 12, left: 20, bottom: 4 }}>
              <CartesianGrid vertical={false} stroke="var(--border)" strokeDasharray="3 3" strokeOpacity={0.45} />
              <XAxis
                dataKey="timestamp"
                axisLine={false}
                tickLine={false}
                tickMargin={12}
                minTickGap={28}
                tickFormatter={(value) => formatXAxisLabel(Number(value), range)}
              />
              <YAxis
                axisLine={false}
                tickLine={false}
                tickMargin={10}
                width={96}
                tickFormatter={(value) => formatTrafficValue(Number(value))}
              />
              <ChartTooltip
                content={(
                  <ChartTooltipContent
                    indicator="line"
                    labelFormatter={(_, payload) => {
                      const timestamp = payload?.[0]?.payload?.timestamp;
                      return typeof timestamp === 'number'
                        ? formatTooltipLabel(timestamp, range)
                        : '';
                    }}
                    formatter={(value, name) => (
                      <>
                        <span className="text-muted-foreground">{chartConfig[String(name)]?.label ?? String(name)}</span>
                        <span className="font-mono font-medium text-foreground tabular-nums">
                          {formatTrafficValue(Number(value))}
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
