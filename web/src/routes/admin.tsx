import { createRoute, Outlet } from '@tanstack/react-router';
import { dashboardRoute } from './dashboard';

export const adminRoute = createRoute({
  getParentRoute: () => dashboardRoute,
  path: '/admin',
  component: AdminLayout,
});

function AdminLayout() {
  return (
    <div className="p-8 max-w-5xl mx-auto w-full flex flex-col gap-6 z-10">
      <Outlet />
    </div>
  );
}
