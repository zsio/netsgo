import { useAgents } from '@/hooks/use-agents';
import { useNavigate } from '@tanstack/react-router';
import { Skeleton } from '@/components/ui/skeleton';
import { Laptop, Cpu, HardDrive } from 'lucide-react';
import { formatPercent } from '@/lib/format';
import { Button } from '@/components/ui/button';
import type { Agent } from '@/types';

function AgentMobileCard({ agent, onNavigate }: { agent: Agent; onNavigate: () => void }) {
  return (
    <div className="p-4 flex flex-col gap-3 border-b border-border/40 last:border-b-0">
      <div className="flex items-center justify-between">
        <span className="font-medium text-foreground text-sm truncate mr-2">{agent.info.hostname}</span>
        {agent.online ? (
          <span className="inline-flex items-center gap-1.5 px-2 py-0.5 rounded-md bg-emerald-500/10 text-emerald-500 text-xs font-medium shrink-0">
            <div className="w-1.5 h-1.5 rounded-full bg-emerald-500" />
            在线
          </span>
        ) : (
          <span className="inline-flex items-center gap-1.5 px-2 py-0.5 rounded-md bg-muted text-muted-foreground text-xs font-medium shrink-0">
            <div className="w-1.5 h-1.5 rounded-full bg-muted-foreground" />
            离线
          </span>
        )}
      </div>
      <div className="flex flex-wrap items-center gap-x-4 gap-y-1 text-xs text-muted-foreground">
        <span>{agent.last_ip || agent.info.ip || '-'}</span>
        <span>{agent.info.os}/{agent.info.arch}</span>
        {agent.stats && (
          <span className="flex items-center gap-2">
            <span className="flex items-center gap-1"><Cpu className="w-3 h-3" />{formatPercent(agent.stats.cpu_usage)}</span>
            <span className="flex items-center gap-1"><HardDrive className="w-3 h-3" />{formatPercent(agent.stats.mem_usage)}</span>
          </span>
        )}
      </div>
      <Button variant="ghost" size="sm" className="self-start -ml-2 h-7 text-xs" onClick={onNavigate}>
        查看详情
      </Button>
    </div>
  );
}

export function DashboardAgentTable() {
  const { data: agents, isLoading } = useAgents();
  const navigate = useNavigate();

  if (isLoading) {
    return <Skeleton className="h-64 rounded-xl" />;
  }

  const navigateToAgent = (agentId: string) => {
    navigate({ to: '/dashboard/agents/$agentId', params: { agentId } });
  };

  return (
    <div className="rounded-xl border border-border/40 bg-card/50 backdrop-blur-sm shadow-sm overflow-hidden">
      <div className="px-4 sm:px-6 py-3 sm:py-4 border-b border-border/40 bg-muted/20 flex items-center justify-between">
        <h3 className="font-semibold text-foreground flex items-center gap-2">
          <Laptop className="h-5 w-5 text-primary" />
          在线端点 (Agents)
        </h3>
      </div>

      {/* Mobile: Card List */}
      <div className="md:hidden">
        {(!agents || agents.length === 0) ? (
          <div className="px-4 py-8 text-center text-muted-foreground text-sm">暂无 Agent 连接</div>
        ) : (
          agents.map((agent) => (
            <AgentMobileCard key={agent.id} agent={agent} onNavigate={() => navigateToAgent(agent.id)} />
          ))
        )}
      </div>

      {/* Desktop: Table */}
      <div className="hidden md:block overflow-x-auto">
        <table className="w-full text-sm text-left">
          <thead className="text-xs text-muted-foreground bg-muted/30 uppercase">
            <tr>
              <th className="px-6 py-3 font-medium">节点名称</th>
              <th className="px-6 py-3 font-medium">IP 地址</th>
              <th className="px-6 py-3 font-medium">状态</th>
              <th className="px-6 py-3 font-medium">系统/架构</th>
              <th className="px-6 py-3 font-medium">CPU / 内存</th>
              <th className="px-6 py-3 font-medium text-right">操作</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-border/40">
            {(!agents || agents.length === 0) ? (
              <tr>
                <td colSpan={6} className="px-6 py-8 text-center text-muted-foreground">
                  暂无 Agent 连接
                </td>
              </tr>
            ) : (
              agents.map((agent) => (
                <tr key={agent.id} className="hover:bg-muted/30 transition-colors">
                  <td className="px-6 py-3 font-medium text-foreground">{agent.info.hostname}</td>
                  <td className="px-6 py-3">{agent.last_ip || agent.info.ip || '-'}</td>
                  <td className="px-6 py-3">
                    {agent.online ? (
                      <span className="inline-flex items-center gap-1.5 px-2 py-1 rounded-md bg-emerald-500/10 text-emerald-500 text-xs font-medium">
                        <div className="w-1.5 h-1.5 rounded-full bg-emerald-500" />
                        在线
                      </span>
                    ) : (
                      <span className="inline-flex items-center gap-1.5 px-2 py-1 rounded-md bg-muted text-muted-foreground text-xs font-medium">
                        <div className="w-1.5 h-1.5 rounded-full bg-muted-foreground" />
                        离线
                      </span>
                    )}
                  </td>
                  <td className="px-6 py-3 text-muted-foreground">{agent.info.os} / {agent.info.arch}</td>
                  <td className="px-6 py-3">
                    {agent.stats ? (
                      <div className="flex items-center gap-3">
                        <span className="flex items-center gap-1"><Cpu className="w-3 h-3 text-muted-foreground" /> {formatPercent(agent.stats.cpu_usage)}</span>
                        <span className="flex items-center gap-1"><HardDrive className="w-3 h-3 text-muted-foreground" /> {formatPercent(agent.stats.mem_usage)}</span>
                      </div>
                    ) : (
                      <span className="text-muted-foreground">-</span>
                    )}
                  </td>
                  <td className="px-6 py-3 text-right">
                    <Button variant="ghost" size="sm" onClick={() => navigateToAgent(agent.id)}>
                      查看详情
                    </Button>
                  </td>
                </tr>
              ))
            )}
          </tbody>
        </table>
      </div>
    </div>
  );
}
