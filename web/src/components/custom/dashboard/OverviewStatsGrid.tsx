import { Monitor, MonitorOff, Zap, Pause } from 'lucide-react';
import { useAgents } from '@/hooks/use-agents';
import { useServerStatus } from '@/hooks/use-server-status';
import { Skeleton } from '@/components/ui/skeleton';

export function OverviewStatsGrid() {
  const { data: agents, isLoading: agentsLoading } = useAgents();
  const { data: status, isLoading: statusLoading } = useServerStatus();

  if (agentsLoading || statusLoading) {
    return (
      <div className="grid grid-cols-1 md:grid-cols-4 gap-4">
        {[1, 2, 3, 4].map((i) => (
          <Skeleton key={i} className="h-32 rounded-xl" />
        ))}
      </div>
    );
  }

  const onlineAgents = agents?.filter((a) => a.stats !== null).length ?? 0;
  const offlineAgents = agents?.filter((a) => a.stats === null).length ?? 0;
  const activeTunnels = status?.tunnel_active ?? 0;
  const pausedOrStoppedTunnels = (status?.tunnel_paused ?? 0) + (status?.tunnel_stopped ?? 0);

  return (
    <div className="grid grid-cols-1 md:grid-cols-4 gap-4">
      {/* 在线 Agent */}
      <div className="p-4 rounded-xl border border-border/40 bg-card/50 backdrop-blur-sm shadow-sm flex flex-col justify-between hover:bg-card/80 transition-colors">
        <div className="flex items-center justify-between text-muted-foreground mb-4">
          <span className="text-sm font-medium">在线节点</span>
          <Monitor className="h-4 w-4" />
        </div>
        <div>
          <div className="text-3xl font-bold text-emerald-500">{onlineAgents}</div>
        </div>
      </div>

      {/* 离线 Agent */}
      <div className="p-4 rounded-xl border border-border/40 bg-card/50 backdrop-blur-sm shadow-sm flex flex-col justify-between hover:bg-card/80 transition-colors">
        <div className="flex items-center justify-between text-muted-foreground mb-4">
          <span className="text-sm font-medium">离线节点</span>
          <MonitorOff className="h-4 w-4" />
        </div>
        <div>
          <div className="text-3xl font-bold text-muted-foreground">{offlineAgents}</div>
        </div>
      </div>

      {/* 活跃隧道 */}
      <div className="p-4 rounded-xl border border-border/40 bg-card/50 backdrop-blur-sm shadow-sm flex flex-col justify-between hover:bg-card/80 transition-colors">
        <div className="flex items-center justify-between text-muted-foreground mb-4">
          <span className="text-sm font-medium">活跃隧道</span>
          <Zap className="h-4 w-4" />
        </div>
        <div>
          <div className="text-3xl font-bold text-primary">{activeTunnels}</div>
        </div>
      </div>

      {/* 停止/暂停隧道 */}
      <div className="p-4 rounded-xl border border-border/40 bg-card/50 backdrop-blur-sm shadow-sm flex flex-col justify-between hover:bg-card/80 transition-colors">
        <div className="flex items-center justify-between text-muted-foreground mb-4">
          <span className="text-sm font-medium">挂起隧道</span>
          <Pause className="h-4 w-4" />
        </div>
        <div>
          <div className="text-3xl font-bold text-amber-500">{pausedOrStoppedTunnels}</div>
        </div>
      </div>
    </div>
  );
}
