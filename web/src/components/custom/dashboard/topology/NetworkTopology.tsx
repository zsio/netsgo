import { useCallback, useEffect, useMemo, useState } from 'react';
import { useNavigate } from '@tanstack/react-router';
import { useTranslation } from 'react-i18next';
import {
  GitBranchPlus,
  LayersPlus,
  Maximize2,
  Undo2,
  Waypoints,
  X,
} from 'lucide-react';

import { useAddClientDialog } from '@/components/custom/client/add-client-dialog-context';
import { TunnelDialog } from '@/components/custom/tunnel/TunnelDialog';
import { Button } from '@/components/ui/button';
import { Skeleton } from '@/components/ui/skeleton';
import { useClients } from '@/hooks/use-clients';
import { useRealtimeTraffic } from '@/hooks/use-realtime-traffic';
import { useResourceBootstrap } from '@/hooks/use-server-status';
import type { ResourceScope } from '@/lib/resource-scope';
import type { Client, ClientTrafficResponse } from '@/types';
import { formatServerEndpointLabel } from './topology-layout';
import {
  buildTopologyGraph,
  buildTopologyTrafficSnapshot,
  normalizeTopologyFocusId,
} from './topology-model';
import { TopologyCanvas } from './TopologyCanvas';

export function TopologyHeaderActions({
  scope,
  activeClientId,
  clients,
  onAddClient,
}: {
  scope: ResourceScope;
  activeClientId: string | null;
  clients: Client[];
  onAddClient: () => void;
}) {
  const { t } = useTranslation();

  if (activeClientId) {
    return (
      <TunnelDialog
        scope={scope}
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
    <Button type="button" variant="secondary" size="sm" className="h-8" onClick={onAddClient}>
      <LayersPlus className="mr-1.5 h-4 w-4" />
      {t('dashboard.addClient')}
    </Button>
  );
}

export function NetworkTopology({ scope }: { scope: ResourceScope }) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const { data: clients, isLoading } = useClients(scope);
  const { data: bootstrap } = useResourceBootstrap(scope);
  const { openAddClientDialog } = useAddClientDialog();
  const [focusId, setFocusId] = useState<string | null>(null);
  const [isFullscreen, setIsFullscreen] = useState(false);

  const graph = useMemo(() => buildTopologyGraph(clients), [clients]);
  const topologyFocusId = normalizeTopologyFocusId(focusId);
  const topologyFocusNode = useMemo(
    () => (topologyFocusId ? graph.nodes.find((node) => node.id === topologyFocusId) : undefined),
    [graph, topologyFocusId],
  );
  const activeTopologyFocusId = topologyFocusNode?.id ?? null;

  const onlineClientIdSet = useMemo(
    () => new Set((clients ?? []).filter((client) => client.online).map((client) => client.id)),
    [clients],
  );
  const totalClientCount = (clients ?? []).length;
  const activeTunnelCount = useMemo(
    () => graph.edges.filter((edge) => edge.status.key === 'exposed').length,
    [graph.edges],
  );
  const serverEndpoint = useMemo(
    () => formatServerEndpointLabel(bootstrap?.server_addr),
    [bootstrap?.server_addr],
  );

  const { data: realtimeTraffic } = useRealtimeTraffic(scope, {
    enabled: onlineClientIdSet.size > 0,
  });
  const trafficByClientId = useMemo(() => {
    const traffic = new Map<string, ClientTrafficResponse | undefined>();
    for (const item of realtimeTraffic?.clients ?? []) {
      if (!onlineClientIdSet.has(item.client_id)) {
        continue;
      }
      traffic.set(item.client_id, {
        resolution: item.resolution,
        items: item.items ?? [],
      });
    }
    return traffic;
  }, [onlineClientIdSet, realtimeTraffic]);

  const trafficSnapshot = useMemo(
    () => buildTopologyTrafficSnapshot(graph, trafficByClientId),
    [graph, trafficByClientId],
  );

  const handleOpenClientDetail = useCallback(
    (clientId: string) => {
      if (scope.kind === 'admin-user') {
        navigate({
          to: '/dashboard/users/$userId/clients/$clientId',
          params: { userId: scope.userId, clientId },
        });
        return;
      }
      navigate({
        to: '/dashboard/clients/$clientId',
        params: { clientId },
      });
    },
    [navigate, scope],
  );

  useEffect(() => {
    if (!isFullscreen && !activeTopologyFocusId) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== 'Escape' || e.defaultPrevented || document.querySelector('[role="dialog"]')) {
        return;
      }
      if (isFullscreen) {
        setIsFullscreen(false);
      } else if (activeTopologyFocusId) {
        setFocusId(null);
      }
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [activeTopologyFocusId, isFullscreen]);

  if (isLoading) {
    return <Skeleton className="h-[280px] w-full rounded-xl" />;
  }

  const hasClients = graph.nodes.some((node) => node.kind === 'client');

  const canvasEl = hasClients ? (
    <TopologyCanvas
      scope={scope}
      clients={clients ?? []}
      serverEndpoint={serverEndpoint}
      onlineClientCount={onlineClientIdSet.size}
      totalClientCount={totalClientCount}
      graph={graph}
      trafficSnapshot={trafficSnapshot}
      focusId={activeTopologyFocusId}
      onFocusChange={setFocusId}
      onOpenClientDetail={handleOpenClientDetail}
      isFullscreen={isFullscreen}
    />
  ) : null;

  const renderHeaderTitle = () => {
    if (topologyFocusNode?.kind !== 'client') {
      return <span className="truncate text-sm font-semibold">{t('dashboard.topologyTitle')}</span>;
    }
    return (
      <span className="flex min-w-0 items-center gap-1.5 text-sm">
        <button
          type="button"
          className="shrink-0 font-medium text-muted-foreground transition-colors hover:text-foreground"
          onClick={() => setFocusId(null)}
        >
          {t('dashboard.topologyTitle')}
        </button>
        <span className="text-muted-foreground/50">/</span>
        <button
          type="button"
          className="min-w-0 truncate border-b border-dashed border-current/45 font-semibold text-foreground transition-colors hover:text-foreground/80"
          onClick={() => handleOpenClientDetail(topologyFocusNode.id)}
          title={topologyFocusNode.label}
        >
          {topologyFocusNode.label}
        </button>
      </span>
    );
  };

  const summaryPills = hasClients ? (
    <div className="hidden items-center gap-1.5 sm:flex">
      <span className="inline-flex items-center gap-1.5 rounded-full border border-border/60 bg-muted/40 px-2.5 py-0.5 text-[11px] font-medium text-muted-foreground">
        <span className="size-1.5 rounded-full bg-emerald-500" />
        {t('dashboard.topologyNodeCount', {
          online: onlineClientIdSet.size,
          total: totalClientCount,
        })}
      </span>
      <span className="inline-flex items-center gap-1.5 rounded-full border border-border/60 bg-muted/40 px-2.5 py-0.5 text-[11px] font-medium text-muted-foreground">
        {t('dashboard.topologyTunnelCount', {
          active: activeTunnelCount,
          total: graph.edges.length,
        })}
      </span>
    </div>
  ) : null;

  return (
    <>
      <div className="overflow-hidden rounded-xl border border-border/60 bg-card shadow-2xs">
        <div className="flex items-center justify-between gap-3 border-b border-border/50 bg-card px-4 py-3 sm:px-5">
          <div className="flex min-w-0 items-center gap-2.5">
            <span className="flex size-7 shrink-0 items-center justify-center rounded-lg border border-border/70 bg-muted/60 text-foreground">
              <Waypoints className="size-4" />
            </span>
            <h3 className="flex min-w-0 items-center text-foreground">
              {renderHeaderTitle()}
            </h3>
            {summaryPills}
          </div>

          <div className="flex shrink-0 items-center gap-2">
            {activeTopologyFocusId ? (
              <Button
                type="button"
                variant="ghost"
                size="sm"
                className="hidden h-8 gap-1.5 px-2.5 text-xs text-muted-foreground sm:inline-flex"
                onClick={() => setFocusId(null)}
              >
                <Undo2 className="size-3.5" />
                {t('dashboard.topologyBack')}
              </Button>
            ) : null}
            <TopologyHeaderActions
              scope={scope}
              activeClientId={activeTopologyFocusId}
              clients={clients ?? []}
              onAddClient={openAddClientDialog}
            />
            {hasClients ? (
              <Button
                type="button"
                variant="outline"
                size="icon"
                className="size-8 shrink-0 text-muted-foreground hover:text-foreground"
                onClick={() => setIsFullscreen(true)}
                aria-label={t('dashboard.topologyFullscreen')}
                title={t('dashboard.topologyFullscreen')}
              >
                <Maximize2 className="size-3.5" />
              </Button>
            ) : null}
          </div>
        </div>

        {hasClients ? (
          !isFullscreen && canvasEl
        ) : (
          <div className="flex flex-col items-center justify-center gap-4 bg-muted/20 py-14 text-muted-foreground dark:bg-zinc-950/40">
            <Waypoints className="h-10 w-10 opacity-25" />
            <p className="text-sm">{t('dashboard.noClients')}</p>
            <Button type="button" variant="secondary" size="sm" onClick={openAddClientDialog}>
              <LayersPlus className="mr-1.5 h-4 w-4" />
              {t('dashboard.addClient')}
            </Button>
          </div>
        )}
      </div>

      {isFullscreen && hasClients && (
        <div className="fixed inset-0 z-50 flex flex-col bg-background">
          <div className="flex items-center justify-between gap-3 border-b border-border/50 bg-card px-4 py-2.5 sm:px-5">
            <div className="flex min-w-0 items-center gap-2.5">
              <span className="flex size-7 shrink-0 items-center justify-center rounded-lg border border-border/70 bg-muted/60 text-foreground">
                <Waypoints className="size-4" />
              </span>
              <h3 className="flex min-w-0 items-center text-foreground">
                {renderHeaderTitle()}
              </h3>
              {summaryPills}
            </div>
            <div className="flex items-center gap-2">
              {activeTopologyFocusId ? (
                <Button
                  type="button"
                  variant="ghost"
                  size="sm"
                  className="h-8 gap-1.5 px-2.5 text-xs text-muted-foreground"
                  onClick={() => setFocusId(null)}
                >
                  <Undo2 className="size-3.5" />
                  {t('dashboard.topologyBack')}
                </Button>
              ) : null}
              <TopologyHeaderActions
                scope={scope}
                activeClientId={activeTopologyFocusId}
                clients={clients ?? []}
                onAddClient={openAddClientDialog}
              />
              <Button
                type="button"
                variant="secondary"
                size="sm"
                className="h-8 gap-1.5 px-2.5 text-xs"
                onClick={() => setIsFullscreen(false)}
              >
                <X className="h-3.5 w-3.5" />
                {t('dashboard.topologyExitFullscreen')}
              </Button>
            </div>
          </div>
          <div className="flex min-h-0 flex-1 flex-col">
            {canvasEl}
          </div>
        </div>
      )}
    </>
  );
}
