import { useQuery } from '@tanstack/react-query';
import { api } from '@/lib/api';
import type { ClientTrafficRange, ClientTrafficResponse, TrafficResolution } from '@/types';

export interface UseClientTrafficOptions {
  tunnel?: string;
}

const TRAFFIC_RANGE_CONFIG: Record<
  ClientTrafficRange,
  { durationHours: number; resolution: TrafficResolution; refetchInterval: number }
> = {
  '1h': {
    durationHours: 1,
    resolution: 'minute',
    refetchInterval: 30_000,
  },
  '24h': {
    durationHours: 24,
    resolution: 'minute',
    refetchInterval: 60_000,
  },
  '7d': {
    durationHours: 7 * 24,
    resolution: 'hour',
    refetchInterval: 5 * 60_000,
  },
};

export function buildClientTrafficQueryKey(
  clientId: string | undefined,
  range: ClientTrafficRange,
  options: UseClientTrafficOptions = {},
) {
  return ['client-traffic', clientId, range, options.tunnel ?? ''] as const;
}

export function buildClientTrafficUrl(
  clientId: string,
  range: ClientTrafficRange,
  options: UseClientTrafficOptions = {},
  nowSeconds = Math.floor(Date.now() / 1000),
) {
  const config = TRAFFIC_RANGE_CONFIG[range];
  const params = new URLSearchParams({
    from: String(nowSeconds - config.durationHours * 60 * 60),
    to: String(nowSeconds),
    resolution: config.resolution,
  });

  if (options.tunnel) {
    params.set('tunnel', options.tunnel);
  }

  return `/api/clients/${encodeURIComponent(clientId)}/traffic?${params.toString()}`;
}

export function useClientTraffic(
  clientId: string | undefined,
  range: ClientTrafficRange,
  options: UseClientTrafficOptions = {},
) {
  const config = TRAFFIC_RANGE_CONFIG[range];

  return useQuery({
    queryKey: buildClientTrafficQueryKey(clientId, range, options),
    enabled: Boolean(clientId),
    queryFn: async () => {
      if (!clientId) {
        throw new Error('clientId is required to load traffic data');
      }

      return api.get<ClientTrafficResponse>(buildClientTrafficUrl(clientId, range, options));
    },
    staleTime: 30_000,
    refetchInterval: config.refetchInterval,
    refetchOnWindowFocus: false,
  });
}
