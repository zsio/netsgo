import { createRoute, lazyRouteComponent, redirect } from '@tanstack/react-router';
import { dashboardRoute } from './dashboard';
import { requireAdmin } from '@/lib/auth';

export const adminRoute = createRoute({
  getParentRoute: () => dashboardRoute,
  path: '/admin',
  beforeLoad: requireAdmin,
  component: lazyRouteComponent(
    () => import('@/components/custom/admin/AdminLayout'),
    'AdminLayout',
  ),
});

export const adminIndexRoute = createRoute({
  getParentRoute: () => adminRoute,
  path: '/',
  beforeLoad: () => {
    throw redirect({ to: '/dashboard/admin/config' });
  },
});
