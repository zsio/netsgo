import {
  EMPTY_TOPOLOGY_TRAFFIC_RATE,
  SERVER_NODE_ID,
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
  page?: number;
  clientPageSize?: number;
  routePageSize?: number;
  bundleThreshold?: number;
}

export interface StructuredTopologyFlow {
  focusClientId: string | null;
  ingressPeers: TopologyNode[];
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
  pagination: {
    mode: 'clients' | 'routes' | null;
    page: number;
    totalPages: number;
    totalItems: number;
    rangeStart: number;
    rangeEnd: number;
  };
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
    return a.edge.tunnel.name.localeCompare(b.edge.tunnel.name);
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
    page: rawPage = 0,
    clientPageSize = 6,
    routePageSize = 6,
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

  const candidateClients: TopologyNode[] = (() => {
    if (focusClientId) {
      const keepIds = new Set<string>([focusClientId]);
      for (const edge of scopedEdges) {
        if (edge.targetId !== SERVER_NODE_ID) keepIds.add(edge.targetId);
      }
      return allClients.filter((node) => keepIds.has(node.id));
    }

    const edgeTargetIds = new Set<string>();
    for (const edge of scopedEdges) {
      if (edge.targetId !== SERVER_NODE_ID) {
        edgeTargetIds.add(edge.targetId);
      }
    }

    return allClients.filter((node) => {
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
  })();

  // 第 2 步：分页切片（全览按客户端分页，聚焦态按规则分页）
  let pagedClients = candidateClients;
  let paginationMode: 'clients' | 'routes' | null = null;
  let page = 0;
  let totalPages = 1;
  let totalItems = candidateClients.length;
  let rangeStart = candidateClients.length > 0 ? 1 : 0;
  let rangeEnd = candidateClients.length;

  if (!focusClientId && candidateClients.length > clientPageSize) {
    paginationMode = 'clients';
    totalItems = candidateClients.length;
    totalPages = Math.max(1, Math.ceil(totalItems / clientPageSize));
    page = Math.max(0, Math.min(rawPage, totalPages - 1));
    const startIdx = page * clientPageSize;
    pagedClients = candidateClients.slice(startIdx, startIdx + clientPageSize);
    rangeStart = startIdx + 1;
    rangeEnd = startIdx + pagedClients.length;
  }

  const targetOrder = new Map(pagedClients.map((node, index) => [node.id, index]));
  const clientOrder = new Map(allClients.map((node, index) => [node.id, index]));

  let sortedRoutes: StructuredRouteItem[] = scopedEdges
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
      const targetDiff = (targetOrder.get(a.targetClientId) ?? 0) - (targetOrder.get(b.targetClientId) ?? 0);
      if (targetDiff !== 0) return targetDiff;

      const sourceRankA = a.kind === 'server_expose' ? -1 : (clientOrder.get(a.sourceNodeId) ?? 0);
      const sourceRankB = b.kind === 'server_expose' ? -1 : (clientOrder.get(b.sourceNodeId) ?? 0);
      if (sourceRankA !== sourceRankB) return sourceRankA - sourceRankB;

      return a.edge.tunnel.name.localeCompare(b.edge.tunnel.name);
    });

  if (focusClientId && sortedRoutes.length > routePageSize) {
    paginationMode = 'routes';
    totalItems = sortedRoutes.length;
    totalPages = Math.max(1, Math.ceil(totalItems / routePageSize));
    page = Math.max(0, Math.min(rawPage, totalPages - 1));
    const startIdx = page * routePageSize;
    sortedRoutes = sortedRoutes.slice(startIdx, startIdx + routePageSize);
    rangeStart = startIdx + 1;
    rangeEnd = startIdx + sortedRoutes.length;

    // 聚焦分页后仅保留当前页涉及的目标节点与焦点节点
    const activeTargets = new Set<string>([focusClientId, ...sortedRoutes.map((r) => r.targetClientId)]);
    pagedClients = pagedClients.filter((n) => activeTargets.has(n.id));
  }

  // 第 3 步：全览模式下，当规则数超过阈值时，自动将同 (源 -> 目标) 的多条并行隧道聚合成规则束卡片
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
  for (const item of middleItems) {
    if (item.kind === 'client_to_client' && clientById.has(item.sourceNodeId)) {
      ingressPeerIds.add(item.sourceNodeId);
    }
  }
  const ingressPeers = allClients.filter((node) => ingressPeerIds.has(node.id));

  const routedTargetIds = new Set(middleItems.map((item) => item.targetClientId));
  const unroutedClients = pagedClients.filter((node) => !routedTargetIds.has(node.id));
  const serverExposeCount = graph.edges.filter((edge) => edge.sourceId === SERVER_NODE_ID).length;
  const compact = pagedClients.length > 4 || middleItems.length > 5;
  const hasEmptyFilterResult = allClients.length > 0 && pagedClients.length === 0;

  return {
    focusClientId,
    ingressPeers,
    routes: sortedRoutes,
    middleItems,
    targetClients: pagedClients,
    unroutedClients,
    tunnelCountByClientId,
    serverExposeCount,
    totalScopedRoutes: scopedEdges.length,
    totalScopedClients: candidateClients.length,
    compact,
    showScaleToolbar,
    hasEmptyFilterResult,
    filterCounts,
    pagination: {
      mode: paginationMode,
      page,
      totalPages,
      totalItems,
      rangeStart,
      rangeEnd,
    },
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
