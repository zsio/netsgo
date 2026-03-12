import { useAgents } from '@/hooks/use-agents';
import { useNavigate } from '@tanstack/react-router';
import { Skeleton } from '@/components/ui/skeleton';
import { ArrowRightLeft } from 'lucide-react';
import { Button } from '@/components/ui/button';

export function DashboardTunnelTable() {
  const { data: agents, isLoading } = useAgents();
  const navigate = useNavigate();

  if (isLoading) {
    return <Skeleton className="h-64 rounded-xl" />;
  }

  // 聚合所有隧道的列表
  const allTunnels = agents?.flatMap(agent => 
    (agent.proxies || []).map(proxy => ({
      ...proxy,
      agentId: agent.id,
      agentName: agent.info.hostname,
    }))
  ) || [];

  return (
    <div className="rounded-xl border border-border/40 bg-card/50 backdrop-blur-sm shadow-sm overflow-hidden">
      <div className="px-6 py-4 border-b border-border/40 bg-muted/20 flex items-center justify-between">
        <h3 className="font-semibold text-foreground flex items-center gap-2">
          <ArrowRightLeft className="h-5 w-5 text-primary" />
          全网隧道列表
        </h3>
      </div>
      <div className="overflow-x-auto">
        <table className="w-full text-sm text-left">
          <thead className="text-xs text-muted-foreground bg-muted/30 uppercase">
            <tr>
              <th className="px-6 py-3 font-medium">隧道名称</th>
              <th className="px-6 py-3 font-medium">应用 / 类型</th>
              <th className="px-6 py-3 font-medium">映射关系</th>
              <th className="px-6 py-3 font-medium">状态</th>
              <th className="px-6 py-3 font-medium">归属节点</th>
              <th className="px-6 py-3 font-medium text-right">操作</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-border/40">
            {allTunnels.length === 0 ? (
              <tr>
                <td colSpan={6} className="px-6 py-8 text-center text-muted-foreground">
                  暂无隧道配置
                </td>
              </tr>
            ) : (
              allTunnels.map((tunnel) => (
                <tr key={`${tunnel.agentId}-${tunnel.name}`} className="hover:bg-muted/30 transition-colors">
                  <td className="px-6 py-3 font-medium text-foreground">{tunnel.name}</td>
                  <td className="px-6 py-3 text-muted-foreground uppercase">{tunnel.type}</td>
                  <td className="px-6 py-3 font-mono text-xs">
                    <div className="flex items-center gap-2">
                      <span className="text-primary font-medium">:{tunnel.remote_port}</span>
                      <ArrowRightLeft className="w-3 h-3 text-muted-foreground" />
                      <span>{tunnel.local_ip}:{tunnel.local_port}</span>
                    </div>
                  </td>
                  <td className="px-6 py-3">
                    {tunnel.status === 'active' && <span className="inline-flex items-center gap-1.5 px-2 py-1 rounded-md bg-emerald-500/10 text-emerald-500 text-xs font-medium"><div className="w-1.5 h-1.5 rounded-full bg-emerald-500" />活跃</span>}
                    {tunnel.status === 'paused' && <span className="inline-flex items-center gap-1.5 px-2 py-1 rounded-md bg-amber-500/10 text-amber-500 text-xs font-medium"><div className="w-1.5 h-1.5 rounded-full bg-amber-500" />已暂停</span>}
                    {tunnel.status === 'stopped' && <span className="inline-flex items-center gap-1.5 px-2 py-1 rounded-md bg-muted text-muted-foreground text-xs font-medium"><div className="w-1.5 h-1.5 rounded-full bg-muted-foreground" />已停止</span>}
                  </td>
                  <td className="px-6 py-3 text-muted-foreground">{tunnel.agentName}</td>
                  <td className="px-6 py-3 text-right">
                    <Button variant="ghost" size="sm" onClick={() => navigate({ to: '/dashboard/agents/$agentId', params: { agentId: tunnel.agentId } })}>
                      管理
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
