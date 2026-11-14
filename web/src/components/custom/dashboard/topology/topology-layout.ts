import {
  EMPTY_TOPOLOGY_TRAFFIC_RATE,
  SERVER_NODE_ID,
  compareTopologyLabel,
  normalizeTopologyFocusId,
  topologyEdgeTouches,
  type TopologyEdge,
  type TopologyGraph,
  type TopologyNode,
  type TopologyPoint,
  type TopologyTrafficRate,
  type TopologyTrafficSnapshot,
} from './topology-model';
import type { TunnelStatusPresentation } from '@/lib/tunnel-model';

export type TopologyQuickFilter = 'all' | 'issues' | 'active' | 'c2c';

export interface StructuredRouteItem {
  itemKind: 'single';
  id: string;
  edge: TopologyEdge;
  kind: 'server_expose' | 'client_to_client';
  sourceNodeId: string;
  targetClientId: string;
  sourcePortKey: string;
  routeInPortKey: string;
  routeOutPortKey: string;
  targetPortKey: string;
}

export interface StructuredRouteBundleItem {
  itemKind: 'bundle';
  id: string;
  kind: 'server_expose' | 'client_to_client';
  sourceNodeId: string;
  targetClientId: string;
  sourcePortKey: string;
  routeInPortKey: string;
  routeOutPortKey: string;
  targetPortKey: string;
  routes: StructuredRouteItem[];
  protocolCounts: Array<{ type: string; count: number }>;
  statusSummaryKey: TunnelStatusPresentation['key'];
  errorCount: number;
  exposedCount: number;
  totalRate: TopologyTrafficRate;
}

export type StructuredMiddleItem = StructuredRouteItem | StructuredRouteBundleItem;

export interface TopologyFilterCounts {
  issues: number;
  active: number;
  c2c: number;
}

export interface TopologyFlowOptions {
  searchQuery?: string;
  quickFilter?: TopologyQuickFilter;
  trafficSnapshot?: TopologyTrafficSnapshot;
  bundleThreshold?: number;
}

export interface StructuredTopologyFlow {
  focusClientId: string | null;
  ingressPeers: TopologyNode[];
  c2cRouteCountByPeerId: Map<string, number>;
  routes: StructuredRouteItem[];
  middleItems: StructuredMiddleItem[];
  targetClients: TopologyNode[];
  unroutedClients: TopologyNode[];
  tunnelCountByClientId: Map<string, number>;
  serverExposeCount: number;
  totalScopedRoutes: number;
  totalScopedClients: number;
  compact: boolean;
  showScaleToolbar: boolean;
  hasEmptyFilterResult: boolean;
  filterCounts: TopologyFilterCounts;
}

function matchesClientQuery(node: TopologyNode, q: string): boolean {
  if (!q) return true;
  if (node.label.toLowerCase().includes(q)) return true;
  const info = node.client?.info;
  if (!info) return false;
  return (
    info.hostname.toLowerCase().includes(q) ||
    info.ip.toLowerCase().includes(q) ||
    (node.client?.last_ip?.toLowerCase().includes(q) ?? false) ||
    `${info.os}/${info.arch}`.toLowerCase().includes(q)
  );
}

function matchesEdgeQuery(edge: TopologyEdge, q: string): boolean {
  if (!q) return true;
  const t = edge.tunnel;
  return (
    t.name.toLowerCase().includes(q) ||
    t.type.toLowerCase().includes(q) ||
    (t.domain?.toLowerCase().includes(q) ?? false) ||
    (t.local_ip?.toLowerCase().includes(q) ?? false) ||
    (t.local_port > 0 && String(t.local_port).includes(q)) ||
    (t.remote_port > 0 && String(t.remote_port).includes(q))
  );
}

function isIssueEdge(edge: TopologyEdge): boolean {
  return edge.status.key === 'error' || edge.status.key === 'offline';
}

function summarizeBundleStatus(routes: StructuredRouteItem[]): TunnelStatusPresentation['key'] {
  if (routes.some((r) => r.edge.status.key === 'error')) return 'error';
  if (routes.some((r) => r.edge.status.key === 'exposed')) return 'exposed';
  if (routes.some((r) => r.edge.status.key === 'pending')) return 'pending';
  if (routes.some((r) => r.edge.status.key === 'offline')) return 'offline';
  return 'stopped';
}

function buildRouteBundle(
  bundleKey: string,
  group: StructuredRouteItem[],
  trafficSnapshot?: TopologyTrafficSnapshot,
): StructuredRouteBundleItem {
  const first = group[0];
  const id = `bundle:${bundleKey}`;
  const protoMap = new Map<string, number>();
  let errorCount = 0;
  let exposedCount = 0;
  let ingressBps = 0;
  let egressBps = 0;
  let totalBps = 0;

  // 异常或有实时流量的规则排在前面，方便预览优先露出
  const sortedGroup = [...group].sort((a, b) => {
    const errDiff = (b.edge.status.key === 'error' ? 1 : 0) - (a.edge.status.key === 'error' ? 1 : 0);
    if (errDiff !== 0) return errDiff;
    const rateA = trafficSnapshot?.tunnelRates.get(a.id)?.totalBps ?? 0;
    const rateB = trafficSnapshot?.tunnelRates.get(b.id)?.totalBps ?? 0;
    if (rateA !== rateB) return rateB - rateA;
    return compareTopologyLabel(a.edge.tunnel.name, b.edge.tunnel.name);
  });

  for (const item of sortedGroup) {
    const proto = item.edge.tunnel.type.toUpperCase();
    protoMap.set(proto, (protoMap.get(proto) ?? 0) + 1);
    if (item.edge.status.key === 'error') errorCount += 1;
    if (item.edge.status.key === 'exposed') exposedCount += 1;
    const rate = trafficSnapshot?.tunnelRates.get(item.id) ?? EMPTY_TOPOLOGY_TRAFFIC_RATE;
    ingressBps += rate.ingressBps;
    egressBps += rate.egressBps;
    totalBps += rate.totalBps;
  }

  return {
    itemKind: 'bundle',
    id,
    kind: first.kind,
    sourceNodeId: first.sourceNodeId,
    targetClientId: first.targetClientId,
    sourcePortKey: first.sourcePortKey,
    routeInPortKey: `route-in:${id}`,
    routeOutPortKey: `route-out:${id}`,
    targetPortKey: first.targetPortKey,
    routes: sortedGroup,
    protocolCounts: [...protoMap.entries()].map(([type, count]) => ({ type, count })),
    statusSummaryKey: summarizeBundleStatus(sortedGroup),
    errorCount,
    exposedCount,
    totalRate: { ingressBps, egressBps, totalBps },
  };
}

export function buildStructuredTopologyFlow(
  graph: TopologyGraph,
  rawFocusId: string | null,
  options: TopologyFlowOptions = {},
): StructuredTopologyFlow {
  const {
    searchQuery = '',
    quickFilter = 'all',
    trafficSnapshot,
    bundleThreshold = 5,
  } = options;

  const query = searchQuery.trim().toLowerCase();
  const allClients = graph.nodes.filter((node) => node.kind === 'client');
  const clientById = new Map(allClients.map((node) => [node.id, node]));
  const normalizedFocus = normalizeTopologyFocusId(rawFocusId);
  const focusClientId = normalizedFocus && clientById.has(normalizedFocus) ? normalizedFocus : null;

  const tunnelCountByClientId = new Map<string, number>();
  for (const edge of graph.edges) {
    if (edge.sourceId !== SERVER_NODE_ID) {
      tunnelCountByClientId.set(edge.sourceId, (tunnelCountByClientId.get(edge.sourceId) ?? 0) + 1);
    }
    if (edge.targetId !== SERVER_NODE_ID) {
      tunnelCountByClientId.set(edge.targetId, (tunnelCountByClientId.get(edge.targetId) ?? 0) + 1);
    }
  }

  const baseEdges = focusClientId
    ? graph.edges.filter((edge) => topologyEdgeTouches(edge, focusClientId))
    : graph.edges;

  // 统计快筛角标数（离线节点 + 运行异常隧道，避免同一离线节点下的隧道重复累加）
  const offlineClientCount = focusClientId
    ? (clientById.get(focusClientId)?.online === false ? 1 : 0)
    : allClients.filter((n) => !n.online).length;
  const errorEdgeCount = baseEdges.filter((edge) => edge.status.key === 'error').length;
  const activeEdgeCount = baseEdges.filter(
    (edge) => (trafficSnapshot?.tunnelRates.get(edge.id)?.totalBps ?? 0) > 0,
  ).length;
  const c2cEdgeCount = baseEdges.filter((edge) => edge.sourceId !== SERVER_NODE_ID).length;

  const filterCounts: TopologyFilterCounts = {
    issues: offlineClientCount + errorEdgeCount,
    active: activeEdgeCount,
    c2c: c2cEdgeCount,
  };

  const showScaleToolbar =
    Boolean(query) ||
    quickFilter !== 'all' ||
    (focusClientId ? baseEdges.length > 6 : allClients.length > 4 || graph.edges.length > 6);

  // 第 1 步：按焦点、快筛与搜索词过滤候选边与客户端
  const scopedEdges = baseEdges.filter((edge) => {
    if (quickFilter === 'issues' && !isIssueEdge(edge)) return false;
    if (quickFilter === 'active' && (trafficSnapshot?.tunnelRates.get(edge.id)?.totalBps ?? 0) <= 0) {
      return false;
    }
    if (quickFilter === 'c2c' && edge.sourceId === SERVER_NODE_ID) return false;
    if (!query) return true;
    const srcNode = clientById.get(edge.sourceId);
    const dstNode = clientById.get(edge.targetId);
    return (
      matchesEdgeQuery(edge, query) ||
      (srcNode ? matchesClientQuery(srcNode, query) : false) ||
      (dstNode ? matchesClientQuery(dstNode, query) : false)
    );
  });

  // 计算左栏 C2C 入口节点的固定顺序（在线优先 -> 出向规则数降序 -> 自然名称序），供三栏统一对齐
  const c2cSourceCountByClient = new Map<string, number>();
  for (const edge of scopedEdges) {
    if (edge.sourceId !== SERVER_NODE_ID) {
      c2cSourceCountByClient.set(edge.sourceId, (c2cSourceCountByClient.get(edge.sourceId) ?? 0) + 1);
    }
  }
  const orderedPeerClients = allClients
    .filter((node) => (c2cSourceCountByClient.get(node.id) ?? 0) > 0)
    .sort((a, b) => {
      if (a.online !== b.online) return a.online ? -1 : 1;
      const outDiff = (c2cSourceCountByClient.get(b.id) ?? 0) - (c2cSourceCountByClient.get(a.id) ?? 0);
      if (outDiff !== 0) return outDiff;
      const labelDiff = compareTopologyLabel(a.label, b.label);
      return labelDiff !== 0 ? labelDiff : compareTopologyLabel(a.id, b.id);
    });
  const peerRankById = new Map(orderedPeerClients.map((node, index) => [node.id, index]));

  // 统计每个目标客户端的入向流向归属与 C2C 入口重心（0: 纯公网, 1: 公网+C2C混合, 2: 纯C2C, 3: 无转发规则待命）
  interface ClientFlowMetrics {
    serverInCount: number;
    c2cInCount: number;
    totalInCount: number;
    c2cBarycenter: number;
    hasErrorIn: boolean;
    flowBucket: 0 | 1 | 2 | 3;
  }
  const EMPTY_FLOW_METRICS: ClientFlowMetrics = {
    serverInCount: 0,
    c2cInCount: 0,
    totalInCount: 0,
    c2cBarycenter: 0,
    hasErrorIn: false,
    flowBucket: 3,
  };

  const flowMetricsByClientId = new Map<string, ClientFlowMetrics>();
  for (const node of allClients) {
    let serverInCount = 0;
    let c2cInCount = 0;
    let c2cRankSum = 0;
    let hasErrorIn = false;

    for (const edge of scopedEdges) {
      if (edge.targetId !== node.id) continue;
      if (edge.status.key === 'error') hasErrorIn = true;
      if (edge.sourceId === SERVER_NODE_ID) {
        serverInCount += 1;
      } else {
        c2cInCount += 1;
        c2cRankSum += peerRankById.get(edge.sourceId) ?? 0;
      }
    }

    const totalInCount = serverInCount + c2cInCount;
    const flowBucket: 0 | 1 | 2 | 3 =
      serverInCount > 0 && c2cInCount === 0
        ? 0
        : serverInCount > 0 && c2cInCount > 0
          ? 1
          : c2cInCount > 0
            ? 2
            : 3;

    flowMetricsByClientId.set(node.id, {
      serverInCount,
      c2cInCount,
      totalInCount,
      c2cBarycenter: c2cInCount > 0 ? c2cRankSum / c2cInCount : 0,
      hasErrorIn,
      flowBucket,
    });
  }

  const compareClientTieBreakers = (
    a: TopologyNode,
    b: TopologyNode,
    ma: ClientFlowMetrics,
    mb: ClientFlowMetrics,
  ): number => {
    const inDiff = mb.totalInCount - ma.totalInCount;
    if (inDiff !== 0) return inDiff;

    const totalDiff = (tunnelCountByClientId.get(b.id) ?? 0) - (tunnelCountByClientId.get(a.id) ?? 0);
    if (totalDiff !== 0) return totalDiff;

    const errDiff = (mb.hasErrorIn ? 1 : 0) - (ma.hasErrorIn ? 1 : 0);
    if (errDiff !== 0) return errDiff;

    const labelDiff = compareTopologyLabel(a.label, b.label);
    return labelDiff !== 0 ? labelDiff : compareTopologyLabel(a.id, b.id);
  };

  // 全局零交叉流向对齐：严格按 公网目标 -> 混合 -> C2C目标(按左栏重心) -> 待命节点 纵向单调对齐
  const compareForScreenFlow = (a: TopologyNode, b: TopologyNode): number => {
    const ma = flowMetricsByClientId.get(a.id) ?? EMPTY_FLOW_METRICS;
    const mb = flowMetricsByClientId.get(b.id) ?? EMPTY_FLOW_METRICS;

    if (ma.flowBucket !== mb.flowBucket) return ma.flowBucket - mb.flowBucket;
    if (Math.abs(ma.c2cBarycenter - mb.c2cBarycenter) > 1e-6) {
      return ma.c2cBarycenter - mb.c2cBarycenter;
    }
    if (a.online !== b.online) return a.online ? -1 : 1;

    return compareClientTieBreakers(a, b, ma, mb);
  };

  const targetClients: TopologyNode[] = (() => {
    let filtered: TopologyNode[];
    if (focusClientId) {
      const keepIds = new Set<string>([focusClientId]);
      for (const edge of scopedEdges) {
        if (edge.targetId !== SERVER_NODE_ID) keepIds.add(edge.targetId);
      }
      filtered = allClients.filter((node) => keepIds.has(node.id));
    } else {
      const edgeTargetIds = new Set<string>();
      for (const edge of scopedEdges) {
        if (edge.targetId !== SERVER_NODE_ID) {
          edgeTargetIds.add(edge.targetId);
        }
      }

      filtered = allClients.filter((node) => {
        if (quickFilter === 'active' || quickFilter === 'c2c') {
          return edgeTargetIds.has(node.id);
        }
        if (quickFilter === 'issues') {
          if (edgeTargetIds.has(node.id)) return true;
          if (!node.online) {
            return !query || matchesClientQuery(node, query);
          }
          return false;
        }
        if (query) {
          return matchesClientQuery(node, query) || edgeTargetIds.has(node.id);
        }
        return true;
      });
    }

    return [...filtered].sort(compareForScreenFlow);
  })();

  const targetOrder = new Map(targetClients.map((node, index) => [node.id, index]));

  const sortedRoutes: StructuredRouteItem[] = scopedEdges
    .filter((edge) => targetOrder.has(edge.targetId))
    .map((edge) => {
      const isServerExpose = edge.sourceId === SERVER_NODE_ID;
      return {
        itemKind: 'single' as const,
        id: edge.id,
        edge,
        kind: isServerExpose ? ('server_expose' as const) : ('client_to_client' as const),
        sourceNodeId: edge.sourceId,
        targetClientId: edge.targetId,
        sourcePortKey: isServerExpose ? 'server-out' : `peer-out:${edge.sourceId}`,
        routeInPortKey: `route-in:${edge.id}`,
        routeOutPortKey: `route-out:${edge.id}`,
        targetPortKey: `client-in:${edge.targetId}`,
      };
    })
    .sort((a, b) => {
      const sourceRankA = a.kind === 'server_expose' ? -1 : (peerRankById.get(a.sourceNodeId) ?? 0);
      const sourceRankB = b.kind === 'server_expose' ? -1 : (peerRankById.get(b.sourceNodeId) ?? 0);
      if (sourceRankA !== sourceRankB) return sourceRankA - sourceRankB;

      const targetDiff = (targetOrder.get(a.targetClientId) ?? 0) - (targetOrder.get(b.targetClientId) ?? 0);
      if (targetDiff !== 0) return targetDiff;

      const errDiff = (b.edge.status.key === 'error' ? 1 : 0) - (a.edge.status.key === 'error' ? 1 : 0);
      if (errDiff !== 0) return errDiff;

      const nameDiff = compareTopologyLabel(a.edge.tunnel.name, b.edge.tunnel.name);
      return nameDiff !== 0 ? nameDiff : compareTopologyLabel(a.id, b.id);
    });

  // 第 2 步：全览模式下，当规则数超过阈值时，自动将同 (源 -> 目标) 的多条并行隧道聚合成规则束卡片
  const shouldBundle = !focusClientId && sortedRoutes.length > bundleThreshold;
  const middleItems: StructuredMiddleItem[] = (() => {
    if (!shouldBundle) {
      return sortedRoutes;
    }
    const groups = new Map<string, StructuredRouteItem[]>();
    for (const route of sortedRoutes) {
      const key = `${route.sourceNodeId}->${route.targetClientId}`;
      const bucket = groups.get(key);
      if (bucket) {
        bucket.push(route);
      } else {
        groups.set(key, [route]);
      }
    }
    const items: StructuredMiddleItem[] = [];
    for (const [key, group] of groups.entries()) {
      if (group.length >= 2) {
        items.push(buildRouteBundle(key, group, trafficSnapshot));
      } else {
        items.push(group[0]);
      }
    }
    return items;
  })();

  const ingressPeerIds = new Set<string>();
  const c2cRouteCountByPeerId = new Map<string, number>();
  for (const route of sortedRoutes) {
    if (route.kind === 'client_to_client' && clientById.has(route.sourceNodeId)) {
      ingressPeerIds.add(route.sourceNodeId);
      c2cRouteCountByPeerId.set(
        route.sourceNodeId,
        (c2cRouteCountByPeerId.get(route.sourceNodeId) ?? 0) + 1,
      );
    }
  }
  const ingressPeers = orderedPeerClients.filter((node) => ingressPeerIds.has(node.id));

  const routedTargetIds = new Set(middleItems.map((item) => item.targetClientId));
  const unroutedClients = targetClients.filter((node) => !routedTargetIds.has(node.id));
  const serverExposeCount = graph.edges.filter((edge) => edge.sourceId === SERVER_NODE_ID).length;
  const compact = targetClients.length > 4 || middleItems.length > 5;
  const hasEmptyFilterResult = allClients.length > 0 && targetClients.length === 0;

  return {
    focusClientId,
    ingressPeers,
    c2cRouteCountByPeerId,
    routes: sortedRoutes,
    middleItems,
    targetClients,
    unroutedClients,
    tunnelCountByClientId,
    serverExposeCount,
    totalScopedRoutes: scopedEdges.length,
    totalScopedClients: targetClients.length,
    compact,
    showScaleToolbar,
    hasEmptyFilterResult,
    filterCounts,
  };
}

export function computeCubicWirePath(start: TopologyPoint, end: TopologyPoint): string {
  const spanX = end.x - start.x;
  const controlOffset = Math.max(22, Math.abs(spanX) * 0.52);
  const direction = spanX >= 0 ? 1 : -1;
  const c1x = start.x + controlOffset * direction;
  const c2x = end.x - controlOffset * direction;
  return `M ${start.x} ${start.y} C ${c1x} ${start.y}, ${c2x} ${end.y}, ${end.x} ${end.y}`;
}

export function formatServerEndpointLabel(serverAddr: string | undefined): string {
  if (!serverAddr) return '';
  const trimmed = serverAddr.trim();
  if (!trimmed) return '';
  try {
    const parsed = new URL(trimmed);
    return parsed.host || trimmed;
  } catch {
    return trimmed.replace(/^https?:\/\//i, '').replace(/\/+$/, '');
  }
}
