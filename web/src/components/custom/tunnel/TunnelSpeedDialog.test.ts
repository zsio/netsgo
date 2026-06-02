import { describe, expect, test } from 'bun:test';

import { buildTunnelSpeedFilter } from '@/lib/tunnel-speed';

describe('TunnelSpeedDialog', () => {
  test('builds the traffic rate chart filter for the selected tunnel', () => {
    const tunnel = {
      name: 'api',
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
        can_edit: true,
        can_delete: true,
      },
    } as const;

    expect(buildTunnelSpeedFilter(tunnel)).toEqual([tunnel]);
  });
});
