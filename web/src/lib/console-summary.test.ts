import { describe, expect, test } from 'bun:test';

import type { Client, ProxyConfig } from '@/types';

import { summarizeConsoleClients } from './console-summary';

function createTunnel(overrides: Partial<ProxyConfig> = {}): ProxyConfig {
  return {
    name: 'demo',
    type: 'tcp',
    local_ip: '127.0.0.1',
    local_port: 3000,
    remote_port: 18080,
    domain: '',
    client_id: 'client-1',
    ingress_bps: 0,
    egress_bps: 0,
    desired_state: 'running',
    runtime_state: 'exposed',
    capabilities: {
      can_resume: false,
      can_stop: true,
      can_edit: false,
      can_delete: false,
    },
    ...overrides,
  };
}

function createClient(overrides: Partial<Client> = {}): Client {
  return {
    id: 'client-1',
    info: {
      hostname: 'demo-host',
      os: 'linux',
      arch: 'amd64',
      ip: '127.0.0.1',
      version: '0.1.0',
    },
    stats: null,
    proxies: [],
    online: true,
    ...overrides,
  };
}

describe('summarizeConsoleClients', () => {
  test('按列表展示口径汇总客户端与隧道状态', () => {
    const summary = summarizeConsoleClients([
      createClient({
        id: 'online-client',
        online: true,
        proxies: [
          createTunnel({ name: 'active', desired_state: 'running', runtime_state: 'exposed' }),
          createTunnel({ name: 'pending', desired_state: 'running', runtime_state: 'pending' }),
          createTunnel({ name: 'error', desired_state: 'running', runtime_state: 'error', error: 'boom' }),
        ],
      }),
      createClient({
        id: 'offline-client',
        online: false,
        proxies: [
          createTunnel({ name: 'offline', desired_state: 'running', runtime_state: 'exposed' }),
          createTunnel({ name: 'stopped-a', desired_state: 'stopped', runtime_state: 'idle' }),
          createTunnel({ name: 'stopped', desired_state: 'stopped', runtime_state: 'idle' }),
        ],
      }),
    ]);

    expect(summary.total_clients).toBe(2);
    expect(summary.online_clients).toBe(1);
    expect(summary.offline_clients).toBe(1);
    expect(summary.total_tunnels).toBe(6);
    expect(summary.active_tunnels).toBe(1);
    expect(summary.inactive_tunnels).toBe(5);
    expect(summary.pending_tunnels).toBe(1);
    expect(summary.offline_tunnels).toBe(1);
    expect(summary.stopped_tunnels).toBe(2);
    expect(summary.error_tunnels).toBe(1);
  });
});
