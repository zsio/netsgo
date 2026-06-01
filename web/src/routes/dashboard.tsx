import { createRoute, Outlet } from '@tanstack/react-router';
import { rootRoute } from './__root';
import { ClientSidebar } from '@/components/custom/client/ClientSidebar';
import { TopBar } from '@/components/custom/layout/TopBar';
import { ErrorFallback } from '@/components/custom/layout/ErrorFallback';
import { useClients } from '@/hooks/use-clients';
import { requireConsoleAuth } from '@/lib/auth';
import { SidebarProvider, SidebarInset } from '@/components/ui/sidebar';

function DashboardLayout() {
  const { data: clients, isLoading, isError, error, refetch } = useClients();

  if (isError) {
    return (
      <div className="flex flex-1 overflow-hidden">
        <ErrorFallback error={error as Error} onRetry={() => refetch()} />
      </div>
    );
  }

  return (
    <SidebarProvider className="flex-1 overflow-hidden !min-h-0 min-w-0 bg-transparent">
      <ClientSidebar clients={clients ?? []} isLoading={isLoading} />
      <SidebarInset className="flex min-w-0 flex-col overflow-hidden bg-transparent">
        <TopBar />
        <div className="relative min-w-0 flex-1 overflow-y-auto overflow-x-hidden pb-safe-bottom">
          <Outlet />
        </div>
      </SidebarInset>
    </SidebarProvider>
  );
}

export const dashboardRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/dashboard',
  beforeLoad: requireConsoleAuth,
  component: DashboardLayout,
});
