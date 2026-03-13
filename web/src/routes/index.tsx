import { createRoute } from '@tanstack/react-router';
import { rootRoute } from './__root';
import { redirectFromIndex } from '@/lib/auth';

export const indexRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/',
  beforeLoad: redirectFromIndex,
});
