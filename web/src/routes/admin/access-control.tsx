import { createRoute, lazyRouteComponent } from '@tanstack/react-router';
import { adminRoute } from '../admin';

export const adminAccessControlRoute = createRoute({
  getParentRoute: () => adminRoute,
  path: '/access-control',
  component: lazyRouteComponent(
    () => import('@/components/custom/admin/AdminAccessControlPage'),
    'AdminAccessControlPage',
  ),
});
