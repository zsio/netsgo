import type { Client } from '@/types';

/**
 * 获取客户端展示名：优先使用自定义展示名，否则使用机器名
 */
export function getClientDisplayName(client: Client): string {
  return client.display_name || client.info.hostname;
}
