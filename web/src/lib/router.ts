import { createHashHistory, createRouter } from '@tanstack/react-router';
import { rootRoute } from '@/routes/__root';
import { indexRoute } from '@/routes/index';
import { dashboardRoute } from '@/routes/dashboard';
import { dashboardIndexRoute } from '@/routes/dashboard/index';
import { dashboardAgentRoute } from '@/routes/dashboard/agents.$agentId';

import { loginRoute } from '@/routes/login';
import { setupRoute } from '@/routes/setup';
import { adminRoute } from '@/routes/admin';
import { adminKeysRoute } from '@/routes/admin/keys';
import { adminPoliciesRoute } from '@/routes/admin/policies';
import { adminLogsRoute } from '@/routes/admin/logs';
import { adminEventsRoute } from '@/routes/admin/events';
import { adminConfigRoute } from '@/routes/admin/config';

const adminRouteTree = adminRoute.addChildren([
  adminConfigRoute,
  adminKeysRoute,
  adminPoliciesRoute,
  adminLogsRoute,
  adminEventsRoute,
]);

const dashboardRouteTree = dashboardRoute.addChildren([
  dashboardIndexRoute,
  dashboardAgentRoute,
]);

const routeTree = rootRoute.addChildren([
  indexRoute, 
  dashboardRouteTree,
  loginRoute,
  setupRoute,
  adminRouteTree,
]);

const hashHistory = createHashHistory();

export const router = createRouter({
  routeTree,
  history: hashHistory,
});

// 类型声明 — TanStack Router 类型安全
declare module '@tanstack/react-router' {
  interface Register {
    router: typeof router;
  }
}
