import { ApiError } from '@/lib/api';
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

export const currentIngressTypes = ['tcp_listen', 'udp_listen', 'http_host'] as const satisfies readonly IngressEndpointType[];
export const currentTargetTypes = ['tcp_service', 'udp_service'] as const satisfies readonly TargetEndpointType[];
export const futureOnlyTargetTypes = ['unix_socket', 'static_file', 'serial_device'] as const;

const transportPolicyLabels = {
  server_relay_only: 'Server 中继',
  direct_preferred: 'P2P 优先',
  direct_only: '仅 P2P',
} as const satisfies Record<TransportPolicy, string>;

const actualTransportLabels = {
  unknown: '未建立',
  server_relay: 'Server 中继',
  peer_direct: 'P2P 直连',
  turn_relay: 'TURN 中继',
} as const satisfies Record<ActualTransport, string>;

const p2pStateLabels = {
  idle: '未启用',
  gathering: '收集候选',
  checking: '连通性检查',
  connected: '已直连',
  failed: '直连失败',
  fallback: '已回退中继',
  closed: '已关闭',
} as const satisfies Record<P2PStateValue, string>;

export function buildTunnelMutationPayload(input: TunnelMutationPayloadInput) {
  const localIP = input.local_ip.trim();
  const domain = (input.domain ?? '').trim();
  const remotePort = input.remote_port ?? 0;

  if (input.type !== 'http' && (!Number.isInteger(remotePort) || remotePort < 1 || remotePort > 65535)) {
    throw new Error('TCP/UDP 隧道必须填写明确的公网端口');
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
    switch (body?.error_code) {
      case 'server_addr_conflict':
        return '该域名与当前管理地址冲突，请改用其他业务域名。';
      case 'http_tunnel_conflict':
        return '该域名已被其他 HTTP 隧道占用，请更换域名后重试。';
      case 'metadata_missing':
        return '隧道元数据已删除，仅保留历史流量记录。';
      case 'unknown_target_type':
      case 'unsupported_target_type':
        return '该目标类型暂未支持，当前仅支持 TCP/UDP 服务。';
      case 'direct_transport_unavailable':
        return '当前节点暂不支持 P2P 直连传输，请先选择 Server 中继。';
      default:
        return error.message;
    }
  }

  if (error instanceof Error) {
    return error.message;
  }

  return '提交失败，请稍后重试';
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
        label: '等待建立',
        description: '等待 Client 建立公网入口',
      };
    case 'exposed':
    case 'active':
      return {
        key: 'exposed',
        label: '已建立',
      };
    case 'offline':
      return {
        key: 'offline',
        label: '客户端离线',
      };
    case 'idle':
      return {
        key: 'stopped',
        label: '已停止',
      };
    case 'error':
      return {
        key: 'error',
        label: '异常',
        description: error || '隧道运行异常',
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
      return 'Server 暴露';
    default:
      return topology;
  }
}

function getParticipantLabel(tunnel: ProxyConfig) {
  const ingressClient = tunnel.ingress?.client_id || tunnel.participants?.ingress?.client_id;
  const targetClient = tunnel.target?.client_id || tunnel.participants?.target?.client_id || tunnel.client_id;
  if (tunnel.topology === 'client_to_client') {
    return `入口 ${ingressClient || '未知'} / 目标 ${targetClient || '未知'}`;
  }
  return `目标 ${targetClient || '未知'}`;
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
        return ingress.config.domain || '(未声明域名)';
      case 'tcp_listen':
      case 'udp_listen':
        return `${ingress.config.bind_ip}:${ingress.config.port}`;
    }
  }

  return tunnel.type === 'http'
    ? (tunnel.domain || '(未声明域名)')
    : `:${tunnel.remote_port}`;
}

function getTunnelTargetLabel(tunnel: ProxyConfig) {
  const target = tunnel.target;
  if (target) {
    switch (target.type) {
      case 'tcp_service':
      case 'udp_service':
        return `${target.config.ip}:${target.config.port}`;
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
  const bindIP = ingress.config.bind_ip.trim();
  if (bindIP === '0.0.0.0' || bindIP === '::') {
    return '入口绑定到通配地址，会暴露给入口 Client 所在网络。';
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
    throw new Error('带宽限制必须是非负整数');
  }
  return value;
}

export function getTunnelBandwidthSettings(tunnel: ProxyConfig): BandwidthSettings {
  return tunnel.bandwidth_settings ?? {
    ingress_bps: tunnel.ingress_bps,
    egress_bps: tunnel.egress_bps,
  };
}
