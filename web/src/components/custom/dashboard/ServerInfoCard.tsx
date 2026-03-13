import { Server as ServerIcon, HardDrive, Clock, Cpu, Network, Monitor, Box, Database } from 'lucide-react';
import { useServerStatus } from '@/hooks/use-server-status';
import { Skeleton } from '@/components/ui/skeleton';
import { formatUptime, formatBytes } from '@/lib/format';
import {
  HoverCard,
  HoverCardContent,
  HoverCardTrigger,
} from "@/components/ui/hover-card"

function ProgressBar({ value, label, total, colorClass = "bg-primary" }: { value: number, label: string, total?: string, colorClass?: string }) {
  return (
    <div className="flex flex-col gap-1.5 w-full">
      <div className="flex justify-between items-center text-xs">
        <span className="text-muted-foreground font-medium truncate max-w-[120px]" title={label}>{label}</span>
        <span className="font-medium text-foreground whitespace-nowrap">{value.toFixed(1)}%{total ? ` of ${total}` : ''}</span>
      </div>
      <div className="h-2 w-full bg-secondary overflow-hidden rounded-full">
        <div className={`h-full ${colorClass} transition-all duration-500`} style={{ width: `${Math.min(100, Math.max(0, value))}%` }} />
      </div>
    </div>
  );
}

export function ServerInfoCard() {
  const { data: status, isLoading } = useServerStatus();

  if (isLoading) {
    return <Skeleton className="h-[300px] w-full rounded-xl" />;
  }

  const memPercent = status?.mem_total ? (status.mem_used / status.mem_total) * 100 : 0;
  const diskPercent = status?.disk_total ? (status.disk_used / status.disk_total) * 100 : 0;

  const diskPartitions = status?.disk_partitions || [];
  const multipleDisks = diskPartitions.length > 1;

  return (
    <div className="rounded-xl border border-border/40 bg-card/50 backdrop-blur-sm shadow-sm overflow-hidden flex flex-col">
      <div className="px-4 sm:px-6 py-3 sm:py-4 border-b border-border/40 bg-muted/20 flex items-center justify-between">
        <div className="flex items-center gap-2">
          <ServerIcon className="h-5 w-5 text-primary" />
          <h3 className="font-semibold text-foreground">服务端信息</h3>
        </div>
        <div className="flex items-center gap-2 text-sm">
           <div className={`w-2 h-2 rounded-full ${status?.status === 'running' ? 'bg-emerald-500' : 'bg-destructive'}`} />
           <span className="font-medium text-muted-foreground">{status?.status === 'running' ? '正常运行' : '异常'}</span>
        </div>
      </div>

      {/* 基础网络与软件信息 */}
      <div className="grid grid-cols-1 sm:grid-cols-2 md:grid-cols-4 divide-y sm:divide-y-0 border-b border-border/40">
        <div className="p-4 sm:p-5 flex flex-col gap-1.5 sm:border-r border-border/40">
          <span className="text-xs text-muted-foreground flex items-center gap-1.5"><Monitor className="w-4 h-4" />主机与系统</span>
          <span className="font-medium text-sm truncate" title={status?.hostname}>{status?.hostname || '-'}</span>
          <span className="text-xs text-muted-foreground">{status?.os_arch || '-'}</span>
        </div>
        <div className="p-4 sm:p-5 flex flex-col gap-1.5 md:border-r border-border/40">
          <span className="text-xs text-muted-foreground flex items-center gap-1.5"><Network className="w-4 h-4" />IP 地址</span>
          <span className="font-medium text-sm">{status?.ip_address || '-'}</span>
          <span className="text-xs text-muted-foreground">Port: {status?.listen_port}</span>
        </div>
        <div className="p-4 sm:p-5 flex flex-col gap-1.5 sm:border-r border-border/40">
          <span className="text-xs text-muted-foreground flex items-center gap-1.5"><Clock className="w-4 h-4" />运行时长</span>
          <span className="font-medium text-sm">{formatUptime(status?.uptime ?? 0)}</span>
          <span className="text-xs text-muted-foreground">v{status?.version}</span>
        </div>
        <div className="p-4 sm:p-5 flex flex-col gap-1.5">
          <span className="text-xs text-muted-foreground flex items-center gap-1.5"><Box className="w-4 h-4" />运行状态</span>
          <span className="font-medium text-sm truncate" title={status?.go_version}>{status?.go_version || '-'}</span>
          <span className="text-xs text-muted-foreground">{status?.goroutine_count || 0} Goroutines</span>
        </div>
      </div>

      {/* 硬件资源与性能表现 */}
      <div className="grid grid-cols-1 md:grid-cols-3 divide-y md:divide-y-0 md:divide-x divide-border/40 bg-muted/5">
        <div className="p-4 sm:p-6 flex flex-col gap-3 sm:gap-4">
          <div className="flex items-center gap-2 text-foreground font-medium text-sm">
            <Cpu className="w-4 h-4 text-blue-500" />
            CPU 使用率
            <span className="ml-auto text-xs text-muted-foreground font-normal">{status?.cpu_cores ? `${status.cpu_cores} 核` : '-'}</span>
          </div>
          <ProgressBar value={status?.cpu_usage || 0} label="Usage" colorClass="bg-blue-500" />
        </div>

        <div className="p-4 sm:p-6 flex flex-col gap-3 sm:gap-4">
          <div className="flex items-center gap-2 text-foreground font-medium text-sm">
            <Database className="w-4 h-4 text-emerald-500" />
            内存占用
            <span className="ml-auto text-xs text-muted-foreground font-normal" title="NetsGo App Memory">NetsGo: {status?.app_mem_used ? formatBytes(status.app_mem_used) : '-'}</span>
          </div>
          <ProgressBar
            value={memPercent}
            label="Memory"
            total={status?.mem_total ? formatBytes(status.mem_total) : '-'}
            colorClass={memPercent > 85 ? 'bg-destructive' : 'bg-emerald-500'}
          />
        </div>

        <div className="p-4 sm:p-6 flex flex-col gap-3 sm:gap-4">
          <div className="flex items-center gap-2 text-foreground font-medium text-sm">
            <HardDrive className="w-4 h-4 text-amber-500" />
            磁盘空间
            {multipleDisks && (
              <span className="ml-auto text-xs text-muted-foreground font-normal">悬浮进度条查看明细</span>
            )}
          </div>
          {multipleDisks ? (
            <HoverCard>
              <HoverCardTrigger asChild>
                <div className="cursor-help">
                  <ProgressBar
                    value={diskPercent}
                    label="All Partitions"
                    total={status?.disk_total ? formatBytes(status.disk_total) : '-'}
                    colorClass={diskPercent > 90 ? 'bg-destructive' : 'bg-amber-500'}
                  />
                </div>
              </HoverCardTrigger>
              <HoverCardContent className="w-[320px] p-4 flex flex-col gap-4 shadow-xl border-border/50">
                <div className="flex items-center gap-2 text-sm font-semibold text-foreground pb-2 border-b border-border/40">
                  <HardDrive className="w-4 h-4" />
                  所有分区明细
                </div>
                <div className="flex flex-col gap-4 max-h-[300px] overflow-y-auto [scrollbar-width:none] [&::-webkit-scrollbar]:hidden">
                  {diskPartitions.map((p, idx) => {
                    const dpPercent = p.total ? (p.used / p.total) * 100 : 0;
                    return (
                      <ProgressBar
                        key={idx}
                        value={dpPercent}
                        label={p.path}
                        total={formatBytes(p.total)}
                        colorClass={dpPercent > 90 ? 'bg-destructive' : 'bg-amber-500'}
                      />
                    );
                  })}
                </div>
              </HoverCardContent>
            </HoverCard>
          ) : (
            <ProgressBar
              value={diskPercent}
              label={diskPartitions[0]?.path || "Storage"}
              total={status?.disk_total ? formatBytes(status.disk_total) : '-'}
              colorClass={diskPercent > 90 ? 'bg-destructive' : 'bg-amber-500'}
            />
          )}
        </div>
      </div>
    </div>
  );
}
