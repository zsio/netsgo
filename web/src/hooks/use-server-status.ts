import { useQuery } from '@tanstack/react-query';
import { api } from '@/lib/api';
import type { ServerStatus } from '@/types';

export function useServerStatus() {
  return useQuery({
    queryKey: ['server-status'],
    queryFn: () => api.get<ServerStatus>('/api/status'),
    refetchInterval: 10000, // 10s 轮询（status 无 SSE 推送）
  });
}
