import { useQuery } from '@tanstack/react-query';

import { api } from '@/lib/api';
import { EMPTY_CONSOLE_SUMMARY } from '@/lib/console-summary';
import type { ConsoleSummary, ServerStatus } from '@/types';

export function useConsoleSummary() {
  return useQuery({
    queryKey: ['console-summary'],
    queryFn: async (): Promise<ConsoleSummary> => {
      const status = await api.get<ServerStatus>('/api/status');
      return status.summary ?? EMPTY_CONSOLE_SUMMARY;
    },
    staleTime: Infinity,
  });
}
