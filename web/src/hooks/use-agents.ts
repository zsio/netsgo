import { useQuery } from '@tanstack/react-query';
import { api } from '@/lib/api';
import type { Agent } from '@/types';

export function useAgents() {
  return useQuery({
    queryKey: ['agents'],
    queryFn: () => api.get<Agent[]>('/api/agents'),
    staleTime: Infinity,
  });
}
