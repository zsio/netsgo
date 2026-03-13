import { Button } from '@/components/ui/button';
import { AddTunnelDialog } from '@/components/custom/tunnel/AddTunnelDialog';
import type { Agent } from '@/types';
import { formatUptime } from '@/lib/format';

interface AgentHeaderProps {
  agent: Agent;
}

const osLabels: Record<string, string> = {
  darwin: 'macOS',
  linux: 'Linux',
  windows: 'Windows',
};

export function AgentHeader({ agent }: AgentHeaderProps) {
  const isOnline = agent.online;

  return (
    <div className="flex items-start justify-between">
      <div>
        <div className="flex items-center gap-3 mb-2">
          <div>
            <h1 className="text-2xl font-bold tracking-tight text-foreground flex items-center gap-2">
              {agent.info.hostname}
              {isOnline ? (
                <span className="px-2 py-0.5 rounded text-xs font-medium bg-emerald-500/10 text-emerald-500 border border-emerald-500/20">🟢 在线</span>
              ) : (
                <span className="px-2 py-0.5 rounded text-xs font-medium bg-muted text-muted-foreground border border-border">🔴 离线</span>
              )}
            </h1>
            <div className="text-sm text-muted-foreground flex items-center gap-2 mt-1 flex-wrap">
              <span className="font-mono bg-muted/50 px-1.5 py-0.5 rounded">{agent.id.slice(0, 8)}</span>
              <span>•</span>
              <span>{osLabels[agent.info.os] ?? agent.info.os} / {agent.info.arch}</span>
              <span>•</span>
              <span>{agent.info.ip}</span>
              {agent.stats?.uptime != null && (
                <>
                  <span>•</span>
                  <span>运行 {formatUptime(agent.stats.uptime)}</span>
                </>
              )}
            </div>
          </div>
        </div>
      </div>

      <div className="flex gap-2">
        <Button variant="outline" disabled title="功能开发中">
          Web Terminal
        </Button>
        <AddTunnelDialog agentId={agent.id} />
      </div>
    </div>
  );
}
