import { ApiError } from '@/lib/api';
import { i18n } from '@/i18n';
import type {
  ActualTransport,
  BandwidthSettings,
  IngressEndpointType,
  ProxyConfig,
  ProxyDesiredState,
  ProxyRuntimeState,
  ProxyType,
  P2PStateValue,
  TargetEndpointType,
  TransportPolicy,
  TunnelCapabilities,
  TunnelCreateRequest,
  TunnelIngress,
  TunnelMutationErrorResponse,
  TunnelTarget,
  TunnelTopology,
} from '@/types';

type TunnelStatusKey = 'pending' | 'exposed' | 'offline' | 'stopped' | 'error';

export interface TunnelStatusPresentation {
  key: TunnelStatusKey;
  label: string;
  description?: string;
}

export interface TunnelViewModel extends ProxyConfig {
  targetLabel: string;
  destinationLabel: string;
  routeLabel: string;
  topologyLabel: string;
  participantLabel: string;
  transportLabel: string;
  p2pLabel: string;
  ingressWarning?: string;
  status: TunnelStatusPresentation;
}

export interface TunnelActionAvailability {
  canResume: boolean;
  canStop: boolean;
  canEdit: boolean;
  canDelete: boolean;
}

const requiredTunnelCapabilities = [
  'can_resume',
  'can_stop',
  'can_edit',
  'can_delete',
] as const;

interface TunnelMutationPayloadInput {
  type: ProxyType;
  local_ip: string;
  local_port: number;
  remote_port?: number;
  domain?: string;
  ingress_bps?: number;
  egress_bps?: number;
}

export interface TunnelSpecMutationInput extends TunnelMutationPayloadInput {
  clientId: string;
  name: string;
}

export interface ClientToClientTunnelSpecMutationInput extends TunnelMutationPayloadInput {
  ingressClientId: string;
  targetClientId: string;
  name: string;
  bind_ip: string;
}

export const currentIngressTypes = ['tcp_listen', 'udp_listen', 'http_host'] as const satisfies readonly IngressEndpointType[];
export const currentTargetTypes = ['tcp_service', 'udp_service'] as const satisfies readonly TargetEndpointType[];
export const futureOnlyTargetTypes = ['unix_socket', 'static_file', 'serial_device'] as const;

const transportPolicyLabels = {
  server_relay_only: 'Server relay',
  direct_preferred: 'P2P preferred (unavailable)',
  direct_only: 'P2P only (unavailable)',
} as const satisfies Record<TransportPolicy, string>;

const actualTransportLabels = {
  unknown: 'Not established',
  server_relay: 'Server relay',
  peer_direct: 'P2P direct (unavailable)',
  turn_relay: 'TURN relay',
} as const satisfies Record<ActualTransport, string>;

const p2pStateLabels = {
  idle: 'Disabled',
  gathering: 'Gathering candidates',
  checking: 'Checking connectivity',
  connected: 'Direct connected',
  failed: 'Direct failed',
  fallback: 'Fell back to relay',
  closed: 'Closed',
} as const satisfies Record<P2PStateValue, string>;

export function buildTunnelMutationPayload(input: TunnelMutationPayloadInput) {
  const localIP = input.local_ip.trim();
  const domain = (input.domain ?? '').trim();
  const remotePort = input.remote_port ?? 0;

  if (input.type !== 'http' && (!Number.isInteger(remotePort) || remotePort < 1 || remotePort > 65535)) {
    throw new Error(i18n.t('errors.tcp_udp_remote_port_required'));
  }

  return {
    local_ip: localIP,
    local_port: input.local_port,
    remote_port: input.type === 'http' ? 0 : remotePort,
    domain: input.type === 'http' ? domain : '',
    ingress_bps: normalizeBandwidthLimit(input.ingress_bps),
    egress_bps: normalizeBandwidthLimit(input.egress_bps),
  };
}

export function buildTunnelSpecCreateRequest(input: TunnelSpecMutationInput): TunnelCreateRequest {
  const payload = buildTunnelMutationPayload(input);
  const target: TunnelTarget = input.type === 'udp'
    ? {
      location: 'client',
      client_id: input.clientId,
      type: 'udp_service',
      config: {
        ip: payload.local_ip,
        port: payload.local_port,
      },
    }
    : {
      location: 'client',
      client_id: input.clientId,
      type: 'tcp_service',
      config: {
        ip: payload.local_ip,
        port: payload.local_port,
      },
    };
  const ingress = buildServerExposeIngress(input.type, payload.remote_port, payload.domain);

  return {
    name: input.name,
    topology: 'server_expose',
    ingress,
    target,
    transport_policy: 'server_relay_only',
    bandwidth_settings: {
      ingress_bps: payload.ingress_bps,
      egress_bps: payload.egress_bps,
    },
  };
}

export function buildClientToClientTunnelSpecCreateRequest(
  input: ClientToClientTunnelSpecMutationInput,
): TunnelCreateRequest {
  if (input.type === 'http') {
    throw new Error(i18n.t('errors.client_to_client_http_unsupported'));
  }
  if (input.ingressClientId === input.targetClientId) {
    throw new Error(i18n.t('errors.client_to_client_same_participant'));
  }
  const payload = buildTunnelMutationPayload(input);
  const bindIP = input.bind_ip.trim();
  if (!bindIP) {
    throw new Error(i18n.t('errors.client_to_client_bind_ip_required'));
  }
  const target: TunnelTarget = input.type === 'udp'
    ? {
      location: 'client',
      client_id: input.targetClientId,
      type: 'udp_service',
      config: {
        ip: payload.local_ip,
        port: payload.local_port,
      },
    }
    : {
      location: 'client',
      client_id: input.targetClientId,
      type: 'tcp_service',
      config: {
        ip: payload.local_ip,
        port: payload.local_port,
      },
    };
  const ingress: TunnelIngress = input.type === 'udp'
    ? {
      location: 'client',
      client_id: input.ingressClientId,
      type: 'udp_listen',
      config: {
        bind_ip: bindIP,
        port: payload.remote_port,
      },
    }
    : {
      location: 'client',
      client_id: input.ingressClientId,
      type: 'tcp_listen',
      config: {
        bind_ip: bindIP,
        port: payload.remote_port,
      },
    };

  return {
    name: input.name,
    topology: 'client_to_client',
    ingress,
    target,
    transport_policy: 'server_relay_only',
    bandwidth_settings: {
      ingress_bps: payload.ingress_bps,
      egress_bps: payload.egress_bps,
    },
  };
}

export function buildTunnelViewModel(
  tunnel: ProxyConfig,
  clientOnline: boolean,
): TunnelViewModel {
  const destinationLabel = getTunnelTargetLabel(tunnel);
  const targetLabel = getTunnelIngressLabel(tunnel);
  const transportLabel = getTransportLabel(tunnel);
  const p2pLabel = getP2PLabel(tunnel);

  return {
    ...tunnel,
    targetLabel,
    destinationLabel,
    routeLabel: `${targetLabel} -> ${destinationLabel}`,
    topologyLabel: getTopologyLabel(tunnel.topology),
    participantLabel: getParticipantLabel(tunnel),
    transportLabel,
    p2pLabel,
    ingressWarning: getIngressWarning(tunnel),
    status: resolveTunnelStatus(tunnel, clientOnline),
  };
}

export function getTunnelMutationErrorMessage(error: unknown) {
  if (error instanceof ApiError) {
    const body = error.body as TunnelMutationErrorResponse | undefined;
    const code = body?.code ?? body?.error_code;
    if (code === 'unknown_target_type' || code === 'unsupported_target_type') {
      return i18n.t('errors.unsupported_endpoint_type');
    }
    if (code) {
      const localizedMessage = i18n.t(`errors.${code}`, { defaultValue: '' });
      if (localizedMessage) {
        return localizedMessage;
      }
    }
    return error.message;
  }

  if (error instanceof Error) {
    return error.message;
  }

  return i18n.t('errors.generic');
}

export function getTunnelMutationFieldError(error: unknown) {
  if (!(error instanceof ApiError)) {
    return null;
  }
  const body = error.body as TunnelMutationErrorResponse | undefined;
  if (!body?.field) {
    return null;
  }
  return {
    field: body.field,
    message: getTunnelMutationErrorMessage(error),
    code: body.code ?? body.error_code,
  };
}

export function getTunnelActionAvailability(
  tunnel: Pick<ProxyConfig, 'capabilities'>,
): TunnelActionAvailability {
  const capabilities = requireTunnelCapabilities(tunnel.capabilities);

  return {
    canResume: capabilities.can_resume,
    canStop: capabilities.can_stop,
    canEdit: capabilities.can_edit,
    canDelete: capabilities.can_delete,
  };
}

export function resolveTunnelStatus(
  tunnel: Pick<ProxyConfig, 'desired_state' | 'runtime_state' | 'error'>,
  clientOnline: boolean,
): TunnelStatusPresentation {
  let runtimeState = tunnel.runtime_state;
  if (!clientOnline && tunnel.desired_state === 'running' && runtimeState !== 'error') {
    runtimeState = 'offline';
  }
  return resolveTunnelStatusFromStates(
    tunnel.desired_state,
    runtimeState,
    tunnel.error,
  );
}

function resolveTunnelStatusFromStates(
  _desiredState: ProxyDesiredState,
  runtimeState: ProxyRuntimeState,
  error?: string,
): TunnelStatusPresentation {
  switch (runtimeState) {
    case 'pending':
      return {
        key: 'pending',
        label: 'Pending',
        description: 'Waiting for the client to expose the ingress',
      };
    case 'exposed':
    case 'active':
      return {
        key: 'exposed',
        label: 'Active',
      };
    case 'offline':
      return {
        key: 'offline',
        label: 'Client offline',
      };
    case 'idle':
      return {
        key: 'stopped',
        label: 'Stopped',
      };
    case 'error':
      return {
        key: 'error',
        label: 'Error',
        description: error || 'Tunnel runtime error',
      };
    default:
      return {
        key: 'error',
        label: runtimeState,
        description: error,
      };
  }
}

function buildServerExposeIngress(
  proxyType: ProxyType,
  remotePort: number,
  domain: string,
): TunnelIngress {
  switch (proxyType) {
    case 'http':
      return {
        location: 'server',
        type: 'http_host',
        config: { domain },
      };
    case 'udp':
      return {
        location: 'server',
        type: 'udp_listen',
        config: {
          bind_ip: '0.0.0.0',
          port: remotePort,
        },
      };
    case 'tcp':
    default:
      return {
        location: 'server',
        type: 'tcp_listen',
        config: {
          bind_ip: '0.0.0.0',
          port: remotePort,
        },
      };
  }
}

function getTopologyLabel(topology: TunnelTopology | undefined) {
  switch (topology) {
    case 'client_to_client':
      return 'Client ↔ Client';
    case 'server_expose':
    case undefined:
      return 'Server expose';
    default:
      return topology;
  }
}

function getParticipantLabel(tunnel: ProxyConfig) {
  const ingressClient = tunnel.ingress?.client_id || tunnel.participants?.ingress?.client_id;
  const targetClient = tunnel.target?.client_id || tunnel.participants?.target?.client_id || tunnel.client_id;
  if (tunnel.topology === 'client_to_client') {
    return `Ingress ${ingressClient || 'unknown'} / Target ${targetClient || 'unknown'}`;
  }
  return `Target ${targetClient || 'unknown'}`;
}

function getTransportLabel(tunnel: ProxyConfig) {
  const policy = tunnel.transport?.policy ?? tunnel.transport_policy ?? 'server_relay_only';
  const actual = tunnel.transport?.actual ?? tunnel.actual_transport ?? 'unknown';
  const policyLabel = transportPolicyLabels[policy] ?? policy;
  const actualLabel = actualTransportLabels[actual] ?? actual;
  return `${policyLabel} · ${actualLabel}`;
}

function getP2PLabel(tunnel: ProxyConfig) {
  const state = tunnel.transport?.p2p_state ?? tunnel.p2p?.state ?? 'idle';
  return p2pStateLabels[state] ?? state;
}

function getTunnelIngressLabel(tunnel: ProxyConfig) {
  const ingress = tunnel.ingress;
  if (ingress) {
    switch (ingress.type) {
      case 'http_host':
        return ingress.config?.domain || '(domain not set)';
      case 'tcp_listen':
      case 'udp_listen':
        return `${ingress.config?.bind_ip || '0.0.0.0'}:${ingress.config?.port ?? 0}`;
    }
  }

  return tunnel.type === 'http'
    ? (tunnel.domain || '(domain not set)')
    : `:${tunnel.remote_port}`;
}

function getServiceTargetHost(target: TunnelTarget) {
  return target.config?.ip || target.config?.host || '';
}

function getTunnelTargetLabel(tunnel: ProxyConfig) {
  const target = tunnel.target;
  if (target) {
    switch (target.type) {
      case 'tcp_service':
      case 'udp_service':
        return `${getServiceTargetHost(target)}:${target.config?.port ?? 0}`;
    }
  }

  return `${tunnel.local_ip}:${tunnel.local_port}`;
}

function getIngressWarning(tunnel: ProxyConfig) {
  const ingress = tunnel.ingress;
  if (!ingress || ingress.location !== 'client') {
    return undefined;
  }
  if (ingress.type !== 'tcp_listen' && ingress.type !== 'udp_listen') {
    return undefined;
  }
  const bindIP = ingress.config?.bind_ip?.trim() ?? '';
  if (bindIP === '0.0.0.0' || bindIP === '::') {
    return i18n.t('tunnels.wildcardIngressWarning', {
      defaultValue: 'Ingress binds to a wildcard address and is exposed to the ingress client network.',
    });
  }
  return undefined;
}

function requireTunnelCapabilities(
  capabilities: Partial<TunnelCapabilities> | null | undefined,
): TunnelCapabilities {
  if (!capabilities || typeof capabilities !== 'object') {
    throw new Error('Tunnel capabilities are required');
  }

  for (const key of requiredTunnelCapabilities) {
    if (typeof capabilities[key] !== 'boolean') {
      throw new Error(`Tunnel capability "${key}" is required`);
    }
  }

  return capabilities as TunnelCapabilities;
}

function normalizeBandwidthLimit(value?: number): number {
  if (value == null) {
    return 0;
  }
  if (!Number.isInteger(value) || value < 0) {
    throw new Error(i18n.t('errors.invalid_bandwidth_limit'));
  }
  return value;
}

export function getTunnelBandwidthSettings(tunnel: ProxyConfig): BandwidthSettings {
  return tunnel.bandwidth_settings ?? {
    ingress_bps: tunnel.ingress_bps,
    egress_bps: tunnel.egress_bps,
  };
}
