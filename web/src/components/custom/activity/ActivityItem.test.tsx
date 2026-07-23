import { describe, expect, test } from 'bun:test';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';

import { ActivityItem } from './ActivityItem';
import { i18n } from '@/i18n';
import { TooltipProvider } from '@/components/ui/tooltip';
import type { ActivityItem as ActivityItemType } from '@/types';

function item(overrides: Partial<ActivityItemType> = {}): ActivityItemType {
  return {
    id: 1,
    occurred_at: '2026-07-23T00:00:00Z',
    recorded_at: '2026-07-23T00:00:00Z',
    severity: 'warning',
    category: 'security',
    action: 'client_auth_failed',
    source: 'server',
    actor: { type: 'security' },
    payload_version: 1,
    payload: { summary_key: 'activity.security.client_auth_failed', reason_code: 'invalid_token' },
    clients: [],
    tunnels: [],
    ...overrides,
  };
}

function render(activity: ActivityItemType) {
  const client = new QueryClient();
  return renderToStaticMarkup(createElement(QueryClientProvider, { client }, createElement(TooltipProvider, null, createElement(ActivityItem, { item: activity }))));
}

describe('ActivityItem', () => {
  test('renders allowlisted localized summary without arbitrary payload fields', async () => {
    await i18n.changeLanguage('en-US');
    const markup = render(item({ payload: {
      summary_key: 'activity.security.client_auth_failed',
      reason_code: 'invalid_token',
      session_id: '<script>secret-session</script>',
    } }));
    expect(markup).toContain('Client authentication failed');
    expect(markup).toContain('The token was invalid.');
    expect(markup).not.toContain('secret-session');
    expect(markup).not.toContain('&lt;script&gt;');
  });

  test('unknown payload versions use a generic safe summary', async () => {
    await i18n.changeLanguage('en-US');
    const markup = render(item({ payload_version: 99, payload: { summary_key: 'activity.security.client_auth_failed' } }));
    expect(markup).toContain('Details are unavailable for this event version.');
    expect(markup).not.toContain('Client authentication failed');
  });

  test('renders the Chinese summary and reason', async () => {
    await i18n.changeLanguage('zh-CN');
    const markup = render(item());
    expect(markup).toContain('客户端认证失败');
    expect(markup).toContain('令牌无效');
    await i18n.changeLanguage('en-US');
  });
});
