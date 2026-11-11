import type { QueryClient } from '@tanstack/react-query';
import { api, scopedConsoleSnapshotPath } from '@/lib/api';
import type { ConnectionStatus } from '@/stores/connection-store';
import { resourceScopeKey, scopedQueryKey, type ResourceScope } from '@/lib/resource-scope';
import type { Client, ConsoleSnapshot, ConsoleSummary, ResourceBootstrap } from '@/types';
import { isConsoleSnapshot } from './guards';

export interface EventStreamDiagnostics {
  eventType: string;
  action?: string;
  clientId?: string;
  tunnelId?: string;
  tunnelName?: string;
  runtimeState?: string;
  desiredState?: string;
  snapshotRequestId?: number;
  snapshotGeneratedAt?: string;
  tunnels?: string[];
}

export interface EventStreamSnapshotState {
  requestSeq: number;
  appliedGeneratedAt?: number;
}

export function createEventStreamSnapshotState(): EventStreamSnapshotState {
  return { requestSeq: 0 };
}

function isEventStreamDebugEnabled() {
  try {
    return localStorage.getItem('netsgo:debug:event-stream') === '1';
  } catch {
    return false;
  }
}

export function logEventStreamDiagnostic(stage: string, diagnostic: EventStreamDiagnostics) {
  if (!isEventStreamDebugEnabled()) {
    return;
  }
  console.debug('[netsgo:event-stream]', stage, diagnostic);
}

function summarizeClientTunnelStates(clients: Client[] | undefined) {
  return clients?.flatMap((client) =>
    (client.proxies ?? []).map((proxy) => `${client.id}/${proxy.name}:${proxy.runtime_state}`),
  );
}

function snapshotGeneratedAtMillis(snapshot: ConsoleSnapshot) {
  if (!snapshot.generated_at) {
    return undefined;
  }
  const generatedAt = Date.parse(snapshot.generated_at);
  return Number.isFinite(generatedAt) ? generatedAt : undefined;
}

export function snapshotDiagnostic(
  eventType: string,
  snapshot: ConsoleSnapshot,
  snapshotRequestId?: number,
): EventStreamDiagnostics {
  return {
    eventType,
    clientId: snapshot.clients?.[0]?.id,
    snapshotRequestId,
    snapshotGeneratedAt: snapshot.generated_at,
    tunnels: summarizeClientTunnelStates(snapshot.clients),
  };
}

export function applyConsoleSnapshot(
  queryClient: QueryClient,
  scope: ResourceScope,
  snapshotState: EventStreamSnapshotState,
  snapshot: ConsoleSnapshot,
) {
  const generatedAt = snapshotGeneratedAtMillis(snapshot);
  if (generatedAt !== undefined) {
    const appliedGeneratedAt = snapshotState.appliedGeneratedAt;
    if (appliedGeneratedAt !== undefined && generatedAt < appliedGeneratedAt) {
      return false;
    }
    snapshotState.appliedGeneratedAt = generatedAt;
  }

  queryClient.setQueryData<Client[]>(scopedQueryKey(scope, 'clients'), snapshot.clients);
  queryClient.setQueryData<ConsoleSummary>(scopedQueryKey(scope, 'console-summary'), snapshot.summary);
  queryClient.setQueryData<ResourceBootstrap>(scopedQueryKey(scope, 'resource-bootstrap'), snapshot.bootstrap);
  return true;
}

export async function resyncConsoleSnapshot(
  queryClient: QueryClient,
  scope: ResourceScope,
  snapshotState: EventStreamSnapshotState,
) {
  const snapshotRequestId = ++snapshotState.requestSeq;
  logEventStreamDiagnostic('snapshot_request_start', { eventType: 'snapshot_request', snapshotRequestId });
  let snapshot: ConsoleSnapshot;
  try {
    const response = await api.get<unknown>(scopedConsoleSnapshotPath(scope));
    if (!isConsoleSnapshot(response)) {
      throw new Error('invalid console snapshot response');
    }
    snapshot = response;
  } catch (error) {
    if (snapshotRequestId !== snapshotState.requestSeq) {
      logEventStreamDiagnostic('snapshot_request_stale', { eventType: 'snapshot_request', snapshotRequestId });
      return false;
    }
    throw error;
  }
  const diagnostic = snapshotDiagnostic('snapshot_request', snapshot, snapshotRequestId);
  if (snapshotRequestId !== snapshotState.requestSeq) {
    logEventStreamDiagnostic('snapshot_request_stale', diagnostic);
    return false;
  }
  const applied = applyConsoleSnapshot(queryClient, scope, snapshotState, snapshot);
  logEventStreamDiagnostic(applied ? 'snapshot_request_apply' : 'snapshot_request_stale', diagnostic);
  return applied;
}

function invalidateConsoleSnapshotQueries(queryClient: QueryClient, scope: ResourceScope) {
  queryClient.invalidateQueries({ queryKey: ['users', resourceScopeKey(scope)] });
}

export function resyncConsoleSnapshotSafely(
  queryClient: QueryClient,
  scope: ResourceScope,
  snapshotState: EventStreamSnapshotState,
  setStatus?: (status: ConnectionStatus) => void,
) {
  return resyncConsoleSnapshot(queryClient, scope, snapshotState)
    .then((applied) => {
      if (applied) {
        setStatus?.('connected');
      }
    })
    .catch((error) => {
      console.warn('Failed to resync console snapshot:', error);
      invalidateConsoleSnapshotQueries(queryClient, scope);
      setStatus?.('reconnecting');
    });
}
