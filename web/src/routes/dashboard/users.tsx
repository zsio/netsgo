import { createRoute, lazyRouteComponent } from '@tanstack/react-router';
import { requireAdmin } from '@/lib/auth';
import { dashboardRoute } from '@/routes/dashboard';

export const dashboardUsersRoute = createRoute({
  getParentRoute: () => dashboardRoute,
  path: '/users',
  beforeLoad: requireAdmin,
  component: lazyRouteComponent(
    () => import('@/components/custom/users/UserListPage'),
    'UserListPage',
  ),
});
