import { useEffect } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { useRouterState } from '@tanstack/react-router';
import { api } from '@/lib/api';
import { EMPTY_CONSOLE_SUMMARY } from '@/lib/console-summary';
import { useConnectionStore } from '@/stores/connection-store';
import type { ConnectionStatus } from '@/stores/connection-store';
import { useAuthStore } from '@/stores/auth-store';
import { buildClientTrafficQueryKey } from '@/hooks/use-client-traffic';
import type {
  Client,
  ClientOfflineEvent,
  ClientOnlineEvent,
  ClientTrafficResponse,
  ConsoleSnapshot,
  ConsoleSummary,
  ProxyConfig,
  ServerStatus,
  StatsUpdateEvent,
  TunnelChangedEvent,
  TrafficRealtimeEvent,
} from '@/types';

type EventStreamQueryClient = ReturnType<typeof useQueryClient>;
type JsonObject = Record<string, unknown>;

const consoleSummaryFields = [
  'total_clients',
  'online_clients',
  'offline_clients',
  'total_tunnels',
  'active_tunnels',
  'inactive_tunnels',
  'pending_tunnels',
  'offline_tunnels',
  'stopped_tunnels',
  'error_tunnels',
] as const satisfies readonly (keyof ConsoleSummary)[];
const serverStatusStringFields = ['status', 'version', 'server_addr', 'os_arch', 'go_version', 'hostname', 'ip_address'] as const satisfies readonly (keyof ServerStatus)[];
const serverStatusNumberFields = [
  'client_count',
  'listen_port',
  'uptime',
  'system_uptime',
  'tunnel_active',
  'tunnel_stopped',
  'cpu_usage',
  'cpu_cores',
  'mem_used',
] as const satisfies readonly (keyof ServerStatus)[];

function isRecord(value: unknown): value is JsonObject {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function isNonEmptyString(value: unknown): value is string {
  return typeof value === 'string' && value.length > 0;
}

function isConsoleSummary(value: unknown): value is ConsoleSummary {
  return isRecord(value) && consoleSummaryFields.every((field) => typeof value[field] === 'number');
}

function isServerStatus(value: unknown): value is ServerStatus {
  if (!isRecord(value)) {
    return false;
  }
  if (!serverStatusStringFields.every((field) => typeof value[field] === 'string')) {
    return false;
  }
  if (!serverStatusNumberFields.every((field) => typeof value[field] === 'number')) {
    return false;
  }
  if (!Array.isArray(value.allowed_ports)) {
    return false;
  }
  return value.summary === undefined || isConsoleSummary(value.summary);
}

function isClient(value: unknown): value is Client {
  return (
    isRecord(value) &&
    isNonEmptyString(value.id) &&
    typeof value.ingress_bps === 'number' &&
    typeof value.egress_bps === 'number' &&
    isRecord(value.info) &&
    (value.stats === undefined || value.stats === null || isRecord(value.stats)) &&
    (value.proxies === undefined || Array.isArray(value.proxies)) &&
    typeof value.online === 'boolean'
  );
}

function isConsoleSnapshot(value: unknown): value is ConsoleSnapshot {
  return (
    isRecord(value) &&
    (value.clients === undefined || (Array.isArray(value.clients) && value.clients.every(isClient))) &&
    (value.summary === undefined || isConsoleSummary(value.summary)) &&
    (value.server_status === undefined || isServerStatus(value.server_status))
  );
}

function isStatsUpdateEvent(value: unknown): value is StatsUpdateEvent {
  return (
    isRecord(value) &&
    isNonEmptyString(value.client_id) &&
    isRecord(value.stats)
  );
}

function isTrafficResolution(value: unknown): value is ClientTrafficResponse['resolution'] {
  return value === 'second' || value === 'minute' || value === 'hour';
}

function isTrafficPoint(value: unknown): boolean {
  return (
    isRecord(value) &&
    typeof value.bucket_start === 'string' &&
    typeof value.ingress_bytes === 'number' &&
    typeof value.egress_bytes === 'number' &&
    typeof value.total_bytes === 'number'
  );
}

function isTunnelTrafficSeries(value: unknown): boolean {
  return isRecord(value) && Array.isArray(value.points) && value.points.every(isTrafficPoint);
}

function isTrafficRealtimeClient(value: unknown): value is TrafficRealtimeEvent['clients'][number] {
  return (
    isRecord(value) &&
    isNonEmptyString(value.client_id) &&
    isTrafficResolution(value.resolution) &&
    Array.isArray(value.items) &&
    value.items.every(isTunnelTrafficSeries)
  );
}

function isTrafficRealtimeEvent(value: unknown): value is TrafficRealtimeEvent {
  return (
    isRecord(value) &&
    (value.generated_at === undefined || typeof value.generated_at === 'string') &&
    Array.isArray(value.clients) &&
    value.clients.every(isTrafficRealtimeClient)
  );
}

function isClientOnlineEvent(value: unknown): value is ClientOnlineEvent {
  return isRecord(value) && isNonEmptyString(value.client_id) && isRecord(value.info);
}

function isClientOfflineEvent(value: unknown): value is ClientOfflineEvent {
  return isRecord(value) && isNonEmptyString(value.client_id);
}

function isProxyConfig(value: unknown): value is ProxyConfig {
  return (
    isRecord(value) &&
    isNonEmptyString(value.id) &&
    isNonEmptyString(value.name) &&
    isNonEmptyString(value.type) &&
    typeof value.local_ip === 'string' &&
    typeof value.local_port === 'number' &&
    typeof value.remote_port === 'number' &&
    typeof value.domain === 'string' &&
    isNonEmptyString(value.client_id) &&
    typeof value.ingress_bps === 'number' &&
    typeof value.egress_bps === 'number' &&
    typeof value.created_at === 'string' &&
    isNonEmptyString(value.desired_state) &&
    isNonEmptyString(value.runtime_state) &&
    (value.capabilities === undefined || isRecord(value.capabilities))
  );
}

function isTunnelChangedEvent(value: unknown): value is TunnelChangedEvent {
  return (
    isRecord(value) &&
    isNonEmptyString(value.client_id) &&
    (value.action === undefined || typeof value.action === 'string') &&
    isProxyConfig(value.tunnel)
  );
}

function parseEventPayload<T>(data: string, guard: (value: unknown) => value is T): T | null {
  try {
    const parsed: unknown = JSON.parse(data);
    return guard(parsed) ? parsed : null;
  } catch {
    return null;
  }
}

function snapshotSummary(snapshot: ConsoleSnapshot): ConsoleSummary {
  return snapshot.summary ?? snapshot.server_status?.summary ?? EMPTY_CONSOLE_SUMMARY;
}

function applyConsoleSnapshot(queryClient: EventStreamQueryClient, snapshot: ConsoleSnapshot) {
  const summary = snapshotSummary(snapshot);
  if (Array.isArray(snapshot.clients)) {
    queryClient.setQueryData<Client[]>(['clients'], snapshot.clients);
  }
  queryClient.setQueryData<ConsoleSummary>(['console-summary'], summary);
  if (snapshot.server_status) {
    queryClient.setQueryData<ServerStatus>(['server-status'], {
      ...snapshot.server_status,
      summary,
    });
  }
}

async function resyncConsoleSnapshot(queryClient: EventStreamQueryClient) {
  const snapshot = await api.get<ConsoleSnapshot>('/api/console/snapshot');
  applyConsoleSnapshot(queryClient, snapshot);
}

function invalidateConsoleSnapshotQueries(queryClient: EventStreamQueryClient) {
  queryClient.invalidateQueries({ queryKey: ['clients'] });
  queryClient.invalidateQueries({ queryKey: ['console-summary'] });
  queryClient.invalidateQueries({ queryKey: ['server-status'] });
}

function resyncConsoleSnapshotSafely(queryClient: EventStreamQueryClient, setStatus?: (status: ConnectionStatus) => void) {
  return resyncConsoleSnapshot(queryClient)
    .then(() => {
      setStatus?.('connected');
    })
    .catch((error) => {
      console.warn('Failed to resync console snapshot:', error);
      invalidateConsoleSnapshotQueries(queryClient);
      setStatus?.('reconnecting');
    });
}

function applyRealtimeTraffic(queryClient: EventStreamQueryClient, client: TrafficRealtimeEvent['clients'][number]) {
  const traffic: ClientTrafficResponse = {
    resolution: client.resolution,
    items: client.items ?? [],
  };
  const baseKey = buildClientTrafficQueryKey(client.client_id, '60s');
  queryClient.setQueryData<ClientTrafficResponse>(baseKey, traffic);

  const realtimeQueries = queryClient.getQueryCache().findAll({
    queryKey: ['client-traffic', client.client_id, '60s'],
  });
  for (const query of realtimeQueries) {
    const tunnelName = typeof query.queryKey[3] === 'string' ? query.queryKey[3] : '';
    queryClient.setQueryData<ClientTrafficResponse>(
      query.queryKey,
      tunnelName
        ? { ...traffic, items: traffic.items.filter((item) => item.tunnel_name === tunnelName) }
        : traffic,
    );
  }
}

function getTunnelChangedClientIds(event: TunnelChangedEvent) {
  return Array.from(new Set([
    event.client_id,
    event.tunnel.owner_client_id,
    event.tunnel.ingress?.client_id,
    event.tunnel.target?.client_id,
    event.tunnel.client_id,
  ].filter((clientId): clientId is string => Boolean(clientId))));
}

function applyEvent(queryClient: EventStreamQueryClient, setStatus: (status: ConnectionStatus) => void, eventType: string, data: string) {
  switch (eventType) {
    case 'snapshot': {
      const parsed = parseEventPayload(data, isConsoleSnapshot);
      if (parsed) {
        applyConsoleSnapshot(queryClient, parsed);
      }
      return;
    }
    case 'stats_update': {
      const parsed = parseEventPayload(data, isStatsUpdateEvent);
      if (parsed) {
        queryClient.setQueryData<Client[]>(['clients'], (old) =>
          old?.map((client) =>
            client.id === parsed.client_id ? { ...client, stats: parsed.stats } : client,
          ),
        );
      }
      return;
    }
    case 'traffic_realtime': {
      const parsed = parseEventPayload(data, isTrafficRealtimeEvent);
      if (!parsed) {
        return;
      }
      for (const client of parsed.clients) {
        applyRealtimeTraffic(queryClient, client);
      }
      return;
    }
    case 'client_online':
      {
        const parsed = parseEventPayload(data, isClientOnlineEvent);
        if (!parsed) {
          queryClient.invalidateQueries({ queryKey: ['clients'] });
          return;
        }
        const info = parsed.info as Client['info'];
        queryClient.setQueryData<Client[]>(['clients'], (old) => {
          const base = old ?? [];
          const exists = base.some((client) => client.id === parsed.client_id);
          if (!exists) {
            return [
              ...base,
              {
                id: parsed.client_id,
                ingress_bps: 0,
                egress_bps: 0,
                info,
                stats: null,
                proxies: [],
                online: true,
              },
            ];
          }
          return base.map((client) =>
            client.id === parsed.client_id ? { ...client, info, online: true } : client,
          );
        });
        void resyncConsoleSnapshotSafely(queryClient, setStatus);
      }
      return;
    case 'client_offline':
      {
        const parsed = parseEventPayload(data, isClientOfflineEvent);
        if (!parsed) {
          queryClient.invalidateQueries({ queryKey: ['clients'] });
          return;
        }
        queryClient.setQueryData<Client[]>(['clients'], (old) =>
          old?.map((client) =>
            client.id === parsed.client_id ? { ...client, online: false } : client,
          ),
        );
        void resyncConsoleSnapshotSafely(queryClient, setStatus);
      }
      return;
    case 'tunnel_changed':
      {
        const parsed = parseEventPayload(data, isTunnelChangedEvent);
        if (!parsed) {
          queryClient.invalidateQueries({ queryKey: ['clients'] });
          return;
        }
        const relatedClientIds = getTunnelChangedClientIds(parsed);
        queryClient.setQueryData<Client[]>(['clients'], (old) =>
          old?.map((client) => {
            if (!relatedClientIds.includes(client.id)) {
              return client;
            }

            const proxies = client.proxies ?? [];
            if (parsed.action === 'deleted') {
              return {
                ...client,
                proxies: proxies.filter((proxy) => proxy.id !== parsed.tunnel.id),
              };
            }

            const existingIndex = proxies.findIndex((proxy) => proxy.id === parsed.tunnel.id);
            if (existingIndex === -1) {
              return {
                ...client,
                proxies: [...proxies, parsed.tunnel],
              };
            }

            const nextProxies = [...proxies];
            nextProxies[existingIndex] = parsed.tunnel;
            return {
              ...client,
              proxies: nextProxies,
            };
          }),
        );
        queryClient.invalidateQueries({ queryKey: ['client-tunnels'] });
        queryClient.invalidateQueries({ queryKey: ['client-traffic'] });
        void resyncConsoleSnapshotSafely(queryClient, setStatus);
      }
      return;
    default:
      return;
  }
}

function parseSSE(buffer: string, onEvent: (eventType: string, data: string) => void): string {
  let remaining = buffer;

  while (true) {
    const delimiterIndex = remaining.indexOf('\n\n');
    if (delimiterIndex === -1) {
      break;
    }

    const rawEvent = remaining.slice(0, delimiterIndex);
    remaining = remaining.slice(delimiterIndex + 2);

    let eventType = 'message';
    const dataLines: string[] = [];

    for (const line of rawEvent.split('\n')) {
      if (line.startsWith('event:')) {
        eventType = line.slice(6).trim();
      }
      if (line.startsWith('data:')) {
        dataLines.push(line.slice(5).trim());
      }
    }

    if (dataLines.length > 0) {
      onEvent(eventType, dataLines.join('\n'));
    }
  }

  return remaining;
}

export function useEventStream() {
  const queryClient = useQueryClient();
  const setStatus = useConnectionStore((state) => state.setStatus);
  const isAuthenticated = useAuthStore((state) => state.isAuthenticated);
  const pathname = useRouterState({ select: (state) => state.location.pathname });
  const shouldConnect = isAuthenticated && pathname !== '/login';

  useEffect(() => {
    if (!shouldConnect) {
      setStatus('disconnected');
      return;
    }

    let cancelled = false;
    let activeController: AbortController | null = null;
    let hasConnected = false;

    const connect = async () => {
      while (!cancelled) {
        activeController = new AbortController();

        try {
          const isReconnect = hasConnected;
          setStatus(hasConnected ? 'reconnecting' : 'connecting');

          const response = await fetch('/api/events', {
            method: 'GET',
            headers: {
              Accept: 'text/event-stream',
            },
            credentials: 'same-origin',
            signal: activeController.signal,
          });

          if (response.status === 401) {
            useAuthStore.getState().logout();
            window.location.hash = '#/login';
            setStatus('disconnected');
            return;
          }

          if (!response.ok || !response.body) {
            throw new Error(`event stream failed: ${response.status}`);
          }

          if (isReconnect) {
            await resyncConsoleSnapshotSafely(queryClient, setStatus);
          }

          hasConnected = true;
          setStatus('connected');

          const reader = response.body.getReader();
          const decoder = new TextDecoder();
          let buffer = '';

          while (!cancelled) {
            const { done, value } = await reader.read();
            if (done) {
              throw new Error('event stream closed');
            }

            buffer += decoder.decode(value, { stream: true });
            buffer = parseSSE(buffer, (eventType, data) => applyEvent(queryClient, setStatus, eventType, data));
          }
        } catch (error) {
          if (cancelled) {
            return;
          }
          if (error instanceof DOMException && error.name === 'AbortError') {
            return;
          }

          setStatus(hasConnected ? 'reconnecting' : 'connecting');
          await new Promise((resolve) => window.setTimeout(resolve, 1000));
        }
      }
    };

    void connect();

    return () => {
      cancelled = true;
      activeController?.abort();
      setStatus('disconnected');
    };
  }, [queryClient, setStatus, shouldConnect]);
}
