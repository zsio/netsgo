import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { api } from '@/lib/api';
import type { AdminConfig, ServerConfig } from '@/types';

interface UseAdminConfigOptions {
  enabled?: boolean;
  refetchOnMount?: boolean | 'always';
  staleTime?: number;
}

export function useAdminConfig(options: UseAdminConfigOptions = {}) {
  return useQuery({
    queryKey: ['admin-config'],
    queryFn: () => api.get<AdminConfig>('/api/admin/config'),
    enabled: options.enabled ?? true,
    refetchOnMount: options.refetchOnMount,
    staleTime: options.staleTime ?? Infinity,
  });
}

export function useUpdateAdminConfig() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (data: ServerConfig) => api.put('/api/admin/config', data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['admin-config'] });
    },
  });
}
