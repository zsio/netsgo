import { useState } from 'react';
import { Server as ServerIcon, HardDrive, Clock, Cpu, Network, Monitor, Box, Database, CircleHelp, Globe, Wifi } from 'lucide-react';
import { useServerStatus } from '@/hooks/use-server-status';
import { Skeleton } from '@/components/ui/skeleton';
import { CopyableIpLine } from '@/components/custom/common/CopyableIpLine';
import { VersionUpdateIndicator } from '@/components/custom/common/VersionUpdateIndicator';
import { formatUptime, formatBytes } from '@/lib/format';
import {
  HoverCard,
  HoverCardContent,
  HoverCardTrigger,
} from "@/components/ui/hover-card"
import { useTranslation } from 'react-i18next';

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
  const { t } = useTranslation();
  const { data: status, isLoading } = useServerStatus();
  const [now] = useState(() => Date.now());

  if (isLoading) {
    return <Skeleton className="h-[300px] w-full rounded-xl" />;
  }

  const memPercent = status?.mem_total ? (status.mem_used / status.mem_total) * 100 : 0;
  const diskPercent = status?.disk_total ? (status.disk_used / status.disk_total) * 100 : 0;

  const diskPartitions = status?.disk_partitions || [];
  const multipleDisks = diskPartitions.length > 1;

  const runtimeSeconds = status?.uptime && status.uptime > 0
    ? status.uptime
    : undefined;
  const startTimeText = runtimeSeconds
    ? new Date(now - runtimeSeconds * 1000).toLocaleString(undefined, {
        year: 'numeric',
        month: 'numeric',
        day: 'numeric',
        hour: '2-digit',
        minute: '2-digit',
      })
    : '-';

  return (
    <div className="rounded-xl border border-border/40 bg-card/50 backdrop-blur-sm shadow-sm overflow-hidden flex flex-col">
      <div className="px-4 sm:px-6 py-3 sm:py-4 border-b border-border/40 bg-muted/20 flex items-center justify-between">
        <div className="flex items-center gap-2">
          <ServerIcon className="h-5 w-5 text-primary" />
          <h3 className="font-semibold text-foreground">{t('admin.serverInfo')}</h3>
        </div>
      </div>

      {/* 基础网络与软件信息 */}
      <div className="grid grid-cols-1 sm:grid-cols-2 md:grid-cols-4 divide-y sm:divide-y-0 border-b border-border/40">
        <div className="p-4 sm:p-5 flex flex-col gap-1.5 sm:border-r border-border/40">
          <span className="text-xs text-muted-foreground flex items-center gap-1.5"><Monitor className="w-4 h-4" />{t('clients.hostSystem')}</span>
          <span className="font-medium text-sm truncate" title={status?.hostname}>{status?.hostname || '-'}</span>
          <span className="text-xs text-muted-foreground">{status?.os_arch || '-'}</span>
        </div>
        <div className="p-4 sm:p-5 flex flex-col gap-1.5 md:border-r border-border/40">
          <div className="text-xs text-muted-foreground flex items-center gap-1.5">
            <Network className="w-4 h-4" />
            <span>{t('clients.listenPort', { port: status?.listen_port ? status.listen_port : '-' })}</span>
          </div>
          <CopyableIpLine
            primary
            title={t('clients.publicIp')}
            icon={<Globe className="h-3.5 w-3.5" />}
            value={status?.public_ipv4 || status?.public_ipv6 || '-'}
          />
          <CopyableIpLine
            title={t('clients.privateIp')}
            icon={<Wifi className="h-3.5 w-3.5" />}
            value={status?.ip_address || '-'}
          />
        </div>
        <div className="p-4 sm:p-5 flex flex-col gap-1.5 sm:border-r border-border/40">
          <span className="text-xs text-muted-foreground flex items-center gap-1.5"><Clock className="w-4 h-4" />{t('clients.uptime')}</span>
          <span className="font-medium text-sm">
            {runtimeSeconds ? formatUptime(runtimeSeconds) : '-'}
          </span>
          <span className="text-xs text-muted-foreground">{t('clients.startedAt', { time: startTimeText })}</span>
        </div>
        <div className="p-4 sm:p-5 flex flex-col gap-1.5">
          <span className="text-xs text-muted-foreground flex items-center gap-1.5"><Box className="w-4 h-4" />{t('clients.runtimeStatus')}</span>
          <span className="font-medium text-sm truncate" title={status?.go_version}>{status?.go_version || '-'}</span>
          <span className="group/version-update inline-flex min-w-0 items-center gap-1.5 text-xs text-muted-foreground">
            <span className="truncate">{status?.version || '-'}</span>
            <VersionUpdateIndicator
              target={{
                kind: 'server',
                version: status?.version,
                installMethod: status?.update_capability?.install_method,
              }}
              label={t('admin.serverVersion')}
            />
          </span>
        </div>
      </div>

      {/* 硬件资源与性能表现 */}
      <div className="grid grid-cols-1 md:grid-cols-3 divide-y md:divide-y-0 md:divide-x divide-border/40 bg-muted/5">
        <div className="p-4 sm:p-6 flex flex-col gap-3 sm:gap-4">
          <div className="flex items-center gap-2 text-foreground font-medium text-sm">
            <Cpu className="w-4 h-4 text-blue-500" />
            {t('clients.cpuUsage')}
            <span className="ml-auto text-xs text-muted-foreground font-normal">{status?.cpu_cores ? t('clients.cpuCores', { count: status.cpu_cores }) : '-'}</span>
          </div>
          <ProgressBar value={status?.cpu_usage || 0} label="Usage" colorClass="bg-blue-500" />
        </div>

        <div className="p-4 sm:p-6 flex flex-col gap-3 sm:gap-4">
          <div className="flex items-center gap-2 text-foreground font-medium text-sm">
            <Database className="w-4 h-4 text-emerald-500" />
            {t('clients.memoryUsage')}
            <HoverCard>
              <HoverCardTrigger asChild>
                <span className="ml-auto text-xs text-muted-foreground font-normal cursor-help inline-flex items-center gap-1">
                  NetsGo: {status?.app_mem_used ? formatBytes(status.app_mem_used) : '-'}
                  <CircleHelp className="w-3.5 h-3.5 text-muted-foreground/60" />
                </span>
              </HoverCardTrigger>
              <HoverCardContent className="w-[220px] p-3 text-xs shadow-xl border-border/50" side="bottom" align="end">
                <div className="flex flex-col gap-1.5">
                  <div className="flex justify-between">
                    <span className="text-muted-foreground">{t('clients.heapMemory')}</span>
                    <span className="font-medium text-foreground">{status?.app_mem_used ? formatBytes(status.app_mem_used) : '-'}</span>
                  </div>
                  <div className="flex justify-between">
                    <span className="text-muted-foreground">{t('clients.processUsage')}</span>
                    <span className="font-medium text-foreground">{status?.app_mem_sys ? formatBytes(status.app_mem_sys) : '-'}</span>
                  </div>
                  <p className="text-muted-foreground/70 text-[11px] pt-1 border-t border-border/40">{t('clients.processUsageHelp')}</p>
                </div>
              </HoverCardContent>
            </HoverCard>
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
            {t('clients.diskSpace')}
            {multipleDisks && (
              <span className="ml-auto text-xs text-muted-foreground font-normal">{t('clients.hoverForDetails')}</span>
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
                  {t('clients.allPartitions')}
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
