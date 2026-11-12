import { createRoute, lazyRouteComponent } from '@tanstack/react-router';
import { requireAdmin } from '@/lib/auth';
import { dashboardRoute } from '@/routes/dashboard';

export const dashboardUserWorkspaceRoute = createRoute({
  getParentRoute: () => dashboardRoute,
  path: '/users/$userId',
  beforeLoad: requireAdmin,
  component: lazyRouteComponent(
    () => import('@/components/custom/users/UserWorkspacePage'),
    'UserWorkspacePage',
  ),
});
