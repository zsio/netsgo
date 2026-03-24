import { describe, expect, test } from 'bun:test';

import type { ProxyConfig } from '@/types';

import {
  buildTunnelMutationPayload,
  buildTunnelViewModel,
  getTunnelActionAvailability,
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
    desired_state: 'running',
    runtime_state: 'exposed',
    status: 'active',
    ...overrides,
  };
}

describe('tunnel-model', () => {
  test('支持 running + pending 状态并输出等待建立文案', () => {
    const view = buildTunnelViewModel(
      createTunnel({
        type: 'http',
        domain: 'app.example.com',
        remote_port: 0,
        desired_state: 'running',
        runtime_state: 'pending',
        status: 'pending',
      }),
      true,
    );

    expect(view.routeLabel).toBe('app.example.com -> 127.0.0.1:3000');
    expect(view.status.key).toBe('pending');
    expect(view.status.label).toBe('等待建立');
  });

  test('running + offline 时展示客户端离线', () => {
    const view = buildTunnelViewModel(
      createTunnel({
        type: 'http',
        domain: 'app.example.com',
        remote_port: 0,
        desired_state: 'running',
        runtime_state: 'offline',
        status: 'active',
      }),
      false,
    );

    expect(view.status.key).toBe('offline');
    expect(view.status.label).toBe('客户端离线');
    expect(view.status.description).toContain('等待 Client 上线');
    expect(view.rawStatus).toBe('active');
  });

  test('paused + idle 时展示已暂停', () => {
    const view = buildTunnelViewModel(
      createTunnel({
        desired_state: 'paused',
        runtime_state: 'idle',
        status: 'paused',
      }),
      false,
    );

    expect(view.status.key).toBe('paused');
    expect(view.status.label).toBe('已暂停');
  });

  test('离线 active 隧道允许 pause/edit/delete', () => {
    const permissions = getTunnelActionAvailability(
      createTunnel({
        status: 'active',
        desired_state: 'running',
        runtime_state: 'offline',
      }),
      false,
    );

    expect(permissions.canPause).toBe(true);
    expect(permissions.canResume).toBe(false);
    expect(permissions.canStop).toBe(true);
    expect(permissions.canEdit).toBe(true);
    expect(permissions.canDelete).toBe(true);
  });

  test('error 隧道允许 resume', () => {
    const permissions = getTunnelActionAvailability(
      createTunnel({
        status: 'error',
        desired_state: 'running',
        runtime_state: 'error',
      }),
      true,
    );

    expect(permissions.canPause).toBe(false);
    expect(permissions.canResume).toBe(true);
    expect(permissions.canStop).toBe(true);
    expect(permissions.canEdit).toBe(true);
    expect(permissions.canDelete).toBe(true);
  });

  test('stopped 隧道不再显示 stop 动作', () => {
    const permissions = getTunnelActionAvailability(
      createTunnel({
        status: 'stopped',
        desired_state: 'stopped',
        runtime_state: 'idle',
      }),
      false,
    );

    expect(permissions.canPause).toBe(false);
    expect(permissions.canResume).toBe(true);
    expect(permissions.canStop).toBe(false);
    expect(permissions.canEdit).toBe(true);
    expect(permissions.canDelete).toBe(true);
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

  test('TCP/UDP 缺少 remote_port 时不再自动回退到 0', () => {
    expect(() => buildTunnelMutationPayload({
      type: 'tcp',
      local_ip: '127.0.0.1',
      local_port: 22,
      domain: '',
    })).toThrow('必须填写明确的公网端口');
  });
});
