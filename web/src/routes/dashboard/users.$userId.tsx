import { createRoute, lazyRouteComponent } from '@tanstack/react-router';
import type { DashboardTab } from '@/components/custom/dashboard/OverviewPage';
import { requireAdmin } from '@/lib/auth';
import { dashboardRoute } from '@/routes/dashboard';

export type UserWorkspaceSearch = { tab?: DashboardTab };

export function normalizeUserWorkspaceSearch(search: Record<string, unknown>): UserWorkspaceSearch {
  const tab = search.tab;
  return tab === 'topology' || tab === 'clients' || tab === 'tunnels' ? { tab } : {};
}

export const dashboardUserWorkspaceRoute = createRoute({
  getParentRoute: () => dashboardRoute,
  path: '/users/$userId',
  validateSearch: normalizeUserWorkspaceSearch,
  beforeLoad: requireAdmin,
  component: lazyRouteComponent(
    () => import('@/components/custom/users/UserWorkspacePage'),
    'UserWorkspacePage',
  ),
});
