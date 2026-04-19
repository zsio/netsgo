import { useQuery } from '@tanstack/react-query';
import { api } from '@/lib/api';
import type { ClientTrafficRange, ClientTrafficResponse, TrafficResolution } from '@/types';

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

export function useClientTraffic(clientId: string | undefined, range: ClientTrafficRange) {
  const config = TRAFFIC_RANGE_CONFIG[range];

  return useQuery({
    queryKey: ['client-traffic', clientId, range],
    enabled: Boolean(clientId),
    queryFn: async () => {
      const to = Math.floor(Date.now() / 1000);
      const from = to - config.durationHours * 60 * 60;
      return api.get<ClientTrafficResponse>(
        `/api/clients/${clientId}/traffic?from=${from}&to=${to}&resolution=${config.resolution}`,
      );
    },
    staleTime: 30_000,
    refetchInterval: config.refetchInterval,
    refetchOnWindowFocus: false,
  });
}
