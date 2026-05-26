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
        title: '下属隧道',
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

    expect(markup).not.toContain('title="暂停"');
    expect(markup).toContain('title="启动"');
    expect(markup).not.toContain('title="停止"');
    expect(markup).toContain('title="编辑"');
    expect(markup).toContain('title="删除"');
  });

  test('动作按钮默认直接展示，不再依赖行 hover', () => {
    const markup = renderTable([createTunnel()]);

    expect(markup).toContain('title="停止"');
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
          title: '下属隧道',
          showActions: false,
          showSearch: false,
          showTraffic24h: true,
          traffic24hState: 'ready',
        }),
      ),
    );

    expect(markup).toContain('24 小时流量');
    expect(markup).toContain('1.5 KB');
  });

  test('显示隧道限速列，未配置限速时展示无限符号', () => {
    const markup = renderTable([createTunnel()]);

    expect(markup).toContain('限速');
    expect(markup).toContain('aria-label="不限速"');
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
          title: '下属隧道',
          showActions: false,
          showSearch: true,
          headerAction: createElement('button', null, '添加隧道'),
        }),
      ),
    );

    expect(markup).toContain('添加隧道');
    expect(markup).not.toContain('搜索隧道...');
  });

  test('合并类型与映射关系为映射列', () => {
    const markup = renderTable([
      createTunnel({
        type: 'tcp',
        remote_port: 10123,
        local_ip: '127.0.0.1',
        local_port: 22,
      }),
    ]);

    expect(markup).toContain('映射');
    expect(markup).not.toContain('应用 / 类型');
    expect(markup).not.toContain('映射关系');
    expect(markup).toContain('TCP');
    expect(markup).toContain(':10123');
    expect(markup).toContain('127.0.0.1:22');
    expect(markup).toContain('w-11');
    expect(markup).toContain('w-4');
  });

  test('展示统一隧道拓扑、参与方、传输与 wildcard bind 警告', () => {
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

    expect(markup).toContain('拓扑 / 传输');
    expect(markup).toContain('Client ↔ Client');
    expect(markup).toContain('入口 client-a / 目标 client-b');
    expect(markup).toContain('P2P 优先 · Server 中继');
    expect(markup).toContain('已回退中继');
    expect(markup).toContain('入口绑定到通配地址，会暴露给入口 Client 所在网络。');
  });

  test('归属节点可按回调渲染为可点击按钮', () => {
    const client = new QueryClient();
    const markup = renderToStaticMarkup(
      createElement(
        QueryClientProvider,
        { client },
        createElement(TunnelListTable, {
          tunnels: [createTunnel({ clientName: 'edge-node' })],
          title: '全部隧道列表',
          showActions: false,
          showSearch: false,
          showClient: true,
          onClientClick: () => undefined,
        }),
      ),
    );

    expect(markup).toContain('归属节点');
    expect(markup).toContain('<button');
    expect(markup).toContain('edge-node');
    expect(markup).toContain('cursor-pointer');
    expect(markup).toContain('hover:text-foreground');
    expect(markup).not.toContain('>操作<');
  });

  test('仅在详情表启用速率图标动作', () => {
    const client = new QueryClient();
    const enabledMarkup = renderToStaticMarkup(
      createElement(
        QueryClientProvider,
        { client },
        createElement(TunnelListTable, {
          tunnels: [createTunnel()],
          title: '下属隧道',
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
          title: '全部隧道列表',
          showActions: true,
          showSearch: false,
          showTraffic24h: false,
        }),
      ),
    );

    expect(enabledMarkup).toContain('title="速率趋势"');
    expect(disabledMarkup).not.toContain('title="速率趋势"');
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
            message: '目标客户端确认超时',
            retryable: true,
            observed_at: '2026-05-24T01:00:00Z',
          },
          {
            code: 'ingress_port_in_use',
            scope: 'ingress_client',
            severity: 'error',
            message: '入口端口已被占用',
            retryable: true,
            observed_at: '2026-05-24T01:00:00Z',
          },
        ],
      }),
    ]);

    expect(markup).toContain('入口端口已被占用 +1');
    expect(markup).toContain('error: 入口端口已被占用');
    expect(markup).toContain('error: 目标客户端确认超时');
    expect(markup).toContain('lucide-circle-question-mark');
  });

});
