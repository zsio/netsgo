import { createRoute, lazyRouteComponent } from '@tanstack/react-router';
import { rootRoute } from './__root';
import { requireConsoleAuth } from '@/lib/auth';

export const dashboardRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/dashboard',
  beforeLoad: requireConsoleAuth,
  component: lazyRouteComponent(
    () => import('@/components/custom/layout/DashboardLayout'),
    'DashboardLayout',
  ),
});
