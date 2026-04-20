import { Line, LineChart, CartesianGrid, XAxis, YAxis } from 'recharts';
import { AlertCircle, Activity } from 'lucide-react';

import {
  type ChartConfig,
  ChartContainer,
  ChartTooltip,
  ChartTooltipContent,
} from '@/components/ui/chart';
import { useClientTraffic } from '@/hooks/use-client-traffic';
import { hasTrafficSamples, useAggregatedTrafficRates } from '@/hooks/use-traffic-rates';
import { formatBytes } from '@/lib/format';
import type { ProxyConfig } from '@/types';

interface TrafficRateChartProps {
  clientId: string;
  tunnelFilter?: Pick<ProxyConfig, 'name' | 'type'>[];
}

const chartConfig: ChartConfig = {
  inRate: { label: '下行', color: 'var(--chart-2)' },
  outRate: { label: '上行', color: 'var(--chart-1)' },
};

function formatTrafficValue(value: number) {
  return formatBytes(value).replace('.0 ', ' ');
}

function formatXAxisLabel(timestamp: number) {
  const date = new Date(timestamp);
  return date.toLocaleString('zh-CN', { hour: '2-digit', minute: '2-digit' });
}

function formatTooltipLabel(timestamp: number) {
  const date = new Date(timestamp);
  return date.toLocaleString('zh-CN', { month: 'numeric', day: 'numeric', hour: '2-digit', minute: '2-digit' });
}

function getErrorMessage(error: unknown) {
  if (error instanceof Error && error.message) {
    return error.message;
  }
  return '流量数据加载失败';
}

function getQueryTunnel(tunnelFilter: Pick<ProxyConfig, 'name' | 'type'>[] | undefined) {
  return tunnelFilter?.length === 1 ? tunnelFilter[0].name : undefined;
}

export function TrafficRateChart({ clientId, tunnelFilter }: TrafficRateChartProps) {
  const { data, isLoading, isError, error, isFetching } = useClientTraffic(clientId, '24h', {
    tunnel: getQueryTunnel(tunnelFilter),
  });
  const chartData = useAggregatedTrafficRates(data, '24h', tunnelFilter);
  const hasSamples = hasTrafficSamples(data, tunnelFilter);

  const tunnelLabel = tunnelFilter && tunnelFilter.length > 0
    ? ` · ${tunnelFilter.length} 条隧道`
    : '';

  return (
    <div className="rounded-xl border border-border/40 bg-card/30 p-6 shadow-sm backdrop-blur-sm">
      <div className="mb-5 flex flex-col gap-4 lg:flex-row lg:items-start lg:justify-between">
        <div className="space-y-1">
          <div className="flex items-center gap-2 text-lg font-semibold text-foreground">
            <Activity className="h-5 w-5 text-primary" />
            <h3>24 小时网络速率</h3>
          </div>
          <p className="text-sm text-muted-foreground">
            最近 24 小时 · 按分钟聚合速率{tunnelLabel}
            {isFetching && !isLoading ? ' · 刷新中…' : ''}
          </p>
        </div>
      </div>

      {isLoading ? (
        <div className="h-72 animate-pulse rounded-xl border border-border/60 bg-background/30" />
      ) : isError ? (
        <div className="flex h-72 flex-col items-center justify-center rounded-xl border border-dashed border-destructive/30 bg-destructive/5 text-center">
          <AlertCircle className="mb-3 h-5 w-5 text-destructive" />
          <p className="text-sm font-medium text-foreground">流量数据加载失败</p>
          <p className="mt-1 max-w-md text-sm text-muted-foreground">{getErrorMessage(error)}</p>
        </div>
      ) : !hasSamples ? (
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
                tickFormatter={(value) => formatXAxisLabel(Number(value))}
              />
              <YAxis
                axisLine={false}
                tickLine={false}
                tickMargin={10}
                width={80}
                tickFormatter={(value) => formatTrafficValue(Number(value)) + '/s'}
              />
              <ChartTooltip
                content={(
                  <ChartTooltipContent
                    indicator="line"
                    labelFormatter={(_, payload) => {
                      const timestamp = payload?.[0]?.payload?.timestamp;
                      return typeof timestamp === 'number'
                        ? formatTooltipLabel(timestamp)
                        : '';
                    }}
                    formatter={(value, name) => (
                      <>
                        <span className="text-muted-foreground">{chartConfig[String(name)]?.label ?? String(name)}</span>
                        <span className="font-mono font-medium text-foreground tabular-nums">
                          {formatTrafficValue(Number(value))}/s
                        </span>
                      </>
                    )}
                  />
                )}
              />
              <Line
                type="monotone"
                dataKey="outRate"
                stroke="var(--color-outRate)"
                strokeWidth={2}
                dot={false}
                activeDot={{ r: 4 }}
                connectNulls
              />
              <Line
                type="monotone"
                dataKey="inRate"
                stroke="var(--color-inRate)"
                strokeWidth={2}
                dot={false}
                activeDot={{ r: 4 }}
                connectNulls
              />
            </LineChart>
          </ChartContainer>
        </div>
      )}
    </div>
  );
}
