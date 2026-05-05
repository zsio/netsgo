import { useClients, useDeleteClient } from '@/hooks/use-clients';
import { useNavigate } from '@tanstack/react-router';
import { Skeleton } from '@/components/ui/skeleton';
import { Laptop, Cpu, HardDrive, Eye, Trash2 } from 'lucide-react';
import { formatPercent } from '@/lib/format';
import { CompactTrafficChart } from '@/components/custom/chart/CompactTrafficChart';
import type { Client } from '@/types';
import { getClientDisplayName } from '@/lib/client-utils';
import { useRowVisibility, type RowVisibilityHook } from '@/hooks/use-row-visibility';
import { canRenderDashboardTrafficSparkline } from '@/lib/dashboard-traffic-visibility';
import type { KeyboardEvent, ReactNode } from 'react';
import { useMemo, useState } from 'react';
import { ConfirmDialog } from '@/components/custom/common/ConfirmDialog';
import toast from 'react-hot-toast';

function sortClientsForDashboard(clients: Client[] | undefined) {
  return [...(clients ?? [])].sort((a, b) => {
    if (a.online !== b.online) {
      return a.online ? -1 : 1;
    }
    return getClientDisplayName(a).localeCompare(getClientDisplayName(b));
  });
}

function ActionIcon({
  label,
  disabled = false,
  onClick,
  children,
}: {
  label: string;
  disabled?: boolean;
  onClick?: () => void;
  children: ReactNode;
}) {
  const handleKeyDown = (event: KeyboardEvent<HTMLSpanElement>) => {
    if (disabled || !onClick) return;
    if (event.key === 'Enter' || event.key === ' ') {
      event.preventDefault();
      onClick();
    }
  };

  return (
    <span
      role={disabled || !onClick ? undefined : 'button'}
      tabIndex={disabled || !onClick ? undefined : 0}
      title={label}
      aria-label={label}
      aria-disabled={disabled || undefined}
      className={`inline-flex size-7 items-center justify-center rounded text-muted-foreground transition-colors ${
        disabled || !onClick
          ? 'opacity-30'
          : 'cursor-pointer hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/40'
      }`}
      onClick={onClick}
      onKeyDown={handleKeyDown}
    >
      {children}
    </span>
  );
}

function ClientMobileCard({
  client,
  onNavigate,
  onDelete,
}: {
  client: Client;
  onNavigate: () => void;
  onDelete: () => void;
}) {
  return (
    <div className="p-4 flex flex-col gap-3 border-b border-border/40 last:border-b-0">
      <div className="flex items-center justify-between">
        <span className="font-medium text-foreground text-sm truncate mr-2">{getClientDisplayName(client)}</span>
        {client.online ? (
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
        <span>{client.last_ip || client.info.ip || '-'}</span>
        <span>{client.info.os}/{client.info.arch}</span>
        {client.stats && (
          <span className="flex items-center gap-2">
            <span className="flex items-center gap-1"><Cpu className="w-3 h-3" />{formatPercent(client.stats.cpu_usage)}</span>
            <span className="flex items-center gap-1"><HardDrive className="w-3 h-3" />{formatPercent(client.stats.mem_usage)}</span>
          </span>
        )}
      </div>
      <div className="flex items-center gap-1 -ml-2">
        <ActionIcon label="查看详情" onClick={onNavigate}>
          <Eye className="h-4 w-4" />
        </ActionIcon>
        <ActionIcon
          label={client.online ? '在线客户端不能删除' : '删除离线节点'}
          disabled={client.online}
          onClick={client.online ? undefined : onDelete}
        >
          <Trash2 className={`h-4 w-4 ${client.online ? '' : 'text-destructive'}`} />
        </ActionIcon>
      </div>
    </div>
  );
}

interface DashboardClientTableContentProps {
  clients?: Client[];
  onNavigate: (clientId: string) => void;
  onDelete?: (client: Client) => void;
  rowVisibilityHook?: RowVisibilityHook;
  renderSparkline?: (clientId: string) => ReactNode;
}

function defaultRenderSparkline(clientId: string) {
  return <CompactTrafficChart clientId={clientId} />;
}

function DashboardClientDesktopRow({
  client,
  onNavigate,
  onDelete,
  rowVisibilityHook,
  renderSparkline,
}: {
  client: Client;
  onNavigate: (clientId: string) => void;
  onDelete?: (client: Client) => void;
  rowVisibilityHook: RowVisibilityHook;
  renderSparkline: (clientId: string) => ReactNode;
}) {
  const { ref, hasVisibilitySupport, isDesktop, isVisible } = rowVisibilityHook();
  const showSparkline = canRenderDashboardTrafficSparkline({
    hasVisibilitySupport,
    isDesktop,
    isVisible,
  });

  return (
    <tr ref={ref} className="hover:bg-muted/30 transition-colors">
      <td className="px-6 py-3 font-medium text-foreground">{getClientDisplayName(client)}</td>
      <td className="px-6 py-3">{client.last_ip || client.info.ip || '-'}</td>
      <td className="px-6 py-3">
        {client.online ? (
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
      <td className="px-6 py-3 text-muted-foreground">{client.info.os} / {client.info.arch}</td>
      <td className="px-6 py-3">
        {client.stats ? (
          <div className="flex items-center gap-3">
            <span className="flex items-center gap-1"><Cpu className="w-3 h-3 text-muted-foreground" /> {formatPercent(client.stats.cpu_usage)}</span>
            <span className="flex items-center gap-1"><HardDrive className="w-3 h-3 text-muted-foreground" /> {formatPercent(client.stats.mem_usage)}</span>
          </div>
        ) : (
          <span className="text-muted-foreground">-</span>
        )}
      </td>
      <td className="px-6 py-3">
        {showSparkline ? renderSparkline(client.id) : <span className="text-muted-foreground">-</span>}
      </td>
      <td className="px-6 py-3 text-right">
        <div className="flex items-center justify-end gap-1">
          <ActionIcon label="查看详情" onClick={() => onNavigate(client.id)}>
            <Eye className="h-4 w-4" />
          </ActionIcon>
          <ActionIcon
            label={client.online ? '在线客户端不能删除' : '删除离线节点'}
            disabled={client.online || !onDelete}
            onClick={!client.online && onDelete ? () => onDelete(client) : undefined}
          >
            <Trash2 className={`h-4 w-4 ${client.online || !onDelete ? '' : 'text-destructive'}`} />
          </ActionIcon>
        </div>
      </td>
    </tr>
  );
}

export function DashboardClientTableContent({
  clients,
  onNavigate,
  onDelete,
  rowVisibilityHook = useRowVisibility,
  renderSparkline = defaultRenderSparkline,
}: DashboardClientTableContentProps) {
  const sortedClients = useMemo(() => sortClientsForDashboard(clients), [clients]);

  const navigateToClient = (clientId: string) => {
    onNavigate(clientId);
  };

  return (
    <div className="rounded-xl border border-border/40 bg-card/50 backdrop-blur-sm shadow-sm overflow-hidden">
      <div className="px-4 sm:px-6 py-3 sm:py-4 border-b border-border/40 bg-muted/20 flex items-center justify-between">
        <h3 className="font-semibold text-foreground flex items-center gap-2">
          <Laptop className="h-5 w-5 text-primary" />
          客户端
        </h3>
      </div>

      {/* Mobile: Card List */}
      <div className="md:hidden">
        {sortedClients.length === 0 ? (
          <div className="px-4 py-8 text-center text-muted-foreground text-sm">暂无 Client 连接</div>
        ) : (
          sortedClients.map((client) => (
            <ClientMobileCard
              key={client.id}
              client={client}
              onNavigate={() => navigateToClient(client.id)}
              onDelete={() => onDelete?.(client)}
            />
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
              <th className="px-6 py-3 font-medium">网络趋势 (1h)</th>
              <th className="px-6 py-3 font-medium text-right">操作</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-border/40">
            {sortedClients.length === 0 ? (
              <tr>
                <td colSpan={7} className="px-6 py-8 text-center text-muted-foreground">
                  暂无 Client 连接
                </td>
              </tr>
            ) : (
              sortedClients.map((client) => (
                <DashboardClientDesktopRow
                  key={client.id}
                  client={client}
                  onNavigate={navigateToClient}
                  onDelete={onDelete}
                  rowVisibilityHook={rowVisibilityHook}
                  renderSparkline={renderSparkline}
                />
              ))
            )}
          </tbody>
        </table>
      </div>
    </div>
  );
}

export function DashboardClientTable() {
  const { data: clients, isLoading } = useClients();
  const deleteClient = useDeleteClient();
  const navigate = useNavigate();
  const [deleteTarget, setDeleteTarget] = useState<Client | null>(null);

  if (isLoading) {
    return <Skeleton className="h-64 rounded-xl" />;
  }

  return (
    <>
      <DashboardClientTableContent
        clients={clients}
        onNavigate={(clientId) => {
          navigate({ to: '/dashboard/clients/$clientId', params: { clientId } });
        }}
        onDelete={setDeleteTarget}
      />
      <ConfirmDialog
        open={deleteTarget !== null}
        title="删除离线节点"
        description={`确认删除离线节点「${deleteTarget ? getClientDisplayName(deleteTarget) : ''}」？相关隧道配置和流量历史也会被清理。`}
        confirmLabel="删除"
        variant="destructive"
        onConfirm={() => {
          if (!deleteTarget) return;
          const target = deleteTarget;
          deleteClient.mutate(target.id, {
            onSuccess: () => toast.success(`节点「${getClientDisplayName(target)}」已删除`),
            onError: (err) => toast.error((err as Error).message),
          });
          setDeleteTarget(null);
        }}
        onCancel={() => setDeleteTarget(null)}
      />
    </>
  );
}
