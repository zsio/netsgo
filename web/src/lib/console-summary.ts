import { resolveTunnelStatus } from '@/lib/tunnel-model';
import type { Client, ConsoleSummary } from '@/types';

export const EMPTY_CONSOLE_SUMMARY: ConsoleSummary = {
  total_clients: 0,
  online_clients: 0,
  offline_clients: 0,
  total_tunnels: 0,
  active_tunnels: 0,
  inactive_tunnels: 0,
  pending_tunnels: 0,
  offline_tunnels: 0,
  stopped_tunnels: 0,
  error_tunnels: 0,
};

export function summarizeConsoleClients(clients: Client[] | null | undefined): ConsoleSummary {
  const base: ConsoleSummary = { ...EMPTY_CONSOLE_SUMMARY };

  for (const client of clients ?? []) {
    base.total_clients += 1;
    if (client.online) {
      base.online_clients += 1;
    } else {
      base.offline_clients += 1;
    }

    for (const tunnel of client.proxies ?? []) {
      base.total_tunnels += 1;

      switch (resolveTunnelStatus(tunnel, client.online).key) {
        case 'exposed':
          base.active_tunnels += 1;
          break;
        case 'pending':
          base.pending_tunnels += 1;
          break;
        case 'offline':
          base.offline_tunnels += 1;
          break;
        case 'stopped':
          base.stopped_tunnels += 1;
          break;
        case 'error':
          base.error_tunnels += 1;
          break;
      }
    }
  }

  base.inactive_tunnels = base.total_tunnels - base.active_tunnels;
  return base;
}
