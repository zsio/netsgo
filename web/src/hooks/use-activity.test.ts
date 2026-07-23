import { describe, expect, test } from 'bun:test';
import { QueryClient } from '@tanstack/react-query';
import type { InfiniteData } from '@tanstack/react-query';

import type { ActivityItem, ActivityPage } from '@/types';
import {
  buildActivityQueryKey,
  flattenActivityPages,
  normalizeActivityQuery,
  prependActivityToMatchingQueries,
} from './use-activity';

function item(id: number, overrides: Partial<ActivityItem> = {}): ActivityItem {
  return {
    id,
    occurred_at: '2026-07-23T00:00:00Z',
    recorded_at: '2026-07-23T00:00:00Z',
    severity: 'info',
    category: 'client',
    action: 'online',
    source: 'server',
    actor: { type: 'system' },
    payload_version: 1,
    payload: {},
    clients: [{ client_id: 'client-1', relation: 'subject' }],
    tunnels: [],
    ...overrides,
  };
}

function data(items: ActivityItem[]): InfiniteData<ActivityPage> {
  return { pages: [{ items, has_more: false, direction: 'before' }], pageParams: [undefined] };
}

describe('activity query hook helpers', () => {
  test('normalizes cache identity including scope ID and limit', () => {
    const normalized = normalizeActivityQuery({ scope: 'client', scopeId: 'c1', limit: 10, severities: ['error', 'info', 'error'] });
    expect(normalized.severities).toEqual(['error', 'info']);
    expect(buildActivityQueryKey(normalized)).toEqual(['activity', 'client', 'c1', 10, ['error', 'info'], [], null, null]);
    expect(buildActivityQueryKey({ scope: 'client', scopeId: 'c1', limit: 50 })).not.toEqual(buildActivityQueryKey(normalized));
  });

  test('flattens and de-duplicates pages newest first', () => {
    const pages: InfiniteData<ActivityPage> = {
      pages: [
        { items: [item(4), item(3)], has_more: true, direction: 'before', next_cursor: 3 },
        { items: [item(3), item(2)], has_more: false, direction: 'before' },
      ],
      pageParams: [undefined, 3],
    };
    expect(flattenActivityPages(pages).map((entry) => entry.id)).toEqual([4, 3, 2]);
  });

  test('prepends only to matching global and scoped caches', () => {
    const queryClient = new QueryClient();
    const globalKey = buildActivityQueryKey({ scope: 'global' });
    const clientKey = buildActivityQueryKey({ scope: 'client', scopeId: 'client-1' });
    const otherKey = buildActivityQueryKey({ scope: 'client', scopeId: 'client-2' });
    queryClient.setQueryData(globalKey, data([item(1)]));
    queryClient.setQueryData(clientKey, data([item(1)]));
    queryClient.setQueryData(otherKey, data([]));

    prependActivityToMatchingQueries(queryClient, item(2));
    expect(flattenActivityPages(queryClient.getQueryData(globalKey)).map((entry) => entry.id)).toEqual([2, 1]);
    expect(flattenActivityPages(queryClient.getQueryData(clientKey)).map((entry) => entry.id)).toEqual([2, 1]);
    expect(flattenActivityPages(queryClient.getQueryData(otherKey))).toEqual([]);
    queryClient.clear();
  });
});
