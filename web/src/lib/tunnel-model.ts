import { ApiError } from '@/lib/api';
import type {
  ProxyConfig,
  ProxyDesiredState,
  ProxyRuntimeState,
  ProxyType,
  TunnelCapabilities,
  TunnelMutationErrorResponse,
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
  remote_port: number;
  domain?: string;
}

export function buildTunnelMutationPayload(input: TunnelMutationPayloadInput) {
  const localIP = input.local_ip.trim();
  const domain = (input.domain ?? '').trim();

  if (input.type !== 'http' && (!Number.isInteger(input.remote_port) || input.remote_port < 1 || input.remote_port > 65535)) {
    throw new Error('TCP/UDP 隧道必须填写明确的公网端口');
  }

  return {
    local_ip: localIP,
    local_port: input.local_port,
    remote_port: input.type === 'http' ? 0 : input.remote_port,
    domain: input.type === 'http' ? domain : '',
  };
}

export function buildTunnelViewModel(
  tunnel: ProxyConfig,
  clientOnline: boolean,
): TunnelViewModel {
  const destinationLabel = `${tunnel.local_ip}:${tunnel.local_port}`;
  const targetLabel = tunnel.type === 'http'
    ? (tunnel.domain || '(未声明域名)')
    : `:${tunnel.remote_port}`;

  return {
    ...tunnel,
    targetLabel,
    destinationLabel,
    routeLabel: `${targetLabel} -> ${destinationLabel}`,
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
