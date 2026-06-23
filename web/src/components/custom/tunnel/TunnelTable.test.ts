import { describe, expect, test } from 'bun:test';

import type { ProxyConfig } from '@/types';

import { CLIENT_DETAIL_TUNNEL_ROLE, getClientOwnedTunnelSource, resolveTunnelOwnerClientId } from './TunnelTable.helpers';

function createTunnel(overrides: Partial<ProxyConfig> = {}): ProxyConfig {
  return {
    id: 'tunnel-1',
    name: 'demo',
    type: 'tcp',
    local_ip: '127.0.0.1',
    local_port: 3000,
    remote_port: 18080,
    domain: '',
    client_id: 'legacy-client',
    ingress_bps: 0,
    egress_bps: 0,
    created_at: '2026-05-08T01:00:00Z',
    desired_state: 'running',
    runtime_state: 'exposed',
    capabilities: {
      can_resume: false,
      can_stop: true,
      can_edit: true,
      can_delete: true,
    },
    ...overrides,
  };
}

describe('TunnelTable client detail ownership projection', () => {
  test('uses owner role for child tunnel list queries', () => {
    expect(CLIENT_DETAIL_TUNNEL_ROLE).toBe('owner');
  });

  test('uses the tunnel owner as the action client id', () => {
    expect(resolveTunnelOwnerClientId(createTunnel({ owner_client_id: 'service-client' }), 'detail-client')).toBe('service-client');
    expect(resolveTunnelOwnerClientId(createTunnel({ client_id: 'stored-client' }), 'detail-client')).toBe('stored-client');
  });

  test('filters fallback client proxy cache to owned tunnels only', () => {
    const source = getClientOwnedTunnelSource(undefined, [
      createTunnel({ id: 'owned', owner_client_id: 'detail-client', client_id: 'detail-client' }),
      createTunnel({ id: 'related', owner_client_id: 'service-client', client_id: 'service-client' }),
    ], 'detail-client');

    expect(source.map((tunnel) => tunnel.id)).toEqual(['owned']);
  });
});
