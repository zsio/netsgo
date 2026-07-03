import { describe, expect, test } from 'bun:test';

import type { Client, ProxyConfig, TunnelCapabilities } from '@/types';

import {
  SERVER_NODE_ID,
  buildTopologyGraph,
  computeEdgeOffsets,
  computeQuadraticEdge,
  getTopologyNeighborIds,
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
