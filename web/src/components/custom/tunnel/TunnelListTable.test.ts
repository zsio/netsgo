import { describe, expect, test } from 'bun:test';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';

import type { ProxyConfig } from '@/types';

import { TunnelListTable, type TunnelEntry } from './TunnelListTable';

function createTunnel(overrides: Partial<ProxyConfig> = {}): TunnelEntry {
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
    clientId: 'client-1',
    clientOnline: true,
    ...overrides,
  };
}

function renderTable(tunnels: TunnelEntry[]) {
  const client = new QueryClient();
  return renderToStaticMarkup(
    createElement(
      QueryClientProvider,
      { client },
      createElement(TunnelListTable, {
        tunnels,
        title: 'Child tunnels',
        showActions: true,
        showSearch: false,
      }),
    ),
  );
}

describe('TunnelListTable', () => {
  test('只按 capability projection 渲染动作按钮', () => {
    const markup = renderTable([
      createTunnel({
        capabilities: {
          can_resume: true,
          can_stop: false,
          can_edit: true,
          can_delete: true,
        },
      }),
    ]);

    expect(markup).not.toContain('title="Pause"');
    expect(markup).toContain('title="Start"');
    expect(markup).not.toContain('title="Stop"');
    expect(markup).toContain('title="Edit"');
    expect(markup).toContain('title="Delete"');
  });

  test('动作按钮默认直接展示，不再依赖行 hover', () => {
    const markup = renderTable([createTunnel()]);

    expect(markup).toContain('title="Stop"');
    expect(markup).toContain('lucide-pause');
    expect(markup).not.toContain('lucide-square');
    expect(markup).not.toContain('group-hover:opacity-100');
    expect(markup).not.toContain('opacity-0');
  });

  test('缺失 capability projection 时渲染直接失败', () => {
    expect(() => renderTable([
      createTunnel({
        capabilities: undefined as never,
      }),
    ])).toThrow('Tunnel capabilities are required');
  });

  test('可选显示 24h 流量列', () => {
    const client = new QueryClient();
    const markup = renderToStaticMarkup(
      createElement(
        QueryClientProvider,
        { client },
        createElement(TunnelListTable, {
          tunnels: [
            createTunnel({
              traffic24hBytes: 1536,
            }),
          ],
          title: 'Child tunnels',
          showActions: false,
          showSearch: false,
          showTraffic24h: true,
          traffic24hState: 'ready',
        }),
      ),
    );

    expect(markup).toContain('24h traffic');
    expect(markup).toContain('1.5 KB');
  });

  test('显示隧道限速列，未配置限速时展示无限符号', () => {
    const markup = renderTable([createTunnel()]);

    expect(markup).toContain('Limit');
    expect(markup).toContain('aria-label="Unlimited bandwidth"');
    expect(markup).toContain('lucide-infinity');
  });

  test('显示上下行限速策略', () => {
    const markup = renderTable([
      createTunnel({
        ingress_bps: 1024 * 1024,
        egress_bps: 2 * 1024 * 1024,
      }),
    ]);

    expect(markup).toContain('1.0 MiB/s');
    expect(markup).toContain('2.0 MiB/s');
    expect(markup).toContain('lucide-arrow-down');
    expect(markup).toContain('lucide-arrow-up');
  });

  test('只配置单方向限速时仅显示该方向', () => {
    const markup = renderTable([
      createTunnel({
        ingress_bps: 0,
        egress_bps: 1024 * 1024,
      }),
    ]);

    expect(markup).toContain('1.0 MiB/s');
    expect(markup).toContain('lucide-arrow-up');
    expect(markup).not.toContain('lucide-arrow-down');
    expect(markup).not.toContain('lucide-infinity');
  });

  test('默认按创建时间倒序展示隧道', () => {
    const markup = renderTable([
      createTunnel({ id: 'old', name: 'old-tunnel', created_at: '2026-05-07T01:00:00Z' }),
      createTunnel({ id: 'new', name: 'new-tunnel', created_at: '2026-05-08T01:00:00Z' }),
    ]);

    expect(markup.indexOf('new-tunnel')).toBeLessThan(markup.indexOf('old-tunnel'));
  });

  test('标题栏自定义操作会替代搜索框', () => {
    const client = new QueryClient();
    const markup = renderToStaticMarkup(
      createElement(
        QueryClientProvider,
        { client },
        createElement(TunnelListTable, {
          tunnels: [createTunnel()],
          title: 'Child tunnels',
          showActions: false,
          showSearch: true,
          headerAction: createElement('button', null, 'Add tunnel'),
        }),
      ),
    );

    expect(markup).toContain('Add tunnel');
    expect(markup).not.toContain('Search tunnels...');
  });

  test('拆分入口与目标服务，隐藏内部 endpoint 枚举', () => {
    const markup = renderTable([
      createTunnel({
        type: 'tcp',
        remote_port: 10123,
        local_ip: '127.0.0.1',
        local_port: 22,
      }),
    ]);

    expect(markup).toContain('Ingress');
    expect(markup).toContain('Target service');
    expect(markup).not.toContain('Link');
    expect(markup).not.toContain('Mapping');
    expect(markup).not.toContain('App / Type');
    expect(markup).not.toContain('Mapping relation');
    expect(markup).not.toContain('TCP_LISTEN');
    expect(markup).toContain('TCP');
    expect(markup).not.toContain('TCP listen');
    expect(markup).not.toContain('TCP service');
    expect(markup).toContain(':10123');
    expect(markup).toContain('127.0.0.1:22');
  });

  test('展示统一隧道入口、目标与 wildcard bind 警告', () => {
    const markup = renderTable([
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
        actual_transport: 'server_relay',
        p2p: {
          state: 'fallback',
        },
      }),
    ]);

    expect(markup).not.toContain('Ingress node');
    expect(markup).not.toContain('Target node');
    expect(markup).not.toContain('Link');
    expect(markup).not.toContain('Client ↔ Client');
    expect(markup).toContain('client-a');
    expect(markup).toContain('client-b');
    expect(markup).toContain('0.0.0.0:10022');
    expect(markup).toContain('127.0.0.1:22');
    expect(markup).not.toContain('P2P preferred (not open) · Server relay');
    expect(markup).not.toContain('Fell back to relay');
    expect(markup).toContain('Ingress binds to a wildcard address and is exposed to the ingress client network.');
  });

  test('归属节点可按回调渲染为可点击按钮', () => {
    const client = new QueryClient();
    const markup = renderToStaticMarkup(
      createElement(
        QueryClientProvider,
        { client },
        createElement(TunnelListTable, {
          tunnels: [createTunnel({ clientName: 'edge-node' })],
          title: 'All tunnels',
          showActions: false,
          showSearch: false,
          showClient: true,
          onClientClick: () => undefined,
        }),
      ),
    );

    expect(markup).toContain('Target service');
    expect(markup).not.toContain('Owner node');
    expect(markup).toContain('<button');
    expect(markup).toContain('edge-node');
    expect(markup).toContain('cursor-pointer');
    expect(markup).toContain('hover:text-primary');
    expect(markup).not.toContain('>Actions<');
  });

  test('仅在详情表启用速率图标动作', () => {
    const client = new QueryClient();
    const enabledMarkup = renderToStaticMarkup(
      createElement(
        QueryClientProvider,
        { client },
        createElement(TunnelListTable, {
          tunnels: [createTunnel()],
          title: 'Child tunnels',
          showActions: true,
          showSearch: false,
          showTraffic24h: true,
        }),
      ),
    );

    const disabledMarkup = renderToStaticMarkup(
      createElement(
        QueryClientProvider,
        { client: new QueryClient() },
        createElement(TunnelListTable, {
          tunnels: [createTunnel()],
          title: 'All tunnels',
          showActions: true,
          showSearch: false,
          showTraffic24h: false,
        }),
      ),
    );

    expect(enabledMarkup).toContain('title="Rate trend"');
    expect(disabledMarkup).not.toContain('title="Rate trend"');
  });

  test('停止态不显示速率动作，按编辑、启动、删除顺序展示', () => {
    const client = new QueryClient();
    const markup = renderToStaticMarkup(
      createElement(
        QueryClientProvider,
        { client },
        createElement(TunnelListTable, {
          tunnels: [createTunnel({
            desired_state: 'stopped',
            runtime_state: 'stopped',
            capabilities: {
              can_resume: true,
              can_stop: false,
              can_edit: true,
              can_delete: true,
            },
          })],
          title: 'Child tunnels',
          showActions: true,
          showSearch: false,
          showTraffic24h: true,
        }),
      ),
    );

    expect(markup).not.toContain('title="Rate trend"');
    expect(markup.indexOf('title="Edit"')).toBeLessThan(markup.indexOf('title="Start"'));
    expect(markup.indexOf('title="Start"')).toBeLessThan(markup.indexOf('title="Delete"'));
  });

  test('显示统一隧道运行 issues 摘要与详情', () => {
    const markup = renderTable([
      createTunnel({
        runtime_state: 'error',
        issues: [
          {
            code: 'provision_ack_timeout',
            scope: 'target_client',
            severity: 'error',
            message: 'target client acknowledgement timed out',
            retryable: true,
            observed_at: '2026-05-24T01:00:00Z',
          },
          {
            code: 'ingress_port_in_use',
            scope: 'ingress_client',
            severity: 'error',
            message: 'ingress port is already in use',
            retryable: true,
            observed_at: '2026-05-24T01:00:00Z',
          },
        ],
      }),
    ]);

    expect(markup).toContain('Ingress port is already in use. +1');
    expect(markup).toContain('error: Ingress port is already in use.');
    expect(markup).toContain('error: Client did not confirm tunnel provisioning in time.');
    expect(markup).toContain('lucide-circle-question-mark');
  });

});
