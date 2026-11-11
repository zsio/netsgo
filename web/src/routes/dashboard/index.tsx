import { createRoute, lazyRouteComponent } from '@tanstack/react-router';
import { dashboardRoute } from '@/routes/dashboard';
import { requireConsoleAuth } from '@/lib/auth';

export const dashboardIndexRoute = createRoute({
  getParentRoute: () => dashboardRoute,
  path: '/',
  beforeLoad: requireConsoleAuth,
  component: lazyRouteComponent(
    () => import('@/components/custom/dashboard/OverviewPage'),
    'SelfOverviewPage',
  ),
});
