import { describe, expect, test } from 'bun:test';

import type { ProxyConfig } from '@/types';

import {
  buildTunnelMutationPayload,
  buildTunnelViewModel,
} from './tunnel-model';

function createTunnel(overrides: Partial<ProxyConfig> = {}): ProxyConfig {
  return {
    name: 'demo',
    type: 'tcp',
    local_ip: '127.0.0.1',
    local_port: 3000,
    remote_port: 18080,
    domain: '',
    client_id: 'client-1',
    status: 'active',
    ...overrides,
  };
}

describe('tunnel-model', () => {
  test('支持 pending 状态并输出等待文案', () => {
    const view = buildTunnelViewModel(
      createTunnel({
        type: 'http',
        domain: 'app.example.com',
        remote_port: 0,
        status: 'pending',
      }),
      true,
    );

    expect(view.routeLabel).toBe('app.example.com -> 127.0.0.1:3000');
    expect(view.status.key).toBe('pending');
    expect(view.status.label).toBe('等待就绪');
  });

  test('active 但 client 离线时展示不可服务', () => {
    const view = buildTunnelViewModel(
      createTunnel({
        type: 'http',
        domain: 'app.example.com',
        remote_port: 0,
        status: 'active',
      }),
      false,
    );

    expect(view.status.key).toBe('unavailable');
    expect(view.status.label).toBe('不可服务');
    expect(view.status.description).toContain('Client 离线');
    expect(view.rawStatus).toBe('active');
  });

  test('HTTP 隧道展示为 domain 到本地地址', () => {
    const view = buildTunnelViewModel(
      createTunnel({
        type: 'http',
        domain: 'printer.office.example',
        local_ip: '192.168.1.50',
        local_port: 8080,
        remote_port: 0,
      }),
      true,
    );

    expect(view.targetLabel).toBe('printer.office.example');
    expect(view.destinationLabel).toBe('192.168.1.50:8080');
    expect(view.routeLabel).toBe('printer.office.example -> 192.168.1.50:8080');
  });

  test('HTTP/TCP 提交 payload 分支规则一致', () => {
    expect(
      buildTunnelMutationPayload({
        type: 'http',
        local_ip: '127.0.0.1',
        local_port: 3000,
        remote_port: 2200,
        domain: 'app.example.com',
      }),
    ).toEqual({
      local_ip: '127.0.0.1',
      local_port: 3000,
      remote_port: 0,
      domain: 'app.example.com',
    });

    expect(
      buildTunnelMutationPayload({
        type: 'tcp',
        local_ip: '127.0.0.1',
        local_port: 22,
        remote_port: 2200,
        domain: 'ignored.example.com',
      }),
    ).toEqual({
      local_ip: '127.0.0.1',
      local_port: 22,
      remote_port: 2200,
      domain: '',
    });
  });
});
