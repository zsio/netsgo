import { useMemo, useState } from 'react';
import {
  Search, Play, Pause, Trash2, Pencil, ShieldCheck, HelpCircle, ArrowRightLeft, Activity,
  ArrowDown, ArrowUp, Infinity as InfinityIcon,
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
import { TunnelSpeedDialog } from '@/components/custom/tunnel/TunnelSpeedDialog';
import toast from 'react-hot-toast';
import {
  buildTunnelViewModel,
  getTunnelActionAvailability,
  type TunnelStatusPresentation,
} from '@/lib/tunnel-model';
import { cn } from '@/lib/utils';
import { getClientDisplayName } from '@/lib/client-utils';
import {
  useResumeTunnel, useStopTunnel, useDeleteTunnel,
} from '@/hooks/use-tunnel-mutations';
import type { Client, ProxyConfig } from '@/types';
import { formatBytes, formatNetSpeed } from '@/lib/format';

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
  /** 可用于创建/编辑时选择参与客户端 */
  clients?: Client[];
  /** 表格标题 */
  title: string;
  /** 标题图标 */
  icon?: React.ReactNode;
  /** 是否在目标服务中显示可点击节点（全网视图用） */
  showClient?: boolean;
  /** 是否显示 24h 流量列（Client 详情页用） */
  showTraffic24h?: boolean;
  /** 24h 流量数据状态 */
  traffic24hState?: Traffic24hState;
  /** 是否显示操作按钮（启动/停止/删除/编辑） */
  showActions?: boolean;
  /** 是否显示搜索框 */
  showSearch?: boolean;
  /** 表格标题栏右侧自定义内容 */
  headerAction?: React.ReactNode;
  /** 空状态下的自定义操作（如"立即创建"按钮） */
  emptyAction?: React.ReactNode;
  /** 自定义行操作渲染（如全网视图中的"管理"按钮） */
  renderRowAction?: (tunnel: TunnelEntry) => React.ReactNode;
  /** 目标节点点击回调（全网视图用） */
  onClientClick?: (tunnel: TunnelEntry) => void;
}

function compareTunnelsByCreatedAtDesc(a: TunnelEntry, b: TunnelEntry) {
  const aTime = Date.parse(a.created_at);
  const bTime = Date.parse(b.created_at);

  if (Number.isFinite(aTime) && Number.isFinite(bTime) && aTime !== bTime) {
    return bTime - aTime;
  }
  if (Number.isFinite(aTime) !== Number.isFinite(bTime)) {
    return Number.isFinite(aTime) ? -1 : 1;
  }
  return a.name.localeCompare(b.name);
}

export function TunnelListTable({
  tunnels,
  clients,
  title,
  icon,
  showClient = false,
  showTraffic24h = false,
  traffic24hState = 'ready',
  showActions = true,
  showSearch = true,
  headerAction,
  emptyAction,
  renderRowAction,
  onClientClick,
}: TunnelListTableProps) {
  const resumeTunnel = useResumeTunnel();
  const stopTunnel = useStopTunnel();
  const deleteTunnel = useDeleteTunnel();
  const [searchQuery, setSearchQuery] = useState('');
  const [deleteTarget, setDeleteTarget] = useState<{ id: string; name: string; clientId: string } | null>(null);
  const [editTarget, setEditTarget] = useState<TunnelEntry | null>(null);
  const [speedTarget, setSpeedTarget] = useState<TunnelEntry | null>(null);
  const orderedTunnels = useMemo(() => [...tunnels].sort(compareTunnelsByCreatedAtDesc), [tunnels]);
  const clientNameById = useMemo(() => buildClientNameMap(clients), [clients]);

  const filteredTunnels = useMemo(() => {
    if (!searchQuery.trim()) return orderedTunnels;
    const q = searchQuery.toLowerCase();
    return orderedTunnels.filter(
      (tunnel) => {
        const view = buildTunnelViewModel(tunnel, tunnel.clientOnline);
        const ingress = buildIngressPresentation(tunnel, view, clientNameById);
        const target = buildTargetPresentation(tunnel, view, clientNameById);

        return (
          tunnel.name.toLowerCase().includes(q) ||
          tunnel.type.toLowerCase().includes(q) ||
          view.routeLabel.toLowerCase().includes(q) ||
          ingress.addressLabel.toLowerCase().includes(q) ||
          ingress.nodeLabel.toLowerCase().includes(q) ||
          target.addressLabel.toLowerCase().includes(q) ||
          target.nodeLabel.toLowerCase().includes(q) ||
          view.status.label.toLowerCase().includes(q) ||
          (tunnel.clientName && tunnel.clientName.toLowerCase().includes(q))
        );
      },
    );
  }, [clientNameById, orderedTunnels, searchQuery]);

  const args = (clientId: string, id: string) => ({ clientId, tunnelId: id });

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
        {showTraffic24h && (
          <button
            className="p-1.5 hover:bg-primary/10 rounded text-primary"
            title="速率趋势"
            aria-label="速率趋势"
            onClick={() => setSpeedTarget(tunnel)}
          >
            <Activity className="h-4 w-4" />
          </button>
        )}
        {canResume && (
          <button
            className="p-1.5 hover:bg-emerald-500/10 rounded text-emerald-500"
            title="启动"
            aria-label="启动"
            onClick={() => resumeTunnel.mutate(args(tunnel.clientId, tunnel.id), {
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
            aria-label="停止"
            onClick={() => stopTunnel.mutate(args(tunnel.clientId, tunnel.id), {
              onSuccess: () => toast.success(`隧道「${tunnel.name}」已停止`),
              onError: (err) => toast.error((err as Error).message),
            })}
          >
            <Pause className="h-4 w-4" />
          </button>
        )}
        {canEdit && (
          <button
            className="p-1.5 hover:bg-blue-500/10 rounded text-blue-500"
            title="编辑"
            aria-label="编辑"
            onClick={() => setEditTarget(tunnel)}
          >
            <Pencil className="h-4 w-4" />
          </button>
        )}
        {canDelete && (
          <button
            className="p-1.5 hover:bg-destructive/10 rounded text-destructive"
            title="删除"
            aria-label="删除"
            onClick={() => setDeleteTarget({ id: tunnel.id, name: tunnel.name, clientId: tunnel.clientId })}
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
        <div className="flex items-center justify-between gap-3 border-b border-border/40 bg-muted/20 px-4 py-3 sm:px-6 sm:py-4">
          <h3 className="flex min-w-0 items-center gap-2 font-semibold text-foreground">
            {icon || <ArrowRightLeft className="h-5 w-5 text-primary" />}
            {title}
            <span className="bg-muted text-muted-foreground px-2 py-0.5 rounded-full text-xs font-normal">
              {tunnels.length}
            </span>
          </h3>
          {headerAction ? (
            headerAction
          ) : showSearch && (
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
            <div className="overflow-x-auto [scrollbar-width:thin]">
              <table className="min-w-[56rem] w-full table-fixed text-left text-sm">
                <thead className="text-xs text-muted-foreground bg-muted/30 uppercase">
                  <tr>
                    <th className="w-40 whitespace-nowrap px-4 py-3 font-medium sm:px-6">隧道</th>
                    <th className="w-56 whitespace-nowrap px-4 py-3 font-medium sm:px-6">入口</th>
                    <th className="w-64 whitespace-nowrap px-4 py-3 font-medium sm:px-6">目标服务</th>
                    <th className="w-24 whitespace-nowrap px-4 py-3 font-medium sm:px-6">限速</th>
                    {showTraffic24h && <th className="w-28 whitespace-nowrap px-4 py-3 font-medium sm:px-6">24 小时流量</th>}
                    <th className="w-28 whitespace-nowrap px-4 py-3 font-medium sm:px-6">状态</th>
                    {(showActions || renderRowAction) && (
                      <th className="w-28 whitespace-nowrap px-4 py-3 text-right font-medium sm:px-6">操作</th>
                    )}
                  </tr>
                </thead>
                <tbody className="divide-y divide-border/40">
                  {filteredTunnels.map((tunnel) => (
                    <TunnelTableRow
                      key={tunnel.id}
                      tunnel={tunnel}
                      showClient={showClient}
                      clientNameById={clientNameById}
                      showTraffic24h={showTraffic24h}
                      traffic24hState={traffic24hState}
                      showActions={showActions}
                      renderRowAction={renderRowAction}
                      onClientClick={onClientClick}
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
                deleteTunnel.mutate(args(deleteTarget.clientId, deleteTarget.id), {
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
            clients={clients}
            open={editTarget !== null}
            onOpenChange={(v) => { if (!v) setEditTarget(null); }}
          />
          <TunnelSpeedDialog
            tunnel={speedTarget}
            clientId={speedTarget?.clientId ?? ''}
            open={speedTarget !== null}
            onOpenChange={(v) => { if (!v) setSpeedTarget(null); }}
          />
        </>
      )}
    </TooltipProvider>
  );
}

function TunnelTableRow({
  tunnel,
  showClient,
  clientNameById,
  showTraffic24h,
  traffic24hState,
  showActions,
  renderRowAction,
  onClientClick,
  renderActionButtons,
}: {
  tunnel: TunnelEntry;
  showClient: boolean;
  clientNameById: Map<string, string>;
  showTraffic24h: boolean;
  traffic24hState: Traffic24hState;
  showActions: boolean;
  renderRowAction?: (tunnel: TunnelEntry) => React.ReactNode;
  onClientClick?: (tunnel: TunnelEntry) => void;
  renderActionButtons: (tunnel: TunnelEntry) => React.ReactNode;
}) {
  const view = buildTunnelViewModel(tunnel, tunnel.clientOnline);
  const ingress = buildIngressPresentation(tunnel, view, clientNameById);
  const target = buildTargetPresentation(tunnel, view, clientNameById);

  return (
    <tr className="hover:bg-muted/30 transition-colors">
      <td className="px-4 py-3 sm:px-6">
        <div className="flex min-w-0 items-center gap-2">
          <span className="inline-flex shrink-0 items-center rounded border border-border/60 bg-muted/40 px-1.5 py-0.5 text-[10px] font-medium text-muted-foreground">
            {getTunnelTypeLabel(tunnel)}
          </span>
          <span className="block min-w-0 truncate font-medium text-foreground" title={tunnel.name}>{tunnel.name}</span>
        </div>
      </td>

      <td className="px-4 py-3 sm:px-6">
        <TunnelEndpointCell endpoint={ingress} warning={view.ingressWarning} />
      </td>

      <td className="px-4 py-3 sm:px-6">
        <TunnelEndpointCell
          endpoint={target}
          onNodeClick={showClient && onClientClick ? () => onClientClick(tunnel) : undefined}
        />
      </td>

      <td className="px-4 py-3 sm:px-6">
        <TunnelSpeedLimit tunnel={tunnel} />
      </td>

      {showTraffic24h && (
        <td className="px-4 py-3 sm:px-6">
          <span className="whitespace-nowrap font-mono text-xs text-muted-foreground">
            {traffic24hState === 'error'
              ? '加载失败'
              : traffic24hState === 'loading'
                ? '加载中...'
                : formatBytes(tunnel.traffic24hBytes ?? 0)}
          </span>
        </td>
      )}

      <td className="px-4 py-3 sm:px-6">
        <TunnelStatusBadge status={view.status} error={tunnel.error} issues={tunnel.issues} />
      </td>

      {(showActions || renderRowAction) && (
        <td className="px-4 py-3 text-right sm:px-6">
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

type TunnelEndpointPresentation = {
  nodeLabel: string;
  addressLabel: string;
};

function buildClientNameMap(clients: Client[] | undefined) {
  const names = new Map<string, string>();
  for (const client of clients ?? []) {
    names.set(client.id, getClientDisplayName(client));
  }
  return names;
}

function resolveClientLabel(tunnel: TunnelEntry, clientId: string | undefined, clientNameById: Map<string, string>) {
  if (!clientId) {
    return '未知节点';
  }
  if (clientNameById.has(clientId)) {
    return clientNameById.get(clientId) ?? clientId;
  }
  if (clientId === tunnel.clientId && tunnel.clientName) {
    return tunnel.clientName;
  }
  return compactClientId(clientId);
}

function compactClientId(clientId: string) {
  return clientId.length > 12 ? `${clientId.slice(0, 8)}...` : clientId;
}

function buildIngressPresentation(
  tunnel: TunnelEntry,
  view: ReturnType<typeof buildTunnelViewModel>,
  clientNameById: Map<string, string>,
): TunnelEndpointPresentation {
  const ingress = tunnel.ingress;
  const ingressClientId = ingress?.client_id || tunnel.participants?.ingress?.client_id;
  const isClientIngress = ingress?.location === 'client';

  return {
    nodeLabel: isClientIngress ? resolveClientLabel(tunnel, ingressClientId, clientNameById) : 'Server',
    addressLabel: view.targetLabel,
  };
}

function buildTargetPresentation(
  tunnel: TunnelEntry,
  view: ReturnType<typeof buildTunnelViewModel>,
  clientNameById: Map<string, string>,
): TunnelEndpointPresentation {
  const targetClientId = tunnel.target?.client_id || tunnel.participants?.target?.client_id || tunnel.client_id;

  return {
    nodeLabel: resolveClientLabel(tunnel, targetClientId, clientNameById),
    addressLabel: view.destinationLabel,
  };
}

function getTunnelTypeLabel(tunnel: TunnelEntry) {
  return tunnel.type.toUpperCase();
}

function TunnelSpeedLimit({ tunnel }: { tunnel: TunnelEntry }) {
  const hasIngressLimit = tunnel.ingress_bps > 0;
  const hasEgressLimit = tunnel.egress_bps > 0;

  if (!hasIngressLimit && !hasEgressLimit) {
    return (
      <div className="inline-flex h-6 w-8 items-center justify-center text-muted-foreground" aria-label="不限速">
        <InfinityIcon className="h-4 w-4" />
      </div>
    );
  }

  return (
    <div className="flex flex-col gap-0.5 font-mono text-xs leading-4 text-muted-foreground">
      {hasIngressLimit && (
        <span className="inline-flex items-center gap-1.5 whitespace-nowrap">
          <ArrowDown className="h-3.5 w-3.5 text-emerald-500" />
          <span>{formatNetSpeed(tunnel.ingress_bps)}</span>
        </span>
      )}
      {hasEgressLimit && (
        <span className="inline-flex items-center gap-1.5 whitespace-nowrap">
          <ArrowUp className="h-3.5 w-3.5 text-sky-500" />
          <span>{formatNetSpeed(tunnel.egress_bps)}</span>
        </span>
      )}
    </div>
  );
}

function TunnelEndpointCell({
  endpoint,
  warning,
  onNodeClick,
}: {
  endpoint: TunnelEndpointPresentation;
  warning?: string;
  onNodeClick?: () => void;
}) {
  return (
    <div className="flex min-w-0 flex-col gap-1 text-xs">
      {onNodeClick ? (
        <button
          type="button"
          className="inline-flex w-fit max-w-full cursor-pointer truncate border-b border-dashed border-muted-foreground/50 text-left font-medium text-foreground hover:text-primary"
          title={endpoint.nodeLabel}
          onClick={onNodeClick}
        >
          {endpoint.nodeLabel}
        </button>
      ) : (
        <span className="min-w-0 truncate font-medium text-foreground" title={endpoint.nodeLabel}>
          {endpoint.nodeLabel}
        </span>
      )}
      <span className="block truncate font-mono text-xs text-primary" title={endpoint.addressLabel}>
        {endpoint.addressLabel}
      </span>
      {warning && (
        <span className="truncate text-[11px] text-amber-600" title={warning}>
          {warning}
        </span>
      )}
    </div>
  );
}

function TunnelStatusBadge({
  status,
  error,
  issues,
}: {
  status: TunnelStatusPresentation;
  error?: string;
  issues?: TunnelEntry['issues'];
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

  const issueItems = sortTunnelIssues(issues ?? []);
  const primaryIssue = issueItems[0];
  const additionalIssueCount = Math.max(0, issueItems.length - 1);
  const issueSummary = primaryIssue
    ? `${primaryIssue.message}${additionalIssueCount > 0 ? ` +${additionalIssueCount}` : ''}`
    : '';
  const tooltipLines = issueItems.length > 0
    ? issueItems.map((issue) => `${issue.severity}: ${issue.message}`)
    : error
      ? [error]
      : [];

  return (
    <div className="flex flex-col gap-1 items-start">
      <Badge variant="outline" className={cn(badgeClassName, 'px-2 sm:px-2.5')} aria-label={status.label}>
        <span className={dotClassName} />
        <span className="hidden sm:inline">{status.label}</span>
        {additionalIssueCount > 0 && (
          <span className="rounded bg-background/70 px-1 font-mono text-[10px]">+{additionalIssueCount}</span>
        )}
        {tooltipLines.length > 0 && (
          <Tooltip>
            <TooltipTrigger asChild>
              <HelpCircle className="h-3.5 w-3.5 opacity-70 hover:opacity-100 cursor-help" aria-label={tooltipLines.join('\n')} />
            </TooltipTrigger>
            <TooltipContent side="top" className="max-w-xs space-y-1">
              {tooltipLines.map((line) => (
                <p key={line}>{line}</p>
              ))}
            </TooltipContent>
          </Tooltip>
        )}
      </Badge>
      {issueSummary && (
        <p className="hidden max-w-[18rem] truncate text-[11px] text-destructive sm:block">{issueSummary}</p>
      )}
      {status.description && issueItems.length === 0 && (status.key !== 'error' || !error) && (
        <p className="hidden text-[11px] text-muted-foreground sm:block">{status.description}</p>
      )}
    </div>
  );
}

function sortTunnelIssues(issues: NonNullable<TunnelEntry['issues']>) {
  return [...issues].sort((a, b) => tunnelIssuePriority(a.code) - tunnelIssuePriority(b.code));
}

function tunnelIssuePriority(code: string) {
  if (code.includes('offline') || code.includes('data_channel')) return 0;
  if (code.includes('capability')) return 1;
  if (code.startsWith('ingress_')) return 2;
  if (code.startsWith('provision_')) return 3;
  if (code.includes('stream') || code === 'runtime_report') return 4;
  return 5;
}
