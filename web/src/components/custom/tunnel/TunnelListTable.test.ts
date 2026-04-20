import { describe, expect, test } from 'bun:test';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';

import type { ProxyConfig } from '@/types';

import { TunnelListTable, type TunnelEntry } from './TunnelListTable';

function createTunnel(overrides: Partial<ProxyConfig> = {}): TunnelEntry {
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
});
