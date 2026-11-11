import { createRoute, lazyRouteComponent } from '@tanstack/react-router';
import { requireConsoleAuth } from '@/lib/auth';
import { dashboardRoute } from '@/routes/dashboard';

export const dashboardWebhooksRoute = createRoute({
  getParentRoute: () => dashboardRoute,
  path: '/webhooks',
  beforeLoad: requireConsoleAuth,
  component: lazyRouteComponent(
    () => import('@/components/custom/webhooks/WebhooksPage'),
    'WebhooksPage',
  ),
});
