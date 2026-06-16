import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { ApiError, api, tunnelApi } from '@/lib/api';
import {
  buildClientToClientTunnelSpecCreateRequest,
  buildTunnelMutationPayload,
  buildTunnelSpecCreateRequest,
} from '@/lib/tunnel-model';
import type { CreateTunnelInput, TunnelClientRole, TunnelTopology, UpdateTunnelInput } from '@/types';

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

export function shouldUseLegacyTunnelEndpoint(error: unknown, topology?: TunnelTopology) {
  if (topology === 'client_to_client') {
    return false;
  }
  return error instanceof ApiError && (error.status === 404 || error.status === 405);
}

export function buildLegacyClientTunnelPath(clientId: string, suffix = '') {
  return `/api/clients/${encodeURIComponent(clientId)}/tunnels${suffix}`;
}

function buildTunnelSpec(data: {
  topology?: TunnelTopology;
  ingress_client_id?: string;
  bind_ip?: string;
  clientId: string;
  name: string;
  type: CreateTunnelInput['type'];
  local_ip: string;
  local_port: number;
  remote_port?: number;
  domain?: string;
  ingress_bps?: number;
  egress_bps?: number;
}) {
  if (data.topology === 'client_to_client') {
    return buildClientToClientTunnelSpecCreateRequest({
      ingressClientId: data.ingress_client_id ?? '',
      targetClientId: data.clientId,
      name: data.name,
      type: data.type,
      local_ip: data.local_ip,
      local_port: data.local_port,
      remote_port: data.remote_port,
      domain: data.domain,
      bind_ip: data.bind_ip ?? '',
      ingress_bps: data.ingress_bps,
      egress_bps: data.egress_bps,
    });
  }
  return buildTunnelSpecCreateRequest(data);
}

export function useCreateTunnel() {
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: async (data: CreateTunnelInput) => {
      try {
        return await tunnelApi.create(buildTunnelSpec(data));
      } catch (error) {
        if (!shouldUseLegacyTunnelEndpoint(error, data.topology)) {
          throw error;
        }
        try {
          return await api.post<{ success: boolean; message: string; remote_port: number }>(
            buildLegacyClientTunnelPath(data.clientId),
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
        return api.put(buildLegacyClientTunnelPath(clientId, `/${encodeURIComponent(tunnelId)}/resume`));
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
        return api.put(buildLegacyClientTunnelPath(clientId, `/${encodeURIComponent(tunnelId)}/stop`));
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
        return api.delete(buildLegacyClientTunnelPath(clientId, `/${encodeURIComponent(tunnelId)}`));
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
          spec: buildTunnelSpec(data),
        });
      } catch (error) {
        if (!shouldUseLegacyTunnelEndpoint(error, data.topology)) {
          throw error;
        }
        try {
          return await api.put(buildLegacyClientTunnelPath(data.clientId, `/${encodeURIComponent(data.tunnelId)}`), {
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
