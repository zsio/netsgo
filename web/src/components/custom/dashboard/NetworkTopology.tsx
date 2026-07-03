import { useLayoutEffect, useEffect, useMemo, useRef, useState } from 'react';
import {
  forceCollide,
  forceLink,
  forceManyBody,
  forceRadial,
  forceSimulation,
  type Simulation,
  type SimulationLinkDatum,
  type SimulationNodeDatum,
} from 'd3-force';
import { select } from 'd3-selection';
import { zoom, zoomIdentity, type ZoomBehavior } from 'd3-zoom';
import { drag } from 'd3-drag';
import 'd3-transition';
import { useNavigate } from '@tanstack/react-router';
import { useTranslation } from 'react-i18next';
import {
  Waypoints, Server as ServerIcon, Laptop, Eye, Undo2, MoveRight, LayersPlus,
  Plus, Minus, LocateFixed, Maximize2, Minimize2, X,
} from 'lucide-react';

import { Skeleton } from '@/components/ui/skeleton';
import { Button } from '@/components/ui/button';
import { useClients } from '@/hooks/use-clients';
import { useServerStatus } from '@/hooks/use-server-status';
import { useAddClientDialog } from '@/components/custom/client/add-client-dialog-context';
import { buildTunnelViewModel, type TunnelStatusPresentation } from '@/lib/tunnel-model';
import { cn } from '@/lib/utils';
import {
  SERVER_NODE_ID,
  buildTopologyGraph,
  computeEdgeOffsets,
  computeQuadraticEdge,
  getTopologyNeighborIds,
  type TopologyEdge,
  type TopologyGraph,
  type TopologyNode,
} from './topology-model';

type StatusKey = TunnelStatusPresentation['key'];

const EDGE_STROKE: Record<StatusKey, string> = {
  exposed: 'stroke-emerald-500/70',
  pending: 'stroke-sky-500/70',
  offline: 'stroke-amber-500/60',
  stopped: 'stroke-muted-foreground/35',
  error: 'stroke-destructive/70',
};

const STATUS_DOT: Record<StatusKey, string> = {
  exposed: 'bg-emerald-500',
  pending: 'bg-sky-500',
  offline: 'bg-amber-500',
  stopped: 'bg-muted-foreground/60',
  error: 'bg-destructive',
};

const STATUS_TEXT: Record<StatusKey, string> = {
  exposed: 'text-emerald-600',
  pending: 'text-sky-600',
  offline: 'text-amber-600',
  stopped: 'text-muted-foreground',
  error: 'text-destructive',
};

const LABEL_HALO = {
  paintOrder: 'stroke',
  stroke: 'var(--color-background)',
  strokeWidth: 3,
  strokeLinejoin: 'round',
} as const;

interface SimNode extends SimulationNodeDatum {
  id: string;
}

interface SimLink extends SimulationLinkDatum<SimNode> {
  kind: 'control' | 'c2c';
}

function statusLabel(t: (key: string, options?: Record<string, unknown>) => string, status: TunnelStatusPresentation) {
  return t(`tunnels.status${status.key[0].toUpperCase()}${status.key.slice(1)}`, {
    defaultValue: status.label,
  });
}

function edgeTouches(edge: TopologyEdge, nodeId: string) {
  return edge.sourceId === nodeId || edge.targetId === nodeId;
}

function truncateLabel(value: string, max = 16) {
  return value.length > max ? `${value.slice(0, max - 1)}…` : value;
}

export function NetworkTopology() {
  const { t } = useTranslation();
  const { data: clients, isLoading } = useClients();
  const { openAddClientDialog } = useAddClientDialog();
  const [focusId, setFocusId] = useState<string | null>(null);
  const [hoveredTunnelId, setHoveredTunnelId] = useState<string | null>(null);
  const [isFullscreen, setIsFullscreen] = useState(false);

  const graph = useMemo(() => buildTopologyGraph(clients), [clients]);

  useEffect(() => {
    if (!isFullscreen) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') setIsFullscreen(false);
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [isFullscreen]);

  if (isLoading) {
    return <Skeleton className="h-[460px] w-full rounded-xl" />;
  }

  const hasClients = graph.nodes.some((node) => node.kind === 'client');

  const canvasEl = hasClients && (
    <TopologyCanvas
      graph={graph}
      focusId={focusId}
      hoveredTunnelId={hoveredTunnelId}
      onFocusChange={setFocusId}
      isFullscreen={isFullscreen}
      onToggleFullscreen={() => setIsFullscreen((v) => !v)}
    />
  );

  const panelEl = hasClients && (
    <TopologySidePanel
      graph={graph}
      focusId={focusId}
      hoveredTunnelId={hoveredTunnelId}
      onHoverTunnel={setHoveredTunnelId}
    />
  );

  return (
    <>
      <div className="rounded-xl border border-border/40 bg-card/50 backdrop-blur-sm shadow-sm overflow-hidden">
        <div className="px-4 sm:px-6 py-3 sm:py-4 border-b border-border/40 bg-muted/20 flex items-center justify-between gap-3">
          <h3 className="flex min-w-0 items-center gap-2 font-semibold text-foreground">
            <Waypoints className="h-5 w-5 text-primary" />
            {t('dashboard.topologyTitle')}
          </h3>
          <span className="hidden text-xs text-muted-foreground sm:block">
            {focusId ? t('dashboard.topologyBlurHint') : t('dashboard.topologyFocusHint')}
          </span>
        </div>

        {hasClients ? (
          <div className="flex flex-col lg:flex-row">
            {!isFullscreen && canvasEl}
            {!isFullscreen && panelEl}
          </div>
        ) : (
          <div className="flex flex-col items-center justify-center gap-4 py-16 text-muted-foreground">
            <Waypoints className="h-12 w-12 opacity-20" />
            <p className="text-sm">{t('dashboard.noClients')}</p>
            <Button type="button" variant="secondary" size="sm" onClick={openAddClientDialog}>
              <LayersPlus className="mr-1.5 h-4 w-4" />
              {t('dashboard.addClient')}
            </Button>
          </div>
        )}
      </div>

      {isFullscreen && hasClients && (
        <div className="fixed inset-0 z-50 flex flex-col bg-card/95 backdrop-blur-md">
          <div className="flex items-center justify-between gap-3 border-b border-border/40 bg-muted/20 px-4 py-2.5 sm:px-6">
            <h3 className="flex min-w-0 items-center gap-2 font-semibold text-foreground">
              <Waypoints className="h-5 w-5 text-primary" />
              {t('dashboard.topologyTitle')}
            </h3>
            <Button
              type="button"
              variant="secondary"
              size="sm"
              className="h-7 gap-1.5 px-2.5 text-xs"
              onClick={() => setIsFullscreen(false)}
            >
              <X className="h-3.5 w-3.5" />
              {t('dashboard.topologyExitFullscreen')}
            </Button>
          </div>
          <div className="flex min-h-0 flex-1 flex-col lg:flex-row">
            {canvasEl}
            <div className="hidden lg:flex">{panelEl}</div>
          </div>
        </div>
      )}
    </>
  );
}

function TopologyCanvas({
  graph,
  focusId,
  hoveredTunnelId,
  onFocusChange,
  isFullscreen,
  onToggleFullscreen,
}: {
  graph: TopologyGraph;
  focusId: string | null;
  hoveredTunnelId: string | null;
  onFocusChange: (id: string | null) => void;
  isFullscreen: boolean;
  onToggleFullscreen: () => void;
}) {
  const { t } = useTranslation();
  const containerRef = useRef<HTMLDivElement>(null);
  const svgRef = useRef<SVGSVGElement>(null);
  const sceneRef = useRef<SVGGElement>(null);
  const simRef = useRef<Simulation<SimNode, undefined> | null>(null);
  const nodePosRef = useRef(new Map<string, SimNode>());
  const zoomRef = useRef<ZoomBehavior<SVGSVGElement, unknown> | null>(null);
  const pinnedIdRef = useRef<string>(SERVER_NODE_ID);
  const [size, setSize] = useState({ width: 0, height: 0 });
  const [hoverNodeId, setHoverNodeId] = useState<string | null>(null);

  useLayoutEffect(() => {
    const element = containerRef.current;
    if (!element) return;
    const observer = new ResizeObserver((entries) => {
      const rect = entries[0]?.contentRect;
      if (rect) {
        setSize({ width: rect.width, height: rect.height });
      }
    });
    observer.observe(element);
    return () => observer.disconnect();
  }, []);

  const effectiveFocusId = focusId !== null && graph.nodes.some((node) => node.id === focusId)
    ? focusId
    : null;

  const visibleNodes = useMemo(() => {
    if (!effectiveFocusId || effectiveFocusId === SERVER_NODE_ID) {
      return graph.nodes;
    }
    const keep = getTopologyNeighborIds(graph, effectiveFocusId);
    keep.add(effectiveFocusId);
    keep.add(SERVER_NODE_ID);
    return graph.nodes.filter((node) => keep.has(node.id));
  }, [graph, effectiveFocusId]);

  const visibleEdges = useMemo(() => {
    const ids = new Set(visibleNodes.map((node) => node.id));
    return graph.edges.filter((edge) => ids.has(edge.sourceId) && ids.has(edge.targetId));
  }, [graph, visibleNodes]);

  const edgeOffsets = useMemo(() => computeEdgeOffsets(visibleEdges), [visibleEdges]);

  const tunnelCountByNode = useMemo(() => {
    const counts = new Map<string, number>();
    for (const edge of graph.edges) {
      counts.set(edge.sourceId, (counts.get(edge.sourceId) ?? 0) + 1);
      counts.set(edge.targetId, (counts.get(edge.targetId) ?? 0) + 1);
    }
    return counts;
  }, [graph]);

  // 结构签名：只有拓扑结构真正变化时才重启力导向模拟，
  // 避免周期性的 stats 刷新导致画面持续抖动。
  const structureSignature = useMemo(() => {
    const nodesPart = visibleNodes.map((node) => `${node.id}:${node.online ? 1 : 0}`).join(',');
    const edgesPart = visibleEdges.map((edge) => `${edge.id}:${edge.sourceId}>${edge.targetId}`).join(',');
    return `${nodesPart}|${edgesPart}`;
  }, [visibleNodes, visibleEdges]);

  const visibleNodesRef = useRef(visibleNodes);
  visibleNodesRef.current = visibleNodes;
  const visibleEdgesRef = useRef(visibleEdges);
  visibleEdgesRef.current = visibleEdges;
  const edgeOffsetsRef = useRef(edgeOffsets);
  edgeOffsetsRef.current = edgeOffsets;

  const { width, height } = size;

  // 缩放 / 平移
  useEffect(() => {
    const svgElement = svgRef.current;
    if (!svgElement) return;
    const svg = select(svgElement);
    const behavior = zoom<SVGSVGElement, unknown>()
      .scaleExtent([0.5, 2.5])
      .filter((event: MouseEvent | WheelEvent | TouchEvent) => {
        if (event.type === 'dblclick') return false;
        return !(event as MouseEvent).button;
      })
      .on('zoom', (event) => {
        select(sceneRef.current).attr('transform', event.transform.toString());
      });
    svg.call(behavior);
    zoomRef.current = behavior;
    return () => {
      svg.on('.zoom', null);
      zoomRef.current = null;
    };
  }, []);

  // 力导向模拟
  useLayoutEffect(() => {
    if (!width || !height) return;

    const cx = width / 2;
    const cy = height / 2;
    const ringRadius = Math.max(Math.min(width, height) / 2 - 86, 76);
    const nodePos = nodePosRef.current;

    const nodes = visibleNodesRef.current;
    const edges = visibleEdgesRef.current;
    const pinnedId = effectiveFocusId ?? SERVER_NODE_ID;
    pinnedIdRef.current = pinnedId;

    const simNodes = nodes.map((node) => {
      let sim = nodePos.get(node.id);
      if (!sim) {
        sim = {
          id: node.id,
          x: cx + (Math.random() - 0.5) * 80,
          y: cy + (Math.random() - 0.5) * 80,
        };
        nodePos.set(node.id, sim);
      }
      sim.fx = null;
      sim.fy = null;
      return sim;
    });
    const pinned = nodePos.get(pinnedId);
    if (pinned) {
      pinned.fx = cx;
      pinned.fy = cy;
    }

    const simLinks: SimLink[] = [];
    for (const node of nodes) {
      if (node.kind === 'client') {
        simLinks.push({ source: SERVER_NODE_ID, target: node.id, kind: 'control' });
      }
    }
    const seenPairs = new Set<string>();
    for (const edge of edges) {
      if (edge.sourceId === SERVER_NODE_ID || edge.targetId === SERVER_NODE_ID) continue;
      const key = edge.sourceId < edge.targetId
        ? `${edge.sourceId}|${edge.targetId}`
        : `${edge.targetId}|${edge.sourceId}`;
      if (seenPairs.has(key)) continue;
      seenPairs.add(key);
      simLinks.push({ source: edge.sourceId, target: edge.targetId, kind: 'c2c' });
    }

    const ticked = () => {
      const scene = sceneRef.current;
      if (!scene) return;
      for (const sim of simNodes) {
        sim.x = Math.max(34, Math.min(width - 34, sim.x ?? cx));
        sim.y = Math.max(38, Math.min(height - 46, sim.y ?? cy));
      }
      const sceneSel = select(scene);
      sceneSel.select('[data-layer="nodes"]')
        .selectAll<SVGGElement, unknown>('[data-node-id]')
        .attr('transform', function positionNode() {
          const sim = nodePos.get(this.dataset.nodeId ?? '');
          return sim ? `translate(${sim.x},${sim.y})` : null;
        });

      const server = nodePos.get(SERVER_NODE_ID);
      sceneSel.selectAll<SVGPathElement, unknown>('[data-control-id]')
        .attr('d', function controlPath() {
          const client = nodePos.get(this.dataset.controlId ?? '');
          if (!client || !server) return null;
          return `M ${server.x} ${server.y} L ${client.x} ${client.y}`;
        });

      const offsets = edgeOffsetsRef.current;
      const edgeList = visibleEdgesRef.current;
      const geometryById = new Map<string, ReturnType<typeof computeQuadraticEdge>>();
      for (const edge of edgeList) {
        const source = nodePos.get(edge.sourceId);
        const target = nodePos.get(edge.targetId);
        if (!source || !target) continue;
        geometryById.set(edge.id, computeQuadraticEdge(
          edge.sourceId,
          edge.targetId,
          { x: source.x ?? cx, y: source.y ?? cy },
          { x: target.x ?? cx, y: target.y ?? cy },
          offsets.get(edge.id) ?? 0,
        ));
      }
      sceneSel.selectAll<SVGGElement, unknown>('[data-edge-id]').each(function updateEdge() {
        const geometry = geometryById.get(this.dataset.edgeId ?? '');
        if (!geometry) return;
        select(this).selectAll('path').attr('d', geometry.path);
      });
      sceneSel.selectAll<SVGGElement, unknown>('[data-edge-label]')
        .attr('transform', function positionLabel() {
          const geometry = geometryById.get(this.dataset.edgeLabel ?? '');
          return geometry ? `translate(${geometry.midpoint.x},${geometry.midpoint.y})` : null;
        });
    };

    const simulation = forceSimulation<SimNode>(simNodes)
      .force('charge', forceManyBody<SimNode>().strength(-520))
      .force('collide', forceCollide<SimNode>(50))
      .force('radial', forceRadial<SimNode>(ringRadius, cx, cy).strength(
        (node) => (node.id === pinnedId ? 0 : 0.12),
      ))
      .force('link', forceLink<SimNode, SimLink>(simLinks)
        .id((node) => node.id)
        .distance((link) => (link.kind === 'c2c' ? Math.min(180, ringRadius * 1.15) : ringRadius))
        .strength((link) => (link.kind === 'c2c' ? 0.28 : 0.16)))
      .alpha(0.9)
      .alphaDecay(0.045)
      .on('tick', ticked);

    simRef.current = simulation;
    ticked();

    return () => {
      simulation.stop();
      simRef.current = null;
    };
  }, [structureSignature, effectiveFocusId, width, height]);

  // 节点拖拽
  useEffect(() => {
    const svgElement = svgRef.current;
    if (!svgElement) return;
    const nodePos = nodePosRef.current;
    const behavior = drag<SVGGElement, unknown>()
      .clickDistance(5)
      .on('start', function dragStart(event) {
        event.sourceEvent?.stopPropagation();
        const sim = nodePos.get(this.dataset.nodeId ?? '');
        if (!sim) return;
        simRef.current?.alphaTarget(0.25).restart();
        sim.fx = sim.x;
        sim.fy = sim.y;
      })
      .on('drag', function dragMove(event) {
        const sim = nodePos.get(this.dataset.nodeId ?? '');
        if (!sim) return;
        sim.fx = event.x;
        sim.fy = event.y;
      })
      .on('end', function dragEnd() {
        simRef.current?.alphaTarget(0);
        const id = this.dataset.nodeId ?? '';
        const sim = nodePos.get(id);
        if (!sim) return;
        if (id !== pinnedIdRef.current) {
          sim.fx = null;
          sim.fy = null;
        }
      });
    select(svgElement)
      .selectAll<SVGGElement, unknown>('[data-node-id]')
      .call(behavior);
  }, [structureSignature]);

  const hoverNeighborIds = useMemo(
    () => (hoverNodeId ? getTopologyNeighborIds(graph, hoverNodeId) : null),
    [graph, hoverNodeId],
  );

  const nodeOpacity = (node: TopologyNode) => {
    if (!hoverNodeId || node.id === hoverNodeId) return 1;
    if (node.id === SERVER_NODE_ID || hoverNeighborIds?.has(node.id)) return 1;
    return 0.3;
  };

  const edgeOpacity = (edge: TopologyEdge) => {
    if (hoveredTunnelId) return edge.id === hoveredTunnelId ? 1 : 0.12;
    if (hoverNodeId) return edgeTouches(edge, hoverNodeId) ? 1 : 0.15;
    if (effectiveFocusId) return edgeTouches(edge, effectiveFocusId) ? 0.95 : 0.35;
    return 0.85;
  };

  const controlOpacity = (clientId: string) => {
    if (hoveredTunnelId) return 0.15;
    if (hoverNodeId) return clientId === hoverNodeId ? 0.8 : 0.15;
    return 0.65;
  };

  const zoomBy = (factor: number) => {
    const svgElement = svgRef.current;
    if (!svgElement || !zoomRef.current) return;
    zoomRef.current.scaleBy(select(svgElement).transition().duration(200), factor);
  };

  const resetView = () => {
    const svgElement = svgRef.current;
    if (!svgElement || !zoomRef.current) return;
    zoomRef.current.transform(select(svgElement).transition().duration(280), zoomIdentity);
  };

  return (
    <div
      ref={containerRef}
      className={cn(
        'relative min-w-0 flex-1 overflow-hidden',
        isFullscreen ? 'h-full' : 'h-[340px] sm:h-[460px]',
      )}
    >
      {/* 画布氛围：细点阵 + 中心微光 + 边缘渐隐 */}
      <div
        aria-hidden
        className="absolute inset-0 [background-image:radial-gradient(circle,var(--color-border)_1px,transparent_1px)] [background-size:20px_20px] opacity-50"
      />
      <div
        aria-hidden
        className="absolute inset-0 bg-[radial-gradient(ellipse_60%_55%_at_50%_50%,transparent_55%,var(--color-card)_100%)]"
      />

      <svg ref={svgRef} className="absolute inset-0 h-full w-full cursor-grab active:cursor-grabbing">
        <defs>
          <radialGradient id="topo-center-glow">
            <stop offset="0%" stopColor="var(--color-primary)" stopOpacity="0.07" />
            <stop offset="100%" stopColor="var(--color-primary)" stopOpacity="0" />
          </radialGradient>
          <filter id="topo-soft" x="-60%" y="-60%" width="220%" height="220%">
            <feGaussianBlur stdDeviation="6" />
          </filter>
        </defs>

        <rect
          width="100%"
          height="100%"
          fill="transparent"
          onClick={() => onFocusChange(null)}
        />

        <g ref={sceneRef}>
          {width > 0 && (
            <circle cx={width / 2} cy={height / 2} r={Math.min(width, height) / 2.4} fill="url(#topo-center-glow)" />
          )}

          <g data-layer="links">
            {visibleNodes
              .filter((node) => node.kind === 'client')
              .map((node) => (
                <path
                  key={`control-${node.id}`}
                  data-control-id={node.id}
                  fill="none"
                  strokeWidth={1}
                  strokeDasharray="2 6"
                  strokeLinecap="round"
                  className={cn(
                    'transition-opacity duration-300',
                    node.online ? 'stroke-emerald-500/50' : 'stroke-muted-foreground/40',
                  )}
                  style={{ opacity: controlOpacity(node.id) }}
                />
              ))}
            {visibleEdges.map((edge) => (
              <g
                key={edge.id}
                data-edge-id={edge.id}
                className="transition-opacity duration-300"
                style={{ opacity: edgeOpacity(edge) }}
              >
                <path
                  fill="none"
                  strokeLinecap="round"
                  strokeWidth={hoveredTunnelId === edge.id ? 2.4 : 1.6}
                  className={EDGE_STROKE[edge.status.key]}
                />
                {edge.status.key === 'exposed' && (
                  <path
                    fill="none"
                    strokeLinecap="round"
                    strokeWidth={2.4}
                    strokeDasharray="1.5 10.5"
                    className="stroke-emerald-500"
                    style={{ animation: 'topology-flow 1.1s linear infinite' }}
                  />
                )}
              </g>
            ))}
          </g>

          <g data-layer="nodes">
            {visibleNodes.map((node) => (
              <TopologyNodeView
                key={node.id}
                node={node}
                focused={effectiveFocusId === node.id}
                tunnelCount={tunnelCountByNode.get(node.id) ?? 0}
                opacity={nodeOpacity(node)}
                onClick={() => onFocusChange(effectiveFocusId === node.id ? null : node.id)}
                onHover={(hovering) => setHoverNodeId(hovering ? node.id : null)}
              />
            ))}
          </g>

          <g data-layer="labels" className="pointer-events-none">
            {effectiveFocusId && visibleEdges
              .filter((edge) => edgeTouches(edge, effectiveFocusId))
              .map((edge) => (
                <g key={`edge-label-${edge.id}`} data-edge-label={edge.id}>
                  <text
                    textAnchor="middle"
                    dy={-5}
                    className="fill-muted-foreground font-mono text-[9px]"
                    style={LABEL_HALO}
                  >
                    {truncateLabel(edge.tunnel.name)}
                  </text>
                </g>
              ))}
          </g>
        </g>
      </svg>

      {/* 返回全览 */}
      <div
        className={cn(
          'absolute left-3 top-3 z-20 transition-all duration-300',
          effectiveFocusId ? 'translate-y-0 opacity-100' : 'pointer-events-none -translate-y-1 opacity-0',
        )}
      >
        <Button
          type="button"
          variant="secondary"
          size="sm"
          className="h-7 gap-1.5 border border-border/40 bg-background/80 px-2.5 text-xs shadow-sm backdrop-blur-sm hover:bg-background"
          onClick={() => onFocusChange(null)}
        >
          <Undo2 className="h-3.5 w-3.5" />
          {t('dashboard.topologyBack')}
        </Button>
      </div>

      {/* 缩放控件 */}
      <div className="absolute bottom-3 right-3 z-20 flex flex-col overflow-hidden rounded-lg border border-border/50 bg-background/80 shadow-sm backdrop-blur-sm">
        <button
          type="button"
          className="flex size-7 items-center justify-center text-muted-foreground transition-colors hover:bg-muted/60 hover:text-foreground"
          aria-label={t('dashboard.topologyZoomIn')}
          onClick={() => zoomBy(1.3)}
        >
          <Plus className="size-3.5" />
        </button>
        <button
          type="button"
          className="flex size-7 items-center justify-center border-t border-border/40 text-muted-foreground transition-colors hover:bg-muted/60 hover:text-foreground"
          aria-label={t('dashboard.topologyZoomOut')}
          onClick={() => zoomBy(1 / 1.3)}
        >
          <Minus className="size-3.5" />
        </button>
        <button
          type="button"
          className="flex size-7 items-center justify-center border-t border-border/40 text-muted-foreground transition-colors hover:bg-muted/60 hover:text-foreground"
          aria-label={t('dashboard.topologyResetView')}
          onClick={resetView}
        >
          <LocateFixed className="size-3.5" />
        </button>
        <button
          type="button"
          className="flex size-7 items-center justify-center border-t border-border/40 text-muted-foreground transition-colors hover:bg-muted/60 hover:text-foreground"
          aria-label={isFullscreen ? t('dashboard.topologyExitFullscreen') : t('dashboard.topologyFullscreen')}
          onClick={onToggleFullscreen}
        >
          {isFullscreen ? <Minimize2 className="size-3.5" /> : <Maximize2 className="size-3.5" />}
        </button>
      </div>

      {/* 图例 */}
      <div className="pointer-events-none absolute bottom-3 left-3 z-20 flex items-center gap-3 rounded-lg border border-border/40 bg-background/75 px-2.5 py-1.5 text-[10px] text-muted-foreground shadow-sm backdrop-blur-sm">
        <span className="flex items-center gap-1.5">
          <span className="size-1.5 rounded-full bg-emerald-500" />
          {t('tunnels.statusExposed')}
        </span>
        <span className="flex items-center gap-1.5">
          <span className="size-1.5 rounded-full bg-amber-500" />
          {t('tunnels.statusOffline')}
        </span>
        <span className="flex items-center gap-1.5">
          <span className="size-1.5 rounded-full bg-destructive" />
          {t('tunnels.statusError')}
        </span>
        <span className="hidden items-center gap-1.5 sm:flex">
          <svg width="16" height="6" aria-hidden>
            <line x1="0" y1="3" x2="16" y2="3" strokeDasharray="2 4" strokeWidth="1.2" className="stroke-muted-foreground/70" />
          </svg>
          {t('dashboard.topologyControlLink')}
        </span>
      </div>
    </div>
  );
}

function TopologyNodeView({
  node,
  focused,
  tunnelCount,
  opacity,
  onClick,
  onHover,
}: {
  node: TopologyNode;
  focused: boolean;
  tunnelCount: number;
  opacity: number;
  onClick: () => void;
  onHover: (hovering: boolean) => void;
}) {
  const { t } = useTranslation();
  const { data: status } = useServerStatus();
  const isServer = node.kind === 'server';

  return (
    <g
      data-node-id={node.id}
      className="cursor-pointer transition-opacity duration-300"
      style={{ opacity }}
      onClick={(event) => {
        event.stopPropagation();
        onClick();
      }}
      onMouseEnter={() => onHover(true)}
      onMouseLeave={() => onHover(false)}
      role="button"
      aria-label={isServer ? t('dashboard.topologyServer') : node.label}
      aria-pressed={focused}
    >
      {isServer ? (
        <>
          <circle r={34} className="fill-primary/15" filter="url(#topo-soft)" />
          <circle r={25} className="topology-pulse fill-none stroke-primary/40" strokeWidth={1} />
          <rect
            x={-21}
            y={-21}
            width={42}
            height={42}
            rx={13}
            fill="var(--color-card)"
            strokeWidth={1.5}
            className={cn('transition-colors', focused ? 'stroke-primary' : 'stroke-primary/60')}
          />
          {focused && (
            <rect
              x={-26}
              y={-26}
              width={52}
              height={52}
              rx={16}
              fill="none"
              strokeWidth={1.2}
              strokeDasharray="3 5"
              className="stroke-primary/50"
            />
          )}
          <ServerIcon x={-10} y={-10} width={20} height={20} strokeWidth={1.75} className="text-primary" />
          <text y={38} textAnchor="middle" className="fill-foreground text-[11px] font-medium" style={LABEL_HALO}>
            {t('dashboard.topologyServer')}
          </text>
          {status?.hostname && (
            <text y={51} textAnchor="middle" className="fill-muted-foreground font-mono text-[9px]" style={LABEL_HALO}>
              {truncateLabel(status.hostname, 22)}
            </text>
          )}
        </>
      ) : (
        <>
          <circle
            r={25}
            className={node.online ? 'fill-emerald-500/12' : 'fill-muted-foreground/8'}
            filter="url(#topo-soft)"
          />
          <circle
            r={17}
            fill="var(--color-card)"
            strokeWidth={1.5}
            className={cn(
              'transition-colors',
              focused
                ? 'stroke-primary'
                : node.online
                  ? 'stroke-emerald-500/60'
                  : 'stroke-border',
            )}
          />
          {focused && (
            <circle r={23} fill="none" strokeWidth={1.2} strokeDasharray="3 5" className="stroke-primary/50" />
          )}
          <Laptop
            x={-8}
            y={-8}
            width={16}
            height={16}
            strokeWidth={1.75}
            className={node.online ? 'text-foreground/80' : 'text-muted-foreground/70'}
          />
          <circle
            cx={12}
            cy={-12}
            r={4}
            stroke="var(--color-background)"
            strokeWidth={1.5}
            className={node.online ? 'fill-emerald-500' : 'fill-muted-foreground/50'}
          />
          {tunnelCount > 0 && (
            <g transform="translate(14, 12)">
              <circle r={7} fill="var(--color-muted)" stroke="var(--color-border)" strokeWidth={1} />
              <text
                textAnchor="middle"
                dy={2.5}
                className="fill-muted-foreground font-mono text-[8px] font-medium"
              >
                {tunnelCount}
              </text>
            </g>
          )}
          <text
            y={32}
            textAnchor="middle"
            className={cn(
              'text-[10.5px] font-medium',
              node.online ? 'fill-foreground' : 'fill-muted-foreground',
            )}
            style={LABEL_HALO}
          >
            {truncateLabel(node.label, 20)}
          </text>
        </>
      )}
    </g>
  );
}

function TopologySidePanel({
  graph,
  focusId,
  hoveredTunnelId,
  onHoverTunnel,
}: {
  graph: TopologyGraph;
  focusId: string | null;
  hoveredTunnelId: string | null;
  onHoverTunnel: (id: string | null) => void;
}) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const { data: status } = useServerStatus();

  const focusNode = focusId ? graph.nodes.find((node) => node.id === focusId) : undefined;
  const relatedEdges = focusId
    ? graph.edges.filter((edge) => edgeTouches(edge, focusId))
    : graph.edges;

  const clientNodes = graph.nodes.filter((node) => node.kind === 'client');
  const onlineClients = clientNodes.filter((node) => node.online).length;
  const activeTunnels = graph.edges.filter((edge) => edge.status.key === 'exposed').length;

  return (
    <div className="flex w-full shrink-0 flex-col border-t border-border/40 lg:w-80 lg:border-l lg:border-t-0">
      {/* 节点信息 */}
      <div className="flex flex-col gap-3 border-b border-border/40 bg-muted/10 p-4">
        {focusNode ? (
          <>
            <div className="flex items-center gap-3">
              {focusNode.kind === 'server' ? (
                <span className="flex size-9 shrink-0 items-center justify-center rounded-xl border border-primary/30 bg-gradient-to-br from-primary/15 to-primary/5 text-primary">
                  <ServerIcon className="size-4" />
                </span>
              ) : (
                <span
                  className={cn(
                    'flex size-9 shrink-0 items-center justify-center rounded-xl border bg-gradient-to-br',
                    focusNode.online
                      ? 'border-emerald-500/30 from-emerald-500/15 to-emerald-500/5 text-emerald-600'
                      : 'border-border from-muted/60 to-muted/20 text-muted-foreground',
                  )}
                >
                  <Laptop className="size-4" />
                </span>
              )}
              <div className="min-w-0 flex-1">
                <p className="truncate text-sm font-semibold text-foreground">
                  {focusNode.kind === 'server' ? t('dashboard.topologyServer') : focusNode.label}
                </p>
                {focusNode.kind === 'server' ? (
                  <p className="truncate font-mono text-[11px] text-muted-foreground">{status?.hostname || '-'}</p>
                ) : (
                  <p className={cn(
                    'flex items-center gap-1.5 text-[11px]',
                    focusNode.online ? 'text-emerald-600' : 'text-muted-foreground',
                  )}>
                    <span className={cn('size-1.5 rounded-full', focusNode.online ? 'bg-emerald-500' : 'bg-muted-foreground/50')} />
                    {focusNode.online ? t('clients.online') : t('clients.offline')}
                  </p>
                )}
              </div>
              {focusNode.kind === 'client' && (
                <Button
                  type="button"
                  variant="secondary"
                  size="sm"
                  className="h-7 gap-1.5 px-2.5 text-xs"
                  onClick={() => navigate({
                    to: '/dashboard/clients/$clientId',
                    params: { clientId: focusNode.id },
                  })}
                >
                  <Eye className="h-3.5 w-3.5" />
                  {t('dashboard.viewDetails')}
                </Button>
              )}
            </div>
            <div className="grid grid-cols-2 gap-2">
              {focusNode.kind === 'client' && focusNode.client ? (
                <>
                  <PanelStat label={t('dashboard.system')} value={`${focusNode.client.info.os}/${focusNode.client.info.arch}`} />
                  <PanelStat label={t('clients.clientVersion')} value={focusNode.client.info.version || '-'} />
                </>
              ) : (
                <>
                  <PanelStat label={t('dashboard.topologyListenPort')} value={String(status?.listen_port || '-')} />
                  <PanelStat label={t('dashboard.topologyOnlineClients')} value={`${onlineClients} / ${clientNodes.length}`} />
                </>
              )}
            </div>
          </>
        ) : (
          <>
            <div className="flex items-center gap-3">
              <span className="flex size-9 shrink-0 items-center justify-center rounded-xl border border-border bg-gradient-to-br from-muted/60 to-muted/20 text-muted-foreground">
                <Waypoints className="size-4" />
              </span>
              <div className="min-w-0 flex-1">
                <p className="text-sm font-semibold text-foreground">{t('dashboard.topologyOverviewTitle')}</p>
                <p className="truncate text-[11px] text-muted-foreground">{t('dashboard.topologyOverviewHint')}</p>
              </div>
            </div>
            <div className="grid grid-cols-2 gap-2">
              <PanelStat label={t('dashboard.topologyOnlineClients')} value={`${onlineClients} / ${clientNodes.length}`} />
              <PanelStat label={t('dashboard.activeTunnels')} value={`${activeTunnels} / ${graph.edges.length}`} />
            </div>
          </>
        )}
      </div>

      {/* 相关隧道列表 */}
      <div className="flex min-h-0 flex-1 flex-col">
        <p className="flex items-center px-4 pb-1.5 pt-3 text-[10px] font-medium uppercase tracking-wider text-muted-foreground/70">
          {focusNode ? t('dashboard.topologyRelatedTunnels') : t('dashboard.allTunnels')}
          <span className="ml-1.5 rounded-full bg-muted px-1.5 py-0.5 font-mono text-[9px] text-muted-foreground">
            {relatedEdges.length}
          </span>
        </p>
        {relatedEdges.length === 0 ? (
          <p className="px-4 py-6 text-center text-xs text-muted-foreground">
            {t('dashboard.topologyNoTunnels')}
          </p>
        ) : (
          <div className="max-h-72 overflow-y-auto px-2 pb-2 [scrollbar-width:thin] lg:max-h-none lg:flex-1">
            {relatedEdges.map((edge) => (
              <TunnelListItem
                key={edge.id}
                edge={edge}
                graph={graph}
                hovered={hoveredTunnelId === edge.id}
                onHover={onHoverTunnel}
                onNavigate={() => navigate({
                  to: '/dashboard/clients/$clientId',
                  params: { clientId: edge.tunnel.client_id },
                })}
              />
            ))}
          </div>
        )}
      </div>
    </div>
  );
}

function PanelStat({ label, value }: { label: string; value: string }) {
  return (
    <div className="rounded-lg border border-border/40 bg-background/60 px-2.5 py-1.5">
      <p className="text-[9px] font-medium uppercase tracking-wider text-muted-foreground/70">{label}</p>
      <p className="mt-0.5 truncate font-mono text-xs text-foreground" title={value}>{value}</p>
    </div>
  );
}

function TunnelListItem({
  edge,
  graph,
  hovered,
  onHover,
  onNavigate,
}: {
  edge: TopologyEdge;
  graph: TopologyGraph;
  hovered: boolean;
  onHover: (id: string | null) => void;
  onNavigate: () => void;
}) {
  const { t } = useTranslation();
  const view = buildTunnelViewModel(edge.tunnel, true);
  const sourceNode = graph.nodes.find((node) => node.id === edge.sourceId);
  const targetNode = graph.nodes.find((node) => node.id === edge.targetId);
  const sourceName = sourceNode?.kind === 'server' ? t('dashboard.topologyServer') : sourceNode?.label ?? '-';
  const targetName = targetNode?.kind === 'server' ? t('dashboard.topologyServer') : targetNode?.label ?? '-';

  return (
    <button
      type="button"
      className={cn(
        'flex w-full flex-col gap-1 rounded-lg border border-transparent px-2.5 py-2 text-left transition-colors',
        hovered ? 'border-border/40 bg-muted/50' : 'hover:border-border/40 hover:bg-muted/40',
      )}
      onMouseEnter={() => onHover(edge.id)}
      onMouseLeave={() => onHover(null)}
      onFocus={() => onHover(edge.id)}
      onBlur={() => onHover(null)}
      onClick={onNavigate}
      title={`${view.routeLabel} · ${statusLabel(t, edge.status)}`}
    >
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
      <span className="flex min-w-0 items-center gap-1 font-mono text-[10px] leading-4 text-muted-foreground">
        <span className="truncate" title={`${sourceName} ${view.targetLabel}`}>{sourceName}</span>
        <MoveRight className="size-3 shrink-0 text-emerald-500/70" />
        <span className="truncate" title={`${targetName} ${view.destinationLabel}`}>{targetName}</span>
      </span>
      <span className="block min-w-0 truncate font-mono text-[10px] leading-4 text-primary/70">
        {view.targetLabel} → {view.destinationLabel}
      </span>
    </button>
  );
}
