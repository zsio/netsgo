import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { api } from '@/lib/api';
import type { APIKey } from '@/types';

export function useAdminKeys() {
  return useQuery({
    queryKey: ['admin-keys'],
    queryFn: () => api.get<APIKey[]>('/api/admin/keys'),
  });
}

export function useCreateAPIKey() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (data: { name: string; permissions: string[] }) =>
      api.post<{ key: APIKey; raw_key: string }>('/api/admin/keys', data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['admin-keys'] });
    },
  });
}
