import { createHashHistory, createRouter } from '@tanstack/react-router';
import { rootRoute } from '@/routes/__root';
import { indexRoute } from '@/routes/index';
import { dashboardRoute } from '@/routes/dashboard';
import { dashboardIndexRoute } from '@/routes/dashboard/index';
import { dashboardClientRoute } from '@/routes/dashboard/clients.$clientId';
import { dashboardActivityRoute } from '@/routes/dashboard/activity';

import { loginRoute } from '@/routes/login';
import { adminIndexRoute, adminRoute } from '@/routes/admin';

import { adminAccessControlRoute } from '@/routes/admin/access-control';
import { adminConfigRoute } from '@/routes/admin/config';
import { adminSecurityRoute } from '@/routes/admin/security';

const adminRouteTree = adminRoute.addChildren([
  adminIndexRoute,
  adminConfigRoute,
  adminSecurityRoute,
  adminAccessControlRoute,
]);

const dashboardRouteTree = dashboardRoute.addChildren([
  dashboardIndexRoute,
  dashboardClientRoute,
  dashboardActivityRoute,
  adminRouteTree,
]);

const routeTree = rootRoute.addChildren([
  indexRoute,
  dashboardRouteTree,
  loginRoute,
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
