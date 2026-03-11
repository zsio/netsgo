import { useMutation, useQueryClient } from '@tanstack/react-query';
import { api } from '@/lib/api';
import type { CreateTunnelInput } from '@/types';

export function useCreateTunnel() {
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: (data: CreateTunnelInput) =>
      api.post<{ success: boolean; message: string; remote_port: number }>(
        `/api/agents/${data.agentId}/tunnels`,
        {
          name: data.name,
          type: data.type,
          local_ip: data.local_ip,
          local_port: data.local_port,
          remote_port: data.remote_port ?? 0,
          domain: data.domain ?? '',
        },
      ),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['agents'] });
    },
  });
}

export function useDeleteTunnel() {
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: ({ agentId, tunnelName }: { agentId: string; tunnelName: string }) =>
      api.delete(`/api/agents/${agentId}/tunnels/${tunnelName}`),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['agents'] });
    },
  });
}
