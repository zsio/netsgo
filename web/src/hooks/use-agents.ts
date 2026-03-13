import { useQuery } from '@tanstack/react-query';
import { api } from '@/lib/api';
import { readAgentsCache, writeAgentsCache } from '@/lib/agents-cache';
import type { Agent } from '@/types';

export function useAgents() {
  const cached = readAgentsCache();

  return useQuery({
    queryKey: ['agents'],
    queryFn: async () => {
      const agents = await api.get<Agent[]>('/api/agents');
      writeAgentsCache(agents);
      return agents;
    },
    initialData: cached?.agents,
    initialDataUpdatedAt: cached?.updatedAt,
    refetchOnMount: 'always',
    staleTime: Infinity,
  });
}
