import { createRoute, lazyRouteComponent } from '@tanstack/react-router';
import { adminRoute } from '../admin';

export const adminSecurityRoute = createRoute({
  getParentRoute: () => adminRoute,
  path: '/security',
  component: lazyRouteComponent(
    () => import('@/components/custom/admin/security/AdminSecurityPage'),
    'AdminSecurityPage',
  ),
});
