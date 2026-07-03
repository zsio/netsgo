import type { Client, ProxyConfig } from '@/types';
import { getClientDisplayName } from '@/lib/client-utils';
import { resolveTunnelStatus, type TunnelStatusPresentation } from '@/lib/tunnel-model';

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
  tunnel: ProxyConfig;
  status: TunnelStatusPresentation;
}

export interface TopologyGraph {
  nodes: TopologyNode[];
  edges: TopologyEdge[];
}

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

function sortClientsForTopology(clients: Client[]) {
  return [...clients].sort((a, b) => {
    if (a.online !== b.online) {
      return a.online ? -1 : 1;
    }
    return getClientDisplayName(a).localeCompare(getClientDisplayName(b));
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
      edges.push({
        id: tunnel.id,
        sourceId,
        targetId,
        tunnel,
        status: resolveTunnelStatus(tunnel, sourceOnline && targetOnline),
      });
    }
  }

  return { nodes, edges };
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
      if (touchesServer && Math.abs(offset) < 1) {
        offset = 18;
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

/** 依据统一法线方向（按端点 id 排序）计算带偏移的二次贝塞尔曲线。 */
export function computeQuadraticEdge(
  sourceId: string,
  targetId: string,
  source: TopologyPoint,
  target: TopologyPoint,
  offset: number,
): QuadraticEdgeGeometry {
  const [a, b] = sourceId < targetId ? [source, target] : [target, source];
  const dx = b.x - a.x;
  const dy = b.y - a.y;
  const distance = Math.hypot(dx, dy) || 1;
  const nx = -dy / distance;
  const ny = dx / distance;

  const controlX = (source.x + target.x) / 2 + nx * offset;
  const controlY = (source.y + target.y) / 2 + ny * offset;

  return {
    path: `M ${source.x} ${source.y} Q ${controlX} ${controlY} ${target.x} ${target.y}`,
    midpoint: {
      x: (source.x + 2 * controlX + target.x) / 4,
      y: (source.y + 2 * controlY + target.y) / 4,
    },
  };
}
