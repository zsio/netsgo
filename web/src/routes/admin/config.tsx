import { createRoute, lazyRouteComponent } from '@tanstack/react-router';
import { adminRoute } from '../admin';

export const adminConfigRoute = createRoute({
  getParentRoute: () => adminRoute,
  path: '/config',
  component: lazyRouteComponent(
    () => import('@/components/custom/admin/AdminConfigPage'),
    'AdminConfigPage',
  ),
});
