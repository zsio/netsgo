import { useEffect, useMemo } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import type { QueryClient } from '@tanstack/react-query';
import { useRouterState } from '@tanstack/react-router';
import { scopedEventStreamPath, type ActivityReadScope } from '@/lib/api';
import { useConnectionStore } from '@/stores/connection-store';
import type { ConnectionStatus } from '@/stores/connection-store';
import { useAuthStore } from '@/stores/auth-store';
import { clearClientSessionAndRedirect } from '@/lib/session';
import { useDashboardResourceScope, useDashboardSidebarScope } from '@/hooks/use-dashboard-scope';
import { SELF_RESOURCE_SCOPE, type ResourceScope } from '@/lib/resource-scope';
import { createActivityRecoveryState } from './event-stream/activity-recovery';
import { applyEventForDiagnostics } from './event-stream/apply-event';
import { parseSSE } from './event-stream/guards';
import {
  createEventStreamSnapshotState,
  resyncConsoleSnapshotSafely,
} from './event-stream/snapshot';

export type { EventStreamDiagnostics, EventStreamSnapshotState } from './event-stream/snapshot';
export type { ActivityRecoveryState } from './event-stream/activity-recovery';
export { createEventStreamSnapshotState } from './event-stream/snapshot';
export { createActivityRecoveryState, recoverActivityGap } from './event-stream/activity-recovery';
export { applyEventForDiagnostics } from './event-stream/apply-event';

export function resolveEventStreamScope(
  resourceScope: ResourceScope | null,
  isAdmin: boolean,
  pathname: string,
  selectedActivityUserId?: string,
): ActivityReadScope | null {
  if (!pathname.startsWith('/dashboard')) return null;
  if (resourceScope) return resourceScope;
  if (!isAdmin) return null;
  if (pathname.startsWith('/dashboard/activity') && selectedActivityUserId) {
    return { kind: 'admin-user', userId: selectedActivityUserId };
  }
  return { kind: 'admin-global' };
}

function eventStreamScopesEqual(left: ActivityReadScope, right: ActivityReadScope) {
  return left.kind === right.kind
    && (left.kind !== 'admin-user' || (right.kind === 'admin-user' && left.userId === right.userId));
}

export function resolveEventStreamScopes(
  resourceScope: ResourceScope | null,
  sidebarScope: ResourceScope | null,
  isAdmin: boolean,
  pathname: string,
  selectedActivityUserId?: string,
) {
  const primary = resolveEventStreamScope(resourceScope, isAdmin, pathname, selectedActivityUserId);
  const secondary = primary && sidebarScope && !eventStreamScopesEqual(primary, sidebarScope)
    ? sidebarScope
    : null;
  const global = isAdmin
    && pathname.startsWith('/dashboard/users/')
    && primary?.kind !== 'admin-global'
    ? { kind: 'admin-global' as const }
    : null;
  return { primary, secondary, global };
}

const ignoreConnectionStatus: (status: ConnectionStatus) => void = () => undefined;

function useScopedEventStream(
  queryClient: QueryClient,
  eventScope: ActivityReadScope | null,
  shouldConnect: boolean,
  setStatus: (status: ConnectionStatus) => void,
) {
  useEffect(() => {
    if (!shouldConnect || !eventScope) {
      setStatus('disconnected');
      return;
    }

    const snapshotState = createEventStreamSnapshotState();
    const activityState = createActivityRecoveryState();
    let cancelled = false;
    let activeController: AbortController | null = null;
    let hasConnected = false;

    const connect = async () => {
      while (!cancelled) {
        activeController = new AbortController();

        try {
          const isReconnect = hasConnected;
          setStatus(hasConnected ? 'reconnecting' : 'connecting');

          const response = await fetch(scopedEventStreamPath(eventScope), {
            method: 'GET',
            headers: {
              Accept: 'text/event-stream',
            },
            credentials: 'same-origin',
            signal: activeController.signal,
          });

          if (response.status === 401) {
            clearClientSessionAndRedirect();
            setStatus('disconnected');
            return;
          }

          if (!response.ok || !response.body) {
            throw new Error(`event stream failed: ${response.status}`);
          }

          if (isReconnect && eventScope.kind !== 'admin-global') {
            await resyncConsoleSnapshotSafely(queryClient, eventScope, snapshotState, setStatus);
          }

          hasConnected = true;
          setStatus('connected');

          const reader = response.body.getReader();
          const decoder = new TextDecoder();
          let buffer = '';

          while (!cancelled) {
            const { done, value } = await reader.read();
            if (cancelled) {
              return;
            }
            if (done) {
              throw new Error('event stream closed');
            }

            buffer += decoder.decode(value, { stream: true });
            buffer = parseSSE(buffer, (eventType, data) => applyEventForDiagnostics(
              queryClient,
              setStatus,
              snapshotState,
              eventType,
              data,
              activityState,
              eventScope,
            ));
          }
        } catch (error) {
          if (cancelled) {
            return;
          }
          if (error instanceof DOMException && error.name === 'AbortError') {
            return;
          }

          setStatus(hasConnected ? 'reconnecting' : 'connecting');
          await new Promise((resolve) => window.setTimeout(resolve, 1000));
        }
      }
    };

    void connect();

    return () => {
      cancelled = true;
      activityState.cancelled = true;
      if (activityState.retryTimer) clearTimeout(activityState.retryTimer);
      activeController?.abort();
      setStatus('disconnected');
    };
  }, [queryClient, eventScope, setStatus, shouldConnect]);
}

export function useEventStream() {
  const queryClient = useQueryClient();
  const setStatus = useConnectionStore((state) => state.setStatus);
  const isAuthenticated = useAuthStore((state) => state.isAuthenticated);
  const principal = useAuthStore((state) => state.user);
  const pathname = useRouterState({ select: (state) => state.location.pathname });
  const selectedActivityUserId = useRouterState({
    select: (state) => {
      const value = state.location.search.user_id;
      return typeof value === 'string' && value.length > 0 ? value : undefined;
    },
  });
  const resourceScope = useDashboardResourceScope();
  const sidebarScope = useDashboardSidebarScope();
  const resourceScopeKind = resourceScope?.kind;
  const resourceScopeUserId = resourceScope?.kind === 'admin-user' ? resourceScope.userId : undefined;
  const sidebarScopeKind = sidebarScope?.kind;
  const sidebarScopeUserId = sidebarScope?.kind === 'admin-user' ? sidebarScope.userId : undefined;
  const eventScopes = useMemo(() => {
    const stableResourceScope = resourceScopeKind === 'self'
      ? SELF_RESOURCE_SCOPE
      : resourceScopeKind === 'admin-user' && resourceScopeUserId
        ? { kind: 'admin-user' as const, userId: resourceScopeUserId }
        : null;
    const stableSidebarScope = sidebarScopeKind === 'self'
      ? SELF_RESOURCE_SCOPE
      : sidebarScopeKind === 'admin-user' && sidebarScopeUserId
        ? { kind: 'admin-user' as const, userId: sidebarScopeUserId }
        : null;
    return resolveEventStreamScopes(
      stableResourceScope,
      stableSidebarScope,
      principal?.is_admin === true,
      pathname,
      selectedActivityUserId,
    );
  }, [
    pathname,
    principal?.is_admin,
    resourceScopeKind,
    resourceScopeUserId,
    selectedActivityUserId,
    sidebarScopeKind,
    sidebarScopeUserId,
  ]);

  useScopedEventStream(queryClient, eventScopes.primary, isAuthenticated, setStatus);
  useScopedEventStream(queryClient, eventScopes.secondary, isAuthenticated, ignoreConnectionStatus);
  useScopedEventStream(queryClient, eventScopes.global, isAuthenticated, ignoreConnectionStatus);
}
