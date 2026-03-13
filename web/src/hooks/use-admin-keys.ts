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
    mutationFn: (data: { name: string; permissions?: string[]; max_uses?: number; expires_in?: string }) =>
      api.post<{ key: APIKey; raw_key: string; server_addr: string }>('/api/admin/keys', data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['admin-keys'] });
    },
  });
}

export function useEnableAPIKey() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => api.put(`/api/admin/keys/${id}/enable`),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['admin-keys'] });
    },
  });
}

export function useDisableAPIKey() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => api.put(`/api/admin/keys/${id}/disable`),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['admin-keys'] });
    },
  });
}

export function useDeleteAPIKey() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => api.delete(`/api/admin/keys/${id}`),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['admin-keys'] });
    },
  });
}
