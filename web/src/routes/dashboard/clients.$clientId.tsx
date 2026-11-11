import { createRoute, lazyRouteComponent } from '@tanstack/react-router';
import { dashboardRoute } from '@/routes/dashboard';
import { requireConsoleAuth } from '@/lib/auth';

export const dashboardClientRoute = createRoute({
  getParentRoute: () => dashboardRoute,
  path: '/clients/$clientId',
  beforeLoad: requireConsoleAuth,
  component: lazyRouteComponent(
    () => import('@/components/custom/client/ClientDetailPage'),
    'SelfClientDetailRoute',
  ),
});
