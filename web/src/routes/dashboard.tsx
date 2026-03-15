import { createRoute, Outlet } from '@tanstack/react-router';
import { rootRoute } from './__root';
import { ClientSidebar } from '@/components/custom/client/ClientSidebar';
import { ErrorFallback } from '@/components/custom/layout/ErrorFallback';
import { useClients } from '@/hooks/use-clients';
import { requireConsoleAuth } from '@/lib/auth';

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
    <div className="flex flex-1 overflow-hidden">
      <ClientSidebar clients={clients ?? []} isLoading={isLoading} />

      <main className="flex-1 flex flex-col overflow-y-auto bg-background/50 relative">
        {/* Subtle background glow */}
        <div className="absolute top-0 left-1/4 w-[500px] h-[500px] bg-primary/10 rounded-full blur-3xl pointer-events-none" />
        <Outlet />
      </main>
    </div>
  );
}

export const dashboardRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/dashboard',
  beforeLoad: requireConsoleAuth,
  component: DashboardLayout,
});
