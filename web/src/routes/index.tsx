import { createRoute, redirect, isRedirect } from '@tanstack/react-router';
import { rootRoute } from './__root';
import type { SetupStatus } from '@/types';

export const indexRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/',
  beforeLoad: async () => {
    try {
      // 检查服务是否已初始化
      const resp = await fetch('/api/setup/status');
      const data: SetupStatus = await resp.json();
      if (!data.initialized) {
        throw redirect({ to: '/setup' });
      }
    } catch (err) {
      // 如果是 redirect 则重新抛出
      if (isRedirect(err)) throw err;
      // 网络错误等情况直接进 dashboard
    }
    throw redirect({ to: '/dashboard' });
  },
});
