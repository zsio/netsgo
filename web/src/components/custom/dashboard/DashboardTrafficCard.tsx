import { useEffect, useId, useMemo, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { Activity } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import {
  Area,
  AreaChart,
  CartesianGrid,
  ReferenceDot,
  ReferenceLine,
  XAxis,
  YAxis,
} from 'recharts';

import {
  type ChartConfig,
  ChartContainer,
  ChartTooltip,
  ChartTooltipContent,
} from '@/components/ui/chart';
import { Skeleton } from '@/components/ui/skeleton';
import { buildClientTrafficUrl } from '@/hooks/use-client-traffic';
import { useClients } from '@/hooks/use-clients';
import { useRealtimeTraffic } from '@/hooks/use-realtime-traffic';
import { api } from '@/lib/api';
import { formatBytes, formatTrafficRate } from '@/lib/format';
import { scopedQueryKey, type ResourceScope } from '@/lib/resource-scope';
import { cn } from '@/lib/utils';
import type { ClientTrafficRange, ClientTrafficResponse } from '@/types';
import {
  buildGlobalHistoricalTrafficSummary,
  buildGlobalRealtimeTrafficSummary,
  computeSymmetricTrafficTicks,
} from './dashboard-traffic-summary';

const RANGE_OPTIONS: ReadonlyArray<{ value: ClientTrafficRange; label: string }> = [
  { value: '60s', label: '60s' },
  { value: '1h', label: '1h' },
  { value: '24h', label: '24h' },
  { value: '7d', label: '7d' },
];

const HISTORY_FETCH_CONCURRENCY = 4;

async function fetchGlobalTrafficHistory(
  scope: ResourceScope,
  clientIds: string[],
  range: Exclude<ClientTrafficRange, '60s'>,
): Promise<ClientTrafficResponse[]> {
  const results: ClientTrafficResponse[] = new Array(clientIds.length);
  let cursor = 0;

  async function worker() {
    while (cursor < clientIds.length) {
      const idx = cursor++;
      const clientId = clientIds[idx];
      results[idx] = await api.get<ClientTrafficResponse>(
        buildClientTrafficUrl(scope, clientId, range),
      );
    }
  }

  const workerCount = Math.min(HISTORY_FETCH_CONCURRENCY, clientIds.length);
  await Promise.all(Array.from({ length: workerCount }, () => worker()));
  return results;
}

function formatAxisTick(timestamp: number, range: ClientTrafficRange, language: string) {
  const date = new Date(timestamp);
  if (range === '60s') {
    return date.toLocaleTimeString(language, {
      hour: '2-digit',
      minute: '2-digit',
      second: '2-digit',
      hour12: false,
    });
  }
  if (range === '1h' || range === '24h') {
    return date.toLocaleTimeString(language, {
      hour: '2-digit',
      minute: '2-digit',
      hour12: false,
    });
  }
  return date.toLocaleDateString(language, {
    month: 'numeric',
    day: 'numeric',
  });
}

function formatTooltipTimestamp(timestamp: number, range: ClientTrafficRange, language: string) {
  const date = new Date(timestamp);
  if (range === '60s') {
    return date.toLocaleTimeString(language, {
      hour: '2-digit',
      minute: '2-digit',
      second: '2-digit',
      hour12: false,
    });
  }
  if (range === '1h' || range === '24h') {
    return date.toLocaleString(language, {
      month: 'numeric',
      day: 'numeric',
      hour: '2-digit',
      minute: '2-digit',
      hour12: false,
    });
  }
  return date.toLocaleString(language, {
    month: 'numeric',
    day: 'numeric',
    hour: '2-digit',
    hour12: false,
  });
}

export function DashboardTrafficCard({ scope }: { scope: ResourceScope }) {
  const { t, i18n } = useTranslation();
  const gradientPrefix = useId().replace(/:/g, '');
  const [range, setRange] = useState<ClientTrafficRange>('60s');
  const [clockAnchorMs, setClockAnchorMs] = useState(() => Date.now());

  const { data: clients, isLoading: clientsLoading } = useClients(scope);

  const allClientIds = useMemo(
    () => (clients ?? []).map((client) => client.id).sort(),
    [clients],
  );
  const onlineClientIds = useMemo(
    () => new Set((clients ?? []).filter((c) => c.online).map((c) => c.id)),
    [clients],
  );

  const { data: realtimeTraffic, isLoading: realtimeLoading } = useRealtimeTraffic(scope, {
    enabled: range === '60s' && onlineClientIds.size > 0,
  });

  const historicalRange = range === '60s' ? null : range;
  const { data: historyResponses, isLoading: historyLoading } = useQuery({
    queryKey: scopedQueryKey(
      scope,
      'global-traffic-history',
      historicalRange ?? '1h',
      allClientIds.join(','),
    ),
    enabled: historicalRange !== null && allClientIds.length > 0,
    queryFn: () => fetchGlobalTrafficHistory(scope, allClientIds, historicalRange!),
    staleTime: 30_000,
    refetchInterval: historicalRange === '1h' ? 30_000 : 60_000,
    refetchOnWindowFocus: false,
  });

  useEffect(() => {
    if (range !== '60s') return;
    const timer = window.setInterval(() => {
      setClockAnchorMs(Date.now());
    }, 2_000);
    return () => window.clearInterval(timer);
  }, [range]);

  const summary = useMemo(() => {
    if (range === '60s') {
      return buildGlobalRealtimeTrafficSummary(realtimeTraffic, onlineClientIds, clockAnchorMs);
    }
    return buildGlobalHistoricalTrafficSummary(historyResponses ?? [], range, clockAnchorMs);
  }, [clockAnchorMs, historyResponses, onlineClientIds, range, realtimeTraffic]);

  const maxPeakRate = Math.max(summary.peakInRate, summary.peakOutRate);
  const yAxisTicks = useMemo(
    () => computeSymmetricTrafficTicks(maxPeakRate),
    [maxPeakRate],
  );

  const chartConfig = useMemo<ChartConfig>(
    () => ({
      outRate: { label: t('traffic.outbound'), color: '#0ea5e9' },
      negInRate: { label: t('traffic.inbound'), color: '#10b981' },
    }),
    [t],
  );

  if (clientsLoading || (range === '60s' && onlineClientIds.size > 0 && realtimeLoading && !realtimeTraffic)) {
    return <Skeleton className="h-[316px] w-full rounded-xl" />;
  }

  const inGradId = `in-grad-${gradientPrefix}`;
  const outGradId = `out-grad-${gradientPrefix}`;
  const glowId = `wave-glow-${gradientPrefix}`;
  const lastPoint = summary.series[summary.series.length - 1];
  const hasAnyTraffic = maxPeakRate > 0;
  const isHistoryPending = range !== '60s' && historyLoading && !historyResponses;

  return (
    <section className="flex h-full min-h-[316px] flex-col overflow-hidden rounded-xl border border-border/60 bg-card shadow-2xs">
      {/* 卡片头部 */}
      <div className="flex items-center justify-between gap-2 border-b border-border/50 px-4 py-2.5 sm:px-5">
        <div className="flex items-center gap-2.5">
          <span className="flex size-7 shrink-0 items-center justify-center rounded-lg border border-border/70 bg-muted/60 text-foreground">
            <Activity className="size-4" />
          </span>
          <h3 className="text-sm font-semibold text-foreground">
            {t('dashboard.realtimeTrafficTitle')}
          </h3>
        </div>

        <div className="inline-flex items-center rounded-lg border border-border/60 bg-muted/45 p-0.5">
          {RANGE_OPTIONS.map((option) => {
            const active = option.value === range;
            return (
              <button
                key={option.value}
                type="button"
                aria-pressed={active}
                onClick={() => {
                  setClockAnchorMs(Date.now());
                  setRange(option.value);
                }}
                className={cn(
                  'rounded-md px-2 py-0.5 font-mono text-[11px] font-medium transition-colors',
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

      {/* 单行紧凑内联指标条（无大卡片，把空间全部让给上下行对称图表） */}
      <div className="flex flex-wrap items-center justify-between gap-x-4 gap-y-1 px-4 pt-2.5 pb-0.5 text-xs sm:px-5">
        <div className="flex items-center gap-4">
          <span className="inline-flex items-center gap-1.5">
            <span className="size-2 rounded-full bg-sky-500" />
            <span className="text-muted-foreground">↑ {t('traffic.outbound')}</span>
            <span className="font-mono font-semibold tabular-nums text-foreground">
              {formatTrafficRate(summary.headlineOutRate)}
            </span>
          </span>
          <span className="inline-flex items-center gap-1.5">
            <span className="size-2 rounded-full bg-emerald-500" />
            <span className="text-muted-foreground">↓ {t('traffic.inbound')}</span>
            <span className="font-mono font-semibold tabular-nums text-foreground">
              {formatTrafficRate(summary.headlineInRate)}
            </span>
          </span>
        </div>

        <div className="flex items-center gap-2 font-mono text-[11px] tabular-nums text-muted-foreground">
          <span>{t('dashboard.realtimePeakShort', { value: formatTrafficRate(maxPeakRate) })}</span>
          <span className="text-border">·</span>
          <span>
            {t('dashboard.realtimeTotalShort', {
              value: formatBytes(summary.totalInBytes + summary.totalOutBytes),
            })}
          </span>
        </div>
      </div>

      {/* 上下行 Y 轴镜像对称波形（+Y 上行天青向上，-Y 下行翠绿向下） */}
      {isHistoryPending ? (
        <div className="flex flex-1 items-center justify-center px-4 py-3 sm:px-5">
          <Skeleton className="h-[212px] w-full rounded-lg" />
        </div>
      ) : (
        <div className="relative flex min-h-[218px] min-w-0 flex-1 flex-col justify-end px-2 pt-1 pb-2.5 sm:px-3">
          <span
            aria-hidden
            className="pointer-events-none absolute top-2 right-5 z-10 font-mono text-[10px] font-medium text-sky-600/80 dark:text-sky-400/80"
          >
            ↑ {t('traffic.outbound')}
          </span>
          <span
            aria-hidden
            className="pointer-events-none absolute right-5 bottom-8 z-10 font-mono text-[10px] font-medium text-emerald-600/80 dark:text-emerald-400/80"
          >
            ↓ {t('traffic.inbound')}
          </span>

          <div className="h-[216px] w-full min-w-0 flex-1">
            <ChartContainer config={chartConfig} className="aspect-auto h-full w-full min-w-0">
              <AreaChart data={summary.series} margin={{ top: 10, right: 14, left: 2, bottom: 0 }}>
                <defs>
                  <linearGradient id={outGradId} x1="0" y1="0" x2="0" y2="1">
                    <stop offset="0%" stopColor="#0ea5e9" stopOpacity={0.34} />
                    <stop offset="72%" stopColor="#0ea5e9" stopOpacity={0.08} />
                    <stop offset="100%" stopColor="#0ea5e9" stopOpacity={0.01} />
                  </linearGradient>
                  <linearGradient id={inGradId} x1="0" y1="0" x2="0" y2="1">
                    <stop offset="0%" stopColor="#10b981" stopOpacity={0.01} />
                    <stop offset="28%" stopColor="#10b981" stopOpacity={0.08} />
                    <stop offset="100%" stopColor="#10b981" stopOpacity={0.34} />
                  </linearGradient>
                  <filter id={glowId} x="-10%" y="-20%" width="120%" height="140%">
                    <feGaussianBlur stdDeviation="1.4" result="blur" />
                    <feMerge>
                      <feMergeNode in="blur" />
                      <feMergeNode in="SourceGraphic" />
                    </feMerge>
                  </filter>
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
                  tickMargin={7}
                  minTickGap={48}
                  tick={{ fontSize: 10.5 }}
                  tickFormatter={(value) => formatAxisTick(Number(value), range, i18n.language)}
                />

                <YAxis
                  axisLine={false}
                  tickLine={false}
                  tickMargin={8}
                  width={62}
                  tick={{ fontSize: 10.5 }}
                  domain={[yAxisTicks[0], yAxisTicks[4]]}
                  ticks={yAxisTicks}
                  tickFormatter={(value) => {
                    const num = Number(value);
                    return num === 0 ? '0' : formatTrafficRate(Math.abs(num));
                  }}
                />

                {/* 中心 0 速率基准线 */}
                <ReferenceLine
                  y={0}
                  stroke={hasAnyTraffic ? 'var(--foreground)' : '#10b981'}
                  strokeOpacity={hasAnyTraffic ? 0.2 : 0.38}
                  strokeWidth={1}
                />

                {/* 60s 实时前沿虚线游标 */}
                {range === '60s' && lastPoint ? (
                  <ReferenceLine
                    x={lastPoint.timestamp}
                    stroke="#10b981"
                    strokeOpacity={0.22}
                    strokeDasharray="2 2"
                  />
                ) : null}

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
                        const ts = payload?.[0]?.payload?.timestamp;
                        return typeof ts === 'number'
                          ? formatTooltipTimestamp(ts, range, i18n.language)
                          : '';
                      }}
                      formatter={(value, name) => (
                        <>
                          <span className="text-muted-foreground">
                            {chartConfig[String(name)]?.label ?? String(name)}
                          </span>
                          <span className="font-mono font-medium tabular-nums text-foreground">
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
                  stroke="#0ea5e9"
                  strokeOpacity={summary.peakOutRate > 0 ? 1 : 0}
                  fill={`url(#${outGradId})`}
                  strokeWidth={1.85}
                  filter={summary.peakOutRate > 0 ? `url(#${glowId})` : undefined}
                  dot={false}
                  activeDot={{ r: 3.5, strokeWidth: 1.5, stroke: 'var(--background)' }}
                  isAnimationActive={false}
                />

                <Area
                  type="monotone"
                  dataKey="negInRate"
                  baseValue={0}
                  stroke="#10b981"
                  strokeOpacity={summary.peakInRate > 0 ? 1 : 0}
                  fill={`url(#${inGradId})`}
                  strokeWidth={1.85}
                  filter={summary.peakInRate > 0 ? `url(#${glowId})` : undefined}
                  dot={false}
                  activeDot={{ r: 3.5, strokeWidth: 1.5, stroke: 'var(--background)' }}
                  isAnimationActive={false}
                />

                {/* 60s 实时前沿锚点 */}
                {range === '60s' && lastPoint ? (
                  <ReferenceDot
                    x={lastPoint.timestamp}
                    y={lastPoint.negInRate}
                    r={3}
                    fill="#10b981"
                    stroke="var(--background)"
                    strokeWidth={1.5}
                  />
                ) : null}
                {range === '60s' && lastPoint && lastPoint.outRate > 0 ? (
                  <ReferenceDot
                    x={lastPoint.timestamp}
                    y={lastPoint.outRate}
                    r={3}
                    fill="#0ea5e9"
                    stroke="var(--background)"
                    strokeWidth={1.5}
                  />
                ) : null}
              </AreaChart>
            </ChartContainer>
          </div>
        </div>
      )}
    </section>
  );
}
