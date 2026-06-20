import { describe, expect, test } from 'bun:test';
import { QueryClient } from '@tanstack/react-query';

import type { Client, ProxyConfig } from '@/types';

import { applyEventForDiagnostics, createEventStreamSnapshotState } from './use-event-stream';

function createDeferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

function createTunnel(runtimeState: ProxyConfig['runtime_state']): ProxyConfig {
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
    },
  };
}

function createClient(runtimeState: ProxyConfig['runtime_state']): Client {
  return {
    id: 'client-1',
    ingress_bps: 0,
    egress_bps: 0,
    info: {
      hostname: 'client-1',
      os: 'linux',
      arch: 'amd64',
      ip: '127.0.0.1',
      version: 'v0.1.0',
    },
    stats: null,
    proxies: [createTunnel(runtimeState)],
    online: true,
  };
}

function tunnelChangedEvent(runtimeState: ProxyConfig['runtime_state'], action: string) {
  return JSON.stringify({
    client_id: 'client-1',
    action,
    tunnel: createTunnel(runtimeState),
  });
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

describe('use-event-stream diagnostics', () => {
  test('keeps newer tunnel_changed state when an older console snapshot resolves later', async () => {
    const queryClient = new QueryClient();
    queryClient.setQueryData<Client[]>(['clients'], [createClient('pending')]);

    const originalFetch = globalThis.fetch;
    const requests: ReturnType<typeof createDeferred<Response>>[] = [];
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
});
