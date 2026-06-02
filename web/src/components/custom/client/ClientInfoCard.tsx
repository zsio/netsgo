import { useState } from 'react';
import {
  Monitor, Network, Clock, Cpu, HardDrive, Database,
  Box, CircleHelp, Globe, Wifi, ArrowDownCircle, ArrowUpCircle,
  Pencil,
} from 'lucide-react';
import { formatBytes, formatUptime, formatNetSpeed, formatBandwidthLimit } from '@/lib/format';
import {
  HoverCard,
  HoverCardContent,
  HoverCardTrigger,
} from '@/components/ui/hover-card';
import { CopyableIpLine } from '@/components/custom/common/CopyableIpLine';
import { VersionUpdateIndicator } from '@/components/custom/common/VersionUpdateIndicator';
import { ClientBandwidthDialog } from '@/components/custom/client/ClientBandwidthDialog';
import { Button } from '@/components/ui/button';
import type { Client } from '@/types';
import { getClientDisplayName } from '@/lib/client-utils';
import { useTranslation } from 'react-i18next';

interface ClientInfoCardProps {
  client: Client;
}

const osLabels: Record<string, string> = {
  darwin: 'macOS',
  linux: 'Linux',
  windows: 'Windows',
};

function ProgressBar({ value, label, total, colorClass = 'bg-primary' }: { value: number; label: string; total?: string; colorClass?: string }) {
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

export function ClientInfoCard({ client }: ClientInfoCardProps) {
  const { t } = useTranslation();
  const stats = client.stats;
  const info = client.info;
  const isOnline = client.online;

  const memPercent = stats?.mem_total ? (stats.mem_used / stats.mem_total) * 100 : 0;
  const diskPercent = stats?.disk_total ? (stats.disk_used / stats.disk_total) * 100 : 0;
  const diskPartitions = stats?.disk_partitions || [];
  const multipleDisks = diskPartitions.length > 1;

  const [now] = useState(() => Date.now());
  const runtimeSeconds = stats?.process_uptime && stats.process_uptime > 0
    ? stats.process_uptime
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
      {/* Header */}
      <div className="px-4 sm:px-6 py-3 sm:py-4 border-b border-border/40 bg-muted/20 flex items-center justify-between">
        <div className="flex items-center gap-2">
          <Monitor className="h-5 w-5 text-primary" />
          <h3 className="font-semibold text-foreground">{getClientDisplayName(client)}</h3>
          {client.display_name && (
            <span className="text-xs text-muted-foreground font-normal">({info.hostname})</span>
          )}
        </div>
        <div className="flex items-center gap-2 text-sm">
          <div className={`w-2 h-2 rounded-full ${isOnline ? 'bg-emerald-500' : 'bg-destructive'}`} />
          <div className="flex flex-col items-end leading-tight">
            <span className="font-medium text-muted-foreground">{isOnline ? t('clients.online') : t('clients.offline')}</span>
          </div>
        </div>
      </div>

      {/* Device Info Row */}
      <div className="grid grid-cols-1 sm:grid-cols-2 md:grid-cols-4 divide-y sm:divide-y-0 border-b border-border/40">
        <div className="p-4 sm:p-5 flex flex-col gap-1.5 sm:border-r border-border/40">
          <span className="text-xs text-muted-foreground flex items-center gap-1.5"><Monitor className="w-4 h-4" />{t('clients.operatingSystem')}</span>
          <span className="font-medium text-sm">{osLabels[info.os] ?? info.os} / {info.arch}</span>
          <span className="text-xs text-muted-foreground font-mono">{client.id.slice(0, 8)}</span>
        </div>
        <div className="p-4 sm:p-5 flex flex-col gap-1.5 md:border-r border-border/40">
          <span className="text-xs text-muted-foreground flex items-center gap-1.5"><Network className="w-4 h-4" />{t('clients.ipAddress')}</span>
          <CopyableIpLine
            primary
            title={t('clients.publicIp')}
            icon={<Globe className="h-3.5 w-3.5" />}
            value={info.public_ipv4 || info.public_ipv6 || client.last_ip || '-'}
          />
          <CopyableIpLine
            title={t('clients.privateIp')}
            icon={<Wifi className="h-3.5 w-3.5" />}
            value={info.ip || '-'}
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
          <div className="flex items-center justify-between gap-2">
            <span className="text-xs text-muted-foreground flex items-center gap-1.5"><Box className="w-4 h-4" />{t('clients.bandwidth')}</span>
            <ClientBandwidthDialog
              client={client}
              trigger={
                <Button variant="ghost" size="icon-xs" title={t('clients.editBandwidth')}>
                  <Pencil className="h-3.5 w-3.5" />
                </Button>
              }
            />
          </div>
          <span className="flex min-w-0 items-center gap-2 font-medium text-sm">
            <span className="inline-flex min-w-0 items-center gap-1">
              <ArrowDownCircle className="h-3.5 w-3.5 shrink-0 text-emerald-500" />
              <span className="truncate">{formatBandwidthLimit(client.ingress_bps)}</span>
            </span>
            <span className="text-muted-foreground">/</span>
            <span className="inline-flex min-w-0 items-center gap-1">
              <ArrowUpCircle className="h-3.5 w-3.5 shrink-0 text-blue-500" />
              <span className="truncate">{formatBandwidthLimit(client.egress_bps)}</span>
            </span>
          </span>
          <span className="group/version-update inline-flex min-w-0 items-center gap-1.5 text-xs text-muted-foreground">
            <span className="truncate">{info.version || '-'}</span>
            <VersionUpdateIndicator
              target={{
                kind: 'client',
                id: client.id,
                version: info.version,
                installMethod: info.update_capability?.install_method,
                os: info.os,
                arch: info.arch,
                enabled: client.online,
              }}
              label={t('clients.clientVersion')}
            />
          </span>
        </div>
      </div>

      {/* Hardware Stats */}
      {isOnline && stats && (
        <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 bg-muted/5">
          {/* CPU */}
          <div className="p-4 sm:p-5 flex flex-col gap-3 border-b sm:border-b lg:border-b-0 border-border/40 sm:border-r">
            <div className="flex items-center gap-2 text-foreground font-medium text-sm">
              <Cpu className="w-4 h-4 text-blue-500 shrink-0" />
              CPU
              <span className="ml-auto text-xs text-muted-foreground font-normal">{t('clients.cpuCores', { count: stats.num_cpu })}</span>
            </div>
            <ProgressBar value={stats.cpu_usage} label="Usage" colorClass="bg-blue-500" />
          </div>

          {/* Memory */}
          <div className="p-4 sm:p-5 flex flex-col gap-3 border-b sm:border-b lg:border-b-0 border-border/40 lg:border-r sm:[&:nth-child(2)]:border-r-0 lg:[&:nth-child(2)]:border-r">
            <div className="flex items-center gap-2 text-foreground font-medium text-sm min-w-0">
              <Database className="w-4 h-4 text-emerald-500 shrink-0" />
              <span className="shrink-0">{t('clients.memory')}</span>
              <HoverCard>
                <HoverCardTrigger asChild>
                  <span className="ml-auto text-xs text-muted-foreground font-normal cursor-help inline-flex items-center gap-1 shrink-0">
                    <span className="hidden sm:inline">NetsGo:</span> {stats.app_mem_used ? formatBytes(stats.app_mem_used) : '-'}
                    <CircleHelp className="w-3 h-3 text-muted-foreground/60" />
                  </span>
                </HoverCardTrigger>
                <HoverCardContent className="w-[220px] p-3 text-xs shadow-xl border-border/50" side="bottom" align="end">
                  <div className="flex flex-col gap-1.5">
                    <div className="flex justify-between">
                      <span className="text-muted-foreground">{t('clients.heapMemory')}</span>
                      <span className="font-medium text-foreground">{stats.app_mem_used ? formatBytes(stats.app_mem_used) : '-'}</span>
                    </div>
                    <div className="flex justify-between">
                      <span className="text-muted-foreground">{t('clients.processUsage')}</span>
                      <span className="font-medium text-foreground">{stats.app_mem_sys ? formatBytes(stats.app_mem_sys) : '-'}</span>
                    </div>
                    <p className="text-muted-foreground/70 text-[11px] pt-1 border-t border-border/40">{t('clients.processUsageHelp')}</p>
                  </div>
                </HoverCardContent>
              </HoverCard>
            </div>
            <ProgressBar
              value={memPercent}
              label="Memory"
              total={stats.mem_total ? formatBytes(stats.mem_total) : '-'}
              colorClass={memPercent > 85 ? 'bg-destructive' : 'bg-emerald-500'}
            />
          </div>

          {/* Disk */}
          <div className="p-4 sm:p-5 flex flex-col gap-3 border-b sm:border-b-0 border-border/40 sm:border-r">
            <div className="flex items-center gap-2 text-foreground font-medium text-sm">
              <HardDrive className="w-4 h-4 text-amber-500 shrink-0" />
              <span className="shrink-0">{t('clients.disk')}</span>
              {multipleDisks && (
                <span className="ml-auto text-xs text-muted-foreground font-normal hidden lg:inline">{t('clients.hoverForDetails')}</span>
              )}
            </div>
            {multipleDisks ? (
              <HoverCard>
                <HoverCardTrigger asChild>
                  <div className="cursor-help">
                    <ProgressBar
                      value={diskPercent}
                      label="All Partitions"
                      total={stats.disk_total ? formatBytes(stats.disk_total) : '-'}
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
                label={diskPartitions[0]?.path || 'Storage'}
                total={stats.disk_total ? formatBytes(stats.disk_total) : '-'}
                colorClass={diskPercent > 90 ? 'bg-destructive' : 'bg-amber-500'}
              />
            )}
          </div>

          {/* Network */}
          <div className="p-4 sm:p-5 flex flex-col gap-3">
            <div className="flex items-center gap-2 text-foreground font-medium text-sm">
              <Globe className="w-4 h-4 text-violet-500 shrink-0" />
              {t('clients.networkIo')}
            </div>
            <div className="flex flex-col gap-1.5 mt-auto">
              <div className="flex items-center justify-between text-xs">
                <div className="flex items-center gap-1">
                  <ArrowDownCircle className="h-3.5 w-3.5 text-emerald-500 shrink-0" />
                  <span className="font-mono font-medium">{formatNetSpeed(stats.net_recv_speed)}</span>
                </div>
                <div className="flex items-center gap-1">
                  <ArrowUpCircle className="h-3.5 w-3.5 text-blue-500 shrink-0" />
                  <span className="font-mono font-medium">{formatNetSpeed(stats.net_sent_speed)}</span>
                </div>
              </div>
              <div className="text-[10px] text-muted-foreground/60">
                {t('clients.totalTraffic', { recv: formatBytes(stats.net_recv), sent: formatBytes(stats.net_sent) })}
              </div>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
