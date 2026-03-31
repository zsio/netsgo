import {
  Area,
  AreaChart,
  CartesianGrid,
  XAxis,
  YAxis,
} from "recharts"

import {
  type ChartConfig,
  ChartContainer,
  ChartTooltip,
  ChartTooltipContent,
} from "@/components/ui/chart"

const chartData = [
  { time: "00:00", download: 120, upload: 80 },
  { time: "04:00", download: 300, upload: 150 },
  { time: "08:00", download: 200, upload: 120 },
  { time: "12:00", download: 800, upload: 400 },
  { time: "16:00", download: 600, upload: 300 },
  { time: "20:00", download: 450, upload: 250 },
  { time: "24:00", download: 180, upload: 100 },
]

const chartConfig = {
  download: {
    label: "下行",
    color: "var(--chart-1)",
  },
  upload: {
    label: "上行",
    color: "var(--chart-2)",
  },
} satisfies ChartConfig

export function TrafficChart() {
  return (
    <div className="rounded-xl border border-border/40 bg-card/30 backdrop-blur-sm shadow-sm p-6 relative overflow-hidden group">
      <div className="flex items-center justify-between mb-6 relative z-10">
        <h3 className="font-semibold text-lg">📊 流量趋势</h3>
        <div className="flex gap-2">
          <span className="text-xs bg-muted px-2 py-1 rounded text-muted-foreground cursor-pointer hover:text-foreground">1h</span>
          <span className="text-xs bg-muted px-2 py-1 rounded text-muted-foreground cursor-pointer hover:text-foreground">24h</span>
          <span className="text-xs bg-primary/20 text-primary px-2 py-1 rounded cursor-pointer">7d</span>
        </div>
      </div>

      <div className="h-64 w-full relative z-10">
        <ChartContainer config={chartConfig} className="aspect-auto h-full w-full">
          <AreaChart accessibilityLayer data={chartData} margin={{ top: 10, right: 0, left: -20, bottom: 0 }}>
            <defs>
              <linearGradient id="fillDownload" x1="0" y1="0" x2="0" y2="1">
                <stop offset="5%" stopColor="var(--color-download)" stopOpacity={0.3} />
                <stop offset="95%" stopColor="var(--color-download)" stopOpacity={0} />
              </linearGradient>
              <linearGradient id="fillUpload" x1="0" y1="0" x2="0" y2="1">
                <stop offset="5%" stopColor="var(--color-upload)" stopOpacity={0.3} />
                <stop offset="95%" stopColor="var(--color-upload)" stopOpacity={0} />
              </linearGradient>
            </defs>
            <CartesianGrid strokeDasharray="3 3" vertical={false} stroke="var(--border)" strokeOpacity={0.5} />
            <XAxis
              dataKey="time"
              axisLine={false}
              tickLine={false}
              tick={{ fill: "var(--muted-foreground)", fontSize: 12 }}
              tickMargin={10}
            />
            <YAxis
              axisLine={false}
              tickLine={false}
              tick={{ fill: "var(--muted-foreground)", fontSize: 12 }}
              tickFormatter={(value) => `${value} MB`}
            />
            <ChartTooltip content={<ChartTooltipContent />} />
            <Area
              type="monotone"
              dataKey="upload"
              stroke="var(--color-upload)"
              fillOpacity={1}
              fill="url(#fillUpload)"
              strokeWidth={2}
            />
            <Area
              type="monotone"
              dataKey="download"
              stroke="var(--color-download)"
              fillOpacity={1}
              fill="url(#fillDownload)"
              strokeWidth={2}
            />
          </AreaChart>
        </ChartContainer>
      </div>
      <div className="absolute inset-0 bg-gradient-to-t from-background/80 to-transparent pointer-events-none" />
    </div>
  )
}
