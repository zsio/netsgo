import { ApiError } from '@/lib/api';
import type {
  ProxyConfig,
  ProxyStatus,
  ProxyType,
  TunnelMutationErrorResponse,
} from '@/types';

type TunnelStatusKey = ProxyStatus | 'unavailable';

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

interface TunnelMutationPayloadInput {
  type: ProxyType;
  local_ip: string;
  local_port: number;
  remote_port?: number;
  domain?: string;
}

export function buildTunnelMutationPayload(input: TunnelMutationPayloadInput) {
  const localIP = input.local_ip.trim();
  const domain = (input.domain ?? '').trim();

  return {
    local_ip: localIP,
    local_port: input.local_port,
    remote_port: input.type === 'http' ? 0 : input.remote_port ?? 0,
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

function resolveTunnelStatus(
  tunnel: ProxyConfig,
  clientOnline: boolean,
): TunnelStatusPresentation {
  switch (tunnel.status) {
    case 'pending':
      return {
        key: 'pending',
        label: '等待就绪',
        description: '等待 Client 接受新配置',
      };
    case 'active':
      if (!clientOnline) {
        return {
          key: 'unavailable',
          label: '不可服务',
          description: 'Client 离线，当前无法接收请求',
        };
      }
      return {
        key: 'active',
        label: '运行中',
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
