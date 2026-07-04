import { useEffect, useMemo, useState } from 'react';
import { useNavigate } from '@tanstack/react-router';
import { useQueries } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { GitBranchPlus, LayersPlus, Waypoints, X } from 'lucide-react';

import { Skeleton } from '@/components/ui/skeleton';
import { Button } from '@/components/ui/button';
import { useClients } from '@/hooks/use-clients';
import { buildClientTrafficQueryKey, buildClientTrafficUrl } from '@/hooks/use-client-traffic';
import { useAddClientDialog } from '@/components/custom/client/add-client-dialog-context';
import { TunnelDialog } from '@/components/custom/tunnel/TunnelDialog';
import { api } from '@/lib/api';
import type { Client, ClientTrafficResponse } from '@/types';
import {
  buildTopologyGraph,
  buildTopologyTrafficSnapshot,
  normalizeTopologyFocusId,
} from './topology-model';
import { TopologyCanvas } from './TopologyCanvas';
import { TopologySidePanel } from './TopologySidePanel';

export function TopologyHeaderActions({
  activeClientId,
  clients,
  onAddClient,
}: {
  activeClientId: string | null;
  clients: Client[];
  onAddClient: () => void;
}) {
  const { t } = useTranslation();

  if (activeClientId) {
    return (
      <TunnelDialog
        mode="create"
        clientId={activeClientId}
        clients={clients}
        trigger={(
          <Button type="button" variant="secondary" size="sm" className="h-8 gap-1.5 px-2.5">
            <GitBranchPlus className="h-4 w-4" />
            {t('tunnels.addTunnel')}
          </Button>
        )}
      />
    );
  }

  return (
    <Button type="button" variant="secondary" size="sm" className="h-8 gap-1.5 px-2.5" onClick={onAddClient}>
      <LayersPlus className="h-4 w-4" />
      {t('dashboard.addClient')}
    </Button>
  );
}

export function NetworkTopology() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const { data: clients, isLoading } = useClients();
  const { openAddClientDialog } = useAddClientDialog();
  const [focusId, setFocusId] = useState<string | null>(null);
  const [hoveredTunnelId, setHoveredTunnelId] = useState<string | null>(null);
  const [isFullscreen, setIsFullscreen] = useState(false);

  const graph = useMemo(() => buildTopologyGraph(clients), [clients]);
  const topologyFocusId = normalizeTopologyFocusId(focusId);
  const topologyFocusNode = useMemo(
    () => (topologyFocusId ? graph.nodes.find((node) => node.id === topologyFocusId) : undefined),
    [graph, topologyFocusId],
  );
  const activeTopologyFocusId = topologyFocusNode?.id ?? null;
  const topologyHeaderTitle = topologyFocusNode?.label ?? t('dashboard.topologyOverviewHeader');
  const onlineClientIds = useMemo(
    () => (clients ?? [])
      .filter((client) => client.online)
      .map((client) => client.id)
      .sort(),
    [clients],
  );
  const trafficQueries = useQueries({
    queries: onlineClientIds.map((clientId) => ({
      queryKey: buildClientTrafficQueryKey(clientId, '60s'),
      queryFn: () => api.get<ClientTrafficResponse>(buildClientTrafficUrl(clientId, '60s')),
      staleTime: 30_000,
      refetchInterval: 10_000,
      refetchOnWindowFocus: false,
    })),
  });
  const trafficByClientId = useMemo(() => {
    const traffic = new Map<string, ClientTrafficResponse | undefined>();
    onlineClientIds.forEach((clientId, index) => {
      traffic.set(clientId, trafficQueries[index]?.data);
    });
    return traffic;
  }, [onlineClientIds, trafficQueries]);
  const trafficSnapshot = useMemo(
    () => buildTopologyTrafficSnapshot(graph, trafficByClientId),
    [graph, trafficByClientId],
  );

  useEffect(() => {
    if (!isFullscreen) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') setIsFullscreen(false);
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [isFullscreen]);

  if (isLoading) {
    return <Skeleton className="h-[460px] w-full rounded-xl" />;
  }

  const hasClients = graph.nodes.some((node) => node.kind === 'client');

  const canvasEl = hasClients && (
    <TopologyCanvas
      graph={graph}
      trafficSnapshot={trafficSnapshot}
      focusId={activeTopologyFocusId}
      hoveredTunnelId={hoveredTunnelId}
      onFocusChange={setFocusId}
      isFullscreen={isFullscreen}
      onToggleFullscreen={() => setIsFullscreen((v) => !v)}
    />
  );

  const panelEl = hasClients && topologyFocusNode?.kind === 'client' && (
    <TopologySidePanel
      graph={graph}
      trafficSnapshot={trafficSnapshot}
      focusId={topologyFocusNode.id}
      hoveredTunnelId={hoveredTunnelId}
      onHoverTunnel={setHoveredTunnelId}
    />
  );
  const renderHeaderTitle = () => {
    if (topologyFocusNode?.kind !== 'client') {
      return <span className="truncate">{topologyHeaderTitle}</span>;
    }
    return (
      <button
        type="button"
        className="min-w-0 truncate border-b border-dashed border-current/50 text-left transition-colors hover:text-primary"
        onClick={() => navigate({
          to: '/dashboard/clients/$clientId',
          params: { clientId: topologyFocusNode.id },
        })}
        title={topologyHeaderTitle}
      >
        {topologyHeaderTitle}
      </button>
    );
  };

  return (
    <>
      <div className="rounded-xl border border-border/40 bg-card/50 backdrop-blur-sm shadow-sm overflow-hidden">
        <div className="px-4 sm:px-6 py-3 sm:py-4 border-b border-border/40 bg-muted/20 flex items-center justify-between gap-3">
          <h3 className="flex min-w-0 items-center gap-2 font-semibold text-foreground">
            <Waypoints className="h-5 w-5 text-primary" />
            {renderHeaderTitle()}
          </h3>
          <TopologyHeaderActions
            activeClientId={activeTopologyFocusId}
            clients={clients ?? []}
            onAddClient={openAddClientDialog}
          />
        </div>

        {hasClients ? (
          <div className="flex min-h-0 flex-col lg:h-[460px] lg:flex-row">
            {!isFullscreen && canvasEl}
            {!isFullscreen && panelEl}
          </div>
        ) : (
          <div className="flex flex-col items-center justify-center gap-4 py-16 text-muted-foreground">
            <Waypoints className="h-12 w-12 opacity-20" />
            <p className="text-sm">{t('dashboard.noClients')}</p>
            <Button type="button" variant="secondary" size="sm" onClick={openAddClientDialog}>
              <LayersPlus className="mr-1.5 h-4 w-4" />
              {t('dashboard.addClient')}
            </Button>
          </div>
        )}
      </div>

      {isFullscreen && hasClients && (
        <div className="fixed inset-0 z-50 flex flex-col bg-card/95 backdrop-blur-md">
          <div className="flex items-center justify-between gap-3 border-b border-border/40 bg-muted/20 px-4 py-2.5 sm:px-6">
            <h3 className="flex min-w-0 items-center gap-2 font-semibold text-foreground">
              <Waypoints className="h-5 w-5 text-primary" />
              {renderHeaderTitle()}
            </h3>
            <Button
              type="button"
              variant="secondary"
              size="sm"
              className="h-7 gap-1.5 px-2.5 text-xs"
              onClick={() => setIsFullscreen(false)}
            >
              <X className="h-3.5 w-3.5" />
              {t('dashboard.topologyExitFullscreen')}
            </Button>
          </div>
          <div className="flex min-h-0 flex-1 flex-col lg:flex-row">
            {canvasEl}
            {panelEl && <div className="hidden min-h-0 lg:flex">{panelEl}</div>}
          </div>
        </div>
      )}
    </>
  );
}
