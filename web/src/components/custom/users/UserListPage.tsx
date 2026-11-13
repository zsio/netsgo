import { useMemo, useState } from 'react';
import { Link } from '@tanstack/react-router';
import {
  AlertTriangle, Ellipsis, KeyRound, Loader2, RefreshCcw, ShieldCheck, ShieldOff, Trash2, UserPlus, UserRoundCheck, UserRoundPen, UserRoundX,
} from 'lucide-react';
import toast from 'react-hot-toast';
import { useTranslation } from 'react-i18next';

import { Button } from '@/components/ui/button';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuLabel, DropdownMenuSeparator, DropdownMenuTrigger } from '@/components/ui/dropdown-menu';
import { Input } from '@/components/ui/input';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { Skeleton } from '@/components/ui/skeleton';
import { ConfirmDialog } from '@/components/custom/common/ConfirmDialog';
import { clearClientSessionAndRedirect } from '@/lib/session';
import {
  USER_LIST_PAGE_SIZE,
  useCreateUser,
  useDeleteManagedUser,
  useDisableManagedUser,
  useEnableManagedUser,
  useManagedUserDeletionImpact,
  useRevokeManagedUserSessions,
  useSetManagedAdmin,
  useUpdateManagedPassword,
  useUpdateManagedUsername,
  useUsers,
} from '@/hooks/use-users';
import { useAuthStore } from '@/stores/auth-store';
import type { ManagedUser } from '@/types';
import {
  canSubmitUserTextAction,
  hasUserActionCapability,
  userDeletionImpactTotal,
} from './user-list-state';

type UserAction =
  | { kind: 'create' }
  | { kind: 'rename'; user: ManagedUser }
  | { kind: 'password'; user: ManagedUser }
  | { kind: 'set-admin'; user: ManagedUser }
  | { kind: 'disable'; user: ManagedUser }
  | { kind: 'enable'; user: ManagedUser }
  | { kind: 'delete'; user: ManagedUser }
  | { kind: 'revoke-sessions'; user: ManagedUser };

type Filters = {
  query: string;
  status: 'all' | 'active' | 'disabled';
  admin: 'all' | 'admin' | 'member';
};

const DEFAULT_FILTERS: Filters = { query: '', status: 'all', admin: 'all' };

function formatTimestamp(value: string) {
  const time = Date.parse(value);
  return Number.isFinite(time) ? new Date(time).toLocaleString() : value;
}

function statusBadgeClass(status: string) {
  return status === 'active'
    ? 'border-status-online-border bg-status-online-soft text-status-online-fg'
    : 'bg-muted text-muted-foreground';
}

export function UserListPage() {
  const { t } = useTranslation();
  const principal = useAuthStore((state) => state.user);
  const [draftFilters, setDraftFilters] = useState<Filters>(DEFAULT_FILTERS);
  const [filters, setFilters] = useState<Filters>(DEFAULT_FILTERS);
  const [cursor, setCursor] = useState<string | undefined>();
  const [previousCursors, setPreviousCursors] = useState<string[]>([]);
  const [action, setAction] = useState<UserAction | null>(null);
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');

  const users = useUsers({
    limit: USER_LIST_PAGE_SIZE,
    cursor,
    query: filters.query || undefined,
    status: filters.status === 'all' ? undefined : filters.status,
    isAdmin: filters.admin === 'all' ? undefined : filters.admin === 'admin',
  });
  const createUser = useCreateUser();
  const updateUsername = useUpdateManagedUsername();
  const updatePassword = useUpdateManagedPassword();
  const setAdmin = useSetManagedAdmin();
  const disableUser = useDisableManagedUser();
  const enableUser = useEnableManagedUser();
  const deleteUser = useDeleteManagedUser();
  const revokeSessions = useRevokeManagedUserSessions();

  const pending = createUser.isPending
    || updateUsername.isPending
    || updatePassword.isPending
    || setAdmin.isPending
    || disableUser.isPending
    || enableUser.isPending
    || deleteUser.isPending
    || revokeSessions.isPending;

  const confirmTitle = useMemo(() => {
    if (!action || action.kind === 'create' || action.kind === 'rename' || action.kind === 'password') return '';
    if (action.kind === 'set-admin') return action.user.is_admin ? t('users.removeAdmin') : t('users.makeAdmin');
    if (action.kind === 'disable') return t('users.disable');
    if (action.kind === 'enable') return t('users.enable');
    if (action.kind === 'delete') return t('users.delete');
    return t('users.revokeSessions');
  }, [action, t]);

  const confirmDescription = useMemo(() => {
    if (!action || action.kind === 'create' || action.kind === 'rename' || action.kind === 'password') return '';
    if (action.kind === 'delete') {
      return t('users.deleteDescription', { username: action.user.username });
    }
    if (action.kind === 'disable') return t('users.disableDescription', { username: action.user.username });
    if (action.kind === 'enable') return t('users.enableDescription', { username: action.user.username });
    if (action.kind === 'set-admin') {
      return t(action.user.is_admin ? 'users.removeAdminDescription' : 'users.makeAdminDescription', { username: action.user.username });
    }
    return t('users.revokeSessionsDescription', { username: action.user.username });
  }, [action, t]);

  const clearAction = () => {
    setAction(null);
    setUsername('');
    setPassword('');
  };

  const showError = (error: unknown) => {
    toast.error(error instanceof Error ? error.message : t('errors.generic'));
  };

  const afterCurrentSessionRevoked = (target: ManagedUser) => {
    if (target.id === principal?.id) {
      clearClientSessionAndRedirect();
    }
  };

  const submitTextAction = () => {
    if (!action) return;
    if (action.kind === 'create') {
      createUser.mutate({ username: username.trim(), password }, {
        onSuccess: () => {
          toast.success(t('users.created'));
          clearAction();
        },
        onError: showError,
      });
      return;
    }
    if (action.kind === 'rename') {
      updateUsername.mutate({ userId: action.user.id, username: username.trim() }, {
        onSuccess: () => {
          toast.success(t('users.usernameUpdated'));
          clearAction();
        },
        onError: showError,
      });
      return;
    }
    if (action.kind === 'password') {
      updatePassword.mutate({ userId: action.user.id, password }, {
        onSuccess: () => {
          toast.success(t('users.passwordReset'));
          afterCurrentSessionRevoked(action.user);
          clearAction();
        },
        onError: showError,
      });
    }
  };

  const confirmAction = () => {
    if (!action) return;
    if (action.kind === 'set-admin') {
      setAdmin.mutate({ userId: action.user.id, isAdmin: !action.user.is_admin }, {
        onSuccess: () => {
          toast.success(action.user.is_admin ? t('users.adminRemoved') : t('users.adminGranted'));
          afterCurrentSessionRevoked(action.user);
          clearAction();
        },
        onError: showError,
      });
      return;
    }
    if (action.kind === 'disable') {
      disableUser.mutate(action.user.id, {
        onSuccess: () => {
          toast.success(t('users.disabled'));
          afterCurrentSessionRevoked(action.user);
          clearAction();
        },
        onError: showError,
      });
      return;
    }
    if (action.kind === 'enable') {
      enableUser.mutate(action.user.id, {
        onSuccess: () => {
          toast.success(t('users.enabled'));
          clearAction();
        },
        onError: showError,
      });
      return;
    }
    if (action.kind === 'delete') {
      deleteUser.mutate(action.user.id, {
        onSuccess: () => {
          toast.success(t('users.deleted'));
          clearAction();
        },
        onError: showError,
      });
      return;
    }
    if (action.kind === 'revoke-sessions') {
      revokeSessions.mutate(action.user.id, {
        onSuccess: () => {
          toast.success(t('users.sessionsRevoked'));
          afterCurrentSessionRevoked(action.user);
          clearAction();
        },
        onError: showError,
      });
    }
  };

  const applyFilters = () => {
    setFilters(draftFilters);
    setCursor(undefined);
    setPreviousCursors([]);
  };

  const isTextDialog = action?.kind === 'create' || action?.kind === 'rename' || action?.kind === 'password';
  const selectedUser = action && 'user' in action ? action.user : null;

  return (
    <div className="z-10 mx-auto flex w-full max-w-6xl flex-col gap-5 p-4 sm:gap-6 sm:p-6 lg:p-8">
      <div className="flex flex-col gap-3 sm:flex-row sm:items-end sm:justify-between">
        <div>
          <h1 className="text-2xl font-semibold tracking-tight">{t('users.title')}</h1>
          <p className="mt-1 text-sm text-muted-foreground">{t('users.description')}</p>
        </div>
        <Button type="button" onClick={() => setAction({ kind: 'create' })}>
          <UserPlus data-icon="inline-start" />
          {t('users.add')}
        </Button>
      </div>

      <section className="rounded-xl border border-border/40 bg-card/50 shadow-sm backdrop-blur-sm">
        <div className="flex flex-col gap-2 border-b border-border/40 bg-muted/20 p-3 sm:flex-row sm:items-center sm:p-4">
          <Input
            value={draftFilters.query}
            onChange={(event) => setDraftFilters((current) => ({ ...current, query: event.target.value }))}
            onKeyDown={(event) => { if (event.key === 'Enter') applyFilters(); }}
            placeholder={t('users.searchPlaceholder')}
            className="sm:max-w-64"
          />
          <Select value={draftFilters.status} onValueChange={(value: Filters['status']) => setDraftFilters((current) => ({ ...current, status: value }))}>
            <SelectTrigger className="sm:w-36"><SelectValue /></SelectTrigger>
            <SelectContent>
              <SelectItem value="all">{t('users.allStatuses')}</SelectItem>
              <SelectItem value="active">{t('users.active')}</SelectItem>
              <SelectItem value="disabled">{t('users.disabled')}</SelectItem>
            </SelectContent>
          </Select>
          <Select value={draftFilters.admin} onValueChange={(value: Filters['admin']) => setDraftFilters((current) => ({ ...current, admin: value }))}>
            <SelectTrigger className="sm:w-36"><SelectValue /></SelectTrigger>
            <SelectContent>
              <SelectItem value="all">{t('users.allRoles')}</SelectItem>
              <SelectItem value="admin">{t('users.admin')}</SelectItem>
              <SelectItem value="member">{t('users.member')}</SelectItem>
            </SelectContent>
          </Select>
          <Button type="button" variant="secondary" onClick={applyFilters}>{t('common.search')}</Button>
        </div>

        {users.isLoading ? (
          <div className="space-y-3 p-4">
            {[1, 2, 3, 4].map((value) => <Skeleton key={value} className="h-12 w-full" />)}
          </div>
        ) : users.isError ? (
          <div className="p-8 text-center text-sm text-destructive">{(users.error as Error).message}</div>
        ) : (
          <>
            <div className="overflow-x-auto">
              <table className="w-full min-w-[720px] text-sm">
                <thead className="border-b border-border/40 bg-muted/20 text-left text-xs text-muted-foreground">
                  <tr>
                    <th className="px-4 py-3 font-medium">{t('users.username')}</th>
                    <th className="px-4 py-3 font-medium">{t('users.role')}</th>
                    <th className="px-4 py-3 font-medium">{t('users.status')}</th>
                    <th className="px-4 py-3 font-medium">{t('users.createdAt')}</th>
                    <th className="px-4 py-3 text-right font-medium">{t('tunnels.actions')}</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-border/40">
                  {(users.data?.items ?? []).map((user) => (
                    <tr key={user.id} className="hover:bg-muted/20">
                      <td className="px-4 py-3">
                        <Link
                          to="/dashboard/users/$userId"
                          params={{ userId: user.id }}
                          className="font-medium text-foreground hover:text-primary"
                        >
                          {user.username}
                        </Link>
                        {user.id === principal?.id ? <span className="ml-2 text-xs text-muted-foreground">{t('users.you')}</span> : null}
                      </td>
                      <td className="px-4 py-3">
                        <span className="inline-flex items-center gap-1.5 text-muted-foreground">
                          {user.is_admin ? <ShieldCheck className="size-3.5 text-primary" /> : <UserRoundCheck className="size-3.5" />}
                          {user.is_admin ? t('users.admin') : t('users.member')}
                        </span>
                      </td>
                      <td className="px-4 py-3">
                        <span className={`rounded-full px-2 py-1 text-xs font-medium ${statusBadgeClass(user.status)}`}>
                          {user.status === 'active' ? t('users.active') : t('users.disabled')}
                        </span>
                      </td>
                      <td className="px-4 py-3 text-muted-foreground">{formatTimestamp(user.created_at)}</td>
                      <td className="px-4 py-3 text-right">
                        <UserActionsMenu user={user} onAction={setAction} />
                      </td>
                    </tr>
                  ))}
                  {(users.data?.items.length ?? 0) === 0 ? (
                    <tr><td colSpan={5} className="px-4 py-10 text-center text-muted-foreground">{t('users.empty')}</td></tr>
                  ) : null}
                </tbody>
              </table>
            </div>
            <div className="flex items-center justify-between gap-3 border-t border-border/40 p-3 sm:p-4">
              <Button
                type="button"
                variant="outline"
                disabled={previousCursors.length === 0 || users.isFetching}
                onClick={() => {
                  const nextHistory = [...previousCursors];
                  const previous = nextHistory.pop();
                  setPreviousCursors(nextHistory);
                  setCursor(previous);
                }}
              >
                {t('common.previous')}
              </Button>
              <Button
                type="button"
                variant="outline"
                disabled={!users.data?.has_more || !users.data.next_cursor || users.isFetching}
                onClick={() => {
                  const nextCursor = users.data?.next_cursor;
                  if (!nextCursor) return;
                  setPreviousCursors((history) => [...history, cursor ?? '']);
                  setCursor(nextCursor);
                }}
              >
                {t('common.next')}
              </Button>
            </div>
          </>
        )}
      </section>

      <Dialog open={isTextDialog} onOpenChange={(open) => { if (!open) clearAction(); }}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{action?.kind === 'create' ? t('users.add') : action?.kind === 'rename' ? t('users.rename') : t('users.resetPassword')}</DialogTitle>
            <DialogDescription>{action?.kind === 'create' ? t('users.addDescription') : selectedUser?.username}</DialogDescription>
          </DialogHeader>
          <div className="space-y-3">
            {action?.kind !== 'password' ? (
              <Input
                autoFocus
                value={username}
                placeholder={t('users.username')}
                onChange={(event) => setUsername(event.target.value)}
              />
            ) : null}
            {action?.kind === 'create' || action?.kind === 'password' ? (
              <Input
                type="password"
                value={password}
                placeholder={t('users.password')}
                autoComplete="new-password"
                onChange={(event) => setPassword(event.target.value)}
              />
            ) : null}
          </div>
          <DialogFooter>
            <Button type="button" variant="outline" onClick={clearAction} disabled={pending}>{t('common.cancel')}</Button>
            <Button
              type="button"
              disabled={pending || !canSubmitUserTextAction(
                action?.kind === 'create' || action?.kind === 'rename' || action?.kind === 'password'
                  ? action.kind
                  : undefined,
                username,
                password,
              )}
              onClick={submitTextAction}
            >
              {action?.kind === 'create' ? t('users.add') : t('common.save')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <ConfirmDialog
        open={Boolean(action && !isTextDialog && action.kind !== 'delete')}
        title={confirmTitle}
        description={confirmDescription}
        confirmLabel={action?.kind === 'delete' ? t('common.delete') : t('common.confirm')}
        variant={action?.kind === 'delete' || action?.kind === 'disable' ? 'destructive' : 'default'}
        onConfirm={confirmAction}
        onCancel={clearAction}
      />

      <DeleteUserDialog
        user={action?.kind === 'delete' ? action.user : null}
        isDeleting={deleteUser.isPending}
        onCancel={clearAction}
        onConfirm={confirmAction}
      />
    </div>
  );
}

function UserActionsMenu({ user, onAction }: { user: ManagedUser; onAction: (action: UserAction) => void }) {
  const { t } = useTranslation();
  const can = (capability: keyof NonNullable<ManagedUser['actions']>) => (
    hasUserActionCapability(user.actions, capability)
  );
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button type="button" variant="ghost" size="icon-sm" aria-label={t('users.actions')}>
          <Ellipsis />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="min-w-48">
        <DropdownMenuLabel>{user.username}</DropdownMenuLabel>
        <DropdownMenuItem onSelect={() => onAction({ kind: 'rename', user })} disabled={!can('can_update_username')}>
          <UserRoundPen />{t('users.rename')}
        </DropdownMenuItem>
        <DropdownMenuItem onSelect={() => onAction({ kind: 'password', user })} disabled={!can('can_update_password')}>
          <KeyRound />{t('users.resetPassword')}
        </DropdownMenuItem>
        <DropdownMenuItem onSelect={() => onAction({ kind: 'revoke-sessions', user })} disabled={!can('can_revoke_sessions')}>
          <UserRoundX />{t('users.revokeSessions')}
        </DropdownMenuItem>
        <DropdownMenuSeparator />
        <DropdownMenuItem onSelect={() => onAction({ kind: 'set-admin', user })} disabled={!can('can_change_admin')}>
          {user.is_admin ? <ShieldOff /> : <ShieldCheck />}
          {user.is_admin ? t('users.removeAdmin') : t('users.makeAdmin')}
        </DropdownMenuItem>
        {user.status === 'active' ? (
          <DropdownMenuItem onSelect={() => onAction({ kind: 'disable', user })} disabled={!can('can_disable')}>
            <UserRoundX />{t('users.disable')}
          </DropdownMenuItem>
        ) : (
          <DropdownMenuItem onSelect={() => onAction({ kind: 'enable', user })} disabled={!can('can_enable')}>
            <UserRoundCheck />{t('users.enable')}
          </DropdownMenuItem>
        )}
        <DropdownMenuSeparator />
        <DropdownMenuItem
          variant="destructive"
          onSelect={() => onAction({ kind: 'delete', user })}
          disabled={user.status !== 'disabled' || !can('can_delete')}
        >
          <Trash2 />{t('users.delete')}
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

function DeleteUserDialog({
  user,
  isDeleting,
  onCancel,
  onConfirm,
}: {
  user: ManagedUser | null;
  isDeleting: boolean;
  onCancel: () => void;
  onConfirm: () => void;
}) {
  const { t } = useTranslation();
  const impact = useManagedUserDeletionImpact(user?.id);
  const canDelete = Boolean(
    user
    && user.status === 'disabled'
    && hasUserActionCapability(user.actions, 'can_delete')
    && impact.data
    && !impact.isFetching
    && !impact.isError
    && !isDeleting,
  );
  const rows = impact.data ? [
    [t('users.deletionImpactApiKeys'), impact.data.api_keys],
    [t('users.deletionImpactClients'), impact.data.clients],
    [t('users.deletionImpactTunnels'), impact.data.tunnels],
    [t('users.deletionImpactTraffic'), impact.data.traffic_buckets],
    [t('users.deletionImpactActivity'), impact.data.activity_events],
  ] as const : [];

  return (
    <Dialog
      open={user !== null}
      onOpenChange={(open) => {
        if (!open && !isDeleting) onCancel();
      }}
    >
      <DialogContent>
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2 text-destructive">
            <AlertTriangle className="size-5" />
            {t('users.delete')}
          </DialogTitle>
          <DialogDescription>
            {t('users.deleteDescription', { username: user?.username ?? '' })}
          </DialogDescription>
        </DialogHeader>

        <div className="space-y-3" aria-live="polite">
          {impact.isPending || impact.isFetching ? (
            <div className="flex min-h-28 items-center justify-center gap-2 rounded-lg border border-border/50 bg-muted/20 text-sm text-muted-foreground">
              <Loader2 className="size-4 animate-spin" />
              {t('users.deletionImpactLoading')}
            </div>
          ) : impact.isError ? (
            <div className="space-y-3 rounded-lg border border-destructive/30 bg-destructive/5 p-3">
              <p className="text-sm font-medium text-destructive">{t('users.deletionImpactLoadFailed')}</p>
              <p className="text-xs text-muted-foreground">
                {impact.error instanceof Error ? impact.error.message : t('errors.generic')}
              </p>
              <Button type="button" variant="outline" size="sm" onClick={() => { void impact.refetch(); }}>
                <RefreshCcw data-icon="inline-start" />
                {t('common.retry')}
              </Button>
            </div>
          ) : impact.data ? (
            <div className="overflow-hidden rounded-lg border border-border/50">
              <div className="flex items-center justify-between border-b border-border/50 bg-muted/30 px-3 py-2 text-sm font-medium">
                <span>{t('users.deletionImpactTitle')}</span>
                <span className="tabular-nums">
                  {t('users.deletionImpactTotal', { count: userDeletionImpactTotal(impact.data) })}
                </span>
              </div>
              <dl className="divide-y divide-border/40">
                {rows.map(([label, count]) => (
                  <div key={label} className="flex items-center justify-between px-3 py-2 text-sm">
                    <dt className="text-muted-foreground">{label}</dt>
                    <dd className="font-medium tabular-nums">{count}</dd>
                  </div>
                ))}
              </dl>
              <p className="border-t border-border/50 bg-muted/20 px-3 py-2 text-xs text-muted-foreground">
                {t('users.deletionImpactGeneratedAt', { time: formatTimestamp(impact.data.generated_at) })}
              </p>
            </div>
          ) : null}

          <p className="rounded-lg border border-destructive/30 bg-destructive/5 p-3 text-sm font-medium text-destructive">
            {t('users.deletionIrreversible')}
          </p>
        </div>

        <DialogFooter>
          <Button type="button" variant="outline" onClick={onCancel} disabled={isDeleting}>
            {t('common.cancel')}
          </Button>
          <Button type="button" variant="destructive" onClick={onConfirm} disabled={!canDelete}>
            {isDeleting ? <Loader2 className="animate-spin" data-icon="inline-start" /> : <Trash2 data-icon="inline-start" />}
            {isDeleting ? t('users.deleting') : t('common.delete')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
