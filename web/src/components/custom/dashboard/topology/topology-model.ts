import type { Client, ClientTrafficResponse, ProxyConfig } from '@/types';
import { getClientDisplayName } from '@/lib/client-utils';
import { resolveTunnelStatus, type TunnelStatusPresentation } from '@/lib/tunnel-model';
import { getTrafficSeriesKey, getTunnelSeriesKey } from '@/lib/tunnel-traffic-keys';

export const SERVER_NODE_ID = '__server__';

export interface TopologyNode {
  id: string;
  kind: 'server' | 'client';
  label: string;
  online: boolean;
  client?: Client;
}

export interface TopologyEdge {
  id: string;
  sourceId: string;
  targetId: string;
  flowSourceId: string;
  flowTargetId: string;
  tunnel: ProxyConfig;
  status: TunnelStatusPresentation;
}

export interface TopologyGraph {
  nodes: TopologyNode[];
  edges: TopologyEdge[];
}

export interface TopologyTrafficRate {
  ingressBps: number;
  egressBps: number;
  totalBps: number;
}

export interface TopologyTrafficSnapshot {
  clientRates: Map<string, TopologyTrafficRate>;
  tunnelRates: Map<string, TopologyTrafficRate>;
}

export type TopologyLinkEmphasis = 'hidden' | 'muted' | 'strong';

export interface TopologyViewState {
  focusId: string | null;
  hoverNodeId: string | null;
  hoveredTunnelId: string | null;
}

export const EMPTY_TOPOLOGY_TRAFFIC_RATE: TopologyTrafficRate = {
  ingressBps: 0,
  egressBps: 0,
  totalBps: 0,
};

export function resolveTunnelIngressNodeId(tunnel: ProxyConfig): string {
  if (tunnel.ingress?.location === 'client') {
    const clientId = tunnel.ingress.client_id || tunnel.participants?.ingress?.client_id;
    if (clientId) {
      return clientId;
    }
  }
  return SERVER_NODE_ID;
}

export function resolveTunnelTargetNodeId(tunnel: ProxyConfig): string {
  return tunnel.target?.client_id
    || tunnel.participants?.target?.client_id
    || tunnel.client_id;
}

export function resolveTunnelOwnerNodeId(tunnel: ProxyConfig): string {
  return tunnel.owner_client_id
    || tunnel.target?.client_id
    || tunnel.participants?.target?.client_id
    || tunnel.client_id;
}

function resolveTunnelFlowNodeIds(
  tunnel: ProxyConfig,
  sourceId: string,
  targetId: string,
): Pick<TopologyEdge, 'flowSourceId' | 'flowTargetId'> {
  const ownerId = resolveTunnelOwnerNodeId(tunnel);
  const flowSourceId = ownerId === sourceId || ownerId === targetId ? ownerId : targetId;
  return {
    flowSourceId,
    flowTargetId: flowSourceId === sourceId ? targetId : sourceId,
  };
}

export function topologyEdgeTouches(edge: Pick<TopologyEdge, 'sourceId' | 'targetId'>, nodeId: string) {
  return edge.sourceId === nodeId || edge.targetId === nodeId;
}

export function normalizeTopologyFocusId(focusId: string | null) {
  return focusId === SERVER_NODE_ID ? null : focusId;
}

export function isTopologyOverview(focusId: string | null) {
  return normalizeTopologyFocusId(focusId) === null;
}

export function getTunnelEdgeEmphasis(edge: TopologyEdge, state: TopologyViewState): TopologyLinkEmphasis {
  const focusId = normalizeTopologyFocusId(state.focusId);
  if (focusId === null) {
    if (state.hoveredTunnelId) {
      return edge.id === state.hoveredTunnelId ? 'strong' : 'muted';
    }
    if (state.hoverNodeId) {
      return topologyEdgeTouches(edge, state.hoverNodeId) ? 'strong' : 'muted';
    }
    return 'muted';
  }

  return topologyEdgeTouches(edge, focusId) ? 'strong' : 'hidden';
}

export function shouldRenderControlLink(clientId: string, state: TopologyViewState) {
  const focusId = normalizeTopologyFocusId(state.focusId);
  if (focusId === null) {
    return true;
  }
  return Boolean(clientId);
}

export function getControlLinkEmphasis(clientId: string, state: TopologyViewState): TopologyLinkEmphasis {
  if (!shouldRenderControlLink(clientId, state)) {
    return 'hidden';
  }
  const focusId = normalizeTopologyFocusId(state.focusId);
  if (focusId !== null) {
    return clientId === focusId ? 'strong' : 'muted';
  }
  if (state.hoverNodeId && state.hoverNodeId !== SERVER_NODE_ID && state.hoverNodeId !== clientId) {
    return 'muted';
  }
  return 'strong';
}

const ASCII_LABEL_HEAD_RE = /^[0-9A-Za-z]/;
const latinLabelCollator = new Intl.Collator('en', { numeric: true, sensitivity: 'base' });
const cjkLabelCollator = new Intl.Collator('zh-Hans-CN', { numeric: true, sensitivity: 'base' });

/**
 * 自然名称比较器：
 * 1. ASCII 字母/数字开头的节点名（如主机名、IP 标识）排在非 ASCII（如中文别名）之前，避免中英文混排时中文无故抢占首位；
 * 2. 各自内部启用 numeric 自然序（如 node-2 < node-10），中文内部按拼音排序；
 * 3. 大小写相同时用码点兜底保证确定性。
 */
export function compareTopologyLabel(a: string, b: string): number {
  const trimA = a.trim();
  const trimB = b.trim();
  const aIsAscii = ASCII_LABEL_HEAD_RE.test(trimA);
  const bIsAscii = ASCII_LABEL_HEAD_RE.test(trimB);
  if (aIsAscii !== bIsAscii) {
    return aIsAscii ? -1 : 1;
  }
  const primary = (aIsAscii ? latinLabelCollator : cjkLabelCollator).compare(trimA, trimB);
  if (primary !== 0) {
    return primary;
  }
  return trimA < trimB ? -1 : trimA > trimB ? 1 : 0;
}

function sortClientsForTopology(clients: Client[]) {
  return [...clients].sort((a, b) => {
    if (a.online !== b.online) {
      return a.online ? -1 : 1;
    }
    const labelDiff = compareTopologyLabel(getClientDisplayName(a), getClientDisplayName(b));
    if (labelDiff !== 0) {
      return labelDiff;
    }
    return compareTopologyLabel(a.id, b.id);
  });
}

/**
 * client_to_client 隧道会同时出现在 ingress/target 两个客户端的 proxies 里，
 * 需要按 tunnel id 去重后再生成边。
 */
export function buildTopologyGraph(clients: Client[] | undefined): TopologyGraph {
  const sortedClients = sortClientsForTopology(clients ?? []);
  const nodes: TopologyNode[] = [
    { id: SERVER_NODE_ID, kind: 'server', label: 'Server', online: true },
    ...sortedClients.map((client) => ({
      id: client.id,
      kind: 'client' as const,
      label: getClientDisplayName(client),
      online: client.online,
      client,
    })),
  ];
  const nodeIds = new Set(nodes.map((node) => node.id));
  const onlineById = new Map(sortedClients.map((client) => [client.id, client.online]));

  const edges: TopologyEdge[] = [];
  const seenTunnelIds = new Set<string>();
  for (const client of sortedClients) {
    for (const tunnel of client.proxies ?? []) {
      if (seenTunnelIds.has(tunnel.id)) {
        continue;
      }
      seenTunnelIds.add(tunnel.id);

      const sourceId = resolveTunnelIngressNodeId(tunnel);
      const targetId = resolveTunnelTargetNodeId(tunnel);
      if (!nodeIds.has(sourceId) || !nodeIds.has(targetId)) {
        continue;
      }

      const sourceOnline = sourceId === SERVER_NODE_ID ? true : onlineById.get(sourceId) ?? false;
      const targetOnline = targetId === SERVER_NODE_ID ? true : onlineById.get(targetId) ?? false;
      const flow = resolveTunnelFlowNodeIds(tunnel, sourceId, targetId);
      edges.push({
        id: tunnel.id,
        sourceId,
        targetId,
        ...flow,
        tunnel,
        status: resolveTunnelStatus(tunnel, sourceOnline && targetOnline),
      });
    }
  }

  return { nodes, edges };
}

function rateFromLatestSecond(item: ClientTrafficResponse['items'][number]): TopologyTrafficRate {
  let latestTime = -Infinity;
  let latestPoint: ClientTrafficResponse['items'][number]['points'][number] | undefined;

  for (const point of item.points) {
    const time = Date.parse(point.bucket_start);
    if (!Number.isFinite(time) || time < latestTime) {
      continue;
    }
    latestTime = time;
    latestPoint = point;
  }

  if (!latestPoint) {
    return EMPTY_TOPOLOGY_TRAFFIC_RATE;
  }

  return {
    ingressBps: latestPoint.ingress_bytes,
    egressBps: latestPoint.egress_bytes,
    totalBps: latestPoint.total_bytes,
  };
}

function addRates(a: TopologyTrafficRate, b: TopologyTrafficRate): TopologyTrafficRate {
  return {
    ingressBps: a.ingressBps + b.ingressBps,
    egressBps: a.egressBps + b.egressBps,
    totalBps: a.totalBps + b.totalBps,
  };
}

export function aggregateClientTrafficRate(data: ClientTrafficResponse | undefined): TopologyTrafficRate {
  if (!data || data.resolution !== 'second') {
    return EMPTY_TOPOLOGY_TRAFFIC_RATE;
  }

  return data.items.reduce(
    (total, item) => addRates(total, rateFromLatestSecond(item)),
    EMPTY_TOPOLOGY_TRAFFIC_RATE,
  );
}

export function buildTopologyTrafficSnapshot(
  graph: TopologyGraph,
  trafficByClientId: Map<string, ClientTrafficResponse | undefined>,
): TopologyTrafficSnapshot {
  const clientRates = new Map<string, TopologyTrafficRate>();
  const tunnelRates = new Map<string, TopologyTrafficRate>();
  const tunnelIdBySeriesKey = new Map<string, string>();

  for (const edge of graph.edges) {
    tunnelIdBySeriesKey.set(getTunnelSeriesKey(edge.tunnel), edge.id);
  }

  for (const node of graph.nodes) {
    if (node.kind !== 'client') {
      continue;
    }

    const data = trafficByClientId.get(node.id);
    clientRates.set(node.id, aggregateClientTrafficRate(data));
    if (!data || data.resolution !== 'second') {
      continue;
    }

    for (const item of data.items) {
      const tunnelId = tunnelIdBySeriesKey.get(getTrafficSeriesKey(item));
      if (!tunnelId) {
        continue;
      }

      const rate = rateFromLatestSecond(item);
      const existing = tunnelRates.get(tunnelId);
      if (!existing || rate.totalBps > existing.totalBps) {
        tunnelRates.set(tunnelId, rate);
      }
    }
  }

  return { clientRates, tunnelRates };
}

export function getTopologyNeighborIds(graph: TopologyGraph, nodeId: string): Set<string> {
  const neighbors = new Set<string>();
  for (const edge of graph.edges) {
    if (edge.sourceId === nodeId) {
      neighbors.add(edge.targetId);
    } else if (edge.targetId === nodeId) {
      neighbors.add(edge.sourceId);
    }
  }
  neighbors.delete(nodeId);
  return neighbors;
}

function pairKey(a: string, b: string) {
  return a < b ? `${a}|${b}` : `${b}|${a}`;
}

/**
 * 为同一对节点之间的多条隧道分配法线偏移量，让曲线相互错开；
 * 与 server 相连的单条隧道也保留一个小弧度，避免和虚线控制连接重叠。
 * 偏移量以「按 id 排序后的端点对」的法线方向为准，与边的方向无关。
 */
export function computeEdgeOffsets(edges: TopologyEdge[]): Map<string, number> {
  const groups = new Map<string, TopologyEdge[]>();
  for (const edge of edges) {
    const key = pairKey(edge.sourceId, edge.targetId);
    const group = groups.get(key);
    if (group) {
      group.push(edge);
    } else {
      groups.set(key, [edge]);
    }
  }

  const offsets = new Map<string, number>();
  for (const group of groups.values()) {
    for (let index = 0; index < group.length; index += 1) {
      const edge = group[index];
      let offset = (index - (group.length - 1) / 2) * 30;
      const touchesServer = edge.sourceId === SERVER_NODE_ID || edge.targetId === SERVER_NODE_ID;
      if (Math.abs(offset) < 1) {
        offset = touchesServer ? 20 : -24;
      }
      offsets.set(edge.id, offset);
    }
  }
  return offsets;
}

export interface TopologyPoint {
  x: number;
  y: number;
}

export interface QuadraticEdgeGeometry {
  path: string;
  /** 曲线 t=0.5 处的点，用于放置标签 */
  midpoint: TopologyPoint;
}

export function getNodeBoundaryRadius(nodeId: string): number {
  return nodeId === SERVER_NODE_ID ? 28 : 23;
}

export function trimLineSegment(
  source: TopologyPoint,
  target: TopologyPoint,
  sourceTrim: number,
  targetTrim: number,
): string | null {
  const dx = target.x - source.x;
  const dy = target.y - source.y;
  const dist = Math.hypot(dx, dy);
  if (dist <= sourceTrim + targetTrim + 6) {
    return null;
  }
  const ux = dx / dist;
  const uy = dy / dist;
  return `M ${source.x + ux * sourceTrim} ${source.y + uy * sourceTrim} L ${target.x - ux * targetTrim} ${target.y - uy * targetTrim}`;
}

/** 依据统一法线方向（按端点 id 排序）计算带偏移的二次贝塞尔曲线，支持按节点外圈半径裁剪端点。 */
export function computeQuadraticEdge(
  sourceId: string,
  targetId: string,
  source: TopologyPoint,
  target: TopologyPoint,
  offset: number,
  trim?: { source?: number; target?: number },
): QuadraticEdgeGeometry {
  const [a, b] = sourceId < targetId ? [source, target] : [target, source];
  const dx = b.x - a.x;
  const dy = b.y - a.y;
  const distance = Math.hypot(dx, dy) || 1;
  const nx = -dy / distance;
  const ny = dx / distance;

  const controlX = (source.x + target.x) / 2 + nx * offset;
  const controlY = (source.y + target.y) / 2 + ny * offset;

  const maxTrim = distance * 0.38;
  const sTrim = Math.min(maxTrim, Math.max(0, trim?.source ?? 0));
  const tTrim = Math.min(maxTrim, Math.max(0, trim?.target ?? 0));

  const sx = controlX - source.x;
  const sy = controlY - source.y;
  const sLen = Math.hypot(sx, sy) || 1;

  const tx = controlX - target.x;
  const ty = controlY - target.y;
  const tLen = Math.hypot(tx, ty) || 1;

  const startX = source.x + (sx / sLen) * sTrim;
  const startY = source.y + (sy / sLen) * sTrim;
  const endX = target.x + (tx / tLen) * tTrim;
  const endY = target.y + (ty / tLen) * tTrim;

  return {
    path: `M ${startX} ${startY} Q ${controlX} ${controlY} ${endX} ${endY}`,
    midpoint: {
      x: (startX + 2 * controlX + endX) / 4,
      y: (startY + 2 * controlY + endY) / 4,
    },
  };
}
