import { Line, LineChart } from 'recharts';
import { useClientTraffic } from '@/hooks/use-client-traffic';
import { hasTrafficSamples, useAggregatedTrafficRates } from '@/hooks/use-traffic-rates';
import { ChartContainer, ChartTooltip, ChartTooltipContent } from '@/components/ui/chart';
import { formatBytes } from '@/lib/format';
import type { ChartConfig } from '@/components/ui/chart';

interface CompactTrafficChartProps {
  clientId: string;
}

export function CompactTrafficChart({ clientId }: CompactTrafficChartProps) {
  const { data, isLoading, isError } = useClientTraffic(clientId, '1h');
  const rateData = useAggregatedTrafficRates(data, '1h');
  const hasSamples = hasTrafficSamples(data);

  const chartConfig: ChartConfig = {
    inRate: { label: '下行', color: 'var(--chart-2)' },
    outRate: { label: '上行', color: 'var(--chart-1)' },
  };

  if (isLoading) {
    return <div className="h-8 w-24 animate-pulse rounded bg-muted/50" />;
  }

  if (isError || !hasSamples) {
    return <div className="h-8 w-24 text-xs text-muted-foreground flex items-center justify-center">-</div>;
  }

  return (
    <div className="h-8 w-24">
      <ChartContainer config={chartConfig} className="h-full w-full aspect-auto [&_.recharts-tooltip-cursor]:hidden">
        <LineChart data={rateData} margin={{ top: 2, bottom: 2 }}>
          <ChartTooltip
            content={(
              <ChartTooltipContent
                indicator="line"
                labelFormatter={(_, payload) => {
                  const ts = payload?.[0]?.payload?.timestamp;
                  return typeof ts === 'number' ? new Date(ts).toLocaleString('zh-CN', { hour: '2-digit', minute: '2-digit' }) : '';
                }}
                formatter={(value, name) => (
                  <>
                    <span className="text-muted-foreground">{chartConfig[name as keyof typeof chartConfig]?.label}</span>
                    <span className="font-mono font-medium text-foreground tabular-nums">
                      {formatBytes(Number(value))}/s
                    </span>
                  </>
                )}
              />
            )}
            cursor={false}
          />
          <Line
            type="monotone"
            dataKey="inRate"
            stroke="var(--color-inRate)"
            strokeWidth={1.5}
            strokeOpacity={0.6}
            dot={false}
            isAnimationActive={false}
            connectNulls
          />
          <Line
            type="monotone"
            dataKey="outRate"
            stroke="var(--color-outRate)"
            strokeWidth={1.5}
            strokeOpacity={0.6}
            dot={false}
            isAnimationActive={false}
            connectNulls
          />
        </LineChart>
      </ChartContainer>
    </div>
  );
}
