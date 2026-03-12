import { useQuery } from '@tanstack/react-query';
import { api } from '@/lib/api';
import type { SystemLogEntry } from '@/types';

export function useAdminLogs() {
  return useQuery({
    queryKey: ['admin-logs'],
    queryFn: () => api.get<SystemLogEntry[]>('/api/admin/logs?limit=500'),
    refetchInterval: 5000,
  });
}
