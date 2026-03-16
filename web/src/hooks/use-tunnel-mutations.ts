import { useMutation, useQueryClient } from '@tanstack/react-query';
import { api } from '@/lib/api';
import type { CreateTunnelInput } from '@/types';

export function useCreateTunnel() {
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: (data: CreateTunnelInput) =>
      api.post<{ success: boolean; message: string; remote_port: number }>(
        `/api/clients/${data.clientId}/tunnels`,
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
      queryClient.invalidateQueries({ queryKey: ['clients'] });
    },
  });
}

export function usePauseTunnel() {
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: ({ clientId, tunnelName }: { clientId: string; tunnelName: string }) =>
      api.put(`/api/clients/${clientId}/tunnels/${tunnelName}/pause`),
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
      local_ip,
      local_port,
      remote_port,
      domain,
    }: {
      clientId: string;
      tunnelName: string;
      local_ip: string;
      local_port: number;
      remote_port: number;
      domain: string;
    }) =>
      api.put(`/api/clients/${clientId}/tunnels/${tunnelName}`, {
        local_ip,
        local_port,
        remote_port,
        domain,
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['clients'] });
    },
  });
}
