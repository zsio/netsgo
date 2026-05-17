import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { ApiError, api, tunnelApi } from '@/lib/api';
import { buildTunnelMutationPayload, buildTunnelSpecCreateRequest } from '@/lib/tunnel-model';
import type { CreateTunnelInput, TunnelClientRole, UpdateTunnelInput } from '@/types';

function invalidateTunnelQueries(queryClient: ReturnType<typeof useQueryClient>) {
  queryClient.invalidateQueries({ queryKey: ['clients'] });
  queryClient.invalidateQueries({ queryKey: ['client-tunnels'] });
  queryClient.invalidateQueries({ queryKey: ['console-summary'] });
  queryClient.invalidateQueries({ queryKey: ['server-status'] });
}

export function buildClientTunnelRoleQueryKey(clientId: string | undefined, role = 'owner') {
  return ['client-tunnels', clientId, role] as const;
}

export function useClientTunnelsByRole(clientId: string | undefined, role: TunnelClientRole) {
  return useQuery({
    queryKey: buildClientTunnelRoleQueryKey(clientId, role),
    enabled: Boolean(clientId),
    queryFn: () => {
      if (!clientId) {
        throw new Error('clientId is required to load tunnels by role');
      }
      return tunnelApi.listByClientRole(clientId, role);
    },
    staleTime: 30_000,
  });
}

function shouldUseLegacyTunnelEndpoint(error: unknown) {
  return error instanceof ApiError && (error.status === 404 || error.status === 405);
}

export function useCreateTunnel() {
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: async (data: CreateTunnelInput) => {
      try {
        return await tunnelApi.create(buildTunnelSpecCreateRequest(data));
      } catch (error) {
        if (!shouldUseLegacyTunnelEndpoint(error)) {
          throw error;
        }
        try {
          return await api.post<{ success: boolean; message: string; remote_port: number }>(
            `/api/clients/${data.clientId}/tunnels`,
            {
              name: data.name,
              type: data.type,
              ...buildTunnelMutationPayload(data),
            },
          );
        } catch {
          throw error;
        }
      }
    },
    onSuccess: () => {
      invalidateTunnelQueries(queryClient);
    },
  });
}

export function useResumeTunnel() {
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: ({ clientId, tunnelId }: { clientId: string; tunnelId: string }) =>
      tunnelApi.resume(tunnelId).catch((error) => {
        if (!shouldUseLegacyTunnelEndpoint(error)) {
          throw error;
        }
        return api.put(`/api/clients/${clientId}/tunnels/${encodeURIComponent(tunnelId)}/resume`);
      }),
    onSuccess: () => {
      invalidateTunnelQueries(queryClient);
    },
  });
}

export function useStopTunnel() {
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: ({ clientId, tunnelId }: { clientId: string; tunnelId: string }) =>
      tunnelApi.stop(tunnelId).catch((error) => {
        if (!shouldUseLegacyTunnelEndpoint(error)) {
          throw error;
        }
        return api.put(`/api/clients/${clientId}/tunnels/${encodeURIComponent(tunnelId)}/stop`);
      }),
    onSuccess: () => {
      invalidateTunnelQueries(queryClient);
    },
  });
}

export function useDeleteTunnel() {
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: ({ clientId, tunnelId }: { clientId: string; tunnelId: string }) =>
      tunnelApi.delete(tunnelId).catch((error) => {
        if (!shouldUseLegacyTunnelEndpoint(error)) {
          throw error;
        }
        return api.delete(`/api/clients/${clientId}/tunnels/${encodeURIComponent(tunnelId)}`);
      }),
    onSuccess: () => {
      invalidateTunnelQueries(queryClient);
    },
  });
}

export function useUpdateTunnel() {
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: async (data: UpdateTunnelInput) => {
      try {
        return await tunnelApi.update(data.tunnelId, {
          expected_revision: data.expected_revision,
          spec: buildTunnelSpecCreateRequest(data),
        });
      } catch (error) {
        if (!shouldUseLegacyTunnelEndpoint(error)) {
          throw error;
        }
        try {
          return await api.put(`/api/clients/${data.clientId}/tunnels/${encodeURIComponent(data.tunnelId)}`, {
            name: data.name,
            ...buildTunnelMutationPayload(data),
          });
        } catch {
          throw error;
        }
      }
    },
    onSuccess: () => {
      invalidateTunnelQueries(queryClient);
    },
  });
}
