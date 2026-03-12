import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { api } from '@/lib/api';
import type { TunnelPolicy } from '@/types';

export function useAdminPolicies() {
  return useQuery({
    queryKey: ['admin-policies'],
    queryFn: () => api.get<TunnelPolicy>('/api/admin/policies'),
  });
}

export function useUpdateAdminPolicies() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (data: TunnelPolicy) => api.put('/api/admin/policies', data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['admin-policies'] });
    },
  });
}
