import { useState, type ReactNode } from 'react';
import {
  CircleHelp,
  Clock,
  Globe,
  HardDrive,
  Server as ServerIcon,
  Wifi,
} from 'lucide-react';
import { useTranslation } from 'react-i18next';

import { CopyableIpLine } from '@/components/custom/common/CopyableIpLine';
import { DualTriggerCard } from '@/components/custom/common/DualTriggerCard';
import { VersionUpdateIndicator } from '@/components/custom/common/VersionUpdateIndicator';
import { Skeleton } from '@/components/ui/skeleton';
import { useServerStatus } from '@/hooks/use-server-status';
import { formatBytes, formatUptime } from '@/lib/format';
import type { ResourceScope } from '@/lib/resource-scope';
import { cn } from '@/lib/utils';

type MeterTone = 'normal' | 'warning' | 'critical';

function resolveMeterTone(percent: number, warnAt = 82, critAt = 92): MeterTone {
  if (percent >= critAt) return 'critical';
  if (percent >= warnAt) return 'warning';
  return 'normal';
}

function PartitionRow({
  path,
  percent,
  total,
}: {
  path: string;
  percent: number;
  total: string;
}) {
  const { t } = useTranslation();
  const tone = resolveMeterTone(percent, 85, 92);
  const clamped = Math.min(100, Math.max(0, percent));

  return (
    <div className="flex flex-col gap-1.5">
      <div className="flex items-center justify-between gap-2 text-xs">
        <span className="max-w-[140px] truncate font-medium text-muted-foreground" title={path}>
          {path}
        </span>
        <span className="font-mono font-medium whitespace-nowrap text-foreground">
          {t('clients.percentOfTotal', { percent: percent.toFixed(1), total })}
        </span>
      </div>
      <div className="h-1.5 w-full overflow-hidden rounded-full bg-muted">
        <div
          className={cn(
            'h-full rounded-full transition-all duration-500',
            tone === 'critical'
              ? 'bg-status-danger'
              : tone === 'warning'
                ? 'bg-status-warning'
                : 'bg-meter-track',
          )}
          style={{ width: `${clamped}%` }}
        />
      </div>
    </div>
  );
}

function CompactResourceMeter({
  label,
  percent,
  sublabel,
  tone,
  hintIcon,
  interactive = false,
}: {
  label: string;
  percent: number;
  sublabel: string;
  tone: MeterTone;
  hintIcon?: ReactNode;
  interactive?: boolean;
}) {
  const clamped = Math.min(100, Math.max(0, percent));

  return (
    <div
      className={cn(
        'flex min-w-0 flex-col gap-1.5 rounded-lg transition-colors',
        interactive && 'cursor-pointer',
      )}
    >
      <div className="flex items-center justify-between gap-1 text-xs leading-none">
        <span className="inline-flex min-w-0 items-center gap-1 truncate text-muted-foreground">
          <span className="truncate">{label}</span>
          {hintIcon}
        </span>
        <span
          className={cn(
            'shrink-0 font-mono font-semibold text-foreground',
            tone === 'critical' && 'text-status-danger-fg',
            tone === 'warning' && 'text-status-warning-fg',
          )}
        >
          {`${Math.round(clamped)}%`}
        </span>
      </div>

      <div className="h-1.5 w-full overflow-hidden rounded-full bg-muted">
        <div
          className={cn(
            'h-full rounded-full transition-all duration-500',
            tone === 'critical'
              ? 'bg-status-danger'
              : tone === 'warning'
                ? 'bg-status-warning'
                : 'bg-meter-track',
          )}
          style={{ width: `${clamped}%` }}
        />
      </div>

      <span className="truncate font-mono text-[10.5px] leading-none text-muted-foreground/85" title={sublabel}>
        {sublabel}
      </span>
    </div>
  );
}

function formatCompactUsage(used?: number, total?: number): string {
  if (!total || used === undefined) return '-';
  const usedStr = formatBytes(used);
  const totalStr = formatBytes(total);
  const usedParts = usedStr.split(' ');
  const totalParts = totalStr.split(' ');
  if (usedParts.length === 2 && totalParts.length === 2 && usedParts[1] === totalParts[1]) {
    return `${usedParts[0]} / ${totalParts[0]} ${totalParts[1]}`;
  }
  return `${usedStr} / ${totalStr}`;
}

export function ServerInfoCard({ scope }: { scope?: ResourceScope }) {
  const { t } = useTranslation();
  const { data: status, isLoading } = useServerStatus();
  const [now] = useState(() => Date.now());

  if (isLoading) {
    return <Skeleton className="h-[86px] w-full rounded-xl" />;
  }

  const cpuPercent = status?.cpu_usage ?? 0;
  const memPercent = status?.mem_total ? (status.mem_used / status.mem_total) * 100 : 0;
  const diskPercent = status?.disk_total ? (status.disk_used / status.disk_total) * 100 : 0;

  const diskPartitions = status?.disk_partitions || [];
  const multipleDisks = diskPartitions.length > 1;

  const runtimeSeconds = status?.uptime && status.uptime > 0 ? status.uptime : undefined;
  const startTimeText = runtimeSeconds
    ? new Date(now - runtimeSeconds * 1000).toLocaleString(undefined, {
        month: 'numeric',
        day: 'numeric',
        hour: '2-digit',
        minute: '2-digit',
      })
    : '-';

  const memSublabel = formatCompactUsage(status?.mem_used, status?.mem_total);
  const diskSublabel = formatCompactUsage(status?.disk_used, status?.disk_total);

  const diskMeter = (
    <CompactResourceMeter
      label={t('clients.disk')}
      percent={diskPercent}
      sublabel={diskSublabel}
      tone={resolveMeterTone(diskPercent, 85, 92)}
      interactive={multipleDisks}
      hintIcon={multipleDisks ? <CircleHelp className="size-3 shrink-0 text-muted-foreground/60" /> : undefined}
    />
  );

  return (
    <div className="overflow-hidden rounded-xl border border-border/60 bg-card shadow-2xs">
      <div className="grid grid-cols-1 divide-y divide-border/50 sm:grid-cols-2 sm:divide-y-0 xl:grid-cols-[minmax(0,1.18fr)_minmax(0,1.22fr)_minmax(0,0.78fr)_minmax(0,2.02fr)] xl:divide-x">
        {/* 1. 服务端主机与版本 */}
        <div className="flex items-center gap-3 px-4 py-3.5 sm:border-r sm:border-b sm:border-border/50 sm:px-5 xl:border-r-0 xl:border-b-0">
          <span className="flex size-8 shrink-0 items-center justify-center rounded-lg border border-border/70 bg-muted/60 text-foreground">
            <ServerIcon className="size-4" strokeWidth={1.85} />
          </span>
          <div className="min-w-0 flex-1">
            <div className="flex items-center gap-2">
              <span className="truncate text-sm font-semibold text-foreground" title={status?.hostname}>
                {status?.hostname || '-'}
              </span>
            </div>
            <div className="group/version-update mt-0.5 flex min-w-0 items-center gap-1.5 text-xs text-muted-foreground">
              <span className="shrink-0 font-mono text-foreground/85">{status?.version || '-'}</span>
              {scope ? (
                <VersionUpdateIndicator
                  scope={scope}
                  target={{
                    kind: 'server',
                    version: status?.version,
                    installMethod: status?.update_capability?.install_method,
                  }}
                  label={t('admin.serverVersion')}
                />
              ) : null}
              <span className="text-border">·</span>
              <span
                className="truncate font-mono text-[11px]"
                title={status?.go_version ? `${status?.os_arch || '-'} · ${status.go_version}` : undefined}
              >
                {status?.os_arch || '-'}
              </span>
            </div>
          </div>
        </div>

        {/* 2. 公网 / 内网 IP 与监听端口 */}
        <div className="flex flex-col justify-center gap-1 px-4 py-3.5 sm:border-b sm:border-border/50 sm:px-5 xl:border-b-0">
          <div className="flex items-center justify-between gap-2">
            <CopyableIpLine
              primary
              title={t('clients.publicIp')}
              icon={<Globe className="size-3.5" />}
              value={status?.public_ipv4 || status?.public_ipv6 || '-'}
            />
            {status?.listen_port ? (
              <span
                className="shrink-0 rounded-md border border-border/65 bg-muted/55 px-1.5 py-0.5 font-mono text-[10.5px] font-medium text-muted-foreground"
                title={t('clients.listenPort', { port: status.listen_port })}
              >
                {`:${status.listen_port}`}
              </span>
            ) : null}
          </div>
          <CopyableIpLine
            title={t('clients.privateIp')}
            icon={<Wifi className="size-3.5" />}
            value={status?.ip_address || '-'}
          />
        </div>

        {/* 3. 运行时长 */}
        <div className="flex flex-col justify-center gap-1 px-4 py-3.5 sm:border-r sm:border-border/50 sm:px-5 xl:border-r-0">
          <div className="flex items-center gap-1.5">
            <Clock className="size-3.5 shrink-0 text-muted-foreground" />
            <span className="truncate text-sm font-semibold text-foreground">
              {runtimeSeconds ? formatUptime(runtimeSeconds) : '-'}
            </span>
          </div>
          <span className="truncate text-[11.5px] text-muted-foreground">
            {t('clients.startedAt', { time: startTimeText })}
          </span>
        </div>

        {/* 4. 资源负载（CPU · 内存 · 磁盘） */}
        <div className="grid grid-cols-3 items-center gap-3.5 px-4 py-3.5 sm:gap-4 sm:px-5">
          <CompactResourceMeter
            label="CPU"
            percent={cpuPercent}
            sublabel={status?.cpu_cores ? t('clients.cpuCores', { count: status.cpu_cores }) : '-'}
            tone={resolveMeterTone(cpuPercent, 80, 92)}
          />

          <DualTriggerCard
            side="bottom"
            align="end"
            triggerClassName="block min-w-0"
            contentClassName="w-[228px] border-border/60 p-3 text-xs shadow-xl"
            triggers={
              <div>
                <CompactResourceMeter
                  label={t('clients.memory')}
                  percent={memPercent}
                  sublabel={memSublabel}
                  tone={resolveMeterTone(memPercent, 82, 92)}
                  interactive
                  hintIcon={<CircleHelp className="size-3 shrink-0 text-muted-foreground/60" />}
                />
              </div>
            }
          >
            <div className="flex flex-col gap-1.5">
              <div className="flex justify-between">
                <span className="text-muted-foreground">{t('clients.heapMemory')}</span>
                <span className="font-mono font-medium text-foreground">
                  {status?.app_mem_used ? formatBytes(status.app_mem_used) : '-'}
                </span>
              </div>
              <div className="flex justify-between">
                <span className="text-muted-foreground">{t('clients.processUsage')}</span>
                <span className="font-mono font-medium text-foreground">
                  {status?.app_mem_sys ? formatBytes(status.app_mem_sys) : '-'}
                </span>
              </div>
              <p className="border-t border-border/40 pt-1 text-[11px] text-muted-foreground/75">
                {t('clients.processUsageHelp')}
              </p>
            </div>
          </DualTriggerCard>

          {multipleDisks ? (
            <DualTriggerCard
              side="bottom"
              align="end"
              triggerClassName="block min-w-0"
              contentClassName="flex w-[min(320px,calc(100vw-2rem))] flex-col gap-3.5 border-border/60 p-4 shadow-xl"
              triggers={<div>{diskMeter}</div>}
            >
              <div className="flex items-center gap-2 border-b border-border/40 pb-2 text-xs font-semibold text-foreground">
                <HardDrive className="size-3.5" />
                {t('clients.allPartitions')}
              </div>
              <div className="flex max-h-[280px] flex-col gap-3.5 overflow-y-auto [scrollbar-width:none] [&::-webkit-scrollbar]:hidden">
                {diskPartitions.map((p, idx) => {
                  const dpPercent = p.total ? (p.used / p.total) * 100 : 0;
                  return (
                    <PartitionRow
                      key={`${p.path}-${idx}`}
                      path={p.path}
                      percent={dpPercent}
                      total={formatBytes(p.total)}
                    />
                  );
                })}
              </div>
            </DualTriggerCard>
          ) : (
            diskMeter
          )}
        </div>
      </div>
    </div>
  );
}
