import { describe, expect, test } from 'vitest';
import { QueryClient } from '@tanstack/react-query';

import {
  cacheDeliveryRefresh,
  deleteWebhookMutationOptions,
  removeWebhookFromCache,
  replayWebhookDeliveryMutationOptions,
  saveWebhookMutationOptions,
  testWebhookMutationOptions,
  upsertWebhookInCache,
  webhookDeliveriesQueryKey,
  webhookDeliveryQueryKey,
  webhooksQueryKey,
} from './use-webhooks';

function webhook(id: string, overrides: Partial<ActivityWebhookConfig> = {}): ActivityWebhookConfig {
  return {
    id,
    revision: 1,
    name: `webhook ${id}`,
    enabled: true,
    targetKind: 'client',
    targetMode: 'all',
    targetIds: [],
    method: 'POST',
    url: `https://example.com/${id}`,
    headers: [],
    body: '',
    events: ['client.online'],
    minIntervalSeconds: 0,
    calls24h: 0,
    lastStatus: 'idle',
    consecutiveFailures: 0,
    lastCalledAt: null,
    createdAt: '2026-01-01T00:00:00Z',
    updatedAt: '2026-01-01T00:00:00Z',
    ...overrides,
  };
}

function invocation(id: string, webhookId: string, overrides: Partial<WebhookInvocation> = {}): WebhookInvocation {
  return {
    id,
    webhookId,
    webhookName: `webhook ${webhookId}`,
    eventId: 'evt_1',
    event: 'client.online',
    occurredAt: '2026-01-01T00:00:00Z',
    status: 'success',
    origin: 'test',
    statusCode: 204,
    durationMs: 120,
    attempts: [],
    requestMethod: 'POST',
    requestUrl: `https://example.com/${webhookId}`,
    requestHeaders: {},
    requestBody: null,
    responseHeaders: {},
    responseBody: '',
    createdAt: '2026-01-01T00:00:00Z',
    ...overrides,
  };
}

describe('upsertWebhookInCache', () => {
  test('seeds an empty cache with the saved webhook', () => {
    const queryClient = new QueryClient();
    const saved = webhook('wh_1');

    upsertWebhookInCache(queryClient, saved);

    expect(queryClient.getQueryData(webhooksQueryKey)).toEqual([saved]);
    queryClient.clear();
  });

  test('replaces an existing webhook in place and keeps list order', () => {
    const queryClient = new QueryClient();
    queryClient.setQueryData(webhooksQueryKey, [webhook('wh_1'), webhook('wh_2'), webhook('wh_3')]);
    const updated = webhook('wh_2', { name: 'renamed', revision: 2 });

    upsertWebhookInCache(queryClient, updated);

    const cached = queryClient.getQueryData<ActivityWebhookConfig[]>(webhooksQueryKey);
    expect(cached?.map((item) => item.id)).toEqual(['wh_1', 'wh_2', 'wh_3']);
    expect(cached?.[1]).toEqual(updated);
    queryClient.clear();
  });

  test('appends a webhook with a new id at the end', () => {
    const queryClient = new QueryClient();
    queryClient.setQueryData(webhooksQueryKey, [webhook('wh_1')]);
    const created = webhook('wh_2');

    upsertWebhookInCache(queryClient, created);

    expect(queryClient.getQueryData<ActivityWebhookConfig[]>(webhooksQueryKey)?.map((item) => item.id)).toEqual(['wh_1', 'wh_2']);
    queryClient.clear();
  });
});

describe('removeWebhookFromCache', () => {
  test('drops the webhook from the list and removes its delivery queries', async () => {
    const queryClient = new QueryClient();
    queryClient.setQueryData(webhooksQueryKey, [webhook('wh_1'), webhook('wh_2')]);
    const removedAll = webhookDeliveriesQueryKey('wh_1', 'all');
    const removedFailed = webhookDeliveriesQueryKey('wh_1', 'failed');
    const keptOther = webhookDeliveriesQueryKey('wh_2', 'all');
    queryClient.setQueryData(removedAll, { items: [] });
    queryClient.setQueryData(removedFailed, { items: [] });
    queryClient.setQueryData(keptOther, { items: [] });

    removeWebhookFromCache(queryClient, 'wh_1');
    await Promise.resolve();

    expect(queryClient.getQueryData<ActivityWebhookConfig[]>(webhooksQueryKey)?.map((item) => item.id)).toEqual(['wh_2']);
    expect(queryClient.getQueryState(removedAll)).toBeUndefined();
    expect(queryClient.getQueryState(removedFailed)).toBeUndefined();
    expect(queryClient.getQueryState(keptOther)).toBeDefined();
    queryClient.clear();
  });
});

describe('cacheDeliveryRefresh', () => {
  test('writes the delivery detail and invalidates its webhook delivery list', async () => {
    const queryClient = new QueryClient();
    const delivery = invocation('dlv_1', 'wh_1');
    const listKey = webhookDeliveriesQueryKey('wh_1', 'all');
    const otherListKey = webhookDeliveriesQueryKey('wh_2', 'all');
    queryClient.setQueryData(listKey, { items: [] });
    queryClient.setQueryData(otherListKey, { items: [] });

    cacheDeliveryRefresh(queryClient, delivery);
    await Promise.resolve();

    expect(queryClient.getQueryData(webhookDeliveryQueryKey('dlv_1'))).toEqual(delivery);
    expect(queryClient.getQueryState(listKey)?.isInvalidated).toBe(true);
    expect(queryClient.getQueryState(otherListKey)?.isInvalidated).toBe(false);
    queryClient.clear();
  });
});

describe('webhook mutation options', () => {
  test('saveWebhookMutationOptions routes create vs update by revision', async () => {
    const queryClient = new QueryClient();
    const originalFetch = globalThis.fetch;
    const calls: { method: string; url: string }[] = [];
    const apiItem = (id: string, revision: number) => JSON.stringify({
      id,
      revision,
      name: `webhook ${id}`,
      enabled: true,
      target_kind: 'client',
      target_mode: 'all',
      target_ids: [],
      method: 'POST',
      url: `https://example.com/${id}`,
      headers: [],
      body: '',
      events: ['client.online'],
      min_interval_seconds: 0,
      calls_24h: 0,
      last_status: 'idle',
      consecutive_failures: 0,
      last_called_at: null,
      created_at: '2026-01-01T00:00:00Z',
      updated_at: '2026-01-01T00:00:00Z',
    });
    globalThis.fetch = (async (input: RequestInfo | URL, init?: RequestInit) => {
      const method = init?.method ?? 'GET';
      const id = method === 'PUT' ? 'wh_1' : 'wh_new';
      calls.push({ method, url: String(input) });
      return new Response(apiItem(id, method === 'PUT' ? 3 : 1), { status: 200 });
    }) as typeof fetch;

    try {
      const options = saveWebhookMutationOptions(queryClient);
      const created = await options.mutationFn?.(webhook('wh_1', { revision: 0 }));
      const updated = await options.mutationFn?.(webhook('wh_1', { revision: 2 }));
      expect(calls).toEqual([
        { method: 'POST', url: '/api/webhooks' },
        { method: 'PUT', url: '/api/webhooks/wh_1' },
      ]);
      expect(created).toMatchObject({ id: 'wh_new', revision: 1 });
      expect(updated).toMatchObject({ id: 'wh_1', revision: 3 });
    } finally {
      globalThis.fetch = originalFetch;
      queryClient.clear();
    }
  });

  test('saveWebhookMutationOptions onSuccess upserts the saved webhook into the cache', () => {
    const queryClient = new QueryClient();
    queryClient.setQueryData(webhooksQueryKey, [webhook('wh_1')]);
    const options = saveWebhookMutationOptions(queryClient);

    options.onSuccess?.(webhook('wh_1', { name: 'renamed', revision: 2 }), webhook('wh_1'), undefined);

    const cached = queryClient.getQueryData<ActivityWebhookConfig[]>(webhooksQueryKey);
    expect(cached?.map((item) => item.id)).toEqual(['wh_1']);
    expect(cached?.[0]?.name).toBe('renamed');
    queryClient.clear();
  });

  test('deleteWebhookMutationOptions onSuccess removes the webhook and its delivery queries', async () => {
    const queryClient = new QueryClient();
    queryClient.setQueryData(webhooksQueryKey, [webhook('wh_1'), webhook('wh_2')]);
    const removedKey = webhookDeliveriesQueryKey('wh_1', 'all');
    queryClient.setQueryData(removedKey, { items: [] });
    const options = deleteWebhookMutationOptions(queryClient);

    options.onSuccess?.(undefined, 'wh_1', undefined);
    await Promise.resolve();

    expect(queryClient.getQueryData<ActivityWebhookConfig[]>(webhooksQueryKey)?.map((item) => item.id)).toEqual(['wh_2']);
    expect(queryClient.getQueryState(removedKey)).toBeUndefined();
    queryClient.clear();
  });

  test('testWebhookMutationOptions onSuccess writes the delivery and invalidates its list', async () => {
    const queryClient = new QueryClient();
    const listKey = webhookDeliveriesQueryKey('wh_1', 'all');
    queryClient.setQueryData(listKey, { items: [] });
    const options = testWebhookMutationOptions(queryClient);

    options.onSuccess?.(invocation('dlv_1', 'wh_1'), { config: webhook('wh_1'), event: 'client.online' }, undefined);
    await Promise.resolve();

    expect(queryClient.getQueryData(webhookDeliveryQueryKey('dlv_1'))).toEqual(invocation('dlv_1', 'wh_1'));
    expect(queryClient.getQueryState(listKey)?.isInvalidated).toBe(true);
    expect(queryClient.getQueryState(webhooksQueryKey)?.isInvalidated).not.toBe(true);
    queryClient.clear();
  });

  test('replayWebhookDeliveryMutationOptions onSuccess also invalidates the webhook list', async () => {
    const queryClient = new QueryClient();
    const listKey = webhookDeliveriesQueryKey('wh_1', 'all');
    queryClient.setQueryData(listKey, { items: [] });
    queryClient.setQueryData(webhooksQueryKey, []);
    const options = replayWebhookDeliveryMutationOptions(queryClient);

    options.onSuccess?.(invocation('dlv_1', 'wh_1'), 'dlv_1', undefined);
    await Promise.resolve();

    expect(queryClient.getQueryData(webhookDeliveryQueryKey('dlv_1'))).toEqual(invocation('dlv_1', 'wh_1'));
    expect(queryClient.getQueryState(listKey)?.isInvalidated).toBe(true);
    expect(queryClient.getQueryState(webhooksQueryKey)?.isInvalidated).toBe(true);
    queryClient.clear();
  });
});
