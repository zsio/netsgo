import { describe, expect, test } from 'vitest';

import type { Client, ProxyConfig, TunnelCapabilities } from '@/types';
import { SERVER_NODE_ID, buildTopologyGraph } from './topology-model';
import {
  buildStructuredTopologyFlow,
  computeCubicWirePath,
  formatServerEndpointLabel,
} from './topology-layout';

const capabilities: TunnelCapabilities = {
  can_resume: false,
  can_stop: true,
  can_edit: true,
  can_delete: true,
  can_migrate: true,
};

function createTunnel(overrides: Partial<ProxyConfig> = {}): ProxyConfig {
  return {
    id: 'tunnel-1',
    name: 'web-portal',
    type: 'http',
    local_ip: '127.0.0.1',
    local_port: 3000,
    remote_port: 0,
    domain: 'app.example.com',
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

function createClient(id: string, hostname: string, overrides: Partial<Client> = {}): Client {
  return {
    id,
    ingress_bps: 0,
    egress_bps: 0,
    info: {
      hostname,
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

describe('topology-layout structured flow', () => {
  test('orders routes monotonically by target client order and separates C2C ingress peers', () => {
    const c2c = createTunnel({
      id: 'tun-c2c',
      name: 'db-mesh',
      type: 'tcp',
      topology: 'client_to_client',
      owner_client_id: 'client-b',
      client_id: 'client-b',
      ingress: {
        location: 'client',
        client_id: 'client-a',
        type: 'tcp_listen',
        config: { bind_ip: '127.0.0.1', port: 15432 },
      },
      target: {
        location: 'client',
        client_id: 'client-b',
        type: 'tcp_service',
        config: { ip: '127.0.0.1', port: 5432 },
      },
    });

    const graph = buildTopologyGraph([
      createClient('client-a', 'alpha-node', {
        proxies: [
          createTunnel({ id: 'tun-a', name: 'alpha-http', client_id: 'client-a' }),
          c2c,
        ],
      }),
      createClient('client-b', 'bravo-node', {
        proxies: [
          createTunnel({ id: 'tun-b', name: 'bravo-ssh', type: 'tcp', remote_port: 2202, client_id: 'client-b' }),
          c2c,
        ],
      }),
      createClient('client-c', 'charlie-idle', { online: false, proxies: [] }),
    ]);

    const flow = buildStructuredTopologyFlow(graph, null);

    expect(flow.targetClients.map((n) => n.id)).toEqual(['client-a', 'client-b', 'client-c']);
    expect(flow.ingressPeers.map((n) => n.id)).toEqual(['client-a']);
    // Routes targeting client-a come before client-b, with server_expose before c2c on client-b
    expect(flow.routes.map((r) => r.id)).toEqual(['tun-a', 'tun-b', 'tun-c2c']);
    expect(flow.unroutedClients.map((n) => n.id)).toEqual(['client-c']);
    expect(flow.serverExposeCount).toBe(2);
    expect(flow.tunnelCountByClientId.get('client-a')).toBe(2);
    expect(flow.tunnelCountByClientId.get('client-b')).toBe(2);
  });

  test('filters routes and target clients cleanly when focusing an ingress or target client', () => {
    const c2c = createTunnel({
      id: 'tun-c2c',
      name: 'db-mesh',
      type: 'tcp',
      topology: 'client_to_client',
      owner_client_id: 'client-b',
      client_id: 'client-b',
      ingress: {
        location: 'client',
        client_id: 'client-a',
        type: 'tcp_listen',
        config: { bind_ip: '127.0.0.1', port: 15432 },
      },
      target: {
        location: 'client',
        client_id: 'client-b',
        type: 'tcp_service',
        config: { ip: '127.0.0.1', port: 5432 },
      },
    });
    const graph = buildTopologyGraph([
      createClient('client-a', 'alpha-node', { proxies: [c2c] }),
      createClient('client-b', 'bravo-node', { proxies: [c2c] }),
      createClient('client-c', 'charlie-node', {
        proxies: [createTunnel({ id: 'tun-c', client_id: 'client-c' })],
      }),
    ]);

    // Focusing ingress client-a keeps both client-a and its C2C target client-b, excluding client-c
    const focusIngress = buildStructuredTopologyFlow(graph, 'client-a');
    expect(focusIngress.focusClientId).toBe('client-a');
    expect(focusIngress.routes.map((r) => r.id)).toEqual(['tun-c2c']);
    expect(focusIngress.targetClients.map((n) => n.id)).toEqual(['client-a', 'client-b']);
    expect(focusIngress.unroutedClients.map((n) => n.id)).toEqual(['client-a']);

    // Focusing target client-b puts client-a in ingressPeers (Col 1) and only client-b in targetClients (Col 3)
    const focusTarget = buildStructuredTopologyFlow(graph, 'client-b');
    expect(focusTarget.ingressPeers.map((n) => n.id)).toEqual(['client-a']);
    expect(focusTarget.targetClients.map((n) => n.id)).toEqual(['client-b']);
    expect(focusTarget.unroutedClients).toHaveLength(0);

    // Server id normalizes to overview
    expect(buildStructuredTopologyFlow(graph, SERVER_NODE_ID).focusClientId).toBeNull();
  });

  test('formats cubic wire paths and server endpoint host labels cleanly', () => {
    const path = computeCubicWirePath({ x: 100, y: 50 }, { x: 200, y: 120 });
    expect(path).toBe('M 100 50 C 152 50, 148 120, 200 120');

    expect(formatServerEndpointLabel('http://100.106.144.38:5173/')).toBe('100.106.144.38:5173');
    expect(formatServerEndpointLabel('https://tunnel.example.com')).toBe('tunnel.example.com');
    expect(formatServerEndpointLabel('')).toBe('');
  });

  test('bundles parallel routes in overview when exceeding threshold and expands all in focus mode', () => {
    const clientATunnels = Array.from({ length: 6 }, (_, idx) =>
      createTunnel({
        id: `tun-a-${idx + 1}`,
        name: `svc-${idx + 1}`,
        type: idx % 2 === 0 ? 'http' : 'tcp',
        remote_port: idx % 2 === 0 ? 0 : 10000 + idx,
        runtime_state: idx === 2 ? 'error' : 'active',
        client_id: 'client-a',
      }),
    );
    const graph = buildTopologyGraph([
      createClient('client-a', 'alpha-node', { proxies: clientATunnels }),
      createClient('client-b', 'bravo-node', {
        proxies: [createTunnel({ id: 'tun-b-1', name: 'bravo-web', client_id: 'client-b' })],
      }),
    ]);

    const overview = buildStructuredTopologyFlow(graph, null, {
      trafficSnapshot: {
        clientRates: new Map(),
        tunnelRates: new Map([
          ['tun-a-1', { ingressBps: 1024, egressBps: 2048, totalBps: 3072 }],
          ['tun-a-2', { ingressBps: 512, egressBps: 512, totalBps: 1024 }],
        ]),
      },
    });

    expect(overview.showScaleToolbar).toBe(true);
    expect(overview.middleItems).toHaveLength(2);
    const bundle = overview.middleItems[0];
    expect(bundle.itemKind).toBe('bundle');
    if (bundle.itemKind === 'bundle') {
      expect(bundle.routes).toHaveLength(6);
      expect(bundle.errorCount).toBe(1);
      expect(bundle.statusSummaryKey).toBe('error');
      expect(bundle.totalRate.totalBps).toBe(4096);
      expect(bundle.protocolCounts).toEqual([
        { type: 'HTTP', count: 3 },
        { type: 'TCP', count: 3 },
      ]);
      // Error tunnel prioritized first in preview order
      expect(bundle.routes[0].id).toBe('tun-a-3');
    }
    expect(overview.middleItems[1].itemKind).toBe('single');

    // Focusing client-a expands every individual route without bundling
    const focused = buildStructuredTopologyFlow(graph, 'client-a');
    expect(focused.middleItems).toHaveLength(6);
    expect(focused.middleItems.every((item) => item.itemKind === 'single')).toBe(true);
  });

  test('supports search query, quick filters, and single-screen pagination at scale', () => {
    const clients = Array.from({ length: 11 }, (_, idx) => {
      const id = `client-${idx + 1}`;
      const isOffline = idx === 9;
      return createClient(id, `edge-node-${String(idx + 1).padStart(2, '0')}`, {
        online: !isOffline,
        proxies: isOffline
          ? []
          : [
              createTunnel({
                id: `tun-${idx + 1}`,
                name: `api-${idx + 1}`,
                domain: `n${idx + 1}.example.com`,
                runtime_state: idx === 4 ? 'error' : 'active',
                client_id: id,
              }),
            ],
      });
    });

    const graph = buildTopologyGraph(clients);

    // Overview pagination: 11 clients sliced at 8 per page -> 2 pages, compact mode active
    const page0 = buildStructuredTopologyFlow(graph, null, { page: 0, clientPageSize: 8 });
    expect(page0.compact).toBe(true);
    expect(page0.pagination).toEqual({
      mode: 'clients',
      page: 0,
      totalPages: 2,
      totalItems: 11,
      rangeStart: 1,
      rangeEnd: 8,
    });
    expect(page0.targetClients).toHaveLength(8);

    const page1 = buildStructuredTopologyFlow(graph, null, { page: 1, clientPageSize: 8 });
    expect(page1.targetClients).toHaveLength(3);
    expect(page1.pagination.rangeStart).toBe(9);
    expect(page1.pagination.rangeEnd).toBe(11);

    // Quick filter 'issues' returns only the error tunnel target (client-5) and offline node (client-10)
    const issuesOnly = buildStructuredTopologyFlow(graph, null, { quickFilter: 'issues' });
    expect(issuesOnly.filterCounts.issues).toBe(2);
    expect(issuesOnly.targetClients.map((n) => n.id)).toEqual(['client-5', 'client-10']);
    expect(issuesOnly.routes.map((r) => r.id)).toEqual(['tun-5']);

    // Search query by domain narrows to matching node & route
    const searched = buildStructuredTopologyFlow(graph, null, { searchQuery: 'n7.example.com' });
    expect(searched.targetClients.map((n) => n.id)).toEqual(['client-7']);
    expect(searched.routes.map((r) => r.id)).toEqual(['tun-7']);
  });
});
