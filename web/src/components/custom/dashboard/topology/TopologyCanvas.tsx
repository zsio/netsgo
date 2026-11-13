import { useCallback, useLayoutEffect, useMemo, useRef, useState } from 'react';
import {
  ArrowRightLeft,
  ChevronLeft,
  ChevronRight,
  Globe,
  Laptop,
  Search,
  SearchX,
  X,
} from 'lucide-react';
import { useTranslation } from 'react-i18next';

import { Button } from '@/components/ui/button';
import type { ResourceScope } from '@/lib/resource-scope';
import { cn } from '@/lib/utils';
import type { Client } from '@/types';
import {
  buildStructuredTopologyFlow,
  computeCubicWirePath,
  type StructuredMiddleItem,
  type TopologyQuickFilter,
} from './topology-layout';
import type {
  TopologyGraph,
  TopologyPoint,
  TopologyTrafficSnapshot,
} from './topology-model';
import {
  ClientNodeCard,
  ControlBridgeCard,
  IdleControlRow,
  PeerIngressCard,
  ServerHubCard,
  TunnelBundleCard,
  TunnelRouteCard,
} from './TopologyNodeView';
import {
  STATUS_WIRE_BASE,
  STATUS_WIRE_FLOW,
  hasTraffic,
  tunnelStreamDuration,
  type StatusKey,
} from './topology-rendering';

type HoverTarget =
  | { kind: 'server' }
  | { kind: 'peer'; clientId: string }
  | { kind: 'middle'; itemId: string }
  | { kind: 'idle' }
  | { kind: 'client'; clientId: string }
  | null;

interface WireDescriptor {
  key: string;
  fromPort: string;
  toPort: string;
  kind: 'route' | 'control';
  isBundle?: boolean;
  statusKey?: StatusKey;
  online?: boolean;
  flowing: boolean;
  hasLiveTraffic: boolean;
  duration: string;
  highlighted: boolean;
  dimmed: boolean;
}

export function TopologyCanvas({
  scope,
  clients,
  serverEndpoint,
  onlineClientCount,
  totalClientCount,
  graph,
  trafficSnapshot,
  focusId,
  onFocusChange,
  onOpenClientDetail,
  isFullscreen,
}: {
  scope: ResourceScope;
  clients: Client[];
  serverEndpoint: string;
  onlineClientCount: number;
  totalClientCount: number;
  graph: TopologyGraph;
  trafficSnapshot: TopologyTrafficSnapshot;
  focusId: string | null;
  onFocusChange: (id: string | null) => void;
  onOpenClientDetail: (clientId: string) => void;
  isFullscreen: boolean;
}) {
  const { t } = useTranslation();
  const stageRef = useRef<HTMLDivElement>(null);
  const [hoverTarget, setHoverTarget] = useState<HoverTarget>(null);
  const [searchQuery, setSearchQuery] = useState('');
  const [quickFilter, setQuickFilter] = useState<TopologyQuickFilter>('all');
  const [pageState, setPageState] = useState<{ scopeKey: string; page: number }>({
    scopeKey: '',
    page: 0,
  });
  const [portMap, setPortMap] = useState<Map<string, TopologyPoint>>(() => new Map());
  const [stageSize, setStageSize] = useState({ width: 0, height: 0 });

  const scopeKey = `${focusId ?? 'overview'}|${quickFilter}|${searchQuery.trim().toLowerCase()}`;
  const currentPage = pageState.scopeKey === scopeKey ? pageState.page : 0;
  const pageSize = isFullscreen ? 10 : 6;

  const flow = useMemo(
    () =>
      buildStructuredTopologyFlow(graph, focusId, {
        searchQuery,
        quickFilter,
        trafficSnapshot,
        page: currentPage,
        clientPageSize: pageSize,
        routePageSize: pageSize,
      }),
    [currentPage, focusId, graph, pageSize, quickFilter, searchQuery, trafficSnapshot],
  );

  const handlePageChange = useCallback(
    (nextPage: number) => {
      setHoverTarget(null);
      setPageState({ scopeKey, page: nextPage });
    },
    [scopeKey],
  );

  const handleResetFilters = useCallback(() => {
    setHoverTarget(null);
    setSearchQuery('');
    setQuickFilter('all');
    setPageState({ scopeKey: '', page: 0 });
  }, []);

  const measurePorts = useCallback(() => {
    const stage = stageRef.current;
    if (!stage) return;
    const stageRect = stage.getBoundingClientRect();
    if (stageRect.width <= 0 || stageRect.height <= 0) return;

    const nextPorts = new Map<string, TopologyPoint>();
    stage.querySelectorAll<HTMLElement>('[data-port]').forEach((el) => {
      const key = el.dataset.port;
      if (!key) return;
      const rect = el.getBoundingClientRect();
      nextPorts.set(key, {
        x: rect.left - stageRect.left + rect.width / 2,
        y: rect.top - stageRect.top + rect.height / 2,
      });
    });

    setPortMap(nextPorts);
    setStageSize((prev) =>
      prev.width === stageRect.width && prev.height === stageRect.height
        ? prev
        : { width: stageRect.width, height: stageRect.height },
    );
  }, []);

  useLayoutEffect(() => {
    const stage = stageRef.current;
    if (!stage) return;
    const rafId = window.requestAnimationFrame(() => {
      measurePorts();
    });
    const observer = new ResizeObserver(() => {
      measurePorts();
    });
    observer.observe(stage);
    stage.querySelectorAll<HTMLElement>('[data-flow-col]').forEach((col) => {
      observer.observe(col);
    });
    return () => {
      window.cancelAnimationFrame(rafId);
      observer.disconnect();
    };
  }, [flow, isFullscreen, measurePorts]);

  const unroutedSet = useMemo(
    () => new Set(flow.unroutedClients.map((n) => n.id)),
    [flow.unroutedClients],
  );

  const isMiddleItemHighlighted = useCallback(
    (item: StructuredMiddleItem) => {
      if (!hoverTarget) return false;
      switch (hoverTarget.kind) {
        case 'middle':
          return hoverTarget.itemId === item.id;
        case 'client':
          return item.targetClientId === hoverTarget.clientId;
        case 'peer':
          return item.kind === 'client_to_client' && item.sourceNodeId === hoverTarget.clientId;
        case 'server':
          return item.kind === 'server_expose';
        case 'idle':
          return false;
      }
    },
    [hoverTarget],
  );

  const isServerHighlighted = useMemo(() => {
    if (!hoverTarget) return false;
    switch (hoverTarget.kind) {
      case 'server':
      case 'idle':
        return true;
      case 'middle': {
        const item = flow.middleItems.find((m) => m.id === hoverTarget.itemId);
        return item?.kind === 'server_expose';
      }
      case 'client':
        return (
          unroutedSet.has(hoverTarget.clientId) ||
          flow.middleItems.some(
            (m) => m.targetClientId === hoverTarget.clientId && m.kind === 'server_expose',
          )
        );
      case 'peer':
        return false;
    }
  }, [flow.middleItems, hoverTarget, unroutedSet]);

  const isPeerHighlighted = useCallback(
    (peerId: string) => {
      if (!hoverTarget) return false;
      switch (hoverTarget.kind) {
        case 'peer':
          return hoverTarget.clientId === peerId;
        case 'middle': {
          const item = flow.middleItems.find((m) => m.id === hoverTarget.itemId);
          return item?.kind === 'client_to_client' && item.sourceNodeId === peerId;
        }
        case 'client':
          return flow.middleItems.some(
            (m) =>
              m.targetClientId === hoverTarget.clientId &&
              m.kind === 'client_to_client' &&
              m.sourceNodeId === peerId,
          );
        default:
          return false;
      }
    },
    [flow.middleItems, hoverTarget],
  );

  const isClientHighlighted = useCallback(
    (clientId: string) => {
      if (!hoverTarget) return false;
      switch (hoverTarget.kind) {
        case 'client':
          return hoverTarget.clientId === clientId;
        case 'middle': {
          const item = flow.middleItems.find((m) => m.id === hoverTarget.itemId);
          return item?.targetClientId === clientId;
        }
        case 'peer':
          return flow.middleItems.some(
            (m) =>
              m.kind === 'client_to_client' &&
              m.sourceNodeId === hoverTarget.clientId &&
              m.targetClientId === clientId,
          );
        case 'server':
          return (
            unroutedSet.has(clientId) ||
            flow.middleItems.some(
              (m) => m.kind === 'server_expose' && m.targetClientId === clientId,
            )
          );
        case 'idle':
          return unroutedSet.has(clientId);
      }
    },
    [flow.middleItems, hoverTarget, unroutedSet],
  );

  const isIdleHighlighted = useMemo(() => {
    if (!hoverTarget) return false;
    if (hoverTarget.kind === 'idle' || hoverTarget.kind === 'server') return true;
    if (hoverTarget.kind === 'client') return unroutedSet.has(hoverTarget.clientId);
    return false;
  }, [hoverTarget, unroutedSet]);

  const hasHover = hoverTarget !== null;

  const wireDescriptors = useMemo<WireDescriptor[]>(() => {
    const list: WireDescriptor[] = [];

    if (flow.hasEmptyFilterResult) {
      return list;
    }

    if (flow.middleItems.length === 0) {
      const anyOnline = flow.targetClients.some((n) => n.online);
      list.push({
        key: 'bridge-in',
        fromPort: 'server-out',
        toPort: 'bridge-in',
        kind: 'control',
        online: anyOnline,
        flowing: false,
        hasLiveTraffic: false,
        duration: '1.20s',
        highlighted: isServerHighlighted,
        dimmed: hasHover && !isServerHighlighted,
      });
      for (const client of flow.targetClients) {
        const hi = isClientHighlighted(client.id);
        list.push({
          key: `bridge-out-${client.id}`,
          fromPort: 'bridge-out',
          toPort: `client-in:${client.id}`,
          kind: 'control',
          online: client.online,
          flowing: false,
          hasLiveTraffic: false,
          duration: '1.20s',
          highlighted: hi,
          dimmed: hasHover && !hi,
        });
      }
      return list;
    }

    for (const item of flow.middleItems) {
      const isBundle = item.itemKind === 'bundle';
      const rate = isBundle ? item.totalRate : trafficSnapshot.tunnelRates.get(item.id);
      const live = hasTraffic(rate);
      const hi = isMiddleItemHighlighted(item);
      const statusKey = isBundle ? item.statusSummaryKey : item.edge.status.key;
      const flowing = statusKey === 'exposed';
      const duration = tunnelStreamDuration(rate);

      list.push({
        key: `route-in-${item.id}`,
        fromPort: item.sourcePortKey,
        toPort: item.routeInPortKey,
        kind: 'route',
        isBundle,
        statusKey,
        flowing,
        hasLiveTraffic: live,
        duration,
        highlighted: hi,
        dimmed: hasHover && !hi,
      });
      list.push({
        key: `route-out-${item.id}`,
        fromPort: item.routeOutPortKey,
        toPort: item.targetPortKey,
        kind: 'route',
        isBundle,
        statusKey,
        flowing,
        hasLiveTraffic: live,
        duration,
        highlighted: hi,
        dimmed: hasHover && !hi,
      });
    }

    if (flow.unroutedClients.length > 0) {
      const anyOnlineUnrouted = flow.unroutedClients.some((n) => n.online);
      list.push({
        key: 'idle-in',
        fromPort: 'server-out',
        toPort: 'idle-in',
        kind: 'control',
        online: anyOnlineUnrouted,
        flowing: false,
        hasLiveTraffic: false,
        duration: '1.20s',
        highlighted: isIdleHighlighted,
        dimmed: hasHover && !isIdleHighlighted,
      });
      for (const client of flow.unroutedClients) {
        const hi = isIdleHighlighted && isClientHighlighted(client.id);
        list.push({
          key: `idle-out-${client.id}`,
          fromPort: 'idle-out',
          toPort: `client-in:${client.id}`,
          kind: 'control',
          online: client.online,
          flowing: false,
          hasLiveTraffic: false,
          duration: '1.20s',
          highlighted: hi,
          dimmed: hasHover && !hi,
        });
      }
    }

    return list;
  }, [
    flow.hasEmptyFilterResult,
    flow.middleItems,
    flow.targetClients,
    flow.unroutedClients,
    hasHover,
    isClientHighlighted,
    isIdleHighlighted,
    isMiddleItemHighlighted,
    isServerHighlighted,
    trafficSnapshot.tunnelRates,
  ]);

  const defaultBridgeClientId = useMemo(() => {
    if (flow.focusClientId) return flow.focusClientId;
    const online = flow.targetClients.find((n) => n.online);
    return online?.id ?? flow.targetClients[0]?.id ?? null;
  }, [flow.focusClientId, flow.targetClients]);

  const filterPills = useMemo(() => {
    const items: Array<{
      key: TopologyQuickFilter;
      label: string;
      count?: number;
      dotClass?: string;
    }> = [
      { key: 'all', label: t('dashboard.topologyFilterAll') },
      {
        key: 'issues',
        label: t('dashboard.topologyFilterIssues'),
        count: flow.filterCounts.issues,
        dotClass: flow.filterCounts.issues > 0 ? 'bg-status-danger' : undefined,
      },
      {
        key: 'active',
        label: t('dashboard.topologyFilterActive'),
        count: flow.filterCounts.active,
        dotClass: flow.filterCounts.active > 0 ? 'bg-status-online' : undefined,
      },
    ];
    if (flow.filterCounts.c2c > 0 || quickFilter === 'c2c') {
      items.push({
        key: 'c2c',
        label: t('dashboard.topologyFilterC2C'),
        count: flow.filterCounts.c2c,
      });
    }
    return items;
  }, [flow.filterCounts, quickFilter, t]);

  return (
    <div className={cn('flex min-h-0 flex-1 flex-col', isFullscreen && 'h-full')}>
      {/* 规模化检索与快筛栏：仅在节点 > 4 或规则 > 6（或正在检索）时按需呈现，小规模零干扰 */}
      {flow.showScaleToolbar ? (
        <div className="flex flex-wrap items-center justify-between gap-2.5 border-b border-border/50 bg-card/85 px-5 py-2 sm:px-6">
          <div className="relative w-full sm:w-64">
            <Search className="pointer-events-none absolute top-1/2 left-2.5 size-3.5 -translate-y-1/2 text-muted-foreground/75" />
            <input
              type="text"
              value={searchQuery}
              onChange={(e) => {
                setHoverTarget(null);
                setSearchQuery(e.target.value);
              }}
              placeholder={t('dashboard.topologySearchPlaceholder')}
              className="h-7 w-full rounded-lg border border-border/75 bg-background/80 pr-7 pl-8 text-xs text-foreground transition-colors placeholder:text-muted-foreground/70 focus-visible:border-foreground/40 focus-visible:outline-none"
            />
            {searchQuery ? (
              <button
                type="button"
                onClick={() => {
                  setHoverTarget(null);
                  setSearchQuery('');
                }}
                aria-label={t('dashboard.topologyClearFilter')}
                className="absolute top-1/2 right-1.5 flex size-4.5 -translate-y-1/2 items-center justify-center rounded text-muted-foreground hover:bg-muted hover:text-foreground"
              >
                <X className="size-3" />
              </button>
            ) : null}
          </div>

          <div className="flex flex-wrap items-center gap-1">
            {filterPills.map((pill) => {
              const selected = quickFilter === pill.key;
              return (
                <button
                  key={pill.key}
                  type="button"
                  onClick={() => {
                    setHoverTarget(null);
                    setQuickFilter(pill.key);
                  }}
                  className={cn(
                    'inline-flex h-7 items-center gap-1.5 rounded-lg border px-2.5 text-[11px] font-medium transition-colors',
                    selected
                      ? 'border-brand-soft-border bg-brand-soft text-brand-strong shadow-2xs'
                      : 'border-transparent text-muted-foreground hover:bg-muted/80 hover:text-foreground',
                  )}
                >
                  {pill.dotClass && !selected ? (
                    <span className={cn('size-1.5 rounded-full', pill.dotClass)} />
                  ) : null}
                  <span>{pill.label}</span>
                  {pill.count !== undefined ? (
                    <span
                      className={cn(
                        'rounded-full px-1.5 py-px font-mono text-[10px] leading-tight',
                        selected
                          ? 'bg-primary/15 text-brand-strong'
                          : 'bg-muted text-muted-foreground',
                      )}
                    >
                      {pill.count}
                    </span>
                  ) : null}
                </button>
              );
            })}
          </div>
        </div>
      ) : null}

      {/* 舞台主区域：温润云石微底色 */}
      <div
        className={cn(
          'min-h-0 flex-1 overflow-y-auto bg-muted/30 px-5 py-4 dark:bg-background/50 sm:px-6 sm:py-5',
          !isFullscreen && 'max-h-[640px]',
        )}
      >
        {flow.hasEmptyFilterResult ? (
          <div className="flex min-h-[200px] flex-col items-center justify-center gap-2.5 rounded-xl border border-dashed border-border/75 bg-card/65 px-6 py-10 text-center">
            <span className="flex size-9 items-center justify-center rounded-full bg-muted text-muted-foreground">
              <SearchX className="size-4" />
            </span>
            <p className="text-xs font-medium text-muted-foreground">
              {t('dashboard.topologyNoMatch')}
            </p>
            <Button
              type="button"
              variant="secondary"
              size="sm"
              className="mt-0.5 h-7 px-3 text-xs"
              onClick={handleResetFilters}
            >
              {t('dashboard.topologyClearFilter')}
            </Button>
          </div>
        ) : (
          <>
            {/* 三栏列标题 */}
            <div className="mb-3 grid grid-cols-[216px_minmax(240px,1fr)_232px] items-center gap-9 px-0.5 text-[11px] font-medium text-muted-foreground xl:grid-cols-[240px_minmax(280px,1fr)_284px] xl:gap-12">
              <div className="flex items-center gap-1.5">
                <Globe className="size-3.5 text-muted-foreground/80" />
                <span>{t('dashboard.topologyColIngress')}</span>
              </div>
              <div className="flex items-center gap-1.5">
                <ArrowRightLeft className="size-3.5 text-muted-foreground/80" />
                <span>{t('dashboard.topologyColRoutes')}</span>
                <span className="rounded-full bg-muted px-1.5 py-px font-mono text-[10px] text-muted-foreground">
                  {flow.totalScopedRoutes}
                </span>
              </div>
              <div className="flex items-center justify-between gap-2">
                <span className="flex items-center gap-1.5">
                  <Laptop className="size-3.5 text-muted-foreground/80" />
                  <span>{t('dashboard.topologyColClients')}</span>
                  <span className="rounded-full bg-muted px-1.5 py-px font-mono text-[10px] text-muted-foreground">
                    {flow.totalScopedClients}
                  </span>
                </span>
                <span className="hidden truncate text-[10px] text-muted-foreground/75 xl:inline">
                  {t('dashboard.topologyFilterHint')}
                </span>
              </div>
            </div>

            {/* 三栏流向舞台 + 精确端口三次贝塞尔连线层 */}
            <div
              ref={stageRef}
              className="relative grid min-h-[164px] grid-cols-[216px_minmax(240px,1fr)_232px] items-stretch gap-9 xl:grid-cols-[240px_minmax(280px,1fr)_284px] xl:gap-12"
            >
              {stageSize.width > 0 && stageSize.height > 0 && (
                <svg
                  aria-hidden
                  width={stageSize.width}
                  height={stageSize.height}
                  className="pointer-events-none absolute inset-0 z-0 overflow-visible"
                >
                  {wireDescriptors.map((wire) => {
                    const start = portMap.get(wire.fromPort);
                    const end = portMap.get(wire.toPort);
                    if (!start || !end) return null;
                    const d = computeCubicWirePath(start, end);

                    if (wire.kind === 'control') {
                      return (
                        <path
                          key={wire.key}
                          d={d}
                          fill="none"
                          strokeWidth={wire.highlighted ? 1.85 : 1.35}
                          strokeDasharray="4 5"
                          strokeLinecap="round"
                          className={cn(
                            'transition-opacity duration-200',
                            wire.highlighted
                              ? 'stroke-primary/80'
                              : wire.online
                                ? 'stroke-status-online/50'
                                : 'stroke-muted-foreground/45',
                          )}
                          style={{ opacity: wire.dimmed ? 0.2 : wire.highlighted ? 1 : 0.85 }}
                        />
                      );
                    }

                    const statusKey = wire.statusKey ?? 'stopped';
                    const dashedStatus = statusKey === 'offline' || statusKey === 'stopped';
                    const highlightWithBrand = wire.highlighted && statusKey !== 'error';
                    const baseWidth = wire.isBundle
                      ? wire.highlighted
                        ? 2.4
                        : 1.85
                      : wire.highlighted
                        ? 2.1
                        : 1.5;

                    return (
                      <g
                        key={wire.key}
                        className="transition-opacity duration-200"
                        style={{ opacity: wire.dimmed ? 0.18 : 1 }}
                      >
                        <path
                          d={d}
                          fill="none"
                          strokeWidth={baseWidth}
                          strokeDasharray={dashedStatus ? '4 5' : undefined}
                          strokeLinecap="round"
                          className={highlightWithBrand ? 'stroke-primary/50' : STATUS_WIRE_BASE[statusKey]}
                        />
                        {wire.flowing ? (
                          <path
                            d={d}
                            fill="none"
                            strokeWidth={wire.highlighted || wire.hasLiveTraffic ? baseWidth + 0.15 : baseWidth}
                            strokeDasharray="7 21"
                            strokeLinecap="round"
                            className={highlightWithBrand ? 'stroke-primary' : STATUS_WIRE_FLOW[statusKey]}
                            style={{
                              animation: `topology-stream ${wire.duration} linear infinite`,
                              opacity: wire.highlighted || wire.hasLiveTraffic ? 0.95 : 0.55,
                            }}
                          />
                        ) : null}
                      </g>
                    );
                  })}
                </svg>
              )}

              {/* 第 1 栏：公网入口与服务端枢纽（+ 按需 C2C 点对点监听入口） */}
              <div data-flow-col="ingress" className="flex flex-col justify-center gap-2.5">
                <ServerHubCard
                  serverEndpoint={serverEndpoint}
                  onlineClientCount={onlineClientCount}
                  totalClientCount={totalClientCount}
                  serverExposeCount={flow.serverExposeCount}
                  highlighted={isServerHighlighted}
                  dimmed={hasHover && !isServerHighlighted}
                  onHover={(hovering) => setHoverTarget(hovering ? { kind: 'server' } : null)}
                />

                {flow.ingressPeers.length > 0 ? (
                  <div className="mt-1 flex flex-col gap-2">
                    <div className="px-0.5 text-[10px] font-medium text-muted-foreground">
                      {t('dashboard.topologyPeerIngress')}
                    </div>
                    {flow.ingressPeers.map((peer) => {
                      const hi = isPeerHighlighted(peer.id);
                      return (
                        <PeerIngressCard
                          key={peer.id}
                          node={peer}
                          compact={flow.compact}
                          focused={flow.focusClientId === peer.id}
                          highlighted={hi}
                          dimmed={hasHover && !hi}
                          onClick={() => {
                            setHoverTarget(null);
                            onFocusChange(flow.focusClientId === peer.id ? null : peer.id);
                          }}
                          onHover={(hovering) =>
                            setHoverTarget(hovering ? { kind: 'peer', clientId: peer.id } : null)
                          }
                        />
                      );
                    })}
                  </div>
                ) : null}
              </div>

              {/* 第 2 栏：隧道转发规则 / 聚合规则束 / 控制连接通道 */}
              <div
                data-flow-col="routes"
                className={cn('flex flex-col justify-center', flow.compact ? 'gap-1.5' : 'gap-2')}
              >
                {flow.middleItems.length === 0 ? (
                  <ControlBridgeCard
                    scope={scope}
                    clients={clients}
                    defaultClientId={defaultBridgeClientId}
                    hasOnlineClient={flow.targetClients.some((n) => n.online)}
                  />
                ) : (
                  <>
                    {flow.middleItems.map((item) => {
                      const hi = isMiddleItemHighlighted(item);
                      if (item.itemKind === 'bundle') {
                        return (
                          <TunnelBundleCard
                            key={item.id}
                            bundle={item}
                            compact={flow.compact}
                            highlighted={hi}
                            dimmed={hasHover && !hi}
                            onClick={() => {
                              setHoverTarget(null);
                              onFocusChange(item.targetClientId);
                            }}
                            onHover={(hovering) =>
                              setHoverTarget(
                                hovering ? { kind: 'middle', itemId: item.id } : null,
                              )
                            }
                          />
                        );
                      }

                      return (
                        <TunnelRouteCard
                          key={item.id}
                          route={item}
                          compact={flow.compact}
                          trafficRate={trafficSnapshot.tunnelRates.get(item.id)}
                          highlighted={hi}
                          dimmed={hasHover && !hi}
                          onClick={() => {
                            setHoverTarget(null);
                            onFocusChange(
                              flow.focusClientId === item.targetClientId
                                ? null
                                : item.targetClientId,
                            );
                          }}
                          onHover={(hovering) =>
                            setHoverTarget(
                              hovering ? { kind: 'middle', itemId: item.id } : null,
                            )
                          }
                        />
                      );
                    })}

                    {flow.unroutedClients.length > 0 ? (
                      <IdleControlRow
                        unroutedCount={flow.unroutedClients.length}
                        hasOnlineUnrouted={flow.unroutedClients.some((n) => n.online)}
                        highlighted={isIdleHighlighted}
                        dimmed={hasHover && !isIdleHighlighted}
                        onHover={(hovering) =>
                          setHoverTarget(hovering ? { kind: 'idle' } : null)
                        }
                      />
                    ) : null}
                  </>
                )}
              </div>

              {/* 第 3 栏：目标客户端节点 */}
              <div
                data-flow-col="clients"
                className={cn('flex flex-col justify-center', flow.compact ? 'gap-1.5' : 'gap-2.5')}
              >
                {flow.targetClients.map((node) => {
                  const hi = isClientHighlighted(node.id);
                  return (
                    <ClientNodeCard
                      key={node.id}
                      scope={scope}
                      node={node}
                      allClients={clients}
                      tunnelCount={flow.tunnelCountByClientId.get(node.id) ?? 0}
                      compact={flow.compact}
                      focused={flow.focusClientId === node.id}
                      highlighted={hi}
                      dimmed={hasHover && !hi}
                      onToggleFocus={() => {
                        setHoverTarget(null);
                        onFocusChange(flow.focusClientId === node.id ? null : node.id);
                      }}
                      onOpenDetail={() => onOpenClientDetail(node.id)}
                      onHover={(hovering) =>
                        setHoverTarget(
                          hovering ? { kind: 'client', clientId: node.id } : null,
                        )
                      }
                    />
                  );
                })}
              </div>
            </div>
          </>
        )}
      </div>

      {/* 底部图例与单屏分片翻页栏 */}
      <div className="flex flex-wrap items-center justify-between gap-3 border-t border-border/50 bg-card px-5 py-2.5 text-[11px] text-muted-foreground">
        <div className="flex flex-wrap items-center gap-3.5">
          <span className="inline-flex items-center gap-1.5">
            <span className="size-1.5 rounded-full bg-status-online" />
            {t('tunnels.statusExposed')}
          </span>
          <span className="inline-flex items-center gap-1.5">
            <span className="size-1.5 rounded-full bg-status-warning" />
            {t('tunnels.statusOffline')}
          </span>
          <span className="inline-flex items-center gap-1.5">
            <span className="size-1.5 rounded-full bg-status-danger" />
            {t('tunnels.statusError')}
          </span>
          <span className="inline-flex items-center gap-1.5">
            <svg width="16" height="6" aria-hidden>
              <line
                x1="0"
                y1="3"
                x2="16"
                y2="3"
                strokeDasharray="3 3"
                strokeWidth="1.35"
                className="stroke-muted-foreground/75"
              />
            </svg>
            {t('dashboard.topologyControlLink')}
          </span>
        </div>

        <div className="flex items-center gap-3">
          {flow.pagination.mode !== null && flow.pagination.totalPages > 1 ? (
            <div className="flex items-center gap-2">
              <span className="font-mono text-[11px] text-muted-foreground">
                {flow.pagination.mode === 'clients'
                  ? t('dashboard.topologyNodePageInfo', {
                      start: flow.pagination.rangeStart,
                      end: flow.pagination.rangeEnd,
                      total: flow.pagination.totalItems,
                    })
                  : t('dashboard.topologyRoutePageInfo', {
                      start: flow.pagination.rangeStart,
                      end: flow.pagination.rangeEnd,
                      total: flow.pagination.totalItems,
                    })}
              </span>
              <div className="flex items-center gap-1">
                <button
                  type="button"
                  disabled={flow.pagination.page <= 0}
                  onClick={() => handlePageChange(flow.pagination.page - 1)}
                  className="flex size-6 items-center justify-center rounded-md border border-border/75 bg-background text-foreground transition-colors hover:bg-muted disabled:pointer-events-none disabled:opacity-40"
                  aria-label="Previous page"
                >
                  <ChevronLeft className="size-3.5" />
                </button>
                <button
                  type="button"
                  disabled={flow.pagination.page >= flow.pagination.totalPages - 1}
                  onClick={() => handlePageChange(flow.pagination.page + 1)}
                  className="flex size-6 items-center justify-center rounded-md border border-border/75 bg-background text-foreground transition-colors hover:bg-muted disabled:pointer-events-none disabled:opacity-40"
                  aria-label="Next page"
                >
                  <ChevronRight className="size-3.5" />
                </button>
              </div>
            </div>
          ) : null}

          {flow.focusClientId ? (
            <button
              type="button"
              onClick={() => onFocusChange(null)}
              className="text-[11px] text-muted-foreground underline-offset-4 transition-colors hover:text-foreground hover:underline sm:hidden"
            >
              {t('dashboard.topologyBack')}
            </button>
          ) : null}
        </div>
      </div>
    </div>
  );
}
