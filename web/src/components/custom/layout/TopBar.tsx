import { useState } from 'react';
import {
  Play, Square, Settings, Network,
} from 'lucide-react';
import { Button } from '@/components/ui/button';
import { ConnectionIndicator } from '@/components/custom/common/ConnectionIndicator';
import { ConfirmDialog } from '@/components/custom/common/ConfirmDialog';
import { useAgents } from '@/hooks/use-agents';
import { useStopTunnel } from '@/hooks/use-tunnel-mutations';
import { useNavigate } from '@tanstack/react-router';

export function TopBar() {
  const navigate = useNavigate({ from: '/' });
  const [showStopAll, setShowStopAll] = useState(false);
  const { data: agents } = useAgents();
  const stopTunnel = useStopTunnel();

  const handleStopAll = () => {
    if (!agents) return;
    for (const agent of agents) {
      for (const proxy of agent.proxies ?? []) {
        if (proxy.status === 'active' || proxy.status === 'paused') {
          stopTunnel.mutate({ agentId: agent.id, tunnelName: proxy.name });
        }
      }
    }
    setShowStopAll(false);
  };

  const totalRunningTunnels = agents?.reduce(
    (sum, a) => sum + (a.proxies?.filter(p => p.status === 'active' || p.status === 'paused').length ?? 0),
    0,
  ) ?? 0;

  return (
    <>
      <header className="h-14 flex items-center justify-between px-4 border-b border-border/40 bg-background/95 backdrop-blur supports-[backdrop-filter]:bg-background/60 z-50">
        <div className="flex items-center gap-3">
          <div className="flex h-8 w-8 items-center justify-center rounded-lg bg-primary/20 text-primary">
            <Network className="h-5 w-5" />
          </div>
          <span className="font-bold text-lg tracking-tight">NetsGo</span>
          <span className="px-2 py-0.5 ml-2 text-xs font-medium rounded-full bg-muted text-muted-foreground border border-border/50">
            Console
          </span>
          <ConnectionIndicator />
        </div>

        <div className="flex items-center gap-2">
          <Button variant="secondary" size="sm" disabled title="功能开发中">
            <Play className="h-4 w-4 mr-1.5" />
            启动压测
          </Button>
          <Button
            variant="destructive"
            size="sm"
            disabled={totalRunningTunnels === 0}
            onClick={() => setShowStopAll(true)}
          >
            <Square className="h-4 w-4 mr-1.5" />
            停止全隧道
            {totalRunningTunnels > 0 && (
              <span className="ml-1.5 bg-white/20 px-1.5 rounded text-[10px]">
                {totalRunningTunnels}
              </span>
            )}
          </Button>
          <div className="w-px h-5 bg-border mx-2" />
          <Button variant="ghost" size="icon" className="text-muted-foreground hover:text-foreground" title="系统管理" onClick={() => navigate({ to: '/admin/keys' })}>
            <Settings className="h-5 w-5" />
          </Button>
        </div>
      </header>

      <ConfirmDialog
        open={showStopAll}
        title="停止所有隧道"
        description={`确认停止所有 ${totalRunningTunnels} 条运行中的隧道？此操作将断开所有通过隧道的连接。`}
        confirmLabel="全部停止"
        variant="destructive"
        onConfirm={handleStopAll}
        onCancel={() => setShowStopAll(false)}
      />
    </>
  );
}
