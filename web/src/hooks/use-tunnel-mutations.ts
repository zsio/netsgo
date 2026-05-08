import { useMutation, useQueryClient } from '@tanstack/react-query';
import { api } from '@/lib/api';
import { buildTunnelMutationPayload } from '@/lib/tunnel-model';
import type { CreateTunnelInput, UpdateTunnelInput } from '@/types';

export function useCreateTunnel() {
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: (data: CreateTunnelInput) =>
      api.post<{ success: boolean; message: string; remote_port: number }>(
        `/api/clients/${data.clientId}/tunnels`,
        {
          name: data.name,
          type: data.type,
          ...buildTunnelMutationPayload(data),
        },
      ),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['clients'] });
    },
  });
}

export function useResumeTunnel() {
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: ({ clientId, tunnelId }: { clientId: string; tunnelId: string }) =>
      api.put(`/api/clients/${clientId}/tunnels/${encodeURIComponent(tunnelId)}/resume`),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['clients'] });
    },
  });
}

export function useStopTunnel() {
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: ({ clientId, tunnelId }: { clientId: string; tunnelId: string }) =>
      api.put(`/api/clients/${clientId}/tunnels/${encodeURIComponent(tunnelId)}/stop`),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['clients'] });
    },
  });
}

export function useDeleteTunnel() {
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: ({ clientId, tunnelId }: { clientId: string; tunnelId: string }) =>
      api.delete(`/api/clients/${clientId}/tunnels/${encodeURIComponent(tunnelId)}`),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['clients'] });
    },
  });
}

export function useUpdateTunnel() {
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: (data: UpdateTunnelInput) =>
      api.put(`/api/clients/${data.clientId}/tunnels/${encodeURIComponent(data.tunnelId)}`, {
        name: data.name,
        ...buildTunnelMutationPayload(data),
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['clients'] });
    },
  });
}
