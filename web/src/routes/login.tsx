import { createRoute, lazyRouteComponent } from '@tanstack/react-router';
import { rootRoute } from './__root';
import { requireLoginPage } from '@/lib/auth';

export const loginRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/login',
  beforeLoad: requireLoginPage,
  component: lazyRouteComponent(
    () => import('@/components/custom/auth/LoginPage'),
    'LoginPage',
  ),
});
