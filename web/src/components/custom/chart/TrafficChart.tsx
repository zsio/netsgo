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
import type { ClientTrafficRange, ProxyConfig, ProxyType } from '@/types';

interface TrafficChartProps {
  clientId: string;
  tunnels: ProxyConfig[];
}

type TunnelMeta = {
  key: string;
  name: string;
  type: ProxyType;
  color: string;
  totalBytes: number;
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

export function TrafficChart({ clientId, tunnels }: TrafficChartProps) {
  const [range, setRange] = useState<ClientTrafficRange>('24h');
  const { data, isLoading, isError, error, isFetching } = useClientTraffic(clientId, range);

  const { chartConfig, chartData, tunnelSeries } = useMemo(() => {
    const knownTunnels = tunnels.map((tunnel) => ({
      name: tunnel.name,
      type: tunnel.type,
    }));

    for (const item of data?.items ?? []) {
      if (!knownTunnels.some((tunnel) => tunnel.name === item.tunnel_name)) {
        knownTunnels.push({
          name: item.tunnel_name,
          type: item.tunnel_type,
        });
      }
    }

    const tunnelSeries: TunnelMeta[] = knownTunnels.map((tunnel, index) => ({
      key: `tunnel_${index}`,
      name: tunnel.name,
      type: tunnel.type,
      color: getTunnelColor(index),
      totalBytes: 0,
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
      let totalBytes = 0;

      for (const point of item.points) {
        const timestamp = new Date(point.bucket_start).getTime();
        pointMap.set(timestamp, point.total_bytes);
        timestamps.add(timestamp);
        totalBytes += point.total_bytes;
      }

      pointsByTunnel.set(item.tunnel_name, pointMap);
      const target = tunnelSeries.find((tunnel) => tunnel.name === item.tunnel_name);
      if (target) {
        target.totalBytes = totalBytes;
      }
    }

    const chartData = Array.from(timestamps)
      .sort((a, b) => a - b)
      .map<ChartRow>((timestamp) => {
        const row: ChartRow = { timestamp };
        for (const tunnel of tunnelSeries) {
          row[tunnel.key] = pointsByTunnel.get(tunnel.name)?.get(timestamp) ?? 0;
        }
        return row;
      });

    return {
      chartConfig,
      chartData,
      tunnelSeries,
    };
  }, [data?.items, tunnels]);

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

      {hasTunnels ? (
        <div className="mb-5 flex flex-wrap gap-2">
          {tunnelSeries.map((tunnel) => (
            <div
              key={tunnel.key}
              className="flex min-w-[180px] items-center gap-2 rounded-lg border border-border/60 bg-background/70 px-3 py-2"
            >
              <span
                className="h-2.5 w-2.5 shrink-0 rounded-full"
                style={{ backgroundColor: tunnel.color }}
              />
              <div className="min-w-0 flex-1">
                <div className="truncate text-sm font-medium text-foreground">{tunnel.name}</div>
                <div className="text-xs text-muted-foreground">
                  {tunnel.type.toUpperCase()} · {formatTrafficValue(tunnel.totalBytes)}
                </div>
              </div>
            </div>
          ))}
        </div>
      ) : null}

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
          <p className="mt-1 text-sm text-muted-foreground">切换到另一个时间范围，或等待下一次自动刷新。</p>
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
