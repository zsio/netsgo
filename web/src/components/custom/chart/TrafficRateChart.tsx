import { useMemo } from 'react';
import { useTranslation } from 'react-i18next';
import { Line, LineChart, CartesianGrid, XAxis, YAxis } from 'recharts';
import { AlertCircle } from 'lucide-react';

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

function formatTrafficValue(value: number) {
  return formatBytes(value).replace('.0 ', ' ');
}

function formatXAxisLabel(timestamp: number, language: string) {
  const date = new Date(timestamp);
  return date.toLocaleString(language, { hour: '2-digit', minute: '2-digit' });
}

function formatTooltipLabel(timestamp: number, language: string) {
  const date = new Date(timestamp);
  return date.toLocaleString(language, { month: 'numeric', day: 'numeric', hour: '2-digit', minute: '2-digit' });
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

export function TrafficRateChart({ clientId, tunnelFilter }: TrafficRateChartProps) {
  const { t, i18n } = useTranslation();
  const chartConfig = useMemo<ChartConfig>(() => ({
    inRate: { label: t('traffic.inbound'), color: 'var(--chart-2)' },
    outRate: { label: t('traffic.outbound'), color: 'var(--chart-1)' },
  }), [t]);
  const { data, isLoading, isError, error } = useClientTraffic(clientId, '24h', {
    tunnel: getQueryTunnel(tunnelFilter),
  });
  const chartData = useAggregatedTrafficRates(data, '24h', tunnelFilter);
  const hasSamples = hasTrafficSamples(data, tunnelFilter);

  return (
    <div className="min-w-0">
      {isLoading ? (
        <div className="h-80 animate-pulse rounded-xl border border-border/60 bg-background/30" />
      ) : isError ? (
        <div className="flex h-80 flex-col items-center justify-center rounded-xl border border-dashed border-destructive/30 bg-destructive/5 text-center">
          <AlertCircle className="mb-3 h-5 w-5 text-destructive" />
          <p className="text-sm font-medium text-foreground">{t('traffic.loadFailed')}</p>
          <p className="mt-1 max-w-md text-sm text-muted-foreground">{getErrorMessage(error, t('traffic.loadFailed'))}</p>
        </div>
      ) : !hasSamples ? (
        <div className="flex h-80 flex-col items-center justify-center rounded-xl border border-dashed border-border/60 bg-background/30 text-center">
          <p className="text-sm font-medium text-foreground">{t('traffic.emptyRange')}</p>
        </div>
      ) : (
        <div className="h-80 min-w-0 w-full">
          <ChartContainer config={chartConfig} className="h-full min-w-0 w-full aspect-auto">
            <LineChart data={chartData} margin={{ top: 12, right: 12, left: 8, bottom: 4 }}>
              <CartesianGrid vertical={false} stroke="var(--border)" strokeDasharray="3 3" strokeOpacity={0.45} />
              <XAxis
                dataKey="timestamp"
                axisLine={false}
                tickLine={false}
                tickMargin={12}
                minTickGap={28}
                tickFormatter={(value) => formatXAxisLabel(Number(value), i18n.language)}
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
                        ? formatTooltipLabel(timestamp, i18n.language)
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
                isAnimationActive={false}
                connectNulls
              />
              <Line
                type="monotone"
                dataKey="inRate"
                stroke="var(--color-inRate)"
                strokeWidth={2}
                dot={false}
                activeDot={{ r: 4 }}
                isAnimationActive={false}
                connectNulls
              />
            </LineChart>
          </ChartContainer>
        </div>
      )}
    </div>
  );
}
