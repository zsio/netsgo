import { createRoute, lazyRouteComponent } from '@tanstack/react-router';
import { requireAdmin } from '@/lib/auth';
import { dashboardRoute } from '@/routes/dashboard';

export const dashboardUserClientRoute = createRoute({
  getParentRoute: () => dashboardRoute,
  path: '/users/$userId/clients/$clientId',
  beforeLoad: requireAdmin,
  component: lazyRouteComponent(
    () => import('@/components/custom/client/ClientDetailPage'),
    'ManagedUserClientDetailRoute',
  ),
});
