import { describe, expect, test } from 'bun:test';
import { QueryClient } from '@tanstack/react-query';

import type { ActivityItem, Client, ProxyConfig } from '@/types';

import { applyEventForDiagnostics, createActivityRecoveryState, createEventStreamSnapshotState } from './use-event-stream';

interface Deferred<T> {
  promise: Promise<T>;
  resolve: (value: T) => void;
  reject: (reason?: unknown) => void;
}

function createDeferred<T>(): Deferred<T> {
  let resolve!: (value: T) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

function createTunnel(
  runtimeState: ProxyConfig['runtime_state'],
  overrides: Partial<ProxyConfig> = {},
): ProxyConfig {
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
    runtime_state: runtimeState,
    capabilities: {
      can_resume: false,
      can_stop: runtimeState === 'exposed',
      can_edit: false,
      can_delete: runtimeState !== 'pending',
      can_migrate: runtimeState !== 'pending',
    },
    ...overrides,
  };
}

function createClientWithTunnels(id: string, proxies: ProxyConfig[]): Client {
  return {
    id,
    ingress_bps: 0,
    egress_bps: 0,
    info: {
      hostname: id,
      os: 'linux',
      arch: 'amd64',
      ip: '127.0.0.1',
      version: 'v0.1.0',
    },
    stats: null,
    proxies,
    online: true,
  };
}

function createClient(runtimeState: ProxyConfig['runtime_state']): Client {
  return createClientWithTunnels('client-1', [createTunnel(runtimeState)]);
}

function tunnelChangedPayload(clientId: string, action: string, tunnel: ProxyConfig) {
  return JSON.stringify({
    client_id: clientId,
    action,
    tunnel,
  });
}

function tunnelChangedEvent(runtimeState: ProxyConfig['runtime_state'], action: string) {
  return tunnelChangedPayload('client-1', action, createTunnel(runtimeState));
}

function snapshotPayload(runtimeState: ProxyConfig['runtime_state'], generatedAt?: string) {
  return JSON.stringify({
    clients: [createClient(runtimeState)],
    generated_at: generatedAt,
  });
}

function snapshotResponse(runtimeState: ProxyConfig['runtime_state']) {
  return new Response(snapshotPayload(runtimeState), {
    status: 200,
    headers: { 'Content-Type': 'application/json' },
  });
}

function clientsSnapshotResponse(
  clients: Client[],
  overrides: Record<string, unknown> = {},
) {
  return new Response(JSON.stringify({ clients, ...overrides }), {
    status: 200,
    headers: { 'Content-Type': 'application/json' },
  });
}

async function waitForRequests(requests: unknown[], count: number) {
  const deadline = Date.now() + 500;
  while (requests.length < count && Date.now() < deadline) {
    await new Promise((resolve) => setTimeout(resolve, 0));
  }
  expect(requests.length).toBe(count);
}

async function flushAsyncWork() {
  await Promise.resolve();
  await new Promise((resolve) => setTimeout(resolve, 0));
}

function activity(id: number): ActivityItem {
  return {
    id, occurred_at: '2026-07-23T00:00:00Z', recorded_at: '2026-07-23T00:00:00Z',
    severity: 'info', category: 'client', action: 'online', source: 'server',
    actor: { type: 'system' }, payload_version: 1, payload: {},
    clients: [{ client_id: 'client-1', relation: 'subject' }], tunnels: [],
  };
}

describe('use-event-stream diagnostics', () => {
  test('keeps newer tunnel_changed state when an older console snapshot resolves later', async () => {
    const queryClient = new QueryClient();
    queryClient.setQueryData<Client[]>(['clients'], [createClient('pending')]);

    const originalFetch = globalThis.fetch;
    const requests: Deferred<Response>[] = [];
    globalThis.fetch = (() => {
      const deferred = createDeferred<Response>();
      requests.push(deferred);
      return deferred.promise;
    }) as typeof fetch;

    try {
      const statuses: string[] = [];
      const snapshotState = createEventStreamSnapshotState();
      applyEventForDiagnostics(queryClient, (status) => statuses.push(status), snapshotState, 'tunnel_changed', tunnelChangedEvent('pending', 'pending'));
      await waitForRequests(requests, 1);

      applyEventForDiagnostics(queryClient, (status) => statuses.push(status), snapshotState, 'tunnel_changed', tunnelChangedEvent('exposed', 'restored'));
      await waitForRequests(requests, 2);

      expect(queryClient.getQueryData<Client[]>(['clients'])?.[0]?.proxies?.[0]?.runtime_state).toBe('exposed');

      requests[1].resolve(snapshotResponse('exposed'));
      await flushAsyncWork();
      expect(queryClient.getQueryData<Client[]>(['clients'])?.[0]?.proxies?.[0]?.runtime_state).toBe('exposed');

      requests[0].resolve(snapshotResponse('pending'));
      await flushAsyncWork();

      expect(queryClient.getQueryData<Client[]>(['clients'])?.[0]?.proxies?.[0]?.runtime_state).toBe('exposed');
      expect(statuses).toEqual(['connected']);
    } finally {
      globalThis.fetch = originalFetch;
      queryClient.clear();
    }
  });

  test('ignores older SSE snapshots after a newer snapshot has been applied', () => {
    const queryClient = new QueryClient();
    const snapshotState = createEventStreamSnapshotState();

    try {
      applyEventForDiagnostics(
        queryClient,
        () => undefined,
        snapshotState,
        'snapshot',
        snapshotPayload('exposed', '2026-05-08T01:00:02Z'),
      );
      expect(queryClient.getQueryData<Client[]>(['clients'])?.[0]?.proxies?.[0]?.runtime_state).toBe('exposed');

      applyEventForDiagnostics(
        queryClient,
        () => undefined,
        snapshotState,
        'snapshot',
        snapshotPayload('pending', '2026-05-08T01:00:01Z'),
      );

      expect(queryClient.getQueryData<Client[]>(['clients'])?.[0]?.proxies?.[0]?.runtime_state).toBe('exposed');
    } finally {
      queryClient.clear();
    }
  });

  test('moves a server-expose tunnel from the old owner to the new owner', async () => {
    const queryClient = new QueryClient();
    const oldOwnerId = 'old-owner';
    const newOwnerId = 'new-owner';
    const oldTunnel = createTunnel('exposed', {
      revision: 7,
      topology: 'server_expose',
      client_id: oldOwnerId,
      owner_client_id: oldOwnerId,
      ingress: {
        location: 'server',
        type: 'tcp_listen',
        config: { bind_ip: '0.0.0.0', port: 18080 },
      },
      target: {
        location: 'client',
        client_id: oldOwnerId,
        type: 'tcp_service',
        config: { host: '127.0.0.1', port: 3000 },
      },
    });
    const migratedTunnel: ProxyConfig = {
      ...oldTunnel,
      revision: 8,
      client_id: newOwnerId,
      owner_client_id: newOwnerId,
      runtime_state: 'pending',
      target: {
        location: 'client',
        client_id: newOwnerId,
        type: 'tcp_service',
        config: { host: '127.0.0.1', port: 3000 },
      },
      capabilities: {
        ...oldTunnel.capabilities,
        can_stop: false,
        can_migrate: false,
      },
    };
    const finalClients = [
      createClientWithTunnels(oldOwnerId, []),
      createClientWithTunnels(newOwnerId, [migratedTunnel]),
    ];
    queryClient.setQueryData<Client[]>(['clients'], [
      createClientWithTunnels(oldOwnerId, [oldTunnel]),
      createClientWithTunnels(newOwnerId, []),
    ]);

    const originalFetch = globalThis.fetch;
    const requests: Deferred<Response>[] = [];
    globalThis.fetch = (() => {
      const deferred = createDeferred<Response>();
      requests.push(deferred);
      return deferred.promise;
    }) as typeof fetch;

    try {
      const snapshotState = createEventStreamSnapshotState();
      applyEventForDiagnostics(
        queryClient,
        () => undefined,
        snapshotState,
        'tunnel_changed',
        tunnelChangedPayload(oldOwnerId, 'migrated_out', oldTunnel),
      );

      let clients = queryClient.getQueryData<Client[]>(['clients']);
      expect(clients?.find((client) => client.id === oldOwnerId)?.proxies).toEqual([]);
      expect(clients?.find((client) => client.id === newOwnerId)?.proxies).toEqual([]);

      applyEventForDiagnostics(
        queryClient,
        () => undefined,
        snapshotState,
        'tunnel_changed',
        tunnelChangedPayload(newOwnerId, 'migrated_in', migratedTunnel),
      );

      clients = queryClient.getQueryData<Client[]>(['clients']);
      expect(clients?.find((client) => client.id === oldOwnerId)?.proxies).toEqual([]);
      expect(clients?.find((client) => client.id === newOwnerId)?.proxies).toEqual([migratedTunnel]);

      await waitForRequests(requests, 2);
      requests[0].resolve(clientsSnapshotResponse(finalClients));
      requests[1].resolve(clientsSnapshotResponse(finalClients));
      await flushAsyncWork();
    } finally {
      globalThis.fetch = originalFetch;
      queryClient.clear();
    }
  });

  test('keeps the c2c ingress copy on migrated_out and updates it on migrated_in', async () => {
    const queryClient = new QueryClient();
    const ingressId = 'ingress-client';
    const oldOwnerId = 'old-target';
    const newOwnerId = 'new-target';
    const oldTunnel = createTunnel('active', {
      revision: 11,
      topology: 'client_to_client',
      client_id: oldOwnerId,
      owner_client_id: oldOwnerId,
      ingress: {
        location: 'client',
        client_id: ingressId,
        type: 'tcp_listen',
        config: { bind_ip: '0.0.0.0', port: 18080 },
      },
      target: {
        location: 'client',
        client_id: oldOwnerId,
        type: 'tcp_service',
        config: { host: '127.0.0.1', port: 3000 },
      },
    });
    const migratedTunnel: ProxyConfig = {
      ...oldTunnel,
      revision: 12,
      client_id: newOwnerId,
      owner_client_id: newOwnerId,
      runtime_state: 'pending',
      target: {
        location: 'client',
        client_id: newOwnerId,
        type: 'tcp_service',
        config: { host: '127.0.0.1', port: 3000 },
      },
      capabilities: {
        ...oldTunnel.capabilities,
        can_stop: false,
        can_migrate: false,
      },
    };
    const finalClients = [
      createClientWithTunnels(ingressId, [migratedTunnel]),
      createClientWithTunnels(oldOwnerId, []),
      createClientWithTunnels(newOwnerId, [migratedTunnel]),
    ];
    queryClient.setQueryData<Client[]>(['clients'], [
      createClientWithTunnels(ingressId, [oldTunnel]),
      createClientWithTunnels(oldOwnerId, [oldTunnel]),
      createClientWithTunnels(newOwnerId, []),
    ]);
    queryClient.setQueryData(['client-tunnels', oldOwnerId, 'owner'], [oldTunnel]);
    queryClient.setQueryData(['client-tunnels', newOwnerId, 'owner'], []);
    queryClient.setQueryData(['client-tunnels', ingressId, 'ingress'], [oldTunnel]);
    queryClient.setQueryData(['client-traffic', oldOwnerId, '60s', ''], { resolution: 'second', items: [] });
    queryClient.setQueryData(['client-traffic', newOwnerId, '60s', 'demo'], { resolution: 'second', items: [] });
    queryClient.setQueryData(['console-summary'], { marker: 'stale-summary' });
    queryClient.setQueryData(['server-status'], { marker: 'stale-status' });

    const originalFetch = globalThis.fetch;
    const requests: Deferred<Response>[] = [];
    globalThis.fetch = (() => {
      const deferred = createDeferred<Response>();
      requests.push(deferred);
      return deferred.promise;
    }) as typeof fetch;

    try {
      const snapshotState = createEventStreamSnapshotState();
      applyEventForDiagnostics(
        queryClient,
        () => undefined,
        snapshotState,
        'tunnel_changed',
        tunnelChangedPayload(oldOwnerId, 'migrated_out', oldTunnel),
      );

      let clients = queryClient.getQueryData<Client[]>(['clients']);
      expect(clients?.find((client) => client.id === oldOwnerId)?.proxies).toEqual([]);
      expect(clients?.find((client) => client.id === ingressId)?.proxies).toEqual([oldTunnel]);

      applyEventForDiagnostics(
        queryClient,
        () => undefined,
        snapshotState,
        'tunnel_changed',
        tunnelChangedPayload(newOwnerId, 'migrated_in', migratedTunnel),
      );

      clients = queryClient.getQueryData<Client[]>(['clients']);
      expect(clients?.find((client) => client.id === oldOwnerId)?.proxies).toEqual([]);
      expect(clients?.find((client) => client.id === ingressId)?.proxies).toEqual([migratedTunnel]);
      expect(clients?.find((client) => client.id === newOwnerId)?.proxies).toEqual([migratedTunnel]);
      expect(queryClient.getQueryState(['client-tunnels', oldOwnerId, 'owner'])?.isInvalidated).toBe(true);
      expect(queryClient.getQueryState(['client-tunnels', newOwnerId, 'owner'])?.isInvalidated).toBe(true);
      expect(queryClient.getQueryState(['client-tunnels', ingressId, 'ingress'])?.isInvalidated).toBe(true);
      expect(queryClient.getQueryState(['client-traffic', oldOwnerId, '60s', ''])?.isInvalidated).toBe(true);
      expect(queryClient.getQueryState(['client-traffic', newOwnerId, '60s', 'demo'])?.isInvalidated).toBe(true);
      expect(queryClient.getQueryData(['console-summary'])).toEqual({ marker: 'stale-summary' });
      expect(queryClient.getQueryData(['server-status'])).toEqual({ marker: 'stale-status' });
      expect(queryClient.getQueryState(['console-summary'])?.isInvalidated).toBe(false);
      expect(queryClient.getQueryState(['server-status'])?.isInvalidated).toBe(false);

      await waitForRequests(requests, 2);
      requests[0].resolve(clientsSnapshotResponse(finalClients));
      requests[1].resolve(clientsSnapshotResponse(finalClients));
      await flushAsyncWork();
    } finally {
      globalThis.fetch = originalFetch;
      queryClient.clear();
    }
  });

  test('ignores a stale migrated_out resync failure after migrated_in resync succeeds', async () => {
    const queryClient = new QueryClient();
    const oldOwnerId = 'old-owner';
    const newOwnerId = 'new-owner';
    const oldTunnel = createTunnel('exposed', {
      client_id: oldOwnerId,
      owner_client_id: oldOwnerId,
    });
    const migratedTunnel = createTunnel('pending', {
      ...oldTunnel,
      client_id: newOwnerId,
      owner_client_id: newOwnerId,
    });
    const finalClients = [
      createClientWithTunnels(oldOwnerId, []),
      createClientWithTunnels(newOwnerId, [migratedTunnel]),
    ];
    queryClient.setQueryData<Client[]>(['clients'], [
      createClientWithTunnels(oldOwnerId, [oldTunnel]),
      createClientWithTunnels(newOwnerId, []),
    ]);
    queryClient.setQueryData(['console-summary'], { marker: 'fresh-summary' });
    queryClient.setQueryData(['server-status'], { marker: 'fresh-status' });

    const originalFetch = globalThis.fetch;
    const requests: Deferred<Response>[] = [];
    globalThis.fetch = (() => {
      const deferred = createDeferred<Response>();
      requests.push(deferred);
      return deferred.promise;
    }) as typeof fetch;

    try {
      const statuses: string[] = [];
      const snapshotState = createEventStreamSnapshotState();
      applyEventForDiagnostics(
        queryClient,
        (status) => statuses.push(status),
        snapshotState,
        'tunnel_changed',
        tunnelChangedPayload(oldOwnerId, 'migrated_out', oldTunnel),
      );
      applyEventForDiagnostics(
        queryClient,
        (status) => statuses.push(status),
        snapshotState,
        'tunnel_changed',
        tunnelChangedPayload(newOwnerId, 'migrated_in', migratedTunnel),
      );

      requests[1].resolve(clientsSnapshotResponse(finalClients, {
        summary: { marker: 'fresh-summary' },
        server_status: { marker: 'fresh-status' },
      }));
      await flushAsyncWork();
      requests[0].reject(new Error('stale migrated_out resync failed'));
      await flushAsyncWork();

      expect(queryClient.getQueryData<Client[]>(['clients'])).toEqual(finalClients);
      expect(queryClient.getQueryData(['console-summary'])).toEqual({ marker: 'fresh-summary' });
      expect(queryClient.getQueryData(['server-status'])).toEqual({
        marker: 'fresh-status',
        summary: { marker: 'fresh-summary' },
      });
      expect(queryClient.getQueryState(['clients'])?.isInvalidated).toBe(false);
      expect(queryClient.getQueryState(['console-summary'])?.isInvalidated).toBe(false);
      expect(queryClient.getQueryState(['server-status'])?.isInvalidated).toBe(false);
      expect(statuses).toEqual(['connected']);
    } finally {
      globalThis.fetch = originalFetch;
      queryClient.clear();
    }
  });
  test('recovers activity gaps from the durable global cursor', async () => {
    const queryClient = new QueryClient();
    const snapshotState = createEventStreamSnapshotState();
    const activityState = createActivityRecoveryState();
    const activityKey = ['activity', 'global', null, 50, ['error', 'info', 'warning'], [], null, null] as const;
    queryClient.setQueryData(activityKey, { pages: [{ items: [], has_more: false, direction: 'before' }], pageParams: [undefined] });
    const originalFetch = globalThis.fetch;
    const calls: string[] = [];
    globalThis.fetch = (async (input) => {
      calls.push(String(input));
      return new Response(JSON.stringify({ items: [activity(12), activity(11)], next_cursor: 12, has_more: false, direction: 'after' }), {
        status: 200, headers: { 'Content-Type': 'application/json' },
      });
    }) as typeof fetch;
    try {
      applyEventForDiagnostics(queryClient, () => undefined, snapshotState, 'ready', JSON.stringify({ activity_cursor: 10 }), activityState);
      applyEventForDiagnostics(queryClient, () => undefined, snapshotState, 'activity_event', JSON.stringify(activity(12)), activityState);
      await flushAsyncWork();
      await flushAsyncWork();
      expect(calls).toHaveLength(1);
      expect(calls[0]).toContain('after=10');
      expect(activityState.lastScannedId).toBe(12);
      const cached = queryClient.getQueryData<{ pages: { items: ActivityItem[] }[] }>(activityKey);
      expect(cached?.pages[0].items.map((entry) => entry.id)).toEqual([12, 11]);
    } finally {
      globalThis.fetch = originalFetch;
      activityState.cancelled = true;
      queryClient.clear();
    }
  });

  test('invalid activity payload invalidates cache without advancing cursor', () => {
    const queryClient = new QueryClient();
    const snapshotState = createEventStreamSnapshotState();
    const activityState = createActivityRecoveryState();
    applyEventForDiagnostics(queryClient, () => undefined, snapshotState, 'ready', JSON.stringify({ activity_cursor: 7 }), activityState);
    applyEventForDiagnostics(queryClient, () => undefined, snapshotState, 'activity_event', '{"id":"bad"}', activityState);
    expect(activityState.lastScannedId).toBe(7);
    expect(activityState.targetId).toBe(7);
    activityState.cancelled = true;
    queryClient.clear();
  });

});
