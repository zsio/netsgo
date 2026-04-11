import { useMutation, useQueryClient } from '@tanstack/react-query';
import { api } from '@/lib/api';
import { buildTunnelMutationPayload } from '@/lib/tunnel-model';
import type { CreateTunnelInput, ProxyType } from '@/types';

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
    mutationFn: ({ clientId, tunnelName }: { clientId: string; tunnelName: string }) =>
      api.put(`/api/clients/${clientId}/tunnels/${tunnelName}/resume`),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['clients'] });
    },
  });
}

export function useStopTunnel() {
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: ({ clientId, tunnelName }: { clientId: string; tunnelName: string }) =>
      api.put(`/api/clients/${clientId}/tunnels/${tunnelName}/stop`),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['clients'] });
    },
  });
}

export function useDeleteTunnel() {
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: ({ clientId, tunnelName }: { clientId: string; tunnelName: string }) =>
      api.delete(`/api/clients/${clientId}/tunnels/${tunnelName}`),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['clients'] });
    },
  });
}

export function useUpdateTunnel() {
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: ({
      clientId,
      tunnelName,
      type,
      local_ip,
      local_port,
      remote_port,
      domain,
    }: {
      clientId: string;
      tunnelName: string;
      type: ProxyType;
      local_ip: string;
      local_port: number;
      remote_port: number;
      domain: string;
    }) =>
      api.put(`/api/clients/${clientId}/tunnels/${tunnelName}`, buildTunnelMutationPayload({
        type,
        local_ip,
        local_port,
        remote_port,
        domain,
      })),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['clients'] });
    },
  });
}
