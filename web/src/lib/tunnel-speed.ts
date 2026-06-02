import type { ProxyConfig } from '@/types';

export function buildTunnelSpeedFilter(tunnel: ProxyConfig) {
  return [tunnel];
}
