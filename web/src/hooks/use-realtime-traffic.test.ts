import { describe, expect, test } from 'vitest';
import { QueryClient } from '@tanstack/react-query';

import { scopedRealtimeTrafficPath } from '@/lib/api';
import { buildClientTrafficQueryKey } from '@/hooks/use-client-traffic';
import {
  buildRealtimeTrafficQueryKey,
  REALTIME_TRAFFIC_FALLBACK_POLL_MS,
  resolveRealtimeTrafficRefetchInterval,
  syncRealtimeTrafficClientCaches,
} from '@/hooks/use-realtime-traffic';
import { adminUserResourceScope, SELF_RESOURCE_SCOPE } from '@/lib/resource-scope';
import type { ClientTrafficResponse, TrafficRealtimeEvent } from '@/types';

describe('useRealtimeTraffic helpers', () => {
  test('builds scoped batch realtime traffic paths and query keys', () => {
    expect(scopedRealtimeTrafficPath(SELF_RESOURCE_SCOPE)).toBe('/api/traffic/realtime');
    expect(scopedRealtimeTrafficPath(adminUserResourceScope('user-42'))).toBe(
      '/api/admin/users/user-42/traffic/realtime',
    );
    expect(buildRealtimeTrafficQueryKey(SELF_RESOURCE_SCOPE)).toEqual([
      'users',
      'self',
      'realtime-traffic',
    ]);
  });

  test('disables HTTP polling while SSE is connected and falls back when degraded', () => {
    expect(resolveRealtimeTrafficRefetchInterval(false, 'disconnected')).toBe(false);
    expect(resolveRealtimeTrafficRefetchInterval(true, 'connected')).toBe(false);
    expect(resolveRealtimeTrafficRefetchInterval(true, 'connecting')).toBe(REALTIME_TRAFFIC_FALLBACK_POLL_MS);
    expect(resolveRealtimeTrafficRefetchInterval(true, 'reconnecting')).toBe(REALTIME_TRAFFIC_FALLBACK_POLL_MS);
    expect(resolveRealtimeTrafficRefetchInterval(true, 'disconnected')).toBe(REALTIME_TRAFFIC_FALLBACK_POLL_MS);
  });

  test('hydrates per-client and filtered 60s traffic caches from batch payload', () => {
    const queryClient = new QueryClient();
    const filteredKey = buildClientTrafficQueryKey(SELF_RESOURCE_SCOPE, 'client-1', '60s', { tunnel: 'ssh' });
    queryClient.setQueryData<ClientTrafficResponse>(filteredKey, { resolution: 'second', items: [] });

    const payload: TrafficRealtimeEvent = {
      generated_at: '2026-11-11T10:00:00Z',
      clients: [
        {
          client_id: 'client-1',
          resolution: 'second',
          items: [
            {
              tunnel_id: 't-1',
              tunnel_name: 'ssh',
              tunnel_type: 'tcp',
              points: [{ bucket_start: '2026-11-11T09:59:59Z', ingress_bytes: 100, egress_bytes: 50, total_bytes: 150 }],
            },
            {
              tunnel_id: 't-2',
              tunnel_name: 'web',
              tunnel_type: 'http',
              points: [{ bucket_start: '2026-11-11T09:59:59Z', ingress_bytes: 200, egress_bytes: 80, total_bytes: 280 }],
            },
          ],
        },
      ],
    };

    syncRealtimeTrafficClientCaches(queryClient, SELF_RESOURCE_SCOPE, payload);

    const fullCache = queryClient.getQueryData<ClientTrafficResponse>(
      buildClientTrafficQueryKey(SELF_RESOURCE_SCOPE, 'client-1', '60s'),
    );
    expect(fullCache?.items).toHaveLength(2);

    const filteredCache = queryClient.getQueryData<ClientTrafficResponse>(filteredKey);
    expect(filteredCache?.items.map((item) => item.tunnel_name)).toEqual(['ssh']);
  });
});
