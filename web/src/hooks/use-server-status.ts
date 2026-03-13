import { useQuery } from '@tanstack/react-query';
import { api } from '@/lib/api';
import type { ServerStatus } from '@/types';

interface UseServerStatusOptions {
  enabled?: boolean;
  refetchOnMount?: boolean | 'always';
  staleTime?: number;
}

export function useServerStatus(options: UseServerStatusOptions = {}) {
  return useQuery({
    queryKey: ['server-status'],
    queryFn: () => api.get<ServerStatus>('/api/status'),
    enabled: options.enabled ?? true,
    refetchOnMount: options.refetchOnMount,
    staleTime: options.staleTime ?? Infinity,
  });
}
