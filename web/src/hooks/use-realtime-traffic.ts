import { useQuery, useQueryClient } from '@tanstack/react-query';
import type { QueryClient } from '@tanstack/react-query';
import { api, scopedRealtimeTrafficPath } from '@/lib/api';
import { buildClientTrafficQueryKey } from '@/hooks/use-client-traffic';
import { scopedQueryKey, type ResourceScope } from '@/lib/resource-scope';
import { useConnectionStore, type ConnectionStatus } from '@/stores/connection-store';
import type { ClientTrafficResponse, TrafficRealtimeEvent } from '@/types';

export const REALTIME_TRAFFIC_FALLBACK_POLL_MS = 10_000;

export function buildRealtimeTrafficQueryKey(scope: ResourceScope) {
  return scopedQueryKey(scope, 'realtime-traffic');
}

export function resolveRealtimeTrafficRefetchInterval(
  enabled: boolean,
  connectionStatus: ConnectionStatus,
): number | false {
  if (!enabled) {
    return false;
  }
  return connectionStatus === 'connected' ? false : REALTIME_TRAFFIC_FALLBACK_POLL_MS;
}

export function syncRealtimeTrafficClientCaches(
  queryClient: QueryClient,
  scope: ResourceScope,
  event: TrafficRealtimeEvent,
) {
  for (const client of event.clients) {
    const traffic: ClientTrafficResponse = {
      resolution: client.resolution,
      items: client.items ?? [],
    };
    queryClient.setQueryData<ClientTrafficResponse>(
      buildClientTrafficQueryKey(scope, client.client_id, '60s'),
      traffic,
    );

    const realtimeQueries = queryClient.getQueryCache().findAll({
      queryKey: scopedQueryKey(scope, 'client-traffic', client.client_id, '60s'),
    });
    for (const query of realtimeQueries) {
      const tunnelName = typeof query.queryKey[5] === 'string' ? query.queryKey[5] : '';
      queryClient.setQueryData<ClientTrafficResponse>(
        query.queryKey,
        tunnelName
          ? { ...traffic, items: traffic.items.filter((item) => item.tunnel_name === tunnelName) }
          : traffic,
      );
    }
  }
}

export function useRealtimeTraffic(
  scope: ResourceScope,
  options: { enabled?: boolean } = {},
) {
  const enabled = options.enabled ?? true;
  const queryClient = useQueryClient();
  const connectionStatus = useConnectionStore((state) => state.status);

  return useQuery({
    queryKey: buildRealtimeTrafficQueryKey(scope),
    enabled,
    queryFn: async () => {
      const event = await api.get<TrafficRealtimeEvent>(scopedRealtimeTrafficPath(scope));
      syncRealtimeTrafficClientCaches(queryClient, scope, event);
      return event;
    },
    staleTime: 30_000,
    refetchInterval: resolveRealtimeTrafficRefetchInterval(enabled, connectionStatus),
    refetchOnWindowFocus: false,
  });
}
