import { Outlet } from '@tanstack/react-router';
import { ClientSidebar } from '@/components/custom/client/ClientSidebar';
import { TopBar } from '@/components/custom/layout/TopBar';
import { ErrorFallback } from '@/components/custom/layout/ErrorFallback';
import { useClients } from '@/hooks/use-clients';
import { useDashboardResourceScope, useDashboardSidebarScope } from '@/hooks/use-dashboard-scope';
import { SidebarProvider, SidebarInset } from '@/components/ui/sidebar';
import { AddClientDialogProvider } from '@/components/custom/client/AddClientDialogProvider';

export function DashboardLayout() {
  const scope = useDashboardResourceScope();
  const sidebarScope = useDashboardSidebarScope();
  const { data: clients, isLoading, isError, error, refetch } = useClients(sidebarScope);

  if (isError) {
    return (
      <div className="flex flex-1 overflow-hidden">
        <ErrorFallback error={error as Error} onRetry={() => refetch()} />
      </div>
    );
  }

  return (
    <AddClientDialogProvider scope={sidebarScope}>
      <SidebarProvider className="flex-1 overflow-hidden !min-h-0 min-w-0 bg-transparent">
        <ClientSidebar scope={sidebarScope} clients={clients ?? []} isLoading={isLoading} />
        <SidebarInset className="flex min-w-0 flex-col overflow-hidden bg-transparent">
          <TopBar scope={scope} />
          <div className="relative min-w-0 flex-1 overflow-y-auto overflow-x-hidden pb-safe-bottom">
            <Outlet />
          </div>
        </SidebarInset>
      </SidebarProvider>
    </AddClientDialogProvider>
  );
}
