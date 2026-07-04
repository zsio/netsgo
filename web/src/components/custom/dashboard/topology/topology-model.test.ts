import { describe, expect, test } from 'bun:test';

import type { Client, ClientTrafficResponse, ProxyConfig, TunnelCapabilities } from '@/types';

import {
  SERVER_NODE_ID,
  aggregateClientTrafficRate,
  buildTopologyGraph,
  buildTopologyTrafficSnapshot,
  computeEdgeOffsets,
  computeQuadraticEdge,
  getControlLinkEmphasis,
  getTunnelEdgeEmphasis,
  getTopologyNeighborIds,
  normalizeTopologyFocusId,
  shouldRenderControlLink,
} from './topology-model';

const capabilities: TunnelCapabilities = {
  can_resume: false,
  can_stop: true,
  can_edit: true,
  can_delete: true,
};

function createTunnel(overrides: Partial<ProxyConfig> = {}): ProxyConfig {
  return {
    id: 'tunnel-1',
    name: 'demo-tunnel',
    type: 'tcp',
    local_ip: '127.0.0.1',
    local_port: 8080,
    remote_port: 9000,
    domain: '',
    client_id: 'client-a',
    ingress_bps: 0,
    egress_bps: 0,
    created_at: '2026-01-01T00:00:00Z',
    desired_state: 'running',
    runtime_state: 'active',
    capabilities,
    ...overrides,
  };
}

function createClient(overrides: Partial<Client> = {}): Client {
  return {
    id: 'client-a',
    ingress_bps: 0,
    egress_bps: 0,
    info: {
      hostname: 'host-a',
      os: 'linux',
      arch: 'amd64',
      ip: '10.0.0.1',
      version: '1.0.0',
    },
    stats: null,
    online: true,
    proxies: [],
    ...overrides,
  };
}

function createTrafficResponse(
  items: ClientTrafficResponse['items'],
  resolution: ClientTrafficResponse['resolution'] = 'second',
): ClientTrafficResponse {
  return { resolution, items };
}

function createTrafficItem(
  tunnel: Pick<ProxyConfig, 'id' | 'name' | 'type'>,
  points: Array<{ at: string; inBytes: number; outBytes: number }>,
): ClientTrafficResponse['items'][number] {
  return {
    tunnel_id: tunnel.id,
    tunnel_name: tunnel.name,
    tunnel_type: tunnel.type,
    points: points.map((point) => ({
      bucket_start: point.at,
      ingress_bytes: point.inBytes,
      egress_bytes: point.outBytes,
      total_bytes: point.inBytes + point.outBytes,
    })),
  };
}

describe('buildTopologyGraph', () => {
  test('builds server node plus client nodes and server_expose edges', () => {
    const graph = buildTopologyGraph([
      createClient({ proxies: [createTunnel()] }),
      createClient({ id: 'client-b', info: { hostname: 'host-b', os: 'linux', arch: 'arm64', ip: '10.0.0.2', version: '1.0.0' } }),
    ]);

    expect(graph.nodes.map((node) => node.id)).toEqual([SERVER_NODE_ID, 'client-a', 'client-b']);
    expect(graph.edges).toHaveLength(1);
    expect(graph.edges[0].sourceId).toBe(SERVER_NODE_ID);
    expect(graph.edges[0].targetId).toBe('client-a');
    expect(graph.edges[0].status.key).toBe('exposed');
  });

  test('dedupes client_to_client tunnels listed under both participants', () => {
    const c2cTunnel = createTunnel({
      id: 'tunnel-c2c',
      topology: 'client_to_client',
      ingress: {
        location: 'client',
        client_id: 'client-b',
        type: 'tcp_listen',
        config: { bind_ip: '127.0.0.1', port: 7890 },
      },
      target: {
        location: 'client',
        client_id: 'client-a',
        type: 'tcp_service',
        config: { ip: '127.0.0.1', port: 7890 },
      },
    });
    const graph = buildTopologyGraph([
      createClient({ proxies: [c2cTunnel] }),
      createClient({
        id: 'client-b',
        info: { hostname: 'host-b', os: 'linux', arch: 'arm64', ip: '10.0.0.2', version: '1.0.0' },
        proxies: [c2cTunnel],
      }),
    ]);

    expect(graph.edges).toHaveLength(1);
    expect(graph.edges[0].sourceId).toBe('client-b');
    expect(graph.edges[0].targetId).toBe('client-a');
  });

  test('marks running tunnels as offline when a participant client is offline', () => {
    const graph = buildTopologyGraph([
      createClient({ online: false, proxies: [createTunnel()] }),
    ]);

    expect(graph.edges[0].status.key).toBe('offline');
  });
});

describe('getTopologyNeighborIds', () => {
  test('collects both directions and excludes the node itself', () => {
    const graph = buildTopologyGraph([
      createClient({ proxies: [createTunnel()] }),
      createClient({ id: 'client-b', info: { hostname: 'host-b', os: 'linux', arch: 'arm64', ip: '10.0.0.2', version: '1.0.0' } }),
    ]);

    expect([...getTopologyNeighborIds(graph, 'client-a')]).toEqual([SERVER_NODE_ID]);
    expect(getTopologyNeighborIds(graph, 'client-b').size).toBe(0);
  });
});

describe('topology traffic aggregation', () => {
  test('sums the latest second for a client across tunnels', () => {
    const first = createTunnel();
    const second = createTunnel({ id: 'tunnel-2', name: 'admin', type: 'udp' });
    const rate = aggregateClientTrafficRate(createTrafficResponse([
      createTrafficItem(first, [
        { at: '2026-01-01T00:00:00Z', inBytes: 10, outBytes: 1 },
        { at: '2026-01-01T00:00:01Z', inBytes: 100, outBytes: 20 },
      ]),
      createTrafficItem(second, [
        { at: '2026-01-01T00:00:01Z', inBytes: 7, outBytes: 3 },
      ]),
    ]));

    expect(rate).toEqual({ ingressBps: 107, egressBps: 23, totalBps: 130 });
  });

  test('returns zero for missing or non-second traffic data', () => {
    expect(aggregateClientTrafficRate(undefined).totalBps).toBe(0);
    expect(aggregateClientTrafficRate(createTrafficResponse([], 'minute')).totalBps).toBe(0);
  });

  test('builds client totals and tunnel rates without double-counting duplicated tunnel series', () => {
    const c2cTunnel = createTunnel({
      id: 'tunnel-c2c',
      name: 'c2c',
      topology: 'client_to_client',
      ingress: {
        location: 'client',
        client_id: 'client-b',
        type: 'tcp_listen',
        config: { bind_ip: '127.0.0.1', port: 7890 },
      },
      target: {
        location: 'client',
        client_id: 'client-a',
        type: 'tcp_service',
        config: { ip: '127.0.0.1', port: 7890 },
      },
    });
    const graph = buildTopologyGraph([
      createClient({ proxies: [c2cTunnel] }),
      createClient({
        id: 'client-b',
        info: { hostname: 'host-b', os: 'linux', arch: 'arm64', ip: '10.0.0.2', version: '1.0.0' },
        proxies: [c2cTunnel],
      }),
    ]);
    const traffic = new Map<string, ClientTrafficResponse | undefined>([
      ['client-a', createTrafficResponse([
        createTrafficItem(c2cTunnel, [{ at: '2026-01-01T00:00:01Z', inBytes: 100, outBytes: 50 }]),
      ])],
      ['client-b', createTrafficResponse([
        createTrafficItem(c2cTunnel, [{ at: '2026-01-01T00:00:01Z', inBytes: 100, outBytes: 50 }]),
      ])],
    ]);

    const snapshot = buildTopologyTrafficSnapshot(graph, traffic);

    expect(snapshot.clientRates.get('client-a')).toEqual({ ingressBps: 100, egressBps: 50, totalBps: 150 });
    expect(snapshot.clientRates.get('client-b')).toEqual({ ingressBps: 100, egressBps: 50, totalBps: 150 });
    expect(snapshot.tunnelRates.get('tunnel-c2c')).toEqual({ ingressBps: 100, egressBps: 50, totalBps: 150 });
  });

  test('ignores traffic items that are not present in the topology graph', () => {
    const graph = buildTopologyGraph([
      createClient({ proxies: [createTunnel()] }),
    ]);
    const ghostTunnel = createTunnel({ id: 'ghost-tunnel', name: 'ghost' });
    const traffic = new Map<string, ClientTrafficResponse | undefined>([
      ['client-a', createTrafficResponse([
        createTrafficItem(createTunnel(), [{ at: '2026-01-01T00:00:01Z', inBytes: 10, outBytes: 5 }]),
        createTrafficItem(ghostTunnel, [{ at: '2026-01-01T00:00:01Z', inBytes: 999, outBytes: 999 }]),
      ])],
    ]);

    const snapshot = buildTopologyTrafficSnapshot(graph, traffic);

    expect(snapshot.tunnelRates.get('tunnel-1')).toEqual({ ingressBps: 10, egressBps: 5, totalBps: 15 });
    expect(snapshot.tunnelRates.has('ghost-tunnel')).toBe(false);
  });
});

describe('topology link emphasis', () => {
  test('normalizes the server node id to the overview focus', () => {
    expect(normalizeTopologyFocusId(SERVER_NODE_ID)).toBeNull();
  });

  test('overview renders control links and hides tunnel edges by default', () => {
    const graph = buildTopologyGraph([
      createClient({ proxies: [createTunnel()] }),
    ]);
    const state = { focusId: null, hoverNodeId: null, hoveredTunnelId: null };

    expect(shouldRenderControlLink('client-a', state)).toBe(true);
    expect(getControlLinkEmphasis('client-a', state)).toBe('strong');
    expect(getTunnelEdgeEmphasis(graph.edges[0], state)).toBe('hidden');
  });

  test('overview weakly reveals tunnels related to the hovered client', () => {
    const graph = buildTopologyGraph([
      createClient({ proxies: [createTunnel()] }),
    ]);

    expect(getTunnelEdgeEmphasis(graph.edges[0], {
      focusId: null,
      hoverNodeId: 'client-a',
      hoveredTunnelId: null,
    })).toBe('muted');
  });

  test('overview weakly reveals the hovered tunnel from the side panel', () => {
    const graph = buildTopologyGraph([
      createClient({ proxies: [createTunnel()] }),
    ]);

    expect(getTunnelEdgeEmphasis(graph.edges[0], {
      focusId: null,
      hoverNodeId: null,
      hoveredTunnelId: 'tunnel-1',
    })).toBe('muted');
  });

  test('client focus shows related tunnel edges and only the focused client control link', () => {
    const graph = buildTopologyGraph([
      createClient({ proxies: [createTunnel()] }),
      createClient({
        id: 'client-b',
        info: { hostname: 'host-b', os: 'linux', arch: 'arm64', ip: '10.0.0.2', version: '1.0.0' },
        proxies: [],
      }),
    ]);
    const state = { focusId: 'client-a', hoverNodeId: null, hoveredTunnelId: null };

    expect(getTunnelEdgeEmphasis(graph.edges[0], state)).toBe('strong');
    expect(shouldRenderControlLink('client-a', state)).toBe(true);
    expect(getControlLinkEmphasis('client-a', state)).toBe('muted');
    expect(shouldRenderControlLink('client-b', state)).toBe(false);
  });
});

describe('computeEdgeOffsets', () => {
  test('spreads parallel edges between the same node pair', () => {
    const graph = buildTopologyGraph([
      createClient({ proxies: [createTunnel(), createTunnel({ id: 'tunnel-2', remote_port: 9001 })] }),
    ]);

    const offsets = computeEdgeOffsets(graph.edges);
    expect(offsets.get('tunnel-1')).not.toBe(offsets.get('tunnel-2'));
  });

  test('keeps a visible arc for a single edge touching the server', () => {
    const graph = buildTopologyGraph([createClient({ proxies: [createTunnel()] })]);

    const offsets = computeEdgeOffsets(graph.edges);
    expect(Math.abs(offsets.get('tunnel-1')!)).toBeGreaterThan(0);
  });
});

describe('computeQuadraticEdge', () => {
  test('offsets the midpoint along the pair normal regardless of edge direction', () => {
    const a = { x: 0, y: 0 };
    const b = { x: 200, y: 0 };

    const forward = computeQuadraticEdge('a', 'b', a, b, 20);
    const backward = computeQuadraticEdge('b', 'a', b, a, 20);

    expect(forward.midpoint.y).toBeCloseTo(backward.midpoint.y);
    expect(Math.abs(forward.midpoint.y)).toBeGreaterThan(0);
  });
});
