import { describe, expect, test } from 'bun:test';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';

import type { Client } from '@/types';

import { TopologyHeaderActions } from './NetworkTopology';

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
    proxies: [],
    ...overrides,
  };
}

function renderHeaderActions(activeClientId: string | null, clients: Client[] = []) {
  const client = new QueryClient();
  return renderToStaticMarkup(
    createElement(
      QueryClientProvider,
      { client },
      createElement(TopologyHeaderActions, {
        activeClientId,
        clients,
        onAddClient: () => {},
      }),
    ),
  );
}

describe('TopologyHeaderActions', () => {
  test('renders add-client action in overview mode', () => {
    const markup = renderHeaderActions(null);

    expect(markup).toContain('Add client');
    expect(markup).not.toContain('Add tunnel');
  });

  test('renders add-tunnel action for the focused client', () => {
    const markup = renderHeaderActions('client-1', [createClient()]);

    expect(markup).toContain('Add tunnel');
    expect(markup).not.toContain('Add client');
  });
});
