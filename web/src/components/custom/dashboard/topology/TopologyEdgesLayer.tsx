import { cn } from '@/lib/utils';
import {
  getControlLinkEmphasis,
  getTunnelEdgeEmphasis,
  topologyEdgeTouches,
  type TopologyEdge,
  type TopologyNode,
  type TopologyTrafficSnapshot,
  type TopologyViewState,
} from './topology-model';
import {
  EDGE_FLOW_COLORS,
  EDGE_STROKE,
  LABEL_HALO,
  emphasisOpacity,
  flowDuration,
  formatTrafficPair,
  hasTraffic,
  trafficStrokeWidth,
  tunnelStreamDuration,
  truncateLabel,
} from './topology-rendering';

export function TopologyDefs({ visibleEdges }: { visibleEdges: TopologyEdge[] }) {
  return (
    <defs>
      <filter id="topo-soft" x="-60%" y="-60%" width="220%" height="220%">
        <feGaussianBlur stdDeviation="6" />
      </filter>
      <filter id="topo-link-glow" x="-35%" y="-35%" width="170%" height="170%">
        <feGaussianBlur stdDeviation="2.4" result="blur" />
        <feMerge>
          <feMergeNode in="blur" />
          <feMergeNode in="SourceGraphic" />
        </feMerge>
      </filter>
      {/* 每条隧道边一个 userSpaceOnUse 渐变，端点在 tick 中同步为 owner → ingress。 */}
      {visibleEdges.map((edge) => {
        const colors = EDGE_FLOW_COLORS[edge.status.key];
        return (
          <g key={`edge-gradients-${edge.id}`}>
            <linearGradient
              id={`topo-edge-${edge.id}`}
              data-edge-gradient={edge.id}
              gradientUnits="userSpaceOnUse"
            >
              <stop offset="0" stopColor={colors.start} stopOpacity="0.68" />
              <stop offset="0.5" stopColor={colors.middle} stopOpacity="1" />
              <stop offset="1" stopColor={colors.end} stopOpacity="0.72" />
            </linearGradient>
            <linearGradient
              id={`topo-flow-${edge.id}`}
              data-edge-gradient={edge.id}
              gradientUnits="userSpaceOnUse"
            >
              <stop offset="0" stopColor={colors.start} stopOpacity="0.32" />
              <stop offset="0.42" stopColor={colors.spark} stopOpacity="1" />
              <stop offset="0.62" stopColor={colors.middle} stopOpacity="1" />
              <stop offset="1" stopColor={colors.end} stopOpacity="0.36" />
            </linearGradient>
            <marker
              id={`topo-arrow-${edge.id}`}
              viewBox="0 0 12 12"
              refX="10"
              refY="6"
              markerWidth="6"
              markerHeight="6"
              orient="auto"
              markerUnits="strokeWidth"
            >
              <path d="M2 2 L10 6 L2 10 Z" fill={colors.spark} opacity="0.92" />
            </marker>
          </g>
        );
      })}
    </defs>
  );
}

export function TopologyLinksGroup({
  controlNodes,
  visibleEdges,
  trafficSnapshot,
  viewState,
  onHoverTunnel,
}: {
  controlNodes: TopologyNode[];
  visibleEdges: TopologyEdge[];
  trafficSnapshot: TopologyTrafficSnapshot;
  viewState: TopologyViewState;
  onHoverTunnel: (id: string | null) => void;
}) {
  const { focusId: effectiveFocusId, hoverNodeId, hoveredTunnelId: effectiveHoveredTunnelId } = viewState;

  const edgeOpacity = (edge: TopologyEdge) => {
    if (effectiveHoveredTunnelId) return edge.id === effectiveHoveredTunnelId ? 1 : 0.12;
    if (hoverNodeId) return topologyEdgeTouches(edge, hoverNodeId) ? 1 : 0.15;
    if (effectiveFocusId) return topologyEdgeTouches(edge, effectiveFocusId) ? 0.95 : 0.35;
    return 0.85;
  };

  return (
    <g data-layer="links">
      {controlNodes.map((node) => {
        const rate = trafficSnapshot.clientRates.get(node.id);
        const emphasis = getControlLinkEmphasis(node.id, viewState);
        const active = hasTraffic(rate);
        return (
          <g
            key={`control-${node.id}`}
            className="transition-opacity duration-300"
            style={{ opacity: emphasisOpacity(emphasis) }}
          >
            <path
              data-control-id={node.id}
              fill="none"
              strokeWidth={trafficStrokeWidth(rate, emphasis === 'strong' ? 1.85 : 0.85, emphasis)}
              strokeDasharray="2 6"
              strokeLinecap="round"
              className={cn(
                'transition-[stroke-width,opacity] duration-300',
                node.online ? 'stroke-emerald-500/60' : 'stroke-muted-foreground/40',
              )}
            />
            {active && (
              <path
                data-control-id={node.id}
                fill="none"
                strokeLinecap="round"
                strokeWidth={trafficStrokeWidth(rate, emphasis === 'strong' ? 2.6 : 1.5, emphasis)}
                strokeDasharray="1.5 9"
                className="stroke-primary"
                style={{ animation: `topology-flow ${flowDuration(rate)} linear infinite` }}
              />
            )}
          </g>
        );
      })}
      {visibleEdges.map((edge) => {
        const emphasis = getTunnelEdgeEmphasis(edge, viewState);
        const rate = trafficSnapshot.tunnelRates.get(edge.id);
        const active = hasTraffic(rate);
        // 焦点模式下已建立的隧道即使没有流量也保留方向性流光
        // （重复流层的方向即 owner → ingress 的隧道发起方向）。
        const flowing = active
          || (emphasis === 'strong' && edge.status.key === 'exposed');
        const hovered = effectiveHoveredTunnelId === edge.id;
        const strokeWidth = trafficStrokeWidth(rate, hovered ? 2.45 : 1.55, emphasis);
        const streamWidth = Math.max(2.15, strokeWidth * 1.18);
        const streamOpacity = flowing ? 1 : 0;
        const streamDuration = tunnelStreamDuration(rate);
        return (
          <g
            key={edge.id}
            data-edge-id={edge.id}
            className="transition-opacity duration-300"
            style={{ opacity: edgeOpacity(edge) * emphasisOpacity(emphasis) }}
          >
            <path
              data-edge-path="base"
              fill="none"
              strokeLinecap="round"
              strokeWidth={strokeWidth + 5}
              className={EDGE_STROKE[edge.status.key]}
              style={{ opacity: hovered ? 0.18 : 0.08 }}
            />
            <path
              data-edge-path="base"
              fill="none"
              strokeLinecap="round"
              strokeWidth={strokeWidth}
              stroke={`url(#topo-edge-${edge.id})`}
              className="transition-[stroke-width,opacity] duration-300"
              style={{ opacity: hovered ? 0.48 : 0.28 }}
            />
            <path
              data-edge-path="flow"
              fill="none"
              strokeLinecap="round"
              strokeWidth={streamWidth}
              stroke={`url(#topo-flow-${edge.id})`}
              markerEnd={`url(#topo-arrow-${edge.id})`}
              className="transition-opacity duration-300"
              strokeDasharray="13 15"
              style={{
                animation: `topology-stream ${streamDuration} linear infinite`,
                filter: flowing ? 'url(#topo-link-glow)' : undefined,
                opacity: streamOpacity,
              }}
            />
            <path
              data-edge-path="hit"
              fill="none"
              stroke="transparent"
              strokeLinecap="round"
              strokeWidth={Math.max(16, strokeWidth + 12)}
              pointerEvents={effectiveFocusId ? 'stroke' : 'none'}
              onMouseEnter={() => {
                if (effectiveFocusId) onHoverTunnel(edge.id);
              }}
              onMouseLeave={() => {
                if (effectiveFocusId) onHoverTunnel(null);
              }}
            />
          </g>
        );
      })}
    </g>
  );
}

export function TopologyLabelsGroup({
  controlNodes,
  visibleEdges,
  trafficSnapshot,
  viewState,
}: {
  controlNodes: TopologyNode[];
  visibleEdges: TopologyEdge[];
  trafficSnapshot: TopologyTrafficSnapshot;
  viewState: TopologyViewState;
}) {
  const { focusId: effectiveFocusId, hoveredTunnelId: effectiveHoveredTunnelId } = viewState;

  // 标签组始终挂载、由 tick 定位，仅用透明度控制可见性；
  // 否则模拟稳定后才出现的标签会停留在原点。
  return (
    <g data-layer="labels" className="pointer-events-none">
      {controlNodes.map((node) => {
        const rate = trafficSnapshot.clientRates.get(node.id);
        const emphasis = getControlLinkEmphasis(node.id, viewState);
        const visible = !effectiveFocusId && hasTraffic(rate);
        return (
          <g
            key={`control-label-${node.id}`}
            data-control-label={node.id}
            className="transition-opacity duration-300"
            style={{ opacity: visible ? (emphasis === 'strong' ? 1 : 0.35) : 0 }}
          >
            <text
              textAnchor="middle"
              dy={-6}
              className="fill-primary font-mono text-[9px] transition-opacity duration-300"
              style={LABEL_HALO}
            >
              {formatTrafficPair(rate)}
            </text>
          </g>
        );
      })}
      {visibleEdges.map((edge) => {
        const rate = trafficSnapshot.tunnelRates.get(edge.id);
        const showTrafficLabel = hasTraffic(rate) || effectiveHoveredTunnelId === edge.id;
        const visible = getTunnelEdgeEmphasis(edge, viewState) === 'strong' && showTrafficLabel;
        return (
          <g
            key={`edge-label-${edge.id}`}
            data-edge-label={edge.id}
            className="transition-opacity duration-300"
            style={{ opacity: visible ? 1 : 0 }}
          >
            {showTrafficLabel && (
              <text
                textAnchor="middle"
                dy={-8}
                className="fill-primary font-mono text-[9px]"
                style={LABEL_HALO}
              >
                {formatTrafficPair(rate)}
              </text>
            )}
            <text
              textAnchor="middle"
              dy={showTrafficLabel ? 6 : -5}
              className="fill-muted-foreground font-mono text-[9px]"
              style={LABEL_HALO}
            >
              {truncateLabel(edge.tunnel.name)}
            </text>
          </g>
        );
      })}
    </g>
  );
}
