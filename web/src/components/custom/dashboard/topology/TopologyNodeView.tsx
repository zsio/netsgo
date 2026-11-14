import {
  ArrowDown,
  ArrowUp,
  Box,
  ChevronRight,
  Cpu,
  GitBranchPlus,
  Laptop,
  Layers,
  Monitor,
  Plus,
  Server as ServerIcon,
  SquareArrowOutUpRight,
} from 'lucide-react';
import { useTranslation } from 'react-i18next';

import { TunnelDialog } from '@/components/custom/tunnel/TunnelDialog';
import { Button } from '@/components/ui/button';
import { formatNetSpeed } from '@/lib/format';
import type { ResourceScope } from '@/lib/resource-scope';
import { buildTunnelViewModel } from '@/lib/tunnel-model';
import { cn } from '@/lib/utils';
import type { Client } from '@/types';
import type { StructuredRouteBundleItem, StructuredRouteItem } from './topology-layout';
import type { TopologyNode, TopologyTrafficRate } from './topology-model';
import {
  STATUS_DOT,
  STATUS_PORT_BORDER,
  STATUS_TEXT,
  hasTraffic,
  rateOrZero,
  statusLabel,
} from './topology-rendering';

const CARD_SURFACE =
  'border-border/80 bg-card shadow-[0_1px_2px_rgba(15,15,15,0.045),0_2px_6px_rgba(15,15,15,0.02),inset_0_1px_0_rgba(255,255,255,0.8)] dark:border-border/70 dark:shadow-[0_1px_3px_rgba(0,0,0,0.32),inset_0_1px_0_rgba(255,255,255,0.045)]';

function PortSocket({
  portKey,
  side,
  borderClass,
  highlighted = false,
}: {
  portKey: string;
  side: 'left' | 'right';
  borderClass: string;
  highlighted?: boolean;
}) {
  return (
    <span
      data-port={portKey}
      aria-hidden
      className={cn(
        'pointer-events-none absolute top-1/2 z-20 size-[7.5px] -translate-y-1/2 rounded-full border-[1.5px] bg-card ring-2 ring-card transition-all duration-150',
        side === 'left' ? '-left-[4px]' : '-right-[4px]',
        borderClass,
        highlighted && 'scale-125 bg-brand-soft ring-brand-soft',
      )}
    />
  );
}

function FlowBridgeArrow({ className }: { className?: string }) {
  return (
    <svg
      width="18"
      height="8"
      viewBox="0 0 18 8"
      fill="none"
      aria-hidden
      className={cn('shrink-0', className)}
    >
      <path
        d="M1 4H15.5M12.5 1.25L16 4L12.5 6.75"
        stroke="currentColor"
        strokeWidth="1.3"
        strokeLinecap="round"
        strokeLinejoin="round"
      />
    </svg>
  );
}

function DeviceAvatar({
  client,
  label,
  online,
  standby = false,
  compact = false,
}: {
  client: Client | undefined;
  label: string;
  online: boolean;
  standby?: boolean;
  compact?: boolean;
}) {
  const lowerName = `${label} ${client?.info.hostname ?? ''}`.toLowerCase();
  const os = (client?.info.os ?? '').toLowerCase();
  const isContainer =
    lowerName.includes('docker') ||
    lowerName.includes('orbstack') ||
    lowerName.includes('container') ||
    lowerName.includes('pod');

  const Icon = isContainer
    ? Box
    : os === 'darwin'
      ? Laptop
      : os === 'windows'
        ? Monitor
        : Cpu;

  return (
    <span
      className={cn(
        'relative flex shrink-0 items-center justify-center rounded-lg border transition-colors',
        compact ? 'size-7' : 'size-8',
        online && !standby
          ? 'border-border/75 bg-muted/55 text-foreground/80 shadow-[inset_0_1px_0_rgba(255,255,255,0.7)] dark:shadow-none'
          : 'border-border/55 bg-muted/30 text-muted-foreground/65',
      )}
    >
      <Icon className={compact ? 'size-3.5' : 'size-4'} strokeWidth={1.75} />
      <span
        className={cn(
          'absolute -right-0.5 -bottom-0.5 size-2.5 rounded-full ring-2 ring-card',
          online ? 'bg-status-online' : 'bg-muted-foreground/45',
        )}
      />
    </span>
  );
}

function MicroLoadMeter({
  shortLabel,
  fullLabel,
  percent,
}: {
  shortLabel: string;
  fullLabel: string;
  percent: number;
}) {
  const warn = percent >= 85;
  return (
    <span className="inline-flex items-center gap-1" title={`${fullLabel} ${percent}%`}>
      <span className="font-sans text-[10px] font-medium text-muted-foreground/60">
        {shortLabel}
      </span>
      <span className="h-1 w-4 overflow-hidden rounded-full bg-muted">
        <span
          className={cn(
            'block h-full rounded-full transition-all duration-500',
            warn ? 'bg-status-warning' : 'bg-meter-track',
          )}
          style={{ width: `${Math.max(percent > 0 ? 14 : 0, percent)}%` }}
        />
      </span>
      <span
        className={cn(
          'w-[26px] text-right font-mono text-[10.5px] tabular-nums',
          warn ? 'font-medium text-status-warning-fg' : 'text-muted-foreground/85',
        )}
      >
        {percent}%
      </span>
    </span>
  );
}

function protocolBadgeClass(proto: string): string {
  const upper = proto.toUpperCase();
  if (upper === 'HTTP' || upper === 'TLS') {
    return 'border-brand-soft-border bg-brand-soft/75 text-brand-strong';
  }
  return 'border-border/75 bg-muted/65 text-foreground/80';
}

function compactDestinationLabel(label: string): string {
  return label === 'Dynamic SOCKS5 CONNECT' ? 'SOCKS5' : label;
}

export function ServerHubCard({
  serverEndpoint,
  onlineClientCount,
  totalClientCount,
  serverExposeCount,
  dimmed,
  highlighted,
  onHover,
}: {
  serverEndpoint: string;
  onlineClientCount: number;
  totalClientCount: number;
  serverExposeCount: number;
  dimmed: boolean;
  highlighted: boolean;
  onHover: (hovering: boolean) => void;
}) {
  const { t } = useTranslation();

  return (
    <div
      onMouseEnter={() => onHover(true)}
      onMouseLeave={() => onHover(false)}
      className={cn(
        'relative z-10 rounded-xl border bg-linear-to-b from-brand-soft/40 via-card via-45% to-card p-3.5 transition-all duration-150',
        CARD_SURFACE,
        highlighted
          ? 'border-primary/55 ring-2 ring-brand-soft'
          : 'hover:border-brand-soft-border',
        dimmed && 'opacity-40',
      )}
    >
      <PortSocket
        portKey="server-out"
        side="right"
        borderClass={
          highlighted
            ? 'border-primary'
            : onlineClientCount > 0
              ? 'border-status-online'
              : 'border-muted-foreground/55'
        }
        highlighted={highlighted}
      />

      <div className="flex items-start gap-2.5">
        <span className="relative flex size-8.5 shrink-0 items-center justify-center rounded-lg border border-brand-soft-border bg-brand-soft text-primary shadow-[inset_0_1px_0_rgba(255,255,255,0.65)] dark:shadow-none">
          <ServerIcon className="size-4" strokeWidth={1.85} />
          <span className="absolute -right-0.5 -bottom-0.5 size-2.5 rounded-full bg-status-online ring-2 ring-card" />
        </span>
        <div className="min-w-0 flex-1">
          <div className="flex items-center justify-between gap-1.5">
            <span className="truncate text-[12.5px] font-semibold tracking-[-0.01em] text-foreground">
              {t('dashboard.topologyServerHub')}
            </span>
            <span className="shrink-0 rounded-full border border-status-online-border bg-status-online-soft px-2 py-0.5 text-[10px] font-medium text-status-online-fg">
              {t('dashboard.topologyServerOnline')}
            </span>
          </div>
          {serverEndpoint ? (
            <div
              className="mt-0.5 truncate font-mono text-[11px] text-muted-foreground"
              title={serverEndpoint}
            >
              {serverEndpoint}
            </div>
          ) : null}
        </div>
      </div>

      <div className="mt-3 grid grid-cols-2 divide-x divide-border/60 rounded-lg border border-border/55 bg-muted/35 py-2">
        <div className="px-2.5">
          <div className="text-[10px] text-muted-foreground">
            {t('dashboard.topologyConnectedNodes')}
          </div>
          <div className="mt-0.5 font-mono text-[12.5px] font-semibold tabular-nums text-foreground">
            {onlineClientCount}
            <span className="font-normal text-muted-foreground">/{totalClientCount}</span>
          </div>
        </div>
        <div className="px-2.5">
          <div className="text-[10px] text-muted-foreground">
            {t('dashboard.topologyPublicRoutes')}
          </div>
          <div className="mt-0.5 font-mono text-[12.5px] font-semibold tabular-nums text-foreground">
            {serverExposeCount}
          </div>
        </div>
      </div>
    </div>
  );
}

export function PeerIngressCard({
  node,
  routeCount = 0,
  compact = false,
  focused,
  dimmed,
  highlighted,
  onClick,
  onHover,
}: {
  node: TopologyNode;
  routeCount?: number;
  compact?: boolean;
  focused: boolean;
  dimmed: boolean;
  highlighted: boolean;
  onClick: () => void;
  onHover: (hovering: boolean) => void;
}) {
  const { t } = useTranslation();
  const clientIp = node.client?.info.ip || node.client?.last_ip;

  return (
    <div
      role="button"
      tabIndex={0}
      onClick={onClick}
      onKeyDown={(e) => {
        if (e.key === 'Enter' || e.key === ' ') {
          e.preventDefault();
          onClick();
        }
      }}
      onMouseEnter={() => onHover(true)}
      onMouseLeave={() => onHover(false)}
      className={cn(
        'relative z-10 flex cursor-pointer items-center gap-2.5 rounded-xl border text-left transition-all duration-150',
        CARD_SURFACE,
        compact ? 'px-2.5 py-2' : 'px-3 py-2.5',
        focused || highlighted
          ? 'border-primary/55 ring-2 ring-brand-soft'
          : 'hover:border-brand-soft-border',
        dimmed && 'opacity-40',
      )}
    >
      <PortSocket
        portKey={`peer-out:${node.id}`}
        side="right"
        borderClass={
          highlighted || focused
            ? 'border-primary'
            : node.online
              ? 'border-status-online'
              : 'border-status-warning'
        }
        highlighted={highlighted || focused}
      />

      <DeviceAvatar client={node.client} label={node.label} online={node.online} compact />

      <div className="min-w-0 flex-1">
        <div className="flex items-center justify-between gap-1.5">
          <span
            className="min-w-0 truncate text-[12px] font-semibold tracking-[-0.01em] text-foreground"
            title={node.label}
          >
            {node.label}
          </span>
          <span className="shrink-0 rounded-md border border-brand-soft-border bg-brand-soft/65 px-1.5 py-px font-mono text-[10px] font-semibold tabular-nums text-brand-strong">
            {routeCount > 0 ? `C2C · ${routeCount}` : t('dashboard.topologyPeerBadge')}
          </span>
        </div>
        {clientIp ? (
          <div className="mt-0.5 truncate font-mono text-[10.5px] text-muted-foreground">
            {clientIp}
          </div>
        ) : null}
      </div>
    </div>
  );
}

export function TunnelRouteCard({
  route,
  trafficRate,
  compact = false,
  dimmed,
  highlighted,
  onClick,
  onHover,
}: {
  route: StructuredRouteItem;
  trafficRate: TopologyTrafficRate | undefined;
  compact?: boolean;
  dimmed: boolean;
  highlighted: boolean;
  onClick: () => void;
  onHover: (hovering: boolean) => void;
}) {
  const { t } = useTranslation();
  const { edge } = route;
  const view = buildTunnelViewModel(edge.tunnel, edge.status.key !== 'offline');
  const rate = rateOrZero(trafficRate);
  const active = hasTraffic(trafficRate);
  const portBorder = STATUS_PORT_BORDER[edge.status.key];
  const shortDest = compactDestinationLabel(view.destinationLabel);
  const isDirectPolicy =
    route.kind === 'client_to_client' &&
    (edge.tunnel.transport?.policy === 'direct_preferred' ||
      edge.tunnel.transport_policy === 'direct_preferred' ||
      edge.tunnel.transport?.policy === 'direct_only' ||
      edge.tunnel.transport_policy === 'direct_only');

  return (
    <div
      role="button"
      tabIndex={0}
      onClick={onClick}
      onKeyDown={(e) => {
        if (e.key === 'Enter' || e.key === ' ') {
          e.preventDefault();
          onClick();
        }
      }}
      onMouseEnter={() => onHover(true)}
      onMouseLeave={() => onHover(false)}
      title={
        route.kind === 'client_to_client'
          ? `${edge.tunnel.name} · ${view.transportLabel}`
          : edge.tunnel.name
      }
      className={cn(
        'relative z-10 flex cursor-pointer flex-col rounded-xl border text-left transition-all duration-150 xl:flex-row xl:items-center xl:gap-3',
        CARD_SURFACE,
        compact ? 'gap-1.5 px-3 py-2' : 'gap-2 px-3.5 py-2.5',
        highlighted
          ? 'border-primary/55 ring-2 ring-brand-soft'
          : 'hover:border-brand-soft-border',
        dimmed && 'opacity-35',
      )}
    >
      <PortSocket
        portKey={route.routeInPortKey}
        side="left"
        borderClass={highlighted ? 'border-primary' : portBorder}
        highlighted={highlighted}
      />
      <PortSocket
        portKey={route.routeOutPortKey}
        side="right"
        borderClass={highlighted ? 'border-primary' : portBorder}
        highlighted={highlighted}
      />

      {/* 左段：协议徽标 + 规则名称（桌面端固定基准列宽，使中段流向胶囊纵向严格对齐） */}
      <div className="flex min-w-0 items-center gap-2 xl:w-[164px] xl:shrink-0">
        <span
          className={cn(
            'inline-flex h-[18px] shrink-0 items-center rounded-md border px-1.5 font-mono text-[10px] font-semibold tracking-wider uppercase leading-none',
            protocolBadgeClass(edge.tunnel.type),
          )}
        >
          {edge.tunnel.type}
        </span>
        <span className="min-w-0 flex-1 truncate text-[12.5px] font-semibold tracking-[-0.01em] text-foreground">
          {edge.tunnel.name}
        </span>
        <span
          className={cn(
            'ml-auto inline-flex shrink-0 items-center gap-1.5 text-[11px] font-medium xl:hidden',
            STATUS_TEXT[edge.status.key],
          )}
        >
          <span className={cn('size-1.5 rounded-full', STATUS_DOT[edge.status.key])} />
          {statusLabel(t, edge.status)}
        </span>
      </div>

      {/* 中段：流向桥接胶囊（入口 ──▶ 目标） */}
      <div className="flex min-w-0 flex-1 items-center gap-2">
        <div className="inline-flex max-w-full min-w-0 items-center gap-2 rounded-lg border border-border/55 bg-muted/40 px-2.5 py-1 font-mono text-[11px]">
          <span className="min-w-0 truncate font-medium text-foreground/90" title={view.targetLabel}>
            {view.targetLabel}
          </span>
          <FlowBridgeArrow
            className={
              edge.status.key === 'exposed'
                ? 'text-primary/80'
                : edge.status.key === 'offline'
                  ? 'text-status-warning/80'
                  : 'text-muted-foreground/55'
            }
          />
          <span className="min-w-0 truncate text-muted-foreground" title={view.destinationLabel}>
            {shortDest}
          </span>
        </div>

        {isDirectPolicy ? (
          <span
            className="hidden shrink-0 rounded-md border border-border/60 bg-muted/45 px-1.5 py-0.5 text-[10px] font-medium text-muted-foreground 2xl:inline-flex"
            title={view.transportLabel}
          >
            P2P
          </span>
        ) : null}
      </div>

      {/* 右段：实时流速 + 状态锚点 */}
      <div className="hidden shrink-0 items-center gap-2.5 xl:flex">
        {active ? (
          <span className="inline-flex items-center gap-2 font-mono text-[10.5px] font-medium tabular-nums">
            <span className="inline-flex items-center gap-0.5 text-flow-down-fg">
              <ArrowDown className="size-2.5" />
              {formatNetSpeed(rate.ingressBps)}
            </span>
            <span className="inline-flex items-center gap-0.5 text-flow-up-fg">
              <ArrowUp className="size-2.5" />
              {formatNetSpeed(rate.egressBps)}
            </span>
          </span>
        ) : edge.status.key === 'error' && edge.status.description ? (
          <span
            className="max-w-[132px] truncate text-[10.5px] text-status-danger-fg"
            title={edge.status.description}
          >
            {edge.status.description}
          </span>
        ) : null}

        <span
          className={cn(
            'inline-flex items-center gap-1.5 text-[11px] font-medium',
            STATUS_TEXT[edge.status.key],
          )}
        >
          <span className={cn('size-1.5 rounded-full', STATUS_DOT[edge.status.key])} />
          {statusLabel(t, edge.status)}
        </span>
      </div>
    </div>
  );
}

export function TunnelBundleCard({
  bundle,
  compact = false,
  dimmed,
  highlighted,
  onClick,
  onHover,
}: {
  bundle: StructuredRouteBundleItem;
  compact?: boolean;
  dimmed: boolean;
  highlighted: boolean;
  onClick: () => void;
  onHover: (hovering: boolean) => void;
}) {
  const { t } = useTranslation();
  const portBorder = STATUS_PORT_BORDER[bundle.statusSummaryKey];
  const active = hasTraffic(bundle.totalRate);
  const previewRoutes = bundle.routes.slice(0, 2);
  const extraCount = Math.max(0, bundle.routes.length - previewRoutes.length);
  const protoSummary = bundle.protocolCounts.map((item) => `${item.type} ×${item.count}`).join(' · ');

  return (
    <div
      className={cn(
        'group relative z-10 pb-1.5 transition-opacity duration-150',
        dimmed && 'opacity-35',
      )}
    >
      {/* 物理叠牌底层叠影 2 */}
      <span
        aria-hidden
        className={cn(
          'pointer-events-none absolute inset-x-3 bottom-0 h-2.5 rounded-b-xl border border-t-0 transition-colors duration-150',
          highlighted ? 'border-primary/25 bg-brand-soft/35' : 'border-border/50 bg-muted/55',
        )}
      />
      {/* 物理叠牌底层叠影 1 */}
      <span
        aria-hidden
        className={cn(
          'pointer-events-none absolute inset-x-1.5 bottom-[3px] h-2.5 rounded-b-xl border border-t-0 bg-card/90 transition-colors duration-150',
          highlighted ? 'border-primary/40' : 'border-border/70',
        )}
      />

      {/* 顶层主牌 */}
      <div
        role="button"
        tabIndex={0}
        onClick={onClick}
        onKeyDown={(e) => {
          if (e.key === 'Enter' || e.key === ' ') {
            e.preventDefault();
            onClick();
          }
        }}
        onMouseEnter={() => onHover(true)}
        onMouseLeave={() => onHover(false)}
        title={t('dashboard.topologyBundleExpand')}
        className={cn(
          'relative z-10 flex cursor-pointer flex-col rounded-xl border text-left transition-all duration-150 xl:flex-row xl:items-center xl:gap-3',
          CARD_SURFACE,
          compact ? 'gap-1.5 px-3 py-2' : 'gap-2 px-3.5 py-2.5',
          highlighted
            ? 'border-primary/55 ring-2 ring-brand-soft'
            : 'group-hover:border-brand-soft-border',
        )}
      >
        <PortSocket
          portKey={bundle.routeInPortKey}
          side="left"
          borderClass={highlighted ? 'border-primary' : portBorder}
          highlighted={highlighted}
        />
        <PortSocket
          portKey={bundle.routeOutPortKey}
          side="right"
          borderClass={highlighted ? 'border-primary' : portBorder}
          highlighted={highlighted}
        />

        {/* 左段：聚合栈徽章 + 规则组名（与单条规则卡保持相同 164px 基准对齐线） */}
        <div className="flex min-w-0 items-center gap-2 xl:w-[164px] xl:shrink-0">
          <span className="inline-flex size-5 shrink-0 items-center justify-center rounded-md border border-brand-soft-border bg-brand-soft text-primary">
            <Layers className="size-3" />
          </span>
          <span className="min-w-0 flex-1 truncate text-[12.5px] font-semibold tracking-[-0.01em] text-foreground">
            {bundle.kind === 'server_expose'
              ? t('dashboard.topologyBundlePublic', { count: bundle.routes.length })
              : t('dashboard.topologyBundlePeer', { count: bundle.routes.length })}
          </span>
          <span className="ml-auto inline-flex shrink-0 items-center gap-1 text-[11px] font-medium xl:hidden">
            <span className={cn('size-1.5 rounded-full', STATUS_DOT[bundle.statusSummaryKey])} />
            <ChevronRight className="size-3.5 text-muted-foreground" />
          </span>
        </div>

        {/* 中段：预览端点胶囊 + 协议构成 */}
        <div className="flex min-w-0 flex-1 items-center gap-2">
          <div className="inline-flex max-w-full min-w-0 items-center gap-1.5 rounded-lg border border-border/55 bg-muted/40 px-2.5 py-1 font-mono text-[11px]">
            {previewRoutes.map((r, idx) => {
              const v = buildTunnelViewModel(r.edge.tunnel, r.edge.status.key !== 'offline');
              return (
                <span key={r.id} className="inline-flex min-w-0 items-center gap-1.5">
                  {idx > 0 ? <span className="text-border">·</span> : null}
                  <span
                    className="max-w-[140px] truncate font-medium text-foreground/90"
                    title={`${r.edge.tunnel.name}: ${v.routeLabel}`}
                  >
                    {v.targetLabel}
                  </span>
                </span>
              );
            })}
            {extraCount > 0 ? (
              <span className="shrink-0 font-sans text-[10.5px] text-muted-foreground">
                {t('dashboard.topologyBundleMore', { count: extraCount })}
              </span>
            ) : null}
          </div>

          {protoSummary ? (
            <span className="hidden shrink-0 font-mono text-[10px] text-muted-foreground/75 2xl:inline">
              {protoSummary}
            </span>
          ) : null}
        </div>

        {/* 右段：聚合速率 + 状态 + 展开箭头 */}
        <div className="hidden shrink-0 items-center gap-2 xl:flex">
          {active ? (
            <span className="inline-flex items-center gap-2 font-mono text-[10.5px] font-medium tabular-nums">
              <span className="inline-flex items-center gap-0.5 text-flow-down-fg">
                <ArrowDown className="size-2.5" />
                {formatNetSpeed(bundle.totalRate.ingressBps)}
              </span>
              <span className="inline-flex items-center gap-0.5 text-flow-up-fg">
                <ArrowUp className="size-2.5" />
                {formatNetSpeed(bundle.totalRate.egressBps)}
              </span>
            </span>
          ) : null}

          {bundle.errorCount > 0 ? (
            <span className="inline-flex items-center gap-1.5 text-[11px] font-medium text-status-danger-fg">
              <span className="size-1.5 rounded-full bg-status-danger" />
              {t('dashboard.topologyBundleErrors', { count: bundle.errorCount })}
            </span>
          ) : (
            <span
              className={cn(
                'inline-flex items-center gap-1.5 text-[11px] font-medium',
                STATUS_TEXT[bundle.statusSummaryKey],
              )}
            >
              <span className={cn('size-1.5 rounded-full', STATUS_DOT[bundle.statusSummaryKey])} />
              {statusLabel(t, { key: bundle.statusSummaryKey, label: bundle.statusSummaryKey })}
            </span>
          )}
          <ChevronRight className="size-3.5 text-muted-foreground/65 transition-transform group-hover:translate-x-0.5 group-hover:text-foreground" />
        </div>
      </div>
    </div>
  );
}

export function ControlBridgeCard({
  scope,
  clients,
  defaultClientId,
  hasOnlineClient,
}: {
  scope: ResourceScope;
  clients: Client[];
  defaultClientId: string | null;
  hasOnlineClient: boolean;
}) {
  const { t } = useTranslation();
  const socketClass = hasOnlineClient ? 'border-status-online' : 'border-muted-foreground/55';

  return (
    <div className="relative z-10 rounded-xl border border-dashed border-border/85 bg-card/90 px-4 py-3.5 shadow-2xs">
      <PortSocket portKey="bridge-in" side="left" borderClass={socketClass} />
      <PortSocket portKey="bridge-out" side="right" borderClass={socketClass} />

      <div className="flex items-center gap-2">
        <span
          className={cn(
            'size-2 shrink-0 rounded-full',
            hasOnlineClient ? 'bg-status-online' : 'bg-muted-foreground/50',
          )}
        />
        <span className="text-[12.5px] font-semibold text-foreground">
          {t('dashboard.topologyControlReadyTitle')}
        </span>
      </div>

      <p className="mt-1 text-[11px] leading-relaxed text-muted-foreground">
        {t('dashboard.topologyControlReadyDesc')}
      </p>

      {defaultClientId ? (
        <div className="mt-3">
          <TunnelDialog
            scope={scope}
            mode="create"
            clientId={defaultClientId}
            clients={clients}
            trigger={
              <Button type="button" variant="secondary" size="sm" className="h-7 gap-1.5 px-2.5 text-xs">
                <GitBranchPlus className="size-3.5" />
                {t('dashboard.topologyCreateFirstTunnel')}
              </Button>
            }
          />
        </div>
      ) : null}
    </div>
  );
}

export function IdleControlRow({
  unroutedCount,
  hasOnlineUnrouted,
  showIngressPort = true,
  dimmed,
  highlighted,
  onHover,
}: {
  unroutedCount: number;
  hasOnlineUnrouted: boolean;
  showIngressPort?: boolean;
  dimmed: boolean;
  highlighted: boolean;
  onHover: (hovering: boolean) => void;
}) {
  const { t } = useTranslation();
  const socketClass = highlighted
    ? 'border-primary'
    : hasOnlineUnrouted
      ? 'border-status-online/80'
      : 'border-muted-foreground/55';

  return (
    <div
      onMouseEnter={() => onHover(true)}
      onMouseLeave={() => onHover(false)}
      className={cn(
        'relative z-10 flex items-center justify-between gap-2 rounded-xl border border-dashed bg-card/70 px-3.5 py-2 transition-all duration-150',
        highlighted ? 'border-primary/50 bg-brand-soft/40' : 'border-border/70',
        dimmed && 'opacity-35',
      )}
    >
      <PortSocket
        portKey="idle-in"
        side="left"
        borderClass={cn(socketClass, !showIngressPort && 'opacity-0')}
        highlighted={highlighted}
      />
      <PortSocket portKey="idle-out" side="right" borderClass={socketClass} highlighted={highlighted} />

      <span className="inline-flex items-center gap-2 text-[11px] font-medium text-muted-foreground">
        <span
          className={cn(
            'size-1.5 rounded-full',
            hasOnlineUnrouted ? 'bg-status-online/85' : 'bg-muted-foreground/50',
          )}
        />
        {t('dashboard.topologyIdleControlTitle')}
      </span>
      <span className="font-mono text-[11px] tabular-nums text-muted-foreground/80">
        {t('dashboard.topologyIdleControlCount', { count: unroutedCount })}
      </span>
    </div>
  );
}

export function ClientNodeCard({
  scope,
  node,
  allClients,
  tunnelCount,
  compact = false,
  focused,
  dimmed,
  highlighted,
  onToggleFocus,
  onOpenDetail,
  onHover,
}: {
  scope: ResourceScope;
  node: TopologyNode;
  allClients: Client[];
  tunnelCount: number;
  compact?: boolean;
  focused: boolean;
  dimmed: boolean;
  highlighted: boolean;
  onToggleFocus: () => void;
  onOpenDetail: () => void;
  onHover: (hovering: boolean) => void;
}) {
  const { t } = useTranslation();
  const client = node.client;
  const stats = node.online ? client?.stats : null;
  const clientIp = client?.info.ip || client?.last_ip;
  const osName = client?.info.os ?? '-';
  const cpuPercent = stats ? Math.min(100, Math.max(0, Math.round(stats.cpu_usage))) : 0;
  const memPercent = stats ? Math.min(100, Math.max(0, Math.round(stats.mem_usage))) : 0;
  const isStandby = tunnelCount === 0;

  return (
    <div
      role="button"
      tabIndex={0}
      onClick={onToggleFocus}
      onKeyDown={(e) => {
        if (e.key === 'Enter' || e.key === ' ') {
          e.preventDefault();
          onToggleFocus();
        }
      }}
      onMouseEnter={() => onHover(true)}
      onMouseLeave={() => onHover(false)}
      aria-pressed={focused}
      className={cn(
        'group/client relative z-10 flex cursor-pointer items-center gap-2.5 rounded-xl border text-left transition-all duration-150',
        compact ? 'px-3 py-2' : 'px-3.5 py-2.5',
        focused
          ? cn(CARD_SURFACE, 'border-primary/65 ring-2 ring-brand-soft')
          : highlighted
            ? cn(CARD_SURFACE, 'border-primary/50 ring-2 ring-brand-soft')
            : isStandby
              ? 'border-border/60 bg-card/65 hover:border-brand-soft-border hover:bg-card'
              : cn(CARD_SURFACE, 'hover:border-brand-soft-border'),
        dimmed && 'opacity-35',
      )}
    >
      <PortSocket
        portKey={`client-in:${node.id}`}
        side="left"
        borderClass={
          focused || highlighted
            ? 'border-primary'
            : node.online
              ? 'border-status-online'
              : 'border-muted-foreground/55'
        }
        highlighted={focused || highlighted}
      />

      <DeviceAvatar
        client={client}
        label={node.label}
        online={node.online}
        standby={isStandby}
        compact={compact}
      />

      <div className="min-w-0 flex-1">
        <div className="flex items-center gap-1.5">
          <span
            className={cn(
              'min-w-0 flex-1 truncate text-[12.5px] font-semibold leading-snug tracking-[-0.01em]',
              node.online ? 'text-foreground' : 'text-foreground/75',
            )}
            title={node.label}
          >
            {node.label}
          </span>

          {focused ? (
            <span className="shrink-0 rounded-full border border-brand-soft-border bg-brand-soft px-1.5 py-px text-[10px] font-medium text-brand-strong">
              {t('dashboard.topologyFocusedBadge')}
            </span>
          ) : null}

          <div
            className="flex shrink-0 items-center gap-0.5 opacity-0 transition-opacity duration-150 group-focus-within/client:opacity-100 group-hover/client:opacity-100 [@media(hover:none)]:opacity-100"
            onClick={(e) => e.stopPropagation()}
            onKeyDown={(e) => e.stopPropagation()}
          >
            <TunnelDialog
              scope={scope}
              mode="create"
              clientId={node.id}
              clients={allClients}
              trigger={
                <button
                  type="button"
                  className="flex size-5.5 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
                  title={t('tunnels.addTunnel')}
                  aria-label={t('tunnels.addTunnel')}
                >
                  <Plus className="size-3.5" />
                </button>
              }
            />
            <button
              type="button"
              className="flex size-5.5 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
              onClick={onOpenDetail}
              title={t('dashboard.viewDetails')}
              aria-label={t('dashboard.viewDetails')}
            >
              <SquareArrowOutUpRight className="size-3" />
            </button>
          </div>

          <span
            className={cn(
              'shrink-0 rounded-md px-1.5 py-0.5 text-[10px] font-medium tabular-nums',
              tunnelCount > 0
                ? 'border border-border/65 bg-muted/65 text-foreground/80'
                : 'border border-dashed border-border/65 text-muted-foreground/65',
            )}
          >
            {t('dashboard.topologyRouteCountBadge', { count: tunnelCount })}
          </span>
        </div>

        <div className="mt-1 flex items-center justify-between gap-2">
          <span className="min-w-0 truncate font-mono text-[11px] leading-none tabular-nums text-muted-foreground">
            {clientIp || osName}
          </span>

          {node.online && stats ? (
            <span className="flex shrink-0 items-center gap-2 leading-none">
              <MicroLoadMeter shortLabel="C" fullLabel="CPU" percent={cpuPercent} />
              <MicroLoadMeter shortLabel="M" fullLabel="MEM" percent={memPercent} />
            </span>
          ) : !node.online ? (
            <span className="shrink-0 text-[10.5px] leading-none text-status-warning-fg/90">
              {clientIp && client ? `${osName} · ` : ''}
              {t('tunnels.statusOffline')}
            </span>
          ) : clientIp && client ? (
            <span className="shrink-0 font-mono text-[10.5px] leading-none text-muted-foreground/70">
              {osName}
            </span>
          ) : null}
        </div>
      </div>
    </div>
  );
}
