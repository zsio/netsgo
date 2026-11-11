import { useEffect, useMemo, useState } from 'react';

import { cn } from '@/lib/utils';
import {
  SERVER_NODE_ID,
  computeEdgeOffsets,
  getTopologyNeighborIds,
  shouldRenderControlLink,
  type TopologyGraph,
  type TopologyNode,
  type TopologyTrafficSnapshot,
  type TopologyViewState,
} from './topology-model';
import {
  TopologyDefs,
  TopologyLabelsGroup,
  TopologyLinksGroup,
} from './TopologyEdgesLayer';
import { TopologyBackdrop, TopologyHud } from './TopologyHud';
import { TopologyNodeView } from './TopologyNodeView';
import { useTopologySimulation } from './use-topology-simulation';

export function TopologyCanvas({
  graph,
  trafficSnapshot,
  focusId,
  hoveredTunnelId,
  onHoverTunnel,
  onFocusChange,
  isFullscreen,
  onToggleFullscreen,
}: {
  graph: TopologyGraph;
  trafficSnapshot: TopologyTrafficSnapshot;
  focusId: string | null;
  hoveredTunnelId: string | null;
  onHoverTunnel: (id: string | null) => void;
  onFocusChange: (id: string | null) => void;
  isFullscreen: boolean;
  onToggleFullscreen: () => void;
}) {
  const [hoverNodeId, setHoverNodeId] = useState<string | null>(null);

  const effectiveFocusId = focusId !== null
    && focusId !== SERVER_NODE_ID
    && graph.nodes.some((node) => node.id === focusId)
    ? focusId
    : null;
  const effectiveHoveredTunnelId = effectiveFocusId ? hoveredTunnelId : null;

  useEffect(() => {
    if (!effectiveFocusId && hoveredTunnelId) {
      onHoverTunnel(null);
    }
  }, [effectiveFocusId, hoveredTunnelId, onHoverTunnel]);

  const visibleNodes = useMemo(() => {
    if (!effectiveFocusId) {
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

  const viewState = useMemo<TopologyViewState>(() => ({
    focusId: effectiveFocusId,
    hoverNodeId,
    hoveredTunnelId: effectiveHoveredTunnelId,
  }), [effectiveFocusId, effectiveHoveredTunnelId, hoverNodeId]);

  const controlNodes = useMemo(
    () => visibleNodes.filter((node) => node.kind === 'client' && shouldRenderControlLink(node.id, viewState)),
    [viewState, visibleNodes],
  );

  const edgeOffsets = useMemo(() => computeEdgeOffsets(visibleEdges), [visibleEdges]);

  const tunnelCountByNode = useMemo(() => {
    const counts = new Map<string, number>();
    for (const edge of graph.edges) {
      counts.set(edge.sourceId, (counts.get(edge.sourceId) ?? 0) + 1);
      counts.set(edge.targetId, (counts.get(edge.targetId) ?? 0) + 1);
    }
    return counts;
  }, [graph]);

  const {
    containerRef,
    svgRef,
    sceneRef,
    layoutReady,
    zoomBy,
    resetView,
  } = useTopologySimulation({
    effectiveFocusId,
    visibleNodes,
    visibleEdges,
    edgeOffsets,
  });

  const hoverNeighborIds = useMemo(
    () => (hoverNodeId ? getTopologyNeighborIds(graph, hoverNodeId) : null),
    [graph, hoverNodeId],
  );

  const nodeOpacity = (node: TopologyNode) => {
    if (!hoverNodeId || node.id === hoverNodeId) return 1;
    if (node.id === SERVER_NODE_ID || hoverNeighborIds?.has(node.id)) return 1;
    return 0.3;
  };

  return (
    <div
      ref={containerRef}
      className={cn(
        'relative min-w-0 flex-1 overflow-hidden',
        isFullscreen ? 'h-full' : 'h-[340px] sm:h-[460px]',
      )}
    >
      <TopologyBackdrop />

      <svg ref={svgRef} className="absolute inset-0 h-full w-full cursor-grab active:cursor-grabbing">
        <TopologyDefs visibleEdges={visibleEdges} />

        <rect
          width="100%"
          height="100%"
          fill="transparent"
        />

        <g
          ref={sceneRef}
          style={{ opacity: layoutReady ? 1 : 0 }}
        >
          <TopologyLinksGroup
            controlNodes={controlNodes}
            visibleEdges={visibleEdges}
            trafficSnapshot={trafficSnapshot}
            viewState={viewState}
            onHoverTunnel={onHoverTunnel}
          />

          <g data-layer="nodes">
            {visibleNodes.map((node) => (
              <TopologyNodeView
                key={node.id}
                node={node}
                focused={effectiveFocusId === node.id}
                tunnelCount={tunnelCountByNode.get(node.id) ?? 0}
                opacity={nodeOpacity(node)}
                onClick={() => onFocusChange(
                  node.id === SERVER_NODE_ID || effectiveFocusId === node.id ? null : node.id,
                )}
                onHover={(hovering) => setHoverNodeId(hovering ? node.id : null)}
              />
            ))}
          </g>

          <TopologyLabelsGroup
            controlNodes={controlNodes}
            visibleEdges={visibleEdges}
            trafficSnapshot={trafficSnapshot}
            viewState={viewState}
          />
        </g>
      </svg>

      <TopologyHud
        effectiveFocusId={effectiveFocusId}
        isFullscreen={isFullscreen}
        onExitFocus={() => onFocusChange(null)}
        onZoomBy={zoomBy}
        onResetView={resetView}
        onToggleFullscreen={onToggleFullscreen}
      />
    </div>
  );
}
