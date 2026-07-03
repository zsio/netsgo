import { useNavigate } from '@tanstack/react-router';
import { ArrowDown, ArrowUp, SquareArrowOutUpRight } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import { formatNetSpeed } from '@/lib/format';
import { buildTunnelViewModel } from '@/lib/tunnel-model';
import { cn } from '@/lib/utils';
import {
  topologyEdgeTouches,
  type TopologyEdge,
  type TopologyGraph,
  type TopologyTrafficRate,
  type TopologyTrafficSnapshot,
} from './topology-model';
import {
  STATUS_DOT,
  STATUS_TEXT,
  hasTraffic,
  rateOrZero,
  statusLabel,
} from './topology-rendering';

export function TopologySidePanel({
  graph,
  trafficSnapshot,
  focusId,
  hoveredTunnelId,
  onHoverTunnel,
}: {
  graph: TopologyGraph;
  trafficSnapshot: TopologyTrafficSnapshot;
  focusId: string;
  hoveredTunnelId: string | null;
  onHoverTunnel: (id: string | null) => void;
}) {
  const { t } = useTranslation();
  const navigate = useNavigate();

  const focusNode = graph.nodes.find((node) => node.id === focusId);
  if (!focusNode || focusNode.kind !== 'client') {
    return null;
  }

  const relatedEdges = graph.edges.filter((edge) => topologyEdgeTouches(edge, focusNode.id));

  return (
    <div className="flex w-full shrink-0 flex-col border-t border-border/40 lg:h-full lg:min-h-0 lg:w-80 lg:overflow-hidden lg:border-l lg:border-t-0">
      <button
        type="button"
        className="group flex w-full items-center gap-2 border-b border-border/30 px-4 pb-2 pt-3 text-left"
        onClick={() => navigate({
          to: '/dashboard/clients/$clientId',
          params: { clientId: focusNode.id },
        })}
        title={focusNode.label}
      >
        <span className="min-w-0 truncate border-b border-dashed border-muted-foreground/50 text-xs font-medium text-foreground transition-colors group-hover:border-primary/60 group-hover:text-primary">
          {focusNode.label}
        </span>
        <SquareArrowOutUpRight className="ml-auto size-3.5 shrink-0 text-muted-foreground/60 transition-colors group-hover:text-primary" />
      </button>
      {relatedEdges.length === 0 ? (
        <p className="px-4 py-6 text-center text-xs text-muted-foreground">
          {t('dashboard.topologyNoTunnels')}
        </p>
      ) : (
        <div className="max-h-72 min-h-0 flex-1 overflow-y-auto [scrollbar-width:thin] lg:max-h-none">
          <div className="flex flex-col divide-y divide-border/30">
            {relatedEdges.map((edge) => (
              <TunnelListItem
                key={edge.id}
                edge={edge}
                graph={graph}
                trafficRate={trafficSnapshot.tunnelRates.get(edge.id)}
                hovered={hoveredTunnelId === edge.id}
                onHover={onHoverTunnel}
              />
            ))}
          </div>
        </div>
      )}
    </div>
  );
}

function TunnelListItem({
  edge,
  graph,
  trafficRate,
  hovered,
  onHover,
}: {
  edge: TopologyEdge;
  graph: TopologyGraph;
  trafficRate: TopologyTrafficRate | undefined;
  hovered: boolean;
  onHover: (id: string | null) => void;
}) {
  const { t } = useTranslation();
  const view = buildTunnelViewModel(edge.tunnel, true);
  const sourceNode = graph.nodes.find((node) => node.id === edge.sourceId);
  const targetNode = graph.nodes.find((node) => node.id === edge.targetId);
  const sourceName = sourceNode?.kind === 'server' ? t('dashboard.topologyServer') : sourceNode?.label ?? '-';
  const targetName = targetNode?.kind === 'server' ? t('dashboard.topologyServer') : targetNode?.label ?? '-';
  const rate = rateOrZero(trafficRate);
  const active = hasTraffic(trafficRate);

  return (
    <div
      className={cn(
        'group relative flex w-full flex-col gap-2 px-4 py-2.5 text-left transition-colors',
        hovered ? 'bg-muted/50' : 'hover:bg-muted/40',
      )}
      onMouseEnter={() => onHover(edge.id)}
      onMouseLeave={() => onHover(null)}
      title={`${view.routeLabel} · ${statusLabel(t, edge.status)}`}
    >
      <span
        className={cn(
          'absolute inset-y-2 left-0 w-0.5 rounded-full bg-primary/70 transition-opacity',
          hovered ? 'opacity-100' : 'opacity-0',
        )}
      />
      <span className="flex min-w-0 items-center gap-1.5">
        <span className={cn('size-1.5 shrink-0 rounded-full', STATUS_DOT[edge.status.key])} />
        <span className="min-w-0 truncate text-xs font-medium text-foreground">{edge.tunnel.name}</span>
        <span className="shrink-0 rounded border border-border/50 bg-muted/30 px-1 text-[9px] leading-3.5 font-medium text-muted-foreground">
          {edge.tunnel.type.toUpperCase()}
        </span>
        <span className={cn('ml-auto shrink-0 text-[10px]', STATUS_TEXT[edge.status.key])}>
          {statusLabel(t, edge.status)}
        </span>
      </span>
      <span className="relative flex flex-col gap-1">
        <span className="absolute bottom-[7px] left-[2.5px] top-[7px] w-px bg-border" aria-hidden />
        <RouteRow kind="source" name={sourceName} address={view.targetLabel} />
        <RouteRow kind="target" name={targetName} address={view.destinationLabel} />
      </span>
      <span className="flex items-center gap-3 pl-3.5 font-mono text-[10px] leading-4">
        <span className={cn('flex items-center gap-0.5', active && rate.ingressBps > 0 ? 'text-primary' : 'text-muted-foreground/70')}>
          <ArrowDown className="size-2.5" />
          {formatNetSpeed(rate.ingressBps)}
        </span>
        <span className={cn('flex items-center gap-0.5', active && rate.egressBps > 0 ? 'text-primary' : 'text-muted-foreground/70')}>
          <ArrowUp className="size-2.5" />
          {formatNetSpeed(rate.egressBps)}
        </span>
      </span>
    </div>
  );
}

function RouteRow({
  kind,
  name,
  address,
}: {
  kind: 'source' | 'target';
  name: string;
  address: string;
}) {
  return (
    <span className="flex min-w-0 items-center gap-2">
      <span
        className={cn(
          'size-1.5 shrink-0 rounded-full',
          kind === 'source'
            ? 'border border-muted-foreground/50 bg-background'
            : 'bg-primary/70',
        )}
      />
      <span className="min-w-0 flex-1 truncate text-[10px] leading-4 text-muted-foreground" title={name}>
        {name}
      </span>
      <span className="min-w-0 max-w-[55%] shrink-0 truncate font-mono text-[10px] leading-4 text-foreground/75" title={address}>
        {address}
      </span>
    </span>
  );
}
