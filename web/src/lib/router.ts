import { createHashHistory, createRouter } from '@tanstack/react-router';
import { rootRoute } from '@/routes/__root';
import { indexRoute } from '@/routes/index';
import { dashboardRoute } from '@/routes/dashboard';

const routeTree = rootRoute.addChildren([indexRoute, dashboardRoute]);

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
