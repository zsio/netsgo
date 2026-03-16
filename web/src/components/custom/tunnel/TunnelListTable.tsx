import { useState, useMemo } from 'react';
import {
  Search, Play, Pause, Trash2, ShieldCheck, HelpCircle, ArrowRightLeft,
} from 'lucide-react';

import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from '@/components/ui/tooltip';
import { ConfirmDialog } from '@/components/custom/common/ConfirmDialog';
import {
  usePauseTunnel, useResumeTunnel, useDeleteTunnel,
} from '@/hooks/use-tunnel-mutations';
import type { ProxyConfig } from '@/types';

// 扩展的隧道条目，可以附带归属节点信息
export interface TunnelEntry extends ProxyConfig {
  clientId: string;
  clientName?: string;
}

interface TunnelListTableProps {
  /** 隧道列表 */
  tunnels: TunnelEntry[];
  /** 表格标题 */
  title: string;
  /** 标题图标 */
  icon?: React.ReactNode;
  /** 是否显示归属节点列（全网视图用） */
  showClient?: boolean;
  /** 是否显示操作按钮（暂停/恢复/删除） */
  showActions?: boolean;
  /** 是否显示搜索框 */
  showSearch?: boolean;
  /** 空状态下的自定义操作（如"立即创建"按钮） */
  emptyAction?: React.ReactNode;
  /** 自定义行操作渲染（如全网视图中的"管理"按钮） */
  renderRowAction?: (tunnel: TunnelEntry) => React.ReactNode;
}

export function TunnelListTable({
  tunnels,
  title,
  icon,
  showClient = false,
  showActions = true,
  showSearch = true,
  emptyAction,
  renderRowAction,
}: TunnelListTableProps) {
  const pauseTunnel = usePauseTunnel();
  const resumeTunnel = useResumeTunnel();
  const deleteTunnel = useDeleteTunnel();
  const [searchQuery, setSearchQuery] = useState('');
  const [deleteTarget, setDeleteTarget] = useState<{ name: string; clientId: string } | null>(null);

  const filteredTunnels = useMemo(() => {
    if (!searchQuery.trim()) return tunnels;
    const q = searchQuery.toLowerCase();
    return tunnels.filter(
      (t) =>
        t.name.toLowerCase().includes(q) ||
        t.type.toLowerCase().includes(q) ||
        (t.clientName && t.clientName.toLowerCase().includes(q)),
    );
  }, [tunnels, searchQuery]);



  const args = (clientId: string, name: string) => ({ clientId, tunnelName: name });

  return (
    <TooltipProvider delayDuration={200}>
      <div className="rounded-xl border border-border/40 bg-card/50 backdrop-blur-sm shadow-sm overflow-hidden">
        {/* Header */}
        <div className="px-6 py-4 border-b border-border/40 bg-muted/20 flex items-center justify-between">
          <h3 className="font-semibold text-foreground flex items-center gap-2">
            {icon || <ArrowRightLeft className="h-5 w-5 text-primary" />}
            {title}
            <span className="bg-muted text-muted-foreground px-2 py-0.5 rounded-full text-xs font-normal">
              {tunnels.length}
            </span>
          </h3>
          {showSearch && (
            <div className="relative hidden sm:block">
              <Search className="absolute left-2.5 top-2 h-4 w-4 text-muted-foreground" />
              <input
                type="text"
                placeholder="搜索隧道..."
                value={searchQuery}
                onChange={(e) => setSearchQuery(e.target.value)}
                className="h-8 pl-8 pr-3 rounded bg-background border border-border/50 text-xs w-48 focus:outline-none focus:border-primary/50"
              />
            </div>
          )}
        </div>

        {/* Table */}
        {tunnels.length > 0 ? (
          filteredTunnels.length > 0 ? (
            <div className="overflow-x-auto">
              <table className="w-full text-sm text-left">
                <thead className="text-xs text-muted-foreground bg-muted/30 uppercase">
                  <tr>
                    <th className="px-6 py-3 font-medium">隧道名称</th>
                    <th className="px-6 py-3 font-medium">应用 / 类型</th>
                    <th className="px-6 py-3 font-medium">映射关系</th>
                    <th className="px-6 py-3 font-medium">状态</th>
                    {showClient && <th className="px-6 py-3 font-medium">归属节点</th>}
                    {(showActions || renderRowAction) && (
                      <th className="px-6 py-3 font-medium text-right">操作</th>
                    )}
                  </tr>
                </thead>
                <tbody className="divide-y divide-border/40">
                  {filteredTunnels.map((tunnel) => (
                    <tr
                      key={`${tunnel.clientId}-${tunnel.name}`}
                      className="hover:bg-muted/30 transition-colors group"
                    >
                      {/* 隧道名称 */}
                      <td className="px-6 py-3 font-medium text-foreground">{tunnel.name}</td>

                      {/* 应用 / 类型 */}
                      <td className="px-6 py-3">
                        <span className="text-[10px] font-mono px-1.5 py-0.5 rounded bg-secondary text-secondary-foreground border border-border/50 uppercase">
                          {tunnel.type}
                        </span>
                      </td>

                      {/* 映射关系 */}
                      <td className="px-6 py-3 font-mono text-xs">
                        <div className="flex items-center gap-2">
                          <span className="text-primary font-medium">:{tunnel.remote_port}</span>
                          <ArrowRightLeft className="w-3 h-3 text-muted-foreground" />
                          <span>{tunnel.local_ip}:{tunnel.local_port}</span>
                        </div>
                      </td>

                      {/* 状态 */}
                      <td className="px-6 py-3">
                        <TunnelStatusBadge status={tunnel.status} error={tunnel.error} />
                      </td>

                      {/* 归属节点 */}
                      {showClient && (
                        <td className="px-6 py-3 text-muted-foreground">{tunnel.clientName}</td>
                      )}

                      {/* 操作 */}
                      {(showActions || renderRowAction) && (
                        <td className="px-6 py-3 text-right">
                          {renderRowAction ? (
                            renderRowAction(tunnel)
                          ) : showActions ? (
                            <div className="flex items-center justify-end gap-1 opacity-0 group-hover:opacity-100 transition-opacity">
                              {tunnel.status === 'active' && (
                                <button
                                  className="p-1.5 hover:bg-amber-500/10 rounded text-amber-500"
                                  title="暂停"
                                  onClick={() => pauseTunnel.mutate(args(tunnel.clientId, tunnel.name))}
                                >
                                  <Pause className="h-4 w-4" />
                                </button>
                              )}
                              {(tunnel.status === 'paused' || tunnel.status === 'stopped') && (
                                <>
                                  <button
                                    className="p-1.5 hover:bg-primary/10 rounded text-primary"
                                    title="启动"
                                    onClick={() => resumeTunnel.mutate(args(tunnel.clientId, tunnel.name))}
                                  >
                                    <Play className="h-4 w-4" />
                                  </button>
                                  <button
                                    className="p-1.5 hover:bg-destructive/10 rounded text-destructive"
                                    title="删除"
                                    onClick={() => setDeleteTarget({ name: tunnel.name, clientId: tunnel.clientId })}
                                  >
                                    <Trash2 className="h-4 w-4" />
                                  </button>
                                </>
                              )}
                            </div>
                          ) : null}
                        </td>
                      )}
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
            <p>暂无隧道配置</p>
            {emptyAction}
          </div>
        )}
      </div>

      {showActions && (
        <ConfirmDialog
          open={deleteTarget !== null}
          title="删除隧道"
          description={`确认永久删除隧道「${deleteTarget?.name}」？删除后无法恢复。`}
          confirmLabel="删除"
          variant="destructive"
          onConfirm={() => {
            if (deleteTarget) {
              deleteTunnel.mutate(args(deleteTarget.clientId, deleteTarget.name));
              setDeleteTarget(null);
            }
          }}
          onCancel={() => setDeleteTarget(null)}
        />
      )}
    </TooltipProvider>
  );
}

/** 隧道状态徽章，统一的状态展示组件 */
function TunnelStatusBadge({ status, error }: { status: string; error?: string }) {
  switch (status) {
    case 'active':
      return (
        <span className="inline-flex items-center gap-1.5 px-2 py-1 rounded-md bg-emerald-500/10 text-emerald-500 text-xs font-medium">
          <span className="relative flex h-1.5 w-1.5">
            <span className="animate-ping absolute inline-flex h-full w-full rounded-full bg-emerald-400 opacity-75" />
            <span className="relative inline-flex rounded-full h-1.5 w-1.5 bg-emerald-500" />
          </span>
          活跃
        </span>
      );
    case 'paused':
      return (
        <span className="inline-flex items-center gap-1.5 px-2 py-1 rounded-md bg-amber-500/10 text-amber-500 text-xs font-medium">
          <div className="w-1.5 h-1.5 rounded-full bg-amber-500" />
          已暂停
        </span>
      );
    case 'stopped':
      return (
        <span className="inline-flex items-center gap-1.5 px-2 py-1 rounded-md bg-muted text-muted-foreground text-xs font-medium">
          <div className="w-1.5 h-1.5 rounded-full bg-muted-foreground" />
          已停止
        </span>
      );
    case 'error':
      return (
        <span className="inline-flex items-center gap-1.5 px-2 py-1 rounded-md bg-destructive/10 text-destructive text-xs font-medium">
          <div className="w-1.5 h-1.5 rounded-full bg-destructive" />
          异常
          {error && (
            <Tooltip>
              <TooltipTrigger asChild>
                <HelpCircle className="h-3.5 w-3.5 opacity-70 hover:opacity-100 cursor-help" />
              </TooltipTrigger>
              <TooltipContent side="top">
                <p>{error}</p>
              </TooltipContent>
            </Tooltip>
          )}
        </span>
      );
    default:
      return null;
  }
}
