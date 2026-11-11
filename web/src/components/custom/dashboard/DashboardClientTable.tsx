import { useClients, useDeleteClient } from '@/hooks/use-clients';
import { useNavigate } from '@tanstack/react-router';
import { Skeleton } from '@/components/ui/skeleton';
import { Laptop, Cpu, HardDrive, Eye, Trash2, Globe, Wifi, LayersPlus, Search } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { formatPercent } from '@/lib/format';
import { TableActionIconButton } from '@/components/custom/common/TableActionIconButton';
import { DEFAULT_TABLE_PAGE_SIZE, sliceTablePage } from '@/components/custom/common/table-pagination';
import { TablePaginationBar } from '@/components/custom/common/TablePaginationBar';
import { CopyableIpLine } from '@/components/custom/common/CopyableIpLine';
import { useAddClientDialog } from '@/components/custom/client/add-client-dialog-context';
import type { Client } from '@/types';
import { getClientDisplayName } from '@/lib/client-utils';
import type { ReactNode } from 'react';
import { memo, useMemo, useState } from 'react';
import { ConfirmDialog } from '@/components/custom/common/ConfirmDialog';
import toast from 'react-hot-toast';
import { useTranslation } from 'react-i18next';
import type { ResourceScope } from '@/lib/resource-scope';

const CLIENT_SEARCH_VISIBLE_THRESHOLD = 5;

function matchesClientQuery(client: Client, lowerQuery: string) {
  return (
    getClientDisplayName(client).toLowerCase().includes(lowerQuery)
    || client.info.hostname.toLowerCase().includes(lowerQuery)
    || (client.info.ip && client.info.ip.toLowerCase().includes(lowerQuery))
    || (client.info.public_ipv4 && client.info.public_ipv4.toLowerCase().includes(lowerQuery))
    || (client.info.public_ipv6 && client.info.public_ipv6.toLowerCase().includes(lowerQuery))
    || (client.last_ip && client.last_ip.toLowerCase().includes(lowerQuery))
    || `${client.info.os}/${client.info.arch}`.toLowerCase().includes(lowerQuery)
  );
}

function sortClientsForDashboard(clients: Client[] | undefined) {
  return [...(clients ?? [])].sort((a, b) => {
    if (a.online !== b.online) {
      return a.online ? -1 : 1;
    }
    return getClientDisplayName(a).localeCompare(getClientDisplayName(b));
  });
}

function IconActionButton({
  label,
  variant = 'neutral',
  onClick,
  children,
}: {
  label: string;
  variant?: 'neutral' | 'destructive';
  onClick: () => void;
  children: ReactNode;
}) {
  return (
    <TableActionIconButton
      label={label}
      tone={variant === 'destructive' ? 'destructive' : 'neutral'}
      aria-label={label}
      onClick={onClick}
    >
      {children}
    </TableActionIconButton>
  );
}

function DashboardClientNetworkInfo({ client, compact = false }: { client: Client; compact?: boolean }) {
  const { t } = useTranslation();
  const privateIP = client.info.ip || '-';
  const publicIP = client.info.public_ipv4 || client.info.public_ipv6 || client.last_ip || '-';

  if (compact) {
    return (
      <div className="grid min-w-0 grid-cols-2 gap-3">
        <CopyableIpLine
          primary
          title={t('clients.publicIp')}
          icon={<Globe className="h-3.5 w-3.5" />}
          value={publicIP}
        />
        <CopyableIpLine
          title={t('clients.privateIp')}
          icon={<Wifi className="h-3.5 w-3.5" />}
          value={privateIP}
        />
      </div>
    );
  }

  return (
    <div className="flex flex-col gap-1.5 min-w-0">
      <CopyableIpLine
        primary
        title={t('clients.publicIp')}
        icon={<Globe className="h-3.5 w-3.5" />}
        value={publicIP}
      />
      <CopyableIpLine
        title={t('clients.privateIp')}
        icon={<Wifi className="h-3.5 w-3.5" />}
        value={privateIP}
      />
    </div>
  );
}

function MobileInfoItem({
  label,
  children,
}: {
  label: string;
  children: ReactNode;
}) {
  return (
    <div className="min-w-0">
      <div className="text-[0.7rem] font-medium uppercase text-muted-foreground/70">{label}</div>
      <div className="mt-1 min-w-0 text-sm text-foreground">{children}</div>
    </div>
  );
}

function ClientMobileCard({
  client,
  onNavigate,
}: {
  client: Client;
  onNavigate: () => void;
}) {
  const { t } = useTranslation();

  return (
    <div className="flex flex-col gap-3 border-b border-border/40 p-4 last:border-b-0">
      <button
        type="button"
        className="min-w-0 truncate text-left text-base font-semibold text-foreground transition-colors hover:text-primary focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/50"
        onClick={onNavigate}
      >
        {getClientDisplayName(client)}
      </button>

      <div className="min-w-0">
        <DashboardClientNetworkInfo client={client} compact />
      </div>

      <div className="grid grid-cols-2 gap-x-4 gap-y-3 border-t border-border/40 pt-3">
        <MobileInfoItem label={t('dashboard.system')}>
          <span className="block truncate" title={`${client.info.os}/${client.info.arch}`}>
            {client.info.os}/{client.info.arch}
          </span>
        </MobileInfoItem>
        <MobileInfoItem label={t('dashboard.resources')}>
          {client.online && client.stats ? (
            <span className="flex min-w-0 items-center gap-3 text-muted-foreground">
              <span className="inline-flex items-center gap-1">
                <Cpu className="h-3.5 w-3.5 shrink-0" />
                {formatPercent(client.stats.cpu_usage)}
              </span>
              <span className="inline-flex items-center gap-1">
                <HardDrive className="h-3.5 w-3.5 shrink-0" />
                {formatPercent(client.stats.mem_usage)}
              </span>
            </span>
          ) : (
            <span className="text-muted-foreground">-</span>
          )}
        </MobileInfoItem>
      </div>
    </div>
  );
}

const DashboardClientDesktopRow = memo(function DashboardClientDesktopRow({
  client,
  onNavigate,
  onDelete,
}: {
  client: Client;
  onNavigate: (clientId: string) => void;
  onDelete?: (client: Client) => void;
}) {
  const { t } = useTranslation();

  return (
    <tr className="hover:bg-muted/30 transition-colors">
      <td className="px-6 py-3 font-medium text-foreground">{getClientDisplayName(client)}</td>
      <td className="px-6 py-3">
        {client.online ? (
          <span className="inline-flex items-center gap-1.5 px-2 py-1 rounded-md bg-emerald-500/10 text-emerald-500 text-xs font-medium">
            <div className="w-1.5 h-1.5 rounded-full bg-emerald-500" />
            {t('clients.online')}
          </span>
        ) : (
          <span className="inline-flex items-center gap-1.5 px-2 py-1 rounded-md bg-muted text-muted-foreground text-xs font-medium">
            <div className="w-1.5 h-1.5 rounded-full bg-muted-foreground" />
            {t('clients.offline')}
          </span>
        )}
      </td>
      <td className="px-6 py-3 text-muted-foreground">{client.info.os} / {client.info.arch}</td>
      <td className="px-6 py-3">
        {client.online && client.stats ? (
          <div className="flex items-center gap-3">
            <span className="flex items-center gap-1"><Cpu className="w-3 h-3 text-muted-foreground" /> {formatPercent(client.stats.cpu_usage)}</span>
            <span className="flex items-center gap-1"><HardDrive className="w-3 h-3 text-muted-foreground" /> {formatPercent(client.stats.mem_usage)}</span>
          </div>
        ) : (
          <span className="text-muted-foreground">-</span>
        )}
      </td>
      <td className="px-6 py-3 text-right">
        <div className="flex items-center justify-end gap-1">
          <IconActionButton label={t('dashboard.viewDetails')} onClick={() => onNavigate(client.id)}>
            <Eye className="h-4 w-4" />
          </IconActionButton>
          {!client.online && onDelete && (
            <IconActionButton label={t('dashboard.deleteOfflineNode')} variant="destructive" onClick={() => onDelete(client)}>
              <Trash2 className="h-4 w-4" />
            </IconActionButton>
          )}
        </div>
      </td>
    </tr>
  );
});

interface DashboardClientTableContentProps {
  clients?: Client[];
  onNavigate: (clientId: string) => void;
  onDelete?: (client: Client) => void;
  onAddClient?: () => void;
}

export function DashboardClientTableContent({
  clients,
  onNavigate,
  onDelete,
  onAddClient,
}: DashboardClientTableContentProps) {
  const { t } = useTranslation();
  const [searchQuery, setSearchQuery] = useState('');
  const [page, setPage] = useState(1);
  const sortedClients = useMemo(() => sortClientsForDashboard(clients), [clients]);
  const filteredClients = useMemo(() => {
    const trimmed = searchQuery.trim().toLowerCase();
    if (!trimmed) return sortedClients;
    return sortedClients.filter((client) => matchesClientQuery(client, trimmed));
  }, [searchQuery, sortedClients]);
  const { pageItems: pagedClients, currentPage } = useMemo(
    () => sliceTablePage(filteredClients, page, DEFAULT_TABLE_PAGE_SIZE),
    [filteredClients, page],
  );
  const showSearch = sortedClients.length > CLIENT_SEARCH_VISIBLE_THRESHOLD || searchQuery.length > 0;
  const emptyMessage = sortedClients.length === 0 ? t('dashboard.noClients') : t('dashboard.noMatchingClients');

  return (
    <div className="rounded-xl border border-border/40 bg-card/50 backdrop-blur-sm shadow-sm overflow-hidden">
      <div className="px-4 sm:px-6 py-3 sm:py-4 border-b border-border/40 bg-muted/20 flex items-center justify-between gap-3">
        <h3 className="font-semibold text-foreground flex items-center gap-2">
          <Laptop className="h-5 w-5 text-primary" />
          {t('dashboard.onlineEndpoints')}
        </h3>
        <div className="flex items-center gap-2">
          {showSearch && (
            <div className="relative hidden sm:block">
              <Search className="absolute left-2.5 top-2 h-4 w-4 text-muted-foreground" />
              <input
                type="text"
                placeholder={t('dashboard.searchClientsPlaceholder')}
                value={searchQuery}
                onChange={(e) => {
                  setSearchQuery(e.target.value);
                  setPage(1);
                }}
                className="h-8 w-44 rounded bg-background pl-8 pr-3 text-xs border border-border/50 focus:outline-none focus:border-primary/50"
              />
            </div>
          )}
          {onAddClient && (
            <Button type="button" variant="secondary" size="sm" onClick={onAddClient}>
              <LayersPlus className="h-4 w-4 mr-1.5" />
              {t('dashboard.addClient')}
            </Button>
          )}
        </div>
      </div>

      {/* Mobile: Card List */}
      <div className="md:hidden">
        {pagedClients.length === 0 ? (
          <div className="px-4 py-8 text-center text-muted-foreground text-sm">{emptyMessage}</div>
        ) : (
          pagedClients.map((client) => (
            <ClientMobileCard
              key={client.id}
              client={client}
              onNavigate={() => onNavigate(client.id)}
            />
          ))
        )}
      </div>

      {/* Desktop: Table */}
      <div className="hidden md:block overflow-x-auto">
        <table className="w-full text-sm text-left">
          <thead className="text-xs text-muted-foreground bg-muted/30 uppercase">
            <tr>
              <th className="px-6 py-3 font-medium">{t('dashboard.nodeName')}</th>
              <th className="px-6 py-3 font-medium">{t('tunnels.status')}</th>
              <th className="px-6 py-3 font-medium">{t('dashboard.systemArch')}</th>
              <th className="px-6 py-3 font-medium">{t('dashboard.cpuMemory')}</th>
              <th className="px-6 py-3 font-medium text-right">{t('tunnels.actions')}</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-border/40">
            {pagedClients.length === 0 ? (
              <tr>
                <td colSpan={5} className="px-6 py-8 text-center text-muted-foreground">
                  {emptyMessage}
                </td>
              </tr>
            ) : (
              pagedClients.map((client) => (
                <DashboardClientDesktopRow
                  key={client.id}
                  client={client}
                  onNavigate={onNavigate}
                  onDelete={onDelete}
                />
              ))
            )}
          </tbody>
        </table>
      </div>

      <TablePaginationBar
        totalItems={filteredClients.length}
        page={currentPage}
        pageSize={DEFAULT_TABLE_PAGE_SIZE}
        onPageChange={setPage}
      />
    </div>
  );
}

export function DashboardClientTable({ scope }: { scope: ResourceScope }) {
  const { t } = useTranslation();
  const { data: clients, isLoading } = useClients(scope);
  const deleteClient = useDeleteClient(scope);
  const navigate = useNavigate();
  const { openAddClientDialog } = useAddClientDialog();
  const [deleteTarget, setDeleteTarget] = useState<Client | null>(null);

  if (isLoading) {
    return <Skeleton className="h-64 rounded-xl" />;
  }

  return (
    <>
      <DashboardClientTableContent
        clients={clients}
        onNavigate={(clientId) => {
          if (scope.kind === 'admin-user') {
            navigate({
              to: '/dashboard/users/$userId/clients/$clientId',
              params: { userId: scope.userId, clientId },
            });
            return;
          }
          navigate({ to: '/dashboard/clients/$clientId', params: { clientId } });
        }}
        onDelete={setDeleteTarget}
        onAddClient={openAddClientDialog}
      />
      <ConfirmDialog
        open={deleteTarget !== null}
        title={t('dashboard.deleteOfflineNode')}
        description={t('dashboard.deleteOfflineNodeDescription', { name: deleteTarget ? getClientDisplayName(deleteTarget) : '' })}
        confirmLabel={t('common.delete')}
        variant="destructive"
        onConfirm={() => {
          if (!deleteTarget) return;
          const target = deleteTarget;
          deleteClient.mutate(target.id, {
            onSuccess: () => toast.success(t('dashboard.nodeDeleted', { name: getClientDisplayName(target) })),
            onError: (err) => toast.error((err as Error).message),
          });
          setDeleteTarget(null);
        }}
        onCancel={() => setDeleteTarget(null)}
      />
    </>
  );
}
