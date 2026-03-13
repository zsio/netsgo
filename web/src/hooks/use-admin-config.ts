import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { api } from '@/lib/api';
import type { ServerConfig } from '@/types';

export function useAdminConfig() {
  return useQuery({
    queryKey: ['admin-config'],
    queryFn: () => api.get<ServerConfig>('/api/admin/config'),
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
