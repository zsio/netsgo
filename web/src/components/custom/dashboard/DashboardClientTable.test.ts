import { describe, expect, test } from 'bun:test';
import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';

import type { RowVisibilityHook } from '@/hooks/use-row-visibility';
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

function createRowVisibilityHook(
  states: Array<{ hasVisibilitySupport: boolean; isDesktop: boolean; isVisible: boolean }>,
): RowVisibilityHook {
  let index = 0;

  return () => {
    const state = states[index] ?? states.at(-1) ?? {
      hasVisibilitySupport: false,
      isDesktop: false,
      isVisible: false,
    };

    index += 1;

    return {
      ref: () => {},
      ...state,
    };
  };
}

function renderTable(clients: Client[], rowVisibilityHook: RowVisibilityHook) {
  return renderToStaticMarkup(
    createElement(DashboardClientTableContent, {
      clients,
      onNavigate: () => {},
      rowVisibilityHook,
      renderSparkline: (clientId: string) => createElement('span', { 'data-chart-client': clientId }, 'chart'),
    }),
  );
}

describe('DashboardClientTableContent', () => {
  test('only mounts sparklines for visible desktop rows', () => {
    const markup = renderTable(
      [
        createClient({
          id: 'client-visible',
          info: { hostname: 'visible', os: 'linux', arch: 'amd64', ip: '10.0.0.1', version: '1.0.0' },
        }),
        createClient({
          id: 'client-hidden',
          info: { hostname: 'hidden', os: 'linux', arch: 'amd64', ip: '10.0.0.2', version: '1.0.0' },
        }),
      ],
      createRowVisibilityHook([
        { hasVisibilitySupport: true, isDesktop: true, isVisible: true },
        { hasVisibilitySupport: true, isDesktop: true, isVisible: false },
      ]),
    );

    expect(markup).toContain('data-chart-client="client-visible"');
    expect(markup).not.toContain('data-chart-client="client-hidden"');
  });

  test('does not mount sparklines in fallback or non-desktop environments', () => {
    const fallbackMarkup = renderTable(
      [createClient({ id: 'client-fallback' })],
      createRowVisibilityHook([
        { hasVisibilitySupport: false, isDesktop: true, isVisible: true },
      ]),
    );

    const mobileMarkup = renderTable(
      [createClient({ id: 'client-mobile' })],
      createRowVisibilityHook([
        { hasVisibilitySupport: true, isDesktop: false, isVisible: true },
      ]),
    );

    expect(fallbackMarkup).not.toContain('data-chart-client=');
    expect(mobileMarkup).not.toContain('data-chart-client=');
  });
});
