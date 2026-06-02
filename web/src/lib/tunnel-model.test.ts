import { describe, expect, test } from 'bun:test';

import { ApiError } from '@/lib/api';
import type { ProxyConfig } from '@/types';

import {
  buildClientToClientTunnelSpecCreateRequest,
  buildTunnelSpecCreateRequest,
  buildTunnelMutationPayload,
  buildTunnelViewModel,
  getTunnelActionAvailability,
  getTunnelMutationErrorMessage,
  getTunnelMutationFieldError,
} from './tunnel-model';

function createTunnel(overrides: Partial<ProxyConfig> = {}): ProxyConfig {
  return {
    id: 'tunnel-1',
    name: 'demo',
    type: 'tcp',
    local_ip: '127.0.0.1',
    local_port: 3000,
    remote_port: 18080,
    domain: '',
    client_id: 'client-1',
    ingress_bps: 0,
    egress_bps: 0,
    created_at: '2026-05-08T01:00:00Z',
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

describe('tunnel-model', () => {
  test('支持 running + pending 状态并输出等待建立文案', () => {
    const view = buildTunnelViewModel(
      createTunnel({
        type: 'http',
        domain: 'app.example.com',
        remote_port: 0,
        desired_state: 'running',
        runtime_state: 'pending',
      }),
      true,
    );

    expect(view.routeLabel).toBe('app.example.com -> 127.0.0.1:3000');
    expect(view.status.key).toBe('pending');
    expect(view.status.label).toBe('Pending');
  });

  test('running + offline 时展示客户端离线', () => {
    const view = buildTunnelViewModel(
      createTunnel({
        type: 'http',
        domain: 'app.example.com',
        remote_port: 0,
        desired_state: 'running',
        runtime_state: 'offline',
      }),
      false,
    );

    expect(view.status.key).toBe('offline');
    expect(view.status.label).toBe('Client offline');
    expect(view.status.description).toBeUndefined();
  });

  test('idle 时统一展示已停止', () => {
    const view = buildTunnelViewModel(
      createTunnel({
        desired_state: 'stopped',
        runtime_state: 'idle',
      }),
      false,
    );

    expect(view.status.key).toBe('stopped');
    expect(view.status.label).toBe('Stopped');
  });

  test('动作能力直接消费 server capability projection', () => {
    const permissions = getTunnelActionAvailability(
      createTunnel({
        desired_state: 'running',
        runtime_state: 'exposed',
        capabilities: {
          can_resume: true,
          can_stop: false,
          can_edit: true,
          can_delete: true,
        },
      }),
    );

    expect(permissions.canResume).toBe(true);
    expect(permissions.canStop).toBe(false);
    expect(permissions.canEdit).toBe(true);
    expect(permissions.canDelete).toBe(true);
  });

  test('缺失 capability projection 时立即失败，不再回退旧矩阵', () => {
    expect(() => getTunnelActionAvailability({
      ...createTunnel(),
      capabilities: undefined as never,
    })).toThrow('Tunnel capabilities are required');
  });

  test('capability projection 缺字段时立即失败', () => {
    expect(() => getTunnelActionAvailability({
      ...createTunnel(),
      capabilities: {} as never,
    })).toThrow('Tunnel capability "can_resume" is required');
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
      ingress_bps: 0,
      egress_bps: 0,
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
      ingress_bps: 0,
      egress_bps: 0,
    });
  });

  test('创建请求可转换为统一 TunnelSpec server_expose 结构', () => {
    expect(
      buildTunnelSpecCreateRequest({
        clientId: 'client-b',
        name: 'web',
        type: 'tcp',
        local_ip: '127.0.0.1',
        local_port: 80,
        remote_port: 18080,
        ingress_bps: 1024,
        egress_bps: 2048,
      }),
    ).toEqual({
      name: 'web',
      topology: 'server_expose',
      ingress: {
        location: 'server',
        type: 'tcp_listen',
        config: {
          bind_ip: '0.0.0.0',
          port: 18080,
        },
      },
      target: {
        location: 'client',
        client_id: 'client-b',
        type: 'tcp_service',
        config: {
          ip: '127.0.0.1',
          port: 80,
        },
      },
      transport_policy: 'server_relay_only',
      bandwidth_settings: {
        ingress_bps: 1024,
        egress_bps: 2048,
      },
    });
  });

  test('创建请求可转换为统一 TunnelSpec client_to_client TCP 结构', () => {
    expect(
      buildClientToClientTunnelSpecCreateRequest({
        ingressClientId: 'client-b',
        targetClientId: 'client-a',
        name: 'a-web-on-b',
        type: 'tcp',
        bind_ip: '127.0.0.1',
        local_ip: 'a2',
        local_port: 8080,
        remote_port: 18080,
      }),
    ).toEqual({
      name: 'a-web-on-b',
      topology: 'client_to_client',
      ingress: {
        location: 'client',
        client_id: 'client-b',
        type: 'tcp_listen',
        config: {
          bind_ip: '127.0.0.1',
          port: 18080,
        },
      },
      target: {
        location: 'client',
        client_id: 'client-a',
        type: 'tcp_service',
        config: {
          ip: 'a2',
          port: 8080,
        },
      },
      transport_policy: 'server_relay_only',
      bandwidth_settings: {
        ingress_bps: 0,
        egress_bps: 0,
      },
    });
  });

  test('创建请求可转换为统一 TunnelSpec client_to_client UDP 结构', () => {
    expect(
      buildClientToClientTunnelSpecCreateRequest({
        ingressClientId: 'client-b',
        targetClientId: 'client-a',
        name: 'a-dns-on-b',
        type: 'udp',
        bind_ip: '0.0.0.0',
        local_ip: '10.0.0.53',
        local_port: 53,
        remote_port: 1053,
        ingress_bps: 4096,
      }),
    ).toEqual({
      name: 'a-dns-on-b',
      topology: 'client_to_client',
      ingress: {
        location: 'client',
        client_id: 'client-b',
        type: 'udp_listen',
        config: {
          bind_ip: '0.0.0.0',
          port: 1053,
        },
      },
      target: {
        location: 'client',
        client_id: 'client-a',
        type: 'udp_service',
        config: {
          ip: '10.0.0.53',
          port: 53,
        },
      },
      transport_policy: 'server_relay_only',
      bandwidth_settings: {
        ingress_bps: 4096,
        egress_bps: 0,
      },
    });
  });

  test('TunnelSpec 字段优先驱动拓扑、参与方、传输和绑定提示文案', () => {
    const view = buildTunnelViewModel(
      createTunnel({
        topology: 'client_to_client',
        ingress: {
          location: 'client',
          client_id: 'client-a',
          type: 'tcp_listen',
          config: {
            bind_ip: '0.0.0.0',
            port: 10022,
          },
        },
        target: {
          location: 'client',
          client_id: 'client-b',
          type: 'tcp_service',
          config: {
            ip: '127.0.0.1',
            port: 22,
          },
        },
        transport_policy: 'direct_preferred',
        actual_transport: 'peer_direct',
        p2p: {
          state: 'connected',
        },
      }),
      true,
    );

    expect(view.targetLabel).toBe('0.0.0.0:10022');
    expect(view.destinationLabel).toBe('127.0.0.1:22');
    expect(view.topologyLabel).toBe('Client ↔ Client');
    expect(view.participantLabel).toBe('Ingress client-a / Target client-b');
    expect(view.transportLabel).toBe('P2P preferred (unavailable) · P2P direct (unavailable)');
    expect(view.p2pLabel).toBe('Direct connected');
    expect(view.ingressWarning).toBe('Ingress binds to a wildcard address and is exposed to the ingress client network.');
  });

  test('host-backed 目标配置使用 host 字段展示目标地址', () => {
    const view = buildTunnelViewModel(
      createTunnel({
        target: {
          location: 'client',
          client_id: 'client-b',
          type: 'tcp_service',
          config: {
            host: 'legacy.internal',
            port: 22,
          },
        },
      }),
      true,
    );

    expect(view.destinationLabel).toBe('legacy.internal:22');
  });

  test('带宽字段通过共享 payload builder 统一透传', () => {
    expect(
      buildTunnelMutationPayload({
        type: 'tcp',
        local_ip: '127.0.0.1',
        local_port: 22,
        remote_port: 2200,
        domain: '',
        ingress_bps: 2048,
        egress_bps: 4096,
      }),
    ).toEqual({
      local_ip: '127.0.0.1',
      local_port: 22,
      remote_port: 2200,
      domain: '',
      ingress_bps: 2048,
      egress_bps: 4096,
    });
  });

  test('TCP/UDP 缺少 remote_port 时不再自动回退到 0', () => {
    expect(() => buildTunnelMutationPayload({
      type: 'tcp',
      local_ip: '127.0.0.1',
      local_port: 22,
      domain: '',
    })).toThrow('TCP/UDP tunnels require an explicit public port.');
  });

  test('API 字段错误保留字段和错误码，文案按错误码本地化', () => {
    const error = new ApiError(400, 'Bad Request', 'bind_ip must be a valid IPv4 address', {
      field: 'ingress.config.bind_ip',
      code: 'invalid_bind_ip',
    });

    expect(getTunnelMutationFieldError(error)).toEqual({
      field: 'ingress.config.bind_ip',
      message: 'Ingress bind address must be a valid IP address.',
      code: 'invalid_bind_ip',
    });
  });

  test('unsupported_endpoint_type 使用统一可展示文案', () => {
    const error = new ApiError(400, 'Bad Request', 'unsupported target type "static_file"', {
      field: 'target.type',
      code: 'unsupported_endpoint_type',
    });

    expect(getTunnelMutationErrorMessage(error)).toBe('This target type is not supported yet. Only TCP/UDP services are supported.');
    expect(getTunnelMutationFieldError(error)).toEqual({
      field: 'target.type',
      message: 'This target type is not supported yet. Only TCP/UDP services are supported.',
      code: 'unsupported_endpoint_type',
    });
  });

  test('无字段信息的 API 错误不生成字段提示', () => {
    const error = new ApiError(409, 'Conflict', 'port is already in use', {
      code: 'ingress_port_in_use',
    });

    expect(getTunnelMutationFieldError(error)).toBeNull();
    expect(getTunnelMutationFieldError(new Error('plain error'))).toBeNull();
  });
});
