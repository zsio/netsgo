import { useState, useMemo } from 'react';
import {
  Search, Play, Square, Trash2, Pencil, ShieldCheck, HelpCircle, ArrowRightLeft,
} from 'lucide-react';

import { Badge } from '@/components/ui/badge';
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from '@/components/ui/tooltip';
import { ConfirmDialog } from '@/components/custom/common/ConfirmDialog';
import { TunnelDialog } from '@/components/custom/tunnel/TunnelDialog';
import toast from 'react-hot-toast';
import {
  buildTunnelViewModel,
  getTunnelActionAvailability,
  type TunnelStatusPresentation,
} from '@/lib/tunnel-model';
import { cn } from '@/lib/utils';
import {
  useResumeTunnel, useStopTunnel, useDeleteTunnel,
} from '@/hooks/use-tunnel-mutations';
import type { ProxyConfig } from '@/types';
import { formatBytes } from '@/lib/format';

// 扩展的隧道条目，可以附带归属节点信息
export interface TunnelEntry extends ProxyConfig {
  clientId: string;
  clientName?: string;
  clientOnline: boolean;
  traffic24hBytes?: number;
}

type Traffic24hState = 'loading' | 'error' | 'ready';

interface TunnelListTableProps {
  /** 隧道列表 */
  tunnels: TunnelEntry[];
  /** 表格标题 */
  title: string;
  /** 标题图标 */
  icon?: React.ReactNode;
  /** 是否显示归属节点列（全网视图用） */
  showClient?: boolean;
  /** 是否显示 24h 流量列（Client 详情页用） */
  showTraffic24h?: boolean;
  /** 24h 流量数据状态 */
  traffic24hState?: Traffic24hState;
  /** 是否显示操作按钮（启动/停止/删除/编辑） */
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
  showTraffic24h = false,
  traffic24hState = 'ready',
  showActions = true,
  showSearch = true,
  emptyAction,
  renderRowAction,
}: TunnelListTableProps) {
  const resumeTunnel = useResumeTunnel();
  const stopTunnel = useStopTunnel();
  const deleteTunnel = useDeleteTunnel();
  const [searchQuery, setSearchQuery] = useState('');
  const [deleteTarget, setDeleteTarget] = useState<{ name: string; clientId: string } | null>(null);
  const [editTarget, setEditTarget] = useState<TunnelEntry | null>(null);

  const filteredTunnels = useMemo(() => {
    if (!searchQuery.trim()) return tunnels;
    const q = searchQuery.toLowerCase();
    return tunnels.filter(
      (tunnel) => {
        const view = buildTunnelViewModel(tunnel, tunnel.clientOnline);

        return (
          tunnel.name.toLowerCase().includes(q) ||
          tunnel.type.toLowerCase().includes(q) ||
          view.routeLabel.toLowerCase().includes(q) ||
          view.status.label.toLowerCase().includes(q) ||
          (tunnel.clientName && tunnel.clientName.toLowerCase().includes(q))
        );
      },
    );
  }, [tunnels, searchQuery]);

  const args = (clientId: string, name: string) => ({ clientId, tunnelName: name });

  /** 根据隧道状态渲染操作按钮 */
  const renderActionButtons = (tunnel: TunnelEntry) => {
    const {
      canResume,
      canStop,
      canEdit,
      canDelete,
    } = getTunnelActionAvailability(tunnel);

    return (
      <div className="flex items-center justify-end gap-1">
        {canResume && (
          <button
            className="p-1.5 hover:bg-emerald-500/10 rounded text-emerald-500"
            title="启动"
            onClick={() => resumeTunnel.mutate(args(tunnel.clientId, tunnel.name), {
              onSuccess: () => toast.success(`隧道「${tunnel.name}」已启动`),
              onError: (err) => toast.error((err as Error).message),
            })}
          >
            <Play className="h-4 w-4" />
          </button>
        )}
        {canStop && (
          <button
            className="p-1.5 hover:bg-slate-500/10 rounded text-slate-500"
            title="停止"
            onClick={() => stopTunnel.mutate(args(tunnel.clientId, tunnel.name), {
              onSuccess: () => toast.success(`隧道「${tunnel.name}」已停止`),
              onError: (err) => toast.error((err as Error).message),
            })}
          >
            <Square className="h-4 w-4" />
          </button>
        )}
        {canEdit && (
          <button
            className="p-1.5 hover:bg-blue-500/10 rounded text-blue-500"
            title="编辑"
            onClick={() => setEditTarget(tunnel)}
          >
            <Pencil className="h-4 w-4" />
          </button>
        )}
        {canDelete && (
          <button
            className="p-1.5 hover:bg-destructive/10 rounded text-destructive"
            title="删除"
            onClick={() => setDeleteTarget({ name: tunnel.name, clientId: tunnel.clientId })}
          >
            <Trash2 className="h-4 w-4" />
          </button>
        )}
      </div>
    );
  };

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
                    {showTraffic24h && <th className="px-6 py-3 font-medium">24 小时流量</th>}
                    <th className="px-6 py-3 font-medium">状态</th>
                    {showClient && <th className="px-6 py-3 font-medium">归属节点</th>}
                    {(showActions || renderRowAction) && (
                      <th className="px-6 py-3 font-medium text-right">操作</th>
                    )}
                  </tr>
                </thead>
                <tbody className="divide-y divide-border/40">
                  {filteredTunnels.map((tunnel) => (
                    <TunnelTableRow
                      key={`${tunnel.clientId}-${tunnel.name}`}
                      tunnel={tunnel}
                      showClient={showClient}
                      showTraffic24h={showTraffic24h}
                      traffic24hState={traffic24hState}
                      showActions={showActions}
                      renderRowAction={renderRowAction}
                      renderActionButtons={renderActionButtons}
                    />
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
        <>
          <ConfirmDialog
            open={deleteTarget !== null}
            title="删除隧道"
            description={`确认永久删除隧道「${deleteTarget?.name}」？删除后无法恢复。`}
            confirmLabel="删除"
            variant="destructive"
            onConfirm={() => {
              if (deleteTarget) {
                deleteTunnel.mutate(args(deleteTarget.clientId, deleteTarget.name), {
                  onSuccess: () => toast.success(`隧道「${deleteTarget.name}」已删除`),
                  onError: (err) => toast.error((err as Error).message),
                });
                setDeleteTarget(null);
              }
            }}
            onCancel={() => setDeleteTarget(null)}
          />
          <TunnelDialog
            mode="edit"
            tunnel={editTarget}
            open={editTarget !== null}
            onOpenChange={(v) => { if (!v) setEditTarget(null); }}
          />
        </>
      )}
    </TooltipProvider>
  );
}

function TunnelTableRow({
  tunnel,
  showClient,
  showTraffic24h,
  traffic24hState,
  showActions,
  renderRowAction,
  renderActionButtons,
}: {
  tunnel: TunnelEntry;
  showClient: boolean;
  showTraffic24h: boolean;
  traffic24hState: Traffic24hState;
  showActions: boolean;
  renderRowAction?: (tunnel: TunnelEntry) => React.ReactNode;
  renderActionButtons: (tunnel: TunnelEntry) => React.ReactNode;
}) {
  const view = buildTunnelViewModel(tunnel, tunnel.clientOnline);

  return (
    <tr className="hover:bg-muted/30 transition-colors">
      <td className="px-6 py-3 font-medium text-foreground">{tunnel.name}</td>

      <td className="px-6 py-3">
        <span className="text-[10px] font-mono px-1.5 py-0.5 rounded bg-secondary text-secondary-foreground border border-border/50 uppercase">
          {tunnel.type}
        </span>
      </td>

      <td className="px-6 py-3 font-mono text-xs">
        <div className="flex items-center gap-2 min-w-0">
          <span className="text-primary font-medium break-all">{view.targetLabel}</span>
          <ArrowRightLeft className="w-3 h-3 text-muted-foreground" />
          <span className="break-all">{view.destinationLabel}</span>
        </div>
      </td>

      {showTraffic24h && (
        <td className="px-6 py-3">
          <span className="font-mono text-xs text-muted-foreground">
            {traffic24hState === 'error'
              ? '加载失败'
              : traffic24hState === 'loading'
                ? '加载中...'
                : formatBytes(tunnel.traffic24hBytes ?? 0)}
          </span>
        </td>
      )}

      <td className="px-6 py-3">
        <TunnelStatusBadge status={view.status} error={tunnel.error} />
      </td>

      {showClient && (
        <td className="px-6 py-3 text-muted-foreground">{tunnel.clientName}</td>
      )}

      {(showActions || renderRowAction) && (
        <td className="px-6 py-3 text-right">
          {renderRowAction ? (
            renderRowAction(tunnel)
          ) : showActions ? (
            renderActionButtons(tunnel)
          ) : null}
        </td>
      )}
    </tr>
  );
}

function TunnelStatusBadge({
  status,
  error,
}: {
  status: TunnelStatusPresentation;
  error?: string;
}) {
  const dotClassName = cn(
    'size-1.5 rounded-full',
    status.key === 'exposed' && 'bg-emerald-500',
    status.key === 'pending' && 'bg-sky-500',
    status.key === 'offline' && 'bg-amber-500',
    status.key === 'stopped' && 'bg-muted-foreground',
    status.key === 'error' && 'bg-destructive',
  );

  const badgeClassName = cn(
    'gap-1.5',
    status.key === 'exposed' && 'bg-emerald-500/10 text-emerald-600 border-emerald-500/20',
    status.key === 'pending' && 'bg-sky-500/10 text-sky-600 border-sky-500/20',
    status.key === 'offline' && 'bg-amber-500/10 text-amber-600 border-amber-500/20',
    status.key === 'stopped' && 'bg-muted text-muted-foreground border-border/60',
    status.key === 'error' && 'bg-destructive/10 text-destructive border-destructive/20',
  );

  return (
    <div className="flex flex-col gap-1 items-start">
      <Badge variant="outline" className={badgeClassName}>
        <span className={dotClassName} />
        {status.label}
        {status.key === 'error' && error && (
          <Tooltip>
            <TooltipTrigger asChild>
              <HelpCircle className="h-3.5 w-3.5 opacity-70 hover:opacity-100 cursor-help" />
            </TooltipTrigger>
            <TooltipContent side="top">
              <p>{error}</p>
            </TooltipContent>
          </Tooltip>
        )}
      </Badge>
      {status.description && (status.key !== 'error' || !error) && (
        <p className="text-[11px] text-muted-foreground">{status.description}</p>
      )}
    </div>
  );
}
