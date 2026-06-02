import { useState } from 'react';
import { createRoute } from '@tanstack/react-router';
import { adminRoute } from '../admin';
import {
  useAdminKeys,
  useCreateAPIKey,
  useDeleteAPIKey,
  useDisableAPIKey,
  useEnableAPIKey,
} from '@/hooks/use-admin-keys';
import { Skeleton } from '@/components/ui/skeleton';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Key, Power, PowerOff, Trash2 } from 'lucide-react';
import { TableActionIconButton } from '@/components/custom/common/TableActionIconButton';
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogTrigger } from '@/components/ui/dialog';
import { ConfirmDialog } from '@/components/custom/common/ConfirmDialog';
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip';
import { useTranslation } from 'react-i18next';

export const adminKeysRoute = createRoute({
  getParentRoute: () => adminRoute,
  path: '/keys',
  component: AdminKeysPage,
});

const EXPIRY_OPTIONS = [
  { labelKey: 'common.unlimited', value: '' },
  { labelKey: 'clients.expiry1h', value: '1h' },
  { labelKey: 'clients.expiry3h', value: '3h' },
  { labelKey: 'clients.expiry1d', value: '24h' },
  { labelKey: 'clients.expiry7d', value: '168h' },
];

function AdminKeysPage() {
  const { t } = useTranslation();
  const { data: keys = [], isLoading } = useAdminKeys();
  const [isDialogOpen, setIsDialogOpen] = useState(false);
  const [newKeyName, setNewKeyName] = useState('');
  const [expiresIn, setExpiresIn] = useState('');
  const [maxUses, setMaxUses] = useState(0);
  const [createdRawKey, setCreatedRawKey] = useState('');
  const [deleteTarget, setDeleteTarget] = useState<{ id: string; name: string } | null>(null);
  const createKey = useCreateAPIKey();
  const enableKey = useEnableAPIKey();
  const disableKey = useDisableAPIKey();
  const deleteKey = useDeleteAPIKey();

  const handleCreate = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!newKeyName.trim()) {
      return;
    }

    try {
      const response = await createKey.mutateAsync({
        name: newKeyName,
        permissions: ['connect'],
        expires_in: expiresIn || undefined,
        max_uses: maxUses > 0 ? maxUses : undefined,
      });
      setCreatedRawKey(response.raw_key);
      setNewKeyName('');
      setExpiresIn('');
      setMaxUses(0);
    } catch (error) {
      console.error('create api key failed', error);
    }
  };

  const resetDialog = () => {
    setIsDialogOpen(false);
    setCreatedRawKey('');
    setNewKeyName('');
    setExpiresIn('');
    setMaxUses(0);
  };

  const formatExpiry = (expiresAt?: string) => {
    if (!expiresAt) return t('admin.neverExpires');
    const d = new Date(expiresAt);
    if (d < new Date()) return t('admin.expired');
    return d.toLocaleString();
  };

  const formatUsage = (useCount: number, maxUses: number) => {
    if (maxUses === 0) return `${useCount} / ∞`;
    return `${useCount} / ${maxUses}`;
  };

  return (
    <>
      <div className="flex flex-col gap-6 w-full">
        <div className="flex items-center justify-between">
          <h2 className="text-2xl font-bold tracking-tight">{t('admin.keysTitle')}</h2>
          <Dialog open={isDialogOpen} onOpenChange={(open) => {
            if (!open) {
              resetDialog();
              return;
            }
            setIsDialogOpen(true);
          }}>
            <DialogTrigger asChild>
              <Button className="gap-2">
                <Key className="w-4 h-4" /> {t('admin.newKey')}
              </Button>
            </DialogTrigger>
            <DialogContent>
              <DialogHeader>
                <DialogTitle>{t('admin.newApiKey')}</DialogTitle>
              </DialogHeader>
              {createdRawKey ? (
                <div className="flex flex-col gap-4 py-4">
                  <div className="bg-amber-500/10 text-amber-500 p-3 rounded-md text-sm">
                    {t('admin.copyKeyNow')}
                  </div>
                  <div className="p-3 bg-muted font-mono text-sm break-all rounded-md select-all">
                    {createdRawKey}
                  </div>
                  <Button onClick={resetDialog}>{t('clients.done')}</Button>
                </div>
              ) : (
                <form onSubmit={handleCreate} className="flex flex-col gap-4 py-4">
                  <div className="flex flex-col gap-2">
                    <label className="text-sm font-medium">{t('admin.namePurpose')}</label>
                    <Input
                      placeholder={t('admin.namePurposePlaceholder')}
                      value={newKeyName}
                      onChange={(e) => setNewKeyName(e.target.value)}
                      required
                      autoFocus
                    />
                  </div>
                  <div className="flex flex-col gap-2">
                    <label className="text-sm font-medium">{t('admin.expiresAt')}</label>
                    <select
                      value={expiresIn}
                      onChange={(e) => setExpiresIn(e.target.value)}
                      className="flex h-9 w-full rounded-md border border-input bg-transparent px-3 py-1 text-sm shadow-sm transition-colors focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
                    >
                      {EXPIRY_OPTIONS.map((opt) => (
                        <option key={opt.value} value={opt.value}>{t(opt.labelKey)}</option>
                      ))}
                    </select>
                  </div>
                  <div className="flex flex-col gap-2">
                    <label className="text-sm font-medium">{t('admin.maxUses')}</label>
                    <Input
                      type="number"
                      min={0}
                      value={maxUses || ''}
                      onChange={(e) => setMaxUses(Number.parseInt(e.target.value, 10) || 0)}
                      placeholder={t('admin.maxUsesPlaceholder')}
                    />
                    <p className="text-xs text-muted-foreground">{t('admin.maxUsesHelp')}</p>
                  </div>
                  <div className="text-xs text-muted-foreground">{t('admin.connectOnly')}</div>
                  <Button type="submit" disabled={createKey.isPending}>
                    {createKey.isPending ? t('admin.creating') : t('admin.create')}
                  </Button>
                </form>
              )}
            </DialogContent>
          </Dialog>
        </div>

        <div className="rounded-xl border border-border/40 bg-card/50 backdrop-blur-sm shadow-sm overflow-hidden">
          <table className="w-full text-sm text-left">
            <thead className="text-xs text-muted-foreground bg-muted/50 uppercase">
              <tr>
                <th className="px-6 py-3 font-medium">{t('admin.name')}</th>
                <th className="px-6 py-3 font-medium">{t('admin.permissions')}</th>
                <th className="px-6 py-3 font-medium">{t('admin.usage')}</th>
                <th className="px-6 py-3 font-medium">{t('admin.expiresAt')}</th>
                <th className="px-6 py-3 font-medium">{t('admin.status')}</th>
                <th className="px-6 py-3 font-medium text-right">{t('admin.actions')}</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-border/40">
              {isLoading ? (
                <tr><td colSpan={6} className="p-4"><Skeleton className="h-10 w-full" /></td></tr>
              ) : keys.length === 0 ? (
                <tr><td colSpan={6} className="px-6 py-8 text-center text-muted-foreground">{t('admin.noApiKeys')}</td></tr>
              ) : (
                keys.map((key) => (
                  <tr key={key.id} className="hover:bg-muted/30">
                    <td className="px-6 py-3 font-medium">{key.name}</td>
                    <td className="px-6 py-3 text-muted-foreground">{key.permissions.join(', ')}</td>
                    <td className="px-6 py-3 text-muted-foreground tabular-nums">
                      {formatUsage(key.use_count, key.max_uses)}
                    </td>
                    <td className="px-6 py-3 text-muted-foreground">
                      <span className={key.expires_at && new Date(key.expires_at) < new Date() ? 'text-destructive' : ''}>
                        {formatExpiry(key.expires_at)}
                      </span>
                    </td>
                    <td className="px-6 py-3">
                      {key.is_active ? (
                        <span className="text-emerald-500 font-medium">{t('common.enabled')}</span>
                      ) : (
                        <span className="text-muted-foreground">{t('common.disabled')}</span>
                      )}
                    </td>
                    <td className="px-6 py-3">
                      <div className="flex items-center justify-end gap-1">
                        <Tooltip>
                          <TooltipTrigger asChild>
                            <TableActionIconButton
                              label={key.is_active ? t('admin.disableApiKey') : t('admin.enableApiKey')}
                              tone={key.is_active ? 'warning' : 'success'}
                              onClick={() => key.is_active ? disableKey.mutate(key.id) : enableKey.mutate(key.id)}
                            >
                              {key.is_active ? (
                                <PowerOff className="h-4 w-4" />
                              ) : (
                                <Power className="h-4 w-4" />
                              )}
                            </TableActionIconButton>
                          </TooltipTrigger>
                          <TooltipContent>{key.is_active ? t('common.disabled') : t('common.enabled')}</TooltipContent>
                        </Tooltip>

                        <Tooltip>
                          <TooltipTrigger asChild>
                            <TableActionIconButton
                              label={t('admin.deleteApiKey')}
                              tone="destructive"
                              onClick={() => setDeleteTarget({ id: key.id, name: key.name })}
                            >
                              <Trash2 className="h-4 w-4" />
                            </TableActionIconButton>
                          </TooltipTrigger>
                          <TooltipContent>{t('common.delete')}</TooltipContent>
                        </Tooltip>
                      </div>
                    </td>
                  </tr>
                ))
              )}
            </tbody>
          </table>
        </div>
      </div>

      <ConfirmDialog
        open={deleteTarget !== null}
        title={t('admin.deleteApiKey')}
        description={t('admin.deleteApiKeyDescription', { name: deleteTarget?.name ?? '' })}
        confirmLabel={t('common.delete')}
        variant="destructive"
        onCancel={() => setDeleteTarget(null)}
        onConfirm={() => {
          if (!deleteTarget) {
            return;
          }
          deleteKey.mutate(deleteTarget.id);
          setDeleteTarget(null);
        }}
      />
    </>
  );
}
