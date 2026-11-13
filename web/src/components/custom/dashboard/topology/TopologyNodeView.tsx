import {
  ArrowDown,
  ArrowRight,
  ArrowUp,
  ChevronRight,
  GitBranchPlus,
  Layers,
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
        'pointer-events-none absolute top-1/2 z-20 size-2.5 -translate-y-1/2 rounded-full border-2 bg-card transition-transform duration-200',
        side === 'left' ? '-left-[5px]' : '-right-[5px]',
        borderClass,
        highlighted && 'scale-125',
      )}
    />
  );
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
        'relative z-10 rounded-xl border bg-card p-3.5 shadow-2xs transition-all duration-200',
        highlighted
          ? 'border-primary/55 ring-2 ring-brand-soft'
          : 'border-border/80 hover:border-brand-soft-border',
        dimmed && 'opacity-40',
      )}
    >
      <PortSocket
        portKey="server-out"
        side="right"
        borderClass={highlighted ? 'border-primary' : onlineClientCount > 0 ? 'border-status-online' : 'border-muted-foreground/55'}
        highlighted={highlighted}
      />

      <div className="flex items-start gap-2.5">
        <span className="flex size-8 shrink-0 items-center justify-center rounded-lg border border-brand-soft-border bg-brand-soft text-brand-strong">
          <ServerIcon className="size-4" strokeWidth={1.85} />
        </span>
        <div className="min-w-0 flex-1">
          <div className="flex items-center justify-between gap-1.5">
            <span className="truncate text-xs font-semibold text-foreground">
              {t('dashboard.topologyServerHub')}
            </span>
            <span className="inline-flex shrink-0 items-center gap-1 rounded-full border border-status-online-border bg-status-online-soft px-2 py-0.5 text-[10px] font-medium text-status-online-fg">
              <span className="size-1.5 rounded-full bg-status-online" />
              {t('dashboard.topologyServerOnline')}
            </span>
          </div>
          {serverEndpoint ? (
            <div className="mt-0.5 truncate font-mono text-[11px] text-muted-foreground" title={serverEndpoint}>
              {serverEndpoint}
            </div>
          ) : null}
        </div>
      </div>

      <div className="mt-3 grid grid-cols-2 gap-2 border-t border-border/50 pt-2.5">
        <div>
          <div className="text-[10px] text-muted-foreground">{t('dashboard.topologyConnectedNodes')}</div>
          <div className="mt-0.5 font-mono text-xs font-semibold text-foreground">
            {onlineClientCount}
            <span className="font-normal text-muted-foreground">/{totalClientCount}</span>
          </div>
        </div>
        <div>
          <div className="text-[10px] text-muted-foreground">{t('dashboard.topologyPublicRoutes')}</div>
          <div className="mt-0.5 font-mono text-xs font-semibold text-foreground">
            {serverExposeCount}
          </div>
        </div>
      </div>
    </div>
  );
}

export function PeerIngressCard({
  node,
  compact = false,
  focused,
  dimmed,
  highlighted,
  onClick,
  onHover,
}: {
  node: TopologyNode;
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
        'relative z-10 cursor-pointer rounded-xl border bg-card text-left shadow-2xs transition-all duration-200',
        compact ? 'px-3 py-2' : 'px-3.5 py-2.5',
        focused || highlighted
          ? 'border-primary/55 ring-2 ring-brand-soft'
          : 'border-border/80 hover:border-brand-soft-border',
        dimmed && 'opacity-40',
      )}
    >
      <PortSocket
        portKey={`peer-out:${node.id}`}
        side="right"
        borderClass={highlighted || focused ? 'border-primary' : node.online ? 'border-status-online' : 'border-status-warning'}
        highlighted={highlighted || focused}
      />

      <div className="flex items-center gap-2">
        <span
          className={cn(
            'size-2 shrink-0 rounded-full',
            node.online ? 'bg-status-online' : 'bg-muted-foreground/50',
          )}
        />
        <span className="min-w-0 flex-1 truncate text-xs font-semibold text-foreground">
          {node.label}
        </span>
        <span className="shrink-0 rounded border border-border/60 bg-muted/60 px-1.5 py-0.5 text-[9.5px] font-medium text-muted-foreground">
          {t('dashboard.topologyPeerBadge')}
        </span>
      </div>
      {clientIp ? (
        <div className="mt-1 truncate pl-4 font-mono text-[10.5px] text-muted-foreground">
          {clientIp}
        </div>
      ) : null}
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
        'relative z-10 flex cursor-pointer flex-col rounded-xl border bg-card text-left shadow-2xs transition-all duration-200',
        compact ? 'gap-1 px-3 py-2' : 'gap-1.5 px-3.5 py-2.5',
        highlighted
          ? 'border-primary/55 ring-2 ring-brand-soft'
          : 'border-border/80 hover:border-brand-soft-border',
        dimmed && 'opacity-35',
      )}
    >
      <PortSocket portKey={route.routeInPortKey} side="left" borderClass={highlighted ? 'border-primary' : portBorder} highlighted={highlighted} />
      <PortSocket portKey={route.routeOutPortKey} side="right" borderClass={highlighted ? 'border-primary' : portBorder} highlighted={highlighted} />

      <div className="flex min-w-0 items-center gap-1.5">
        <span className="shrink-0 rounded-md border border-border/70 bg-muted/70 px-1.5 py-0.5 font-mono text-[10px] font-semibold uppercase leading-none text-foreground/85">
          {edge.tunnel.type}
        </span>
        <span className="min-w-0 truncate text-xs font-semibold text-foreground" title={edge.tunnel.name}>
          {edge.tunnel.name}
        </span>
        {route.kind === 'client_to_client' ? (
          <span className="shrink-0 rounded border border-border/50 bg-muted/40 px-1.5 py-0.5 text-[9.5px] text-muted-foreground">
            {view.transportLabel}
          </span>
        ) : null}
        <span className={cn('ml-auto inline-flex shrink-0 items-center gap-1 text-[10.5px] font-medium', STATUS_TEXT[edge.status.key])}>
          <span className={cn('size-1.5 rounded-full', STATUS_DOT[edge.status.key])} />
          {statusLabel(t, edge.status)}
        </span>
      </div>

      <div
        className={cn(
          'flex flex-wrap items-center gap-x-2 gap-y-1 font-mono text-[11px]',
          compact ? 'px-0.5 pt-0.5' : 'rounded-lg bg-muted/45 px-2.5 py-1.5',
        )}
      >
        <span className="min-w-0 truncate font-medium text-foreground/90" title={view.targetLabel}>
          {view.targetLabel}
        </span>
        <ArrowRight className="size-3 shrink-0 text-muted-foreground/70" />
        <span className="min-w-0 truncate text-muted-foreground" title={view.destinationLabel}>
          {view.destinationLabel}
        </span>

        {active ? (
          <span className="ml-auto inline-flex shrink-0 items-center gap-2 text-[10px] font-medium">
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
          <span className="ml-auto max-w-[160px] truncate font-sans text-[10px] text-status-danger-fg" title={edge.status.description}>
            {edge.status.description}
          </span>
        ) : null}
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
      title={t('dashboard.topologyBundleExpand')}
      className={cn(
        'group relative z-10 flex cursor-pointer flex-col rounded-xl border bg-card text-left shadow-2xs transition-all duration-200',
        compact ? 'gap-1 px-3 py-2' : 'gap-1.5 px-3.5 py-2.5',
        highlighted
          ? 'border-primary/55 ring-2 ring-brand-soft'
          : 'border-border/80 hover:border-brand-soft-border',
        dimmed && 'opacity-35',
      )}
    >
      <PortSocket portKey={bundle.routeInPortKey} side="left" borderClass={highlighted ? 'border-primary' : portBorder} highlighted={highlighted} />
      <PortSocket portKey={bundle.routeOutPortKey} side="right" borderClass={highlighted ? 'border-primary' : portBorder} highlighted={highlighted} />

      <div className="flex min-w-0 items-center gap-1.5">
        <Layers className="size-3.5 shrink-0 text-muted-foreground" />
        <span className="truncate text-xs font-semibold text-foreground">
          {bundle.kind === 'server_expose'
            ? t('dashboard.topologyBundlePublic', { count: bundle.routes.length })
            : t('dashboard.topologyBundlePeer', { count: bundle.routes.length })}
        </span>

        <div className="hidden items-center gap-1 sm:flex">
          {bundle.protocolCounts.map((item) => (
            <span
              key={item.type}
              className="rounded border border-border/65 bg-muted/60 px-1.5 py-0.5 font-mono text-[9.5px] font-medium text-muted-foreground"
            >
              {`${item.type} ${item.count}`}
            </span>
          ))}
        </div>

        <div className="ml-auto flex shrink-0 items-center gap-1.5">
          {bundle.errorCount > 0 ? (
            <span className="inline-flex items-center gap-1 text-[10.5px] font-medium text-status-danger-fg">
              <span className="size-1.5 rounded-full bg-status-danger" />
              {t('dashboard.topologyBundleErrors', { count: bundle.errorCount })}
            </span>
          ) : (
            <span className={cn('inline-flex items-center gap-1 text-[10.5px] font-medium', STATUS_TEXT[bundle.statusSummaryKey])}>
              <span className={cn('size-1.5 rounded-full', STATUS_DOT[bundle.statusSummaryKey])} />
              {statusLabel(t, { key: bundle.statusSummaryKey, label: bundle.statusSummaryKey })}
            </span>
          )}
          <ChevronRight className="size-3.5 text-muted-foreground/70 transition-transform group-hover:translate-x-0.5 group-hover:text-foreground" />
        </div>
      </div>

      <div className="flex flex-wrap items-center gap-1.5 font-mono text-[10.5px] text-muted-foreground">
        {previewRoutes.map((r) => {
          const v = buildTunnelViewModel(r.edge.tunnel, r.edge.status.key !== 'offline');
          return (
            <span
              key={r.id}
              className="max-w-[148px] truncate rounded bg-muted/50 px-1.5 py-0.5 text-foreground/85"
              title={`${r.edge.tunnel.name}: ${v.routeLabel}`}
            >
              {v.targetLabel}
            </span>
          );
        })}
        {extraCount > 0 ? (
          <span className="rounded bg-muted/40 px-1.5 py-0.5 font-sans text-[10px] text-muted-foreground">
            {t('dashboard.topologyBundleMore', { count: extraCount })}
          </span>
        ) : null}

        {active ? (
          <span className="ml-auto inline-flex shrink-0 items-center gap-2 text-[10px] font-medium">
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
    <div className="relative z-10 rounded-xl border border-dashed border-border/90 bg-card/90 px-4 py-3.5 shadow-2xs">
      <PortSocket portKey="bridge-in" side="left" borderClass={socketClass} />
      <PortSocket portKey="bridge-out" side="right" borderClass={socketClass} />

      <div className="flex items-center gap-2">
        <span
          className={cn(
            'size-2 shrink-0 rounded-full',
            hasOnlineClient ? 'bg-status-online' : 'bg-muted-foreground/50',
          )}
        />
        <span className="text-xs font-semibold text-foreground">
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
            trigger={(
              <Button type="button" variant="secondary" size="sm" className="h-7 gap-1.5 px-2.5 text-xs">
                <GitBranchPlus className="size-3.5" />
                {t('dashboard.topologyCreateFirstTunnel')}
              </Button>
            )}
          />
        </div>
      ) : null}
    </div>
  );
}

export function IdleControlRow({
  unroutedCount,
  hasOnlineUnrouted,
  dimmed,
  highlighted,
  onHover,
}: {
  unroutedCount: number;
  hasOnlineUnrouted: boolean;
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
        'relative z-10 flex items-center justify-between gap-2 rounded-xl border border-dashed bg-card/75 px-3.5 py-2 transition-all duration-200',
        highlighted ? 'border-primary/50 bg-brand-soft/40' : 'border-border/75',
        dimmed && 'opacity-35',
      )}
    >
      <PortSocket portKey="idle-in" side="left" borderClass={socketClass} highlighted={highlighted} />
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
      <span className="font-mono text-[10.5px] text-muted-foreground/85">
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
  const cpuPercent = stats ? Math.min(100, Math.max(0, Math.round(stats.cpu_usage))) : 0;
  const memPercent = stats ? Math.min(100, Math.max(0, Math.round(stats.mem_usage))) : 0;

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
        'relative z-10 flex cursor-pointer flex-col rounded-xl border bg-card text-left shadow-2xs transition-all duration-200',
        compact ? 'gap-1 px-3 py-2' : 'gap-2 p-3.5',
        focused
          ? 'border-primary/65 ring-2 ring-brand-soft'
          : highlighted
            ? 'border-primary/50 ring-2 ring-brand-soft'
            : 'border-border/80 hover:border-brand-soft-border',
        dimmed && 'opacity-35',
      )}
    >
      <PortSocket
        portKey={`client-in:${node.id}`}
        side="left"
        borderClass={focused || highlighted ? 'border-primary' : node.online ? 'border-status-online' : 'border-muted-foreground/55'}
        highlighted={focused || highlighted}
      />

      <div className="flex items-center gap-2">
        <span
          className={cn(
            'size-2 shrink-0 rounded-full',
            node.online
              ? 'bg-status-online ring-3 ring-status-online/15'
              : 'bg-muted-foreground/50',
          )}
        />
        <span className="min-w-0 flex-1 truncate text-xs font-semibold text-foreground" title={node.label}>
          {node.label}
        </span>

        {focused ? (
          <span className="shrink-0 rounded-full border border-brand-soft-border bg-brand-soft px-2 py-0.5 text-[9.5px] font-medium text-brand-strong">
            {t('dashboard.topologyFocusedBadge')}
          </span>
        ) : null}

        <div
          className="flex shrink-0 items-center gap-0.5"
          onClick={(e) => e.stopPropagation()}
          onKeyDown={(e) => e.stopPropagation()}
        >
          <TunnelDialog
            scope={scope}
            mode="create"
            clientId={node.id}
            clients={allClients}
            trigger={(
              <button
                type="button"
                className="flex size-6 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
                title={t('tunnels.addTunnel')}
                aria-label={t('tunnels.addTunnel')}
              >
                <Plus className="size-3.5" />
              </button>
            )}
          />
          <button
            type="button"
            className="flex size-6 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
            onClick={onOpenDetail}
            title={t('dashboard.viewDetails')}
            aria-label={t('dashboard.viewDetails')}
          >
            <SquareArrowOutUpRight className="size-3.5" />
          </button>
        </div>
      </div>

      <div className="flex items-center justify-between gap-2 font-mono text-[10.5px] text-muted-foreground">
        <span className="min-w-0 truncate">
          {compact && node.online && stats ? (
            <>
              <span>{clientIp || (client ? `${client.info.os}/${client.info.arch}` : '-')}</span>
              <span className="text-muted-foreground/85">
                {' · '}
                <span className={cn(cpuPercent >= 85 && 'font-medium text-status-warning-fg')}>
                  CPU {cpuPercent}%
                </span>
                {' · '}
                <span className={cn(memPercent >= 85 && 'font-medium text-status-warning-fg')}>
                  MEM {memPercent}%
                </span>
              </span>
            </>
          ) : (
            <>
              {clientIp ? `${clientIp} · ` : ''}
              {client ? `${client.info.os}/${client.info.arch}` : '-'}
              {compact && !node.online ? (
                <span className="font-sans text-muted-foreground/80">
                  {` · ${t('tunnels.statusOffline')}`}
                </span>
              ) : null}
            </>
          )}
        </span>
        <span className="shrink-0 rounded bg-muted/70 px-1.5 py-0.5 text-[10px] font-sans font-medium text-muted-foreground">
          {t('dashboard.topologyRouteCountBadge', { count: tunnelCount })}
        </span>
      </div>

      {!compact && node.online && stats ? (
        <div className="grid grid-cols-2 gap-2.5 border-t border-border/50 pt-2">
          <div className="flex flex-col gap-1">
            <div className="flex items-center justify-between font-mono text-[10px]">
              <span className="text-muted-foreground">CPU</span>
              <span className="font-medium text-foreground">{cpuPercent}%</span>
            </div>
            <div className="h-1 w-full overflow-hidden rounded-full bg-muted">
              <div
                className={cn(
                  'h-full rounded-full transition-all duration-500',
                  cpuPercent >= 85 ? 'bg-status-warning' : 'bg-meter-track',
                )}
                style={{ width: `${cpuPercent}%` }}
              />
            </div>
          </div>
          <div className="flex flex-col gap-1">
            <div className="flex items-center justify-between font-mono text-[10px]">
              <span className="text-muted-foreground">MEM</span>
              <span className="font-medium text-foreground">{memPercent}%</span>
            </div>
            <div className="h-1 w-full overflow-hidden rounded-full bg-muted">
              <div
                className={cn(
                  'h-full rounded-full transition-all duration-500',
                  memPercent >= 85 ? 'bg-status-warning' : 'bg-meter-track',
                )}
                style={{ width: `${memPercent}%` }}
              />
            </div>
          </div>
        </div>
      ) : !compact && !node.online ? (
        <div className="border-t border-border/50 pt-1.5 text-[10.5px] text-muted-foreground">
          {t('dashboard.topologyClientOfflineHint')}
        </div>
      ) : null}
    </div>
  );
}
