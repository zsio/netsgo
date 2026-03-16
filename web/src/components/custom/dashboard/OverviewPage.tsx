import { OverviewHeader } from './OverviewHeader';
import { ServerInfoCard } from './ServerInfoCard';
import { DashboardClientTable } from './DashboardClientTable';
import { DashboardTunnelTable } from './DashboardTunnelTable';

export function OverviewPage() {
  return (
    <div className="p-8 max-w-6xl mx-auto w-full flex flex-col gap-8 z-10">
      <OverviewHeader />
      <ServerInfoCard />
      <DashboardClientTable />
      <DashboardTunnelTable />
    </div>
  );
}
