import { describe, expect, test } from 'bun:test';
import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';

import type { Client } from '@/types';

import { DashboardClientTableContent } from './DashboardClientTable';

function createClient(overrides: Partial<Client> = {}): Client {
  return {
    id: 'client-1',
    ingress_bps: 0,
    egress_bps: 0,
    info: {
      hostname: 'demo-host',
      os: 'linux',
      arch: 'amd64',
      ip: '10.0.0.1',
      version: '1.0.0',
    },
    stats: null,
    online: true,
    ...overrides,
  };
}

describe('DashboardClientTableContent', () => {
  test('orders online clients before offline clients and renders icon actions', () => {
    const markup = renderToStaticMarkup(
      createElement(DashboardClientTableContent, {
        clients: [
          createClient({
            id: 'offline-client',
            online: false,
            info: { hostname: 'offline-host', os: 'linux', arch: 'amd64', ip: '10.0.0.2', version: '1.0.0' },
          }),
          createClient({
            id: 'online-client',
            online: true,
            info: { hostname: 'online-host', os: 'linux', arch: 'amd64', ip: '10.0.0.1', version: '1.0.0' },
          }),
        ],
        onNavigate: () => {},
        onDelete: () => {},
      }),
    );

    expect(markup.indexOf('online-host')).toBeLessThan(markup.indexOf('offline-host'));
    expect(markup).toContain('title="查看详情"');
    expect(markup).toContain('title="删除离线节点"');
  });

  test('renders the dashboard add-client header action when provided', () => {
    const markup = renderToStaticMarkup(
      createElement(DashboardClientTableContent, {
        clients: [],
        onNavigate: () => {},
        onAddClient: () => {},
      }),
    );

    expect(markup).toContain('在线端点 (Clients)');
    expect(markup).toContain('添加客户端');
  });
});