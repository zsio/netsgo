import { useId, useMemo } from 'react';
import { useTranslation } from 'react-i18next';
import {
  Area,
  AreaChart,
  CartesianGrid,
  ReferenceLine,
  XAxis,
  YAxis,
} from 'recharts';
import { AlertCircle } from 'lucide-react';

import { computeSymmetricTrafficTicks } from '@/components/custom/dashboard/dashboard-traffic-summary';
import {
  type ChartConfig,
  ChartContainer,
  ChartTooltip,
  ChartTooltipContent,
} from '@/components/ui/chart';
import { useClientTraffic } from '@/hooks/use-client-traffic';
import { useAggregatedTrafficRates } from '@/hooks/use-traffic-rates';
import { formatBytes, formatTrafficRate } from '@/lib/format';
import type { ResourceScope } from '@/lib/resource-scope';
import type { ProxyConfig } from '@/types';

interface TrafficRateChartProps {
  scope: ResourceScope;
  clientId: string;
  tunnelFilter?: Pick<ProxyConfig, 'name' | 'type'>[];
}

function formatXAxisLabel(timestamp: number, language: string) {
  const date = new Date(timestamp);
  return date.toLocaleString(language, { hour: '2-digit', minute: '2-digit', hour12: false });
}

function formatTooltipLabel(timestamp: number, language: string) {
  const date = new Date(timestamp);
  return date.toLocaleString(language, {
    month: 'numeric',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
    hour12: false,
  });
}

function getErrorMessage(error: unknown, fallback: string) {
  if (error instanceof Error && error.message) {
    return error.message;
  }
  return fallback;
}

function getQueryTunnel(tunnelFilter: Pick<ProxyConfig, 'name' | 'type'>[] | undefined) {
  return tunnelFilter?.length === 1 ? tunnelFilter[0].name : undefined;
}

function buildRateStats(rawRates: ReadonlyArray<{ timestamp: number; inRate: number; outRate: number }>) {
  let peakIn = 0;
  let peakOut = 0;
  let sumInRate = 0;
  let sumOutRate = 0;
  const series = [];
  for (const pt of rawRates) {
    if (pt.inRate > peakIn) peakIn = pt.inRate;
    if (pt.outRate > peakOut) peakOut = pt.outRate;
    sumInRate += pt.inRate;
    sumOutRate += pt.outRate;
    series.push({
      timestamp: pt.timestamp,
      inRate: pt.inRate,
      outRate: pt.outRate,
      negInRate: pt.inRate === 0 ? 0 : -pt.inRate,
    });
  }
  const bucketSeconds = 300;
  const totalInBytes = sumInRate * bucketSeconds;
  const totalOutBytes = sumOutRate * bucketSeconds;
  const avgIn = rawRates.length > 0 ? sumInRate / rawRates.length : 0;
  const avgOut = rawRates.length > 0 ? sumOutRate / rawRates.length : 0;
  return { series, peakIn, peakOut, totalInBytes, totalOutBytes, avgIn, avgOut };
}

export function TrafficRateChart({ scope, clientId, tunnelFilter }: TrafficRateChartProps) {
  const { t, i18n } = useTranslation();
  const gradientPrefix = useId().replace(/:/g, '');
  const chartConfig = useMemo<ChartConfig>(
    () => ({
      outRate: { label: t('traffic.outbound'), color: 'var(--flow-up)' },
      negInRate: { label: t('traffic.inbound'), color: 'var(--flow-down)' },
    }),
    [t],
  );
  const { data, isLoading, isError, error } = useClientTraffic(scope, clientId, '24h', {
    tunnel: getQueryTunnel(tunnelFilter),
  });
  const rawRates = useAggregatedTrafficRates(data, '24h', tunnelFilter);
  const stats = useMemo(() => buildRateStats(rawRates), [rawRates]);
  const maxPeak = Math.max(stats.peakIn, stats.peakOut);
  const yAxisTicks = useMemo(() => computeSymmetricTrafficTicks(maxPeak), [maxPeak]);

  if (isLoading) {
    return <div className="h-80 animate-pulse rounded-xl border border-border/60 bg-background/30" />;
  }

  if (isError) {
    return (
      <div className="flex h-80 flex-col items-center justify-center rounded-xl border border-dashed border-destructive/30 bg-destructive/5 text-center">
        <AlertCircle className="mb-3 h-5 w-5 text-destructive" />
        <p className="text-sm font-medium text-foreground">{t('traffic.loadFailed')}</p>
        <p className="mt-1 max-w-md text-sm text-muted-foreground">
          {getErrorMessage(error, t('traffic.loadFailed'))}
        </p>
      </div>
    );
  }

  const inGradId = `rate-in-${gradientPrefix}`;
  const outGradId = `rate-out-${gradientPrefix}`;
  const hasAnyTraffic = maxPeak > 0;

  return (
    <div className="flex min-w-0 flex-col gap-3">
      {/* 单行紧凑内联指标条 */}
      <div className="flex flex-wrap items-center justify-between gap-x-4 gap-y-1 px-1 text-xs">
        <div className="flex items-center gap-4">
          <span className="inline-flex items-center gap-1.5">
            <span className="size-2 rounded-full bg-flow-up" />
            <span className="text-muted-foreground">↑ {t('dashboard.realtimeAvgOutbound')}</span>
            <span className="font-mono font-semibold tabular-nums text-foreground">
              {formatTrafficRate(stats.avgOut)}
            </span>
          </span>
          <span className="inline-flex items-center gap-1.5">
            <span className="size-2 rounded-full bg-flow-down" />
            <span className="text-muted-foreground">↓ {t('dashboard.realtimeAvgInbound')}</span>
            <span className="font-mono font-semibold tabular-nums text-foreground">
              {formatTrafficRate(stats.avgIn)}
            </span>
          </span>
        </div>

        <div className="flex items-center gap-2 font-mono text-[11px] tabular-nums text-muted-foreground">
          <span>{t('dashboard.realtimePeakShort', { value: formatTrafficRate(maxPeak) })}</span>
          <span className="text-border">·</span>
          <span>
            {t('dashboard.realtimeTotalShort', {
              value: formatBytes(stats.totalInBytes + stats.totalOutBytes),
            })}
          </span>
        </div>
      </div>

      {/* 上下行 Y 轴镜像对称波形（+Y 上行暖橙向上，-Y 下行青绿向下） */}
      <div className="relative h-72 min-w-0 w-full">
        <span
          aria-hidden
          className="pointer-events-none absolute top-1.5 right-3 z-10 font-mono text-[10px] font-medium text-flow-up-fg/85"
        >
          ↑ {t('traffic.outbound')}
        </span>
        <span
          aria-hidden
          className="pointer-events-none absolute right-3 bottom-8 z-10 font-mono text-[10px] font-medium text-flow-down-fg/85"
        >
          ↓ {t('traffic.inbound')}
        </span>

        <ChartContainer config={chartConfig} className="aspect-auto h-full w-full min-w-0">
          <AreaChart data={stats.series} margin={{ top: 10, right: 12, left: 2, bottom: 4 }}>
            <defs>
              <linearGradient id={outGradId} x1="0" y1="0" x2="0" y2="1">
                <stop offset="0%" stopColor="var(--flow-up)" stopOpacity={0.32} />
                <stop offset="72%" stopColor="var(--flow-up)" stopOpacity={0.08} />
                <stop offset="100%" stopColor="var(--flow-up)" stopOpacity={0.01} />
              </linearGradient>
              <linearGradient id={inGradId} x1="0" y1="0" x2="0" y2="1">
                <stop offset="0%" stopColor="var(--flow-down)" stopOpacity={0.01} />
                <stop offset="28%" stopColor="var(--flow-down)" stopOpacity={0.08} />
                <stop offset="100%" stopColor="var(--flow-down)" stopOpacity={0.3} />
              </linearGradient>
            </defs>
            <CartesianGrid
              vertical
              horizontal
              stroke="var(--border)"
              strokeDasharray="2 3"
              strokeOpacity={0.48}
            />
            <XAxis
              dataKey="timestamp"
              axisLine={false}
              tickLine={false}
              tickMargin={8}
              minTickGap={36}
              tick={{ fontSize: 10.5 }}
              tickFormatter={(value) => formatXAxisLabel(Number(value), i18n.language)}
            />
            <YAxis
              axisLine={false}
              tickLine={false}
              tickMargin={8}
              width={66}
              tick={{ fontSize: 10.5 }}
              domain={[yAxisTicks[0], yAxisTicks[4]]}
              ticks={yAxisTicks}
              tickFormatter={(value) => {
                const num = Number(value);
                return num === 0 ? '0' : formatTrafficRate(Math.abs(num));
              }}
            />
            <ReferenceLine
              y={0}
              stroke={hasAnyTraffic ? 'var(--foreground)' : 'var(--flow-up)'}
              strokeOpacity={hasAnyTraffic ? 0.18 : 0.34}
              strokeWidth={1}
            />
            <ChartTooltip
              cursor={{
                stroke: 'var(--foreground)',
                strokeOpacity: 0.2,
                strokeWidth: 1,
                strokeDasharray: '3 3',
              }}
              content={(
                <ChartTooltipContent
                  indicator="dot"
                  labelFormatter={(_, payload) => {
                    const timestamp = payload?.[0]?.payload?.timestamp;
                    return typeof timestamp === 'number'
                      ? formatTooltipLabel(timestamp, i18n.language)
                      : '';
                  }}
                  formatter={(value, name) => (
                    <>
                      <span className="text-muted-foreground">
                        {chartConfig[String(name)]?.label ?? String(name)}
                      </span>
                      <span className="font-mono font-medium text-foreground tabular-nums">
                        {formatTrafficRate(Math.abs(Number(value)))}
                      </span>
                    </>
                  )}
                />
              )}
            />
            <Area
              type="monotone"
              dataKey="outRate"
              baseValue={0}
              stroke="var(--flow-up)"
              strokeOpacity={stats.peakOut > 0 ? 1 : 0}
              fill={`url(#${outGradId})`}
              strokeWidth={1.85}
              dot={false}
              activeDot={{ r: 3.5 }}
              isAnimationActive={false}
            />
            <Area
              type="monotone"
              dataKey="negInRate"
              baseValue={0}
              stroke="var(--flow-down)"
              strokeOpacity={stats.peakIn > 0 ? 1 : 0}
              fill={`url(#${inGradId})`}
              strokeWidth={1.85}
              dot={false}
              activeDot={{ r: 3.5 }}
              isAnimationActive={false}
            />
          </AreaChart>
        </ChartContainer>
      </div>
    </div>
  );
}
