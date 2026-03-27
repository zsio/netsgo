import { resolveTunnelStatus } from '@/lib/tunnel-model';
import type { Client } from '@/types';

export interface ConsoleSummary {
  totalClients: number;
  onlineClients: number;
  offlineClients: number;
  totalTunnels: number;
  activeTunnels: number;
  inactiveTunnels: number;
  pendingTunnels: number;
  offlineTunnels: number;
  pausedTunnels: number;
  stoppedTunnels: number;
  errorTunnels: number;
}

export function summarizeConsoleClients(clients: Client[] | null | undefined): ConsoleSummary {
  const base: ConsoleSummary = {
    totalClients: 0,
    onlineClients: 0,
    offlineClients: 0,
    totalTunnels: 0,
    activeTunnels: 0,
    inactiveTunnels: 0,
    pendingTunnels: 0,
    offlineTunnels: 0,
    pausedTunnels: 0,
    stoppedTunnels: 0,
    errorTunnels: 0,
  };

  for (const client of clients ?? []) {
    base.totalClients += 1;
    if (client.online) {
      base.onlineClients += 1;
    } else {
      base.offlineClients += 1;
    }

    for (const tunnel of client.proxies ?? []) {
      base.totalTunnels += 1;

      switch (resolveTunnelStatus(tunnel, client.online).key) {
        case 'exposed':
          base.activeTunnels += 1;
          break;
        case 'pending':
          base.pendingTunnels += 1;
          break;
        case 'offline':
          base.offlineTunnels += 1;
          break;
        case 'paused':
          base.pausedTunnels += 1;
          break;
        case 'stopped':
          base.stoppedTunnels += 1;
          break;
        case 'error':
          base.errorTunnels += 1;
          break;
      }
    }
  }

  base.inactiveTunnels = base.totalTunnels - base.activeTunnels;
  return base;
}
