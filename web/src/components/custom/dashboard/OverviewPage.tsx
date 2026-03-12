import { OverviewHeader } from './OverviewHeader';
import { OverviewStatsGrid } from './OverviewStatsGrid';
import { ServerInfoCard } from './ServerInfoCard';
import { DashboardAgentTable } from './DashboardAgentTable';
import { DashboardTunnelTable } from './DashboardTunnelTable';

export function OverviewPage() {
  return (
    <div className="p-8 max-w-6xl mx-auto w-full flex flex-col gap-8 z-10">
      <OverviewHeader />
      <OverviewStatsGrid />
      <ServerInfoCard />
      <DashboardAgentTable />
      <DashboardTunnelTable />
    </div>
  );
}
