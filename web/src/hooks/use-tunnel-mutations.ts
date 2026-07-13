import { useMutation, useQuery, useQueryClient, type QueryClient } from '@tanstack/react-query';
import { tunnelApi } from '@/lib/api';
import {
  buildClientToClientTunnelSpecCreateRequest,
  buildTunnelSpecCreateRequest,
} from '@/lib/tunnel-model';
import type { CreateTunnelInput, MigrateTunnelInput, TransportPolicy, TunnelClientRole, TunnelTopology, UpdateTunnelInput } from '@/types';

export function invalidateTunnelQueries(queryClient: QueryClient) {
  queryClient.invalidateQueries({ queryKey: ['clients'] });
  queryClient.invalidateQueries({ queryKey: ['client-tunnels'] });
	queryClient.invalidateQueries({ queryKey: ['client-traffic'] });
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
  allowed_source_cidrs?: string[];
  ingress_bps?: number;
  egress_bps?: number;
  total_bps?: number;
  transport_policy?: TransportPolicy;
  socks5?: CreateTunnelInput['socks5'];
  http_auth?: CreateTunnelInput['http_auth'];
  confirm_no_auth_risk?: boolean;
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
      allowed_source_cidrs: data.allowed_source_cidrs,
      bind_ip: data.bind_ip ?? '',
      ingress_bps: data.ingress_bps,
      egress_bps: data.egress_bps,
      total_bps: data.total_bps,
      transport_policy: data.transport_policy,
      socks5: data.socks5,
      http_auth: data.http_auth,
      confirm_no_auth_risk: data.confirm_no_auth_risk,
    });
  }
  return buildTunnelSpecCreateRequest(data);
}

export function useCreateTunnel() {
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: (data: CreateTunnelInput) => tunnelApi.create(buildTunnelSpec(data)),
    onSuccess: () => {
      invalidateTunnelQueries(queryClient);
    },
  });
}

export function useResumeTunnel() {
  const queryClient = useQueryClient();

	return useMutation({
		mutationFn: ({ tunnelId }: { tunnelId: string }) => tunnelApi.resume(tunnelId),
		onSuccess: () => {
			invalidateTunnelQueries(queryClient);
		},
  });
}

export function useStopTunnel() {
  const queryClient = useQueryClient();

	return useMutation({
		mutationFn: ({ tunnelId }: { tunnelId: string }) => tunnelApi.stop(tunnelId),
		onSuccess: () => {
			invalidateTunnelQueries(queryClient);
		},
  });
}

export function useDeleteTunnel() {
  const queryClient = useQueryClient();

	return useMutation({
		mutationFn: ({ tunnelId }: { tunnelId: string }) => tunnelApi.delete(tunnelId),
		onSuccess: () => {
			invalidateTunnelQueries(queryClient);
		},
  });
}

export function useUpdateTunnel() {
  const queryClient = useQueryClient();

	return useMutation({
		mutationFn: (data: UpdateTunnelInput) => tunnelApi.update(data.tunnelId, {
			expected_revision: data.expected_revision,
			spec: buildTunnelSpec(data),
		}),
		onSuccess: () => {
			invalidateTunnelQueries(queryClient);
		},
  });
}

export function useMigrateTunnel() {
  const queryClient = useQueryClient();

	return useMutation({
		mutationFn: ({ tunnelId, expected_revision, target_client_id }: MigrateTunnelInput) => tunnelApi.migrate(tunnelId, {
			expected_revision,
			target_client_id,
		}),
		onSuccess: () => {
			invalidateTunnelQueries(queryClient);
		},
	});
}
