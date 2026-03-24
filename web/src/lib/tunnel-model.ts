import { ApiError } from '@/lib/api';
import type {
  ProxyConfig,
  ProxyDesiredState,
  ProxyRuntimeState,
  ProxyStatus,
  ProxyType,
  TunnelMutationErrorResponse,
} from '@/types';

type TunnelStatusKey = 'pending' | 'exposed' | 'offline' | 'paused' | 'stopped' | 'error';

export interface TunnelStatusPresentation {
  key: TunnelStatusKey;
  label: string;
  description?: string;
}

export interface TunnelViewModel extends Omit<ProxyConfig, 'status'> {
  rawStatus: ProxyStatus;
  targetLabel: string;
  destinationLabel: string;
  routeLabel: string;
  status: TunnelStatusPresentation;
}

export interface TunnelActionAvailability {
  canPause: boolean;
  canResume: boolean;
  canStop: boolean;
  canEdit: boolean;
  canDelete: boolean;
}

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
    rawStatus: tunnel.status,
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
  tunnel: ProxyConfig,
  clientOnline: boolean,
): TunnelActionAvailability {
  const isOffline = !clientOnline;
  const status = tunnel.status;

  return {
    canPause: status === 'active',
    canResume: status === 'paused' || status === 'stopped' || status === 'error',
    canStop: status !== 'stopped',
    canEdit: isOffline || status === 'paused' || status === 'stopped' || status === 'error',
    canDelete: isOffline || status === 'paused' || status === 'stopped' || status === 'error',
  };
}

function resolveTunnelStatus(
  tunnel: ProxyConfig,
  clientOnline: boolean,
): TunnelStatusPresentation {
  if (tunnel.desired_state && tunnel.runtime_state) {
    return resolveTunnelStatusFromStates(
      tunnel.desired_state,
      tunnel.runtime_state,
      tunnel.error,
    );
  }

  switch (tunnel.status) {
    case 'pending':
      return {
        key: 'pending',
        label: '等待建立',
        description: '等待 Client 建立公网入口',
      };
    case 'active':
      if (!clientOnline) {
        return {
          key: 'offline',
          label: '客户端离线',
          description: '配置已保存，等待 Client 上线后恢复',
        };
      }
      return {
        key: 'exposed',
        label: '已建立',
      };
    case 'paused':
      return {
        key: 'paused',
        label: '已暂停',
      };
    case 'stopped':
      return {
        key: 'stopped',
        label: '已停止',
      };
    case 'error':
      return {
        key: 'error',
        label: '异常',
        description: tunnel.error || '隧道运行异常',
      };
    default:
      return {
        key: 'error',
        label: tunnel.status,
        description: tunnel.error,
      };
  }
}

function resolveTunnelStatusFromStates(
  desiredState: ProxyDesiredState,
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
        description: '配置已保存，等待 Client 上线后恢复',
      };
    case 'idle':
      if (desiredState === 'paused') {
        return {
          key: 'paused',
          label: '已暂停',
        };
      }
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
