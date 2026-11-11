import type { QueryClient } from '@tanstack/react-query';
import type { ActivityReadScope } from '@/lib/api';
import { buildRealtimeTrafficQueryKey, syncRealtimeTrafficClientCaches } from '@/hooks/use-realtime-traffic';
import { resourceScopeKey, scopedQueryKey, SELF_RESOURCE_SCOPE } from '@/lib/resource-scope';
import type { ConnectionStatus } from '@/stores/connection-store';
import type { Client, TrafficRealtimeEvent, TunnelChangedEvent } from '@/types';
import {
  applyActivityHint,
  applyActivityReady,
  invalidateActivityQueries,
  type ActivityRecoveryState,
} from './activity-recovery';
import {
  isActivityItem,
  isActivityReadyPayload,
  isClientOfflineEvent,
  isClientOnlineEvent,
  isConsoleSnapshot,
  isStatsUpdateEvent,
  isTrafficRealtimeEvent,
  isTunnelChangedEvent,
  isUserListChangedEvent,
  isWebhookChangedEvent,
  isWebhookDeliveryChangedEvent,
  parseEventPayload,
} from './guards';
import {
  applyConsoleSnapshot,
  logEventStreamDiagnostic,
  resyncConsoleSnapshotSafely,
  snapshotDiagnostic,
  type EventStreamSnapshotState,
} from './snapshot';

function getTunnelChangedClientIds(event: TunnelChangedEvent) {
  return Array.from(new Set([
    event.client_id,
    event.tunnel.owner_client_id,
    event.tunnel.ingress?.client_id,
    event.tunnel.target?.client_id,
    event.tunnel.client_id,
  ].filter((clientId): clientId is string => Boolean(clientId))));
}

export function applyEventForDiagnostics(
  queryClient: QueryClient,
  setStatus: (status: ConnectionStatus) => void,
  snapshotState: EventStreamSnapshotState,
  eventType: string,
  data: string,
  activityState?: ActivityRecoveryState,
  readScope: ActivityReadScope = SELF_RESOURCE_SCOPE,
) {
  const resourceScope = readScope.kind === 'admin-global' ? null : readScope;
  switch (eventType) {
    case 'ready': {
      if (!activityState) return;
      const parsed = parseEventPayload(data, isActivityReadyPayload);
      if (!parsed) {
        invalidateActivityQueries(queryClient, readScope);
        return;
      }
      applyActivityReady(queryClient, readScope, activityState, parsed);
      return;
    }
    case 'activity_event': {
      if (!activityState) return;
      const parsed = parseEventPayload(data, isActivityItem);
      if (!parsed) {
        invalidateActivityQueries(queryClient, readScope);
        return;
      }
      applyActivityHint(queryClient, readScope, activityState, parsed);
      return;
    }
    case 'user_list_changed': {
      if (readScope.kind !== 'admin-global') return;
      void queryClient.invalidateQueries({ queryKey: ['admin-users'] });
      const parsed = parseEventPayload(data, isUserListChangedEvent);
      void queryClient.invalidateQueries({
        queryKey: parsed ? ['admin-user', parsed.user_id] : ['admin-user'],
      });
      return;
    }
    case 'webhook_changed': {
      if (!parseEventPayload(data, isWebhookChangedEvent)) return;
      void queryClient.invalidateQueries({ queryKey: ['webhooks'] });
      return;
    }
    case 'webhook_delivery_changed': {
      const parsed = parseEventPayload(data, isWebhookDeliveryChangedEvent);
      if (!parsed) return;
      void queryClient.invalidateQueries({ queryKey: ['webhook-deliveries', parsed.webhook_id] });
      void queryClient.invalidateQueries({ queryKey: ['webhook-delivery', parsed.delivery_id] });
      void queryClient.invalidateQueries({ queryKey: ['webhooks'] });
      return;
    }
    case 'snapshot': {
      if (!resourceScope) return;
      const parsed = parseEventPayload(data, isConsoleSnapshot);
      if (parsed) {
        const applied = applyConsoleSnapshot(queryClient, resourceScope, snapshotState, parsed);
        logEventStreamDiagnostic(applied ? 'sse_snapshot_apply' : 'sse_snapshot_stale', snapshotDiagnostic(eventType, parsed));
      }
      return;
    }
    case 'stats_update': {
      if (!resourceScope) return;
      const parsed = parseEventPayload(data, isStatsUpdateEvent);
      if (parsed) {
        queryClient.setQueryData<Client[]>(scopedQueryKey(resourceScope, 'clients'), (old) =>
          old?.map((client) =>
            client.id === parsed.client_id ? { ...client, stats: parsed.stats } : client,
          ),
        );
      }
      return;
    }
    case 'traffic_realtime': {
      if (!resourceScope) return;
      const parsed = parseEventPayload(data, isTrafficRealtimeEvent);
      if (!parsed) {
        return;
      }
      queryClient.setQueryData<TrafficRealtimeEvent>(buildRealtimeTrafficQueryKey(resourceScope), parsed);
      syncRealtimeTrafficClientCaches(queryClient, resourceScope, parsed);
      return;
    }
    case 'client_online': {
      if (!resourceScope) return;
      const parsed = parseEventPayload(data, isClientOnlineEvent);
      if (!parsed) {
        queryClient.invalidateQueries({ queryKey: scopedQueryKey(resourceScope, 'clients') });
        return;
      }
      const info = parsed.info as Client['info'];
      queryClient.setQueryData<Client[]>(scopedQueryKey(resourceScope, 'clients'), (old) => {
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
      void resyncConsoleSnapshotSafely(queryClient, resourceScope, snapshotState, setStatus);
      return;
    }
    case 'client_offline': {
      if (!resourceScope) return;
      const parsed = parseEventPayload(data, isClientOfflineEvent);
      if (!parsed) {
        queryClient.invalidateQueries({ queryKey: scopedQueryKey(resourceScope, 'clients') });
        return;
      }
      queryClient.setQueryData<Client[]>(scopedQueryKey(resourceScope, 'clients'), (old) =>
        old?.map((client) =>
          client.id === parsed.client_id ? { ...client, online: false } : client,
        ),
      );
      void resyncConsoleSnapshotSafely(queryClient, resourceScope, snapshotState, setStatus);
      return;
    }
    case 'tunnel_changed': {
      if (!resourceScope) return;
      const parsed = parseEventPayload(data, isTunnelChangedEvent);
      if (!parsed) {
        logEventStreamDiagnostic('tunnel_changed_invalid', { eventType });
        queryClient.invalidateQueries({ queryKey: scopedQueryKey(resourceScope, 'clients') });
        return;
      }
      logEventStreamDiagnostic('tunnel_changed_apply', {
        eventType,
        action: parsed.action,
        clientId: parsed.client_id,
        tunnelId: parsed.tunnel.id,
        tunnelName: parsed.tunnel.name,
        runtimeState: parsed.tunnel.runtime_state,
        desiredState: parsed.tunnel.desired_state,
      });
      const migratedOut = parsed.action === 'migrated_out';
      const relatedClientIds = migratedOut ? [parsed.client_id] : getTunnelChangedClientIds(parsed);
      queryClient.setQueryData<Client[]>(scopedQueryKey(resourceScope, 'clients'), (old) =>
        old?.map((client) => {
          if (!relatedClientIds.includes(client.id)) {
            return client;
          }

          const proxies = client.proxies ?? [];
          if (parsed.action === 'deleted' || migratedOut) {
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
      queryClient.invalidateQueries({ queryKey: ['users', resourceScopeKey(resourceScope), 'client-tunnels'] });
      queryClient.invalidateQueries({ queryKey: ['users', resourceScopeKey(resourceScope), 'client-traffic'] });
      queryClient.invalidateQueries({ queryKey: buildRealtimeTrafficQueryKey(resourceScope) });
      void resyncConsoleSnapshotSafely(queryClient, resourceScope, snapshotState, setStatus);
      return;
    }
    default:
      return;
  }
}
