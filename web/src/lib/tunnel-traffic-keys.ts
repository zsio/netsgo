import type { ClientTrafficResponse, ProxyConfig } from '@/types';

type TunnelIdentity = Pick<ProxyConfig, 'name' | 'type'> & Partial<Pick<ProxyConfig, 'id'>>;
type TrafficItem = ClientTrafficResponse['items'][number];

export function getTunnelSeriesKey(tunnel: TunnelIdentity) {
  if (tunnel.id) {
    return `id:${tunnel.id}`;
  }
  return `${tunnel.type}:${tunnel.name}`;
}

export function getTrafficSeriesKey(item: TrafficItem) {
  if (item.tunnel_id) {
    return `id:${item.tunnel_id}`;
  }
  if (item.tunnel_name && item.tunnel_type) {
    return `${item.tunnel_type}:${item.tunnel_name}`;
  }
  return 'metadata_missing';
}
