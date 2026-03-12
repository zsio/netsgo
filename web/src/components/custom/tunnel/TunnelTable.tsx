import { useState, useMemo } from 'react';
import {
  Search, Play, Pause, Trash2, ShieldCheck,
} from 'lucide-react';
import { Button } from '@/components/ui/button';
import { ConfirmDialog } from '@/components/custom/common/ConfirmDialog';
import {
  usePauseTunnel, useResumeTunnel, useDeleteTunnel,
} from '@/hooks/use-tunnel-mutations';
import type { Agent } from '@/types';

interface TunnelTableProps {
  agent: Agent;
}

export function TunnelTable({ agent }: TunnelTableProps) {
  const tunnels = agent.proxies ?? [];
  const pauseTunnel = usePauseTunnel();
  const resumeTunnel = useResumeTunnel();
  const deleteTunnel = useDeleteTunnel();
  const [searchQuery, setSearchQuery] = useState('');
  const [deleteTarget, setDeleteTarget] = useState<string | null>(null);

  const filteredTunnels = useMemo(() => {
    if (!searchQuery.trim()) return tunnels;
    const q = searchQuery.toLowerCase();
    return tunnels.filter(
      (t) =>
        t.name.toLowerCase().includes(q) ||
        t.type.toLowerCase().includes(q),
    );
  }, [tunnels, searchQuery]);

  const args = (name: string) => ({ agentId: agent.id, tunnelName: name });

  return (
    <>
      <div className="rounded-xl border border-border/40 bg-card/30 backdrop-blur-sm shadow-sm overflow-hidden">
        <div className="px-6 py-4 border-b border-border/40 flex items-center justify-between bg-card/50">
          <h3 className="font-semibold text-lg flex items-center gap-2">
            🚇 下属隧道
            <span className="bg-muted text-muted-foreground px-2 py-0.5 rounded-full text-xs font-normal">
              {tunnels.length}
            </span>
          </h3>
          <div className="relative">
            <Search className="absolute left-2.5 top-2 h-4 w-4 text-muted-foreground" />
            <input
              type="text"
              placeholder="搜索隧道..."
              value={searchQuery}
              onChange={(e) => setSearchQuery(e.target.value)}
              className="h-8 pl-8 pr-3 rounded bg-background border border-border/50 text-xs w-48 focus:outline-none focus:border-primary/50"
            />
          </div>
        </div>

        {tunnels.length > 0 ? (
          filteredTunnels.length > 0 ? (
            <div className="overflow-x-auto">
              <table className="w-full text-sm text-left">
                <thead className="text-xs text-muted-foreground bg-muted/20">
                  <tr>
                    <th className="px-6 py-3 font-medium">名称 / 协议</th>
                    <th className="px-6 py-3 font-medium">本地映射</th>
                    <th className="px-6 py-3 font-medium">公网入口</th>
                    <th className="px-6 py-3 font-medium">状态</th>
                    <th className="px-6 py-3 font-medium text-right">操作</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-border/30">
                  {filteredTunnels.map((tunnel) => (
                    <tr key={tunnel.name} className="hover:bg-muted/10 transition-colors group">
                      <td className="px-6 py-4">
                        <div className="flex items-center gap-2">
                          <span className="font-medium text-foreground">{tunnel.name}</span>
                          <span className="text-[10px] font-mono px-1.5 py-0.5 rounded bg-secondary text-secondary-foreground border border-border/50 uppercase">
                            {tunnel.type}
                          </span>
                        </div>
                      </td>
                      <td className="px-6 py-4 font-mono text-xs text-muted-foreground">
                        {tunnel.local_ip}:{tunnel.local_port}
                      </td>
                      <td className="px-6 py-4">
                        <span className="font-mono text-xs text-primary bg-primary/10 px-2 py-1 rounded border border-primary/20">
                          :{tunnel.remote_port}
                        </span>
                      </td>
                      <td className="px-6 py-4">
                        {tunnel.status === 'active' && (
                          <div className="flex items-center text-emerald-500">
                            <span className="relative flex h-2 w-2 mr-2">
                              <span className="animate-ping absolute inline-flex h-full w-full rounded-full bg-emerald-400 opacity-75" />
                              <span className="relative inline-flex rounded-full h-2 w-2 bg-emerald-500" />
                            </span>
                            运行中
                          </div>
                        )}
                        {tunnel.status === 'paused' && (
                          <div className="flex items-center text-amber-500">
                            <div className="h-2 w-2 rounded-full bg-amber-500 mr-2" />
                            已暂停
                          </div>
                        )}
                        {tunnel.status === 'stopped' && (
                          <div className="flex items-center text-muted-foreground">
                            <div className="h-2 w-2 rounded-full bg-muted-foreground/50 mr-2" />
                            已停止
                          </div>
                        )}
                        {tunnel.status === 'error' && (
                          <div className="flex items-center text-destructive">
                            <div className="h-2 w-2 rounded-full bg-destructive mr-2" />
                            异常
                          </div>
                        )}
                      </td>
                      <td className="px-6 py-4 text-right">
                        <div className="flex items-center justify-end gap-1 opacity-0 group-hover:opacity-100 transition-opacity">
                          {/* active → 暂停 */}
                          {tunnel.status === 'active' && (
                            <button
                              className="p-1.5 hover:bg-amber-500/10 rounded text-amber-500"
                              title="暂停"
                              onClick={() => pauseTunnel.mutate(args(tunnel.name))}
                            >
                              <Pause className="h-4 w-4" />
                            </button>
                          )}
                          {/* paused / stopped → 启动 + 删除 */}
                          {(tunnel.status === 'paused' || tunnel.status === 'stopped') && (
                            <>
                              <button
                                className="p-1.5 hover:bg-primary/10 rounded text-primary"
                                title="启动"
                                onClick={() => resumeTunnel.mutate(args(tunnel.name))}
                              >
                                <Play className="h-4 w-4" />
                              </button>
                              <button
                                className="p-1.5 hover:bg-destructive/10 rounded text-destructive"
                                title="删除"
                                onClick={() => setDeleteTarget(tunnel.name)}
                              >
                                <Trash2 className="h-4 w-4" />
                              </button>
                            </>
                          )}
                        </div>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          ) : (
            <div className="flex flex-col items-center justify-center py-8 text-muted-foreground">
              <Search className="h-8 w-8 mb-3 opacity-20" />
              <p className="text-sm">未找到匹配的隧道</p>
            </div>
          )
        ) : (
          <div className="flex flex-col items-center justify-center py-12 text-muted-foreground">
            <ShieldCheck className="h-12 w-12 mb-4 opacity-20" />
            <p>该节点暂无隧道</p>
            <Button variant="outline" className="mt-4">
              + 立即创建
            </Button>
          </div>
        )}
      </div>

      <ConfirmDialog
        open={deleteTarget !== null}
        title="删除隧道"
        description={`确认永久删除隧道「${deleteTarget}」？删除后无法恢复。`}
        confirmLabel="删除"
        variant="destructive"
        onConfirm={() => {
          if (deleteTarget) {
            deleteTunnel.mutate(args(deleteTarget));
            setDeleteTarget(null);
          }
        }}
        onCancel={() => setDeleteTarget(null)}
      />
    </>
  );
}
