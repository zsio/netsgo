import { useQuery } from '@tanstack/react-query';
import { api } from '@/lib/api';
import type { EventRecord } from '@/types';

export function useAdminEvents() {
  return useQuery({
    queryKey: ['admin-events'],
    queryFn: () => api.get<EventRecord[]>('/api/admin/events?limit=200'),
    refetchInterval: 5000,
  });
}
