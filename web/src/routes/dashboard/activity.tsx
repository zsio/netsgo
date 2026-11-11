import { createRoute, lazyRouteComponent } from '@tanstack/react-router';
import { dashboardRoute } from '@/routes/dashboard';
import { requireConsoleAuth } from '@/lib/auth';
import type { ActivityCategory, ActivityScope, ActivitySeverity } from '@/types';

const severityValues = new Set<ActivitySeverity>(['debug', 'info', 'warning', 'error']);
const categoryValues = new Set<ActivityCategory>(['client', 'tunnel', 'p2p', 'admin', 'security']);
const defaultSeverities: ActivitySeverity[] = ['info', 'warning', 'error'];

export interface ActivitySearch {
  scope: ActivityScope;
  client_id?: string;
  tunnel_id?: string;
  user_id?: string;
  severity: ActivitySeverity[];
  category: ActivityCategory[];
  from?: string;
  to?: string;
}

function strings(value: unknown) {
  if (Array.isArray(value)) return value.filter((entry): entry is string => typeof entry === 'string');
  return typeof value === 'string' ? [value] : [];
}

function validDate(value: unknown) {
  return typeof value === 'string' && /^\d{4}-\d{2}-\d{2}$/.test(value) && Number.isFinite(Date.parse(`${value}T00:00:00`)) ? value : undefined;
}

export function normalizeActivitySearch(search: Record<string, unknown>): ActivitySearch {
  const rawScope = search.scope;
  const clientId = typeof search.client_id === 'string' && search.client_id.trim() ? search.client_id : undefined;
  const tunnelId = typeof search.tunnel_id === 'string' && search.tunnel_id.trim() ? search.tunnel_id : undefined;
  const scope: ActivityScope = rawScope === 'client' && clientId
    ? 'client'
    : rawScope === 'tunnel' && tunnelId
      ? 'tunnel'
      : 'global';
  const severity = [...new Set(strings(search.severity).filter((entry): entry is ActivitySeverity => severityValues.has(entry as ActivitySeverity)))];
  const category = [...new Set(strings(search.category).filter((entry): entry is ActivityCategory => categoryValues.has(entry as ActivityCategory)))];
  return {
    scope,
    client_id: scope === 'client' ? clientId : undefined,
    tunnel_id: scope === 'tunnel' ? tunnelId : undefined,
    user_id: typeof search.user_id === 'string' && search.user_id.trim() ? search.user_id : undefined,
    severity: severity.length > 0 ? severity : defaultSeverities,
    category,
    from: validDate(search.from),
    to: validDate(search.to),
  };
}

export const dashboardActivityRoute = createRoute({
  getParentRoute: () => dashboardRoute,
  path: '/activity',
  validateSearch: normalizeActivitySearch,
  beforeLoad: requireConsoleAuth,
  component: lazyRouteComponent(
    () => import('@/components/custom/activity/ActivityPage'),
    'ActivityPage',
  ),
});
