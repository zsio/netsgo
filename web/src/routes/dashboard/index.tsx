import { createRoute } from '@tanstack/react-router';
import { dashboardRoute } from '@/routes/dashboard';
import { OverviewPage } from '@/components/custom/dashboard/OverviewPage';

function DashboardIndexPage() {
  return <OverviewPage />;
}

export const dashboardIndexRoute = createRoute({
  getParentRoute: () => dashboardRoute,
  path: '/',
  component: DashboardIndexPage,
});
