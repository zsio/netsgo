import { useMemo, useState } from 'react';
import { createRoute, useNavigate } from '@tanstack/react-router';
import {
  Check,
  CheckCircle2,
  Fingerprint,
  KeyRound,
  Pencil,
  Plus,
  RotateCcw,
  ShieldCheck,
  ShieldOff,
  Trash2,
  UserRound,
  X,
} from 'lucide-react';
import toast from 'react-hot-toast';
import { useTranslation } from 'react-i18next';
import { adminRoute } from '../admin';
import { useAdminSecurity, useAdminSecurityMutations } from '@/hooks/use-admin-security';
import {
  isPasskeySupported,
  normalizeCreationOptions,
  publicKeyCredentialToJSON,
} from '@/lib/webauthn';
import { useAuthStore } from '@/stores/auth-store';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Skeleton } from '@/components/ui/skeleton';
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { cn } from '@/lib/utils';
import {
  emptyCredentialForm,
  emptyPasskeyForm,
  emptyPasswordForm,
  emptyUsernameForm,
  type CredentialForm,
  type PasskeyForm,
  type PasswordForm,
  type UsernameForm,
} from './security-state';
import type {
  PasskeyChallengeResponse,
  PasskeySummary,
  RecoveryCodesResponse,
  TOTPBeginResponse,
} from '@/types';

export const adminSecurityRoute = createRoute({
  getParentRoute: () => adminRoute,
  path: '/security',
  component: AdminSecurityPage,
});

function AdminSecurityPage() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const logout = useAuthStore((s) => s.logout);
  const { data, isLoading } = useAdminSecurity();
  const mutations = useAdminSecurityMutations();
  const [usernameForm, setUsernameForm] = useState(() => emptyUsernameForm());
  const [passwordForm, setPasswordForm] = useState(() => emptyPasswordForm());
  const [totpForm, setTotpForm] = useState(() => emptyCredentialForm());
  const [totpSetup, setTotpSetup] = useState<TOTPBeginResponse | null>(null);
  const [totpCode, setTotpCode] = useState('');
  const [recoveryCodes, setRecoveryCodes] = useState<string[]>([]);
  const [passkeyForm, setPasskeyForm] = useState(() => emptyPasskeyForm());
  const [renamePasskey, setRenamePasskey] = useState<PasskeySummary | null>(null);
  const [renameForm, setRenameForm] = useState(() => emptyPasskeyForm());
  const [deletePasskey, setDeletePasskey] = useState<PasskeySummary | null>(null);
  const [deleteForm, setDeleteForm] = useState(() => emptyCredentialForm());
  const [securitySection, setSecuritySection] = useState('account');
  const [accountDialog, setAccountDialog] = useState<'username' | 'password' | null>(null);
  const [totpAction, setTotpAction] = useState<'enable' | 'regenerate' | 'disable' | null>(null);
  const [passkeyAddOpen, setPasskeyAddOpen] = useState(false);

  const passkeySupported = useMemo(() => isPasskeySupported(), []);

  const showSecurityError = (error: unknown) => {
    toast.error(error instanceof Error ? error.message : t('errors.generic'));
  };

  const clearCredentialDialogs = () => {
    setUsernameForm(emptyUsernameForm());
    setPasswordForm(emptyPasswordForm());
    setTotpForm(emptyCredentialForm());
    setTotpSetup(null);
    setTotpCode('');
    setPasskeyForm(emptyPasskeyForm());
    setRenameForm(emptyPasskeyForm());
    setDeleteForm(emptyCredentialForm());
    setAccountDialog(null);
    setTotpAction(null);
    setPasskeyAddOpen(false);
    setRenamePasskey(null);
    setDeletePasskey(null);
  };

  const forceRelogin = (message: string) => {
    clearCredentialDialogs();
    toast.success(message);
    logout();
    void navigate({ to: '/login' });
  };

  const closeAccountDialog = () => {
    setAccountDialog(null);
    setUsernameForm(emptyUsernameForm());
    setPasswordForm(emptyPasswordForm());
  };

  const closeTOTPAction = () => {
    setTotpAction(null);
    setTotpForm(emptyCredentialForm());
  };

  const closeTOTPSetup = () => {
    setTotpSetup(null);
    setTotpCode('');
  };

  const closePasskeyAdd = () => {
    setPasskeyAddOpen(false);
    setPasskeyForm(emptyPasskeyForm());
  };

  const closeRenamePasskey = () => {
    setRenamePasskey(null);
    setRenameForm(emptyPasskeyForm());
  };

  const closeDeletePasskey = () => {
    setDeletePasskey(null);
    setDeleteForm(emptyCredentialForm());
  };

  const currentUser = data?.user;
  const requiresMFA = data?.totp_enabled ?? false;

  const handleUsernameSubmit = async (event: React.FormEvent) => {
    event.preventDefault();
    try {
      const resp = await mutations.updateUsername.mutateAsync({
        current_password: usernameForm.currentPassword,
        mfa_code: usernameForm.mfaCode || undefined,
        new_username: usernameForm.newUsername,
      });
      if (resp.requires_relogin) {
        forceRelogin(t('admin.securityReloginRequired'));
        return;
      }
      closeAccountDialog();
    } catch (error) {
      showSecurityError(error);
    }
  };

  const handlePasswordSubmit = async (event: React.FormEvent) => {
    event.preventDefault();
    try {
      const resp = await mutations.updatePassword.mutateAsync({
        current_password: passwordForm.currentPassword,
        mfa_code: passwordForm.mfaCode || undefined,
        new_password: passwordForm.newPassword,
      });
      if (resp.requires_relogin) {
        forceRelogin(t('admin.securityReloginRequired'));
        return;
      }
      closeAccountDialog();
    } catch (error) {
      showSecurityError(error);
    }
  };

  const beginTOTP = async (event: React.FormEvent) => {
    event.preventDefault();
    try {
      const setup = await mutations.beginTOTP.mutateAsync({
        current_password: totpForm.currentPassword,
        mfa_code: totpForm.mfaCode || undefined,
      });
      setTotpSetup(setup);
      closeTOTPAction();
    } catch (error) {
      showSecurityError(error);
    }
  };

  const confirmTOTP = async () => {
    if (!totpSetup) return;
    try {
      const resp = await mutations.confirmTOTP.mutateAsync({
        setup_token: totpSetup.setup_token,
        code: totpCode,
      });
      setRecoveryCodes(resp.recovery_codes);
      closeTOTPSetup();
      setTotpForm(emptyCredentialForm());
    } catch (error) {
      showSecurityError(error);
    }
  };

  const disableTOTP = async () => {
    try {
      const resp = await mutations.disableTOTP.mutateAsync({
        current_password: totpForm.currentPassword,
        mfa_code: totpForm.mfaCode || undefined,
      });
      if (resp.requires_relogin) {
        forceRelogin(t('admin.securityReloginRequired'));
        return;
      }
      closeTOTPAction();
    } catch (error) {
      showSecurityError(error);
    }
  };

  const regenerateRecoveryCodes = async () => {
    try {
      const resp: RecoveryCodesResponse = await mutations.regenerateRecoveryCodes.mutateAsync({
        current_password: totpForm.currentPassword,
        mfa_code: totpForm.mfaCode || undefined,
      });
      setRecoveryCodes(resp.recovery_codes);
      closeTOTPAction();
    } catch (error) {
      showSecurityError(error);
    }
  };

  const addPasskey = async (event: React.FormEvent) => {
    event.preventDefault();
    if (!passkeySupported) {
      toast.error(t('admin.passkeyUnsupported'));
      return;
    }
    try {
      const begin: PasskeyChallengeResponse = await mutations.beginPasskey.mutateAsync({
        current_password: passkeyForm.currentPassword,
        mfa_code: passkeyForm.mfaCode || undefined,
        name: passkeyForm.name,
      });
      const credential = await navigator.credentials.create({
        publicKey: normalizeCreationOptions(begin.public_key),
      });
      if (!(credential instanceof PublicKeyCredential)) {
        toast.error(t('admin.passkeyCreateFailed'));
        return;
      }
      const resp = await mutations.finishPasskey.mutateAsync({
        challenge_id: begin.challenge_id,
        credential: publicKeyCredentialToJSON(credential),
      });
      if (resp.requires_relogin) {
        forceRelogin(t('admin.securityReloginRequired'));
        return;
      }
      closePasskeyAdd();
    } catch (error) {
      showSecurityError(error);
    }
  };

  const submitRenamePasskey = async () => {
    if (!renamePasskey) return;
    try {
      await mutations.renamePasskey.mutateAsync({
        id: renamePasskey.id,
        current_password: renameForm.currentPassword,
        mfa_code: renameForm.mfaCode || undefined,
        name: renameForm.name,
      });
      closeRenamePasskey();
      toast.success(t('admin.passkeyRenamed'));
    } catch (error) {
      showSecurityError(error);
    }
  };

  const submitDeletePasskey = async () => {
    if (!deletePasskey) return;
    try {
      const resp = await mutations.deletePasskey.mutateAsync({
        id: deletePasskey.id,
        current_password: deleteForm.currentPassword,
        mfa_code: deleteForm.mfaCode || undefined,
      });
      if (resp.requires_relogin) {
        forceRelogin(t('admin.securityReloginRequired'));
        return;
      }
      closeDeletePasskey();
    } catch (error) {
      showSecurityError(error);
    }
  };

  if (isLoading || !data || !currentUser) {
    return <SecuritySkeleton />;
  }

  return (
    <div className="pb-10">
      <Tabs value={securitySection} onValueChange={setSecuritySection}>
        <TabsList>
          <SecuritySectionTrigger value="account" icon={UserRound} title={t('admin.accountPasswordTab')} />
          <SecuritySectionTrigger value="totp" icon={ShieldCheck} title={t('admin.twoFactorAuth')} />
          <SecuritySectionTrigger value="passkey" icon={Fingerprint} title={t('admin.passkeys')} />
        </TabsList>

        <TabsContent value="account" className="mt-0">
          <AccountPasswordSection
            currentUser={currentUser}
            onEditUsername={() => {
              setUsernameForm(emptyUsernameForm(currentUser.username));
              setAccountDialog('username');
            }}
            onEditPassword={() => {
              setPasswordForm(emptyPasswordForm());
              setAccountDialog('password');
            }}
          />
        </TabsContent>

        <TabsContent value="totp" className="mt-0">
          <TOTPSection
            enabled={data.totp_enabled}
            recoveryCodesRemaining={data.recovery_codes_remaining}
            onEnableRequest={() => {
              setTotpForm(emptyCredentialForm());
              setTotpAction('enable');
            }}
            onRegenerateRequest={() => {
              setTotpForm(emptyCredentialForm());
              setTotpAction('regenerate');
            }}
            onDisableRequest={() => {
              setTotpForm(emptyCredentialForm());
              setTotpAction('disable');
            }}
          />
        </TabsContent>

        <TabsContent value="passkey" className="mt-0">
          <PasskeySection
            passkeys={data.passkeys}
            passkeySupported={passkeySupported}
            onAddRequest={() => {
              setPasskeyForm(emptyPasskeyForm());
              setPasskeyAddOpen(true);
            }}
            onRename={(passkey) => {
              setRenamePasskey(passkey);
              setRenameForm(emptyPasskeyForm(passkey.name));
            }}
            onDelete={(passkey) => {
              setDeletePasskey(passkey);
              setDeleteForm(emptyCredentialForm());
            }}
          />
        </TabsContent>
      </Tabs>

      <UsernameDialog
        open={accountDialog === 'username'}
        form={usernameForm}
        requiresMFA={requiresMFA}
        isPending={mutations.updateUsername.isPending}
        onChange={(patch) => setUsernameForm({ ...usernameForm, ...patch })}
        onClose={closeAccountDialog}
        onSubmit={handleUsernameSubmit}
      />
      <PasswordDialog
        open={accountDialog === 'password'}
        form={passwordForm}
        requiresMFA={requiresMFA}
        isPending={mutations.updatePassword.isPending}
        onChange={(patch) => setPasswordForm({ ...passwordForm, ...patch })}
        onClose={closeAccountDialog}
        onSubmit={handlePasswordSubmit}
      />
      <TOTPActionDialog
        action={totpAction}
        form={totpForm}
        requiresMFA={requiresMFA}
        isPending={
          totpAction === 'enable'
            ? mutations.beginTOTP.isPending
            : totpAction === 'regenerate'
              ? mutations.regenerateRecoveryCodes.isPending
              : mutations.disableTOTP.isPending
        }
        onChange={(patch) => setTotpForm({ ...totpForm, ...patch })}
        onClose={closeTOTPAction}
        onBegin={beginTOTP}
        onRegenerate={regenerateRecoveryCodes}
        onDisable={disableTOTP}
      />
      <TOTPSetupDialog setup={totpSetup} code={totpCode} onCodeChange={setTotpCode} onCancel={closeTOTPSetup} onConfirm={confirmTOTP} />
      <RecoveryCodesDialog codes={recoveryCodes} onClose={() => {
        setRecoveryCodes([]);
        forceRelogin(t('admin.securityReloginRequired'));
      }} />
      <PasskeyAddDialog
        open={passkeyAddOpen}
        form={passkeyForm}
        requiresMFA={requiresMFA}
        passkeySupported={passkeySupported}
        isAdding={mutations.beginPasskey.isPending || mutations.finishPasskey.isPending}
        onChange={(patch) => setPasskeyForm({ ...passkeyForm, ...patch })}
        onClose={closePasskeyAdd}
        onSubmit={addPasskey}
      />
      <PasskeyNameDialog passkey={renamePasskey} form={renameForm} requiresMFA={requiresMFA} onChange={(patch) => setRenameForm({ ...renameForm, ...patch })} onClose={closeRenamePasskey} onSubmit={submitRenamePasskey} />
      <PasskeyDeleteDialog passkey={deletePasskey} form={deleteForm} requiresMFA={requiresMFA} onChange={(patch) => setDeleteForm({ ...deleteForm, ...patch })} onClose={closeDeletePasskey} onSubmit={submitDeletePasskey} />
    </div>
  );
}

function SecuritySkeleton() {
  return (
    <div className="flex flex-col gap-5 pb-10">
      <Skeleton className="h-10 w-full max-w-md rounded-lg" />
      <Skeleton className="h-[360px] w-full rounded-xl" />
    </div>
  );
}

function SecuritySectionTrigger({ value, icon: Icon, title, description }: {
  value: string;
  icon: React.ComponentType<React.SVGProps<SVGSVGElement>>;
  title: string;
  description?: string;
}) {
  return (
    <TabsTrigger value={value}>
      <Icon data-icon="inline-start" />
      <span className="truncate">{title}</span>
      {description ? <span className="sr-only">{description}</span> : null}
    </TabsTrigger>
  );
}

function AccountPasswordSection({
  currentUser,
  onEditUsername,
  onEditPassword,
}: {
  currentUser: { username: string };
  onEditUsername: () => void;
  onEditPassword: () => void;
}) {
  const { t } = useTranslation();

  return (
    <SettingsList>
      <SettingRow
        title={t('admin.currentAdmin')}
        description={t('admin.accountProfileDescription')}
        value={currentUser.username}
        actionLabel={t('admin.updateUsername')}
        onAction={onEditUsername}
      />
      <SettingRow
        title={t('admin.passwordSecurity')}
        description={t('admin.passwordSecurityDescription')}
        value={t('admin.passwordConfigured')}
        actionLabel={t('admin.updatePassword')}
        onAction={onEditPassword}
      />
    </SettingsList>
  );
}

function TOTPSection({
  enabled,
  recoveryCodesRemaining,
  onEnableRequest,
  onRegenerateRequest,
  onDisableRequest,
}: {
  enabled: boolean;
  recoveryCodesRemaining: number;
  onEnableRequest: () => void;
  onRegenerateRequest: () => void;
  onDisableRequest: () => void;
}) {
  const { t } = useTranslation();

  return (
    <SettingsList>
      <SettingRow
        title={enabled ? t('admin.totpEnabled') : t('admin.totpDisabled')}
        description={
          enabled
            ? t('admin.totpEnabledDescription', { count: recoveryCodesRemaining })
            : t('admin.totpDisabledDescription')
        }
        value={(
          <Badge variant={enabled ? 'default' : 'secondary'}>
            {enabled ? t('common.enabled') : t('common.disabled')}
          </Badge>
        )}
        actionLabel={enabled ? t('admin.disableTOTP') : t('admin.enableTOTP')}
        actionVariant={enabled ? 'destructive' : 'default'}
        onAction={enabled ? onDisableRequest : onEnableRequest}
      />

      {enabled ? (
        <SettingRow
          title={t('admin.recoveryCodes')}
          description={t('admin.recoveryCodesManageDescription')}
          value={t('admin.recoveryCodesRemaining', { count: recoveryCodesRemaining })}
          actionLabel={t('admin.regenerateRecoveryCodes')}
          onAction={onRegenerateRequest}
        />
      ) : null}
    </SettingsList>
  );
}

function PasskeySection({
  passkeys,
  passkeySupported,
  onAddRequest,
  onRename,
  onDelete,
}: {
  passkeys: PasskeySummary[];
  passkeySupported: boolean;
  onAddRequest: () => void;
  onRename: (passkey: PasskeySummary) => void;
  onDelete: (passkey: PasskeySummary) => void;
}) {
  const { t } = useTranslation();

  return (
    <SettingsList>
      <SettingRow
        title={t('admin.addPasskey')}
        description={passkeySupported ? t('admin.passkeyAddDescription') : t('admin.passkeyUnsupported')}
        value={!passkeySupported ? <Badge variant="secondary">{t('common.disabled')}</Badge> : undefined}
        actionLabel={t('admin.addPasskey')}
        actionDisabled={!passkeySupported}
        onAction={onAddRequest}
      />

      {passkeys.length === 0 ? (
        <SettingRow
          title={t('admin.noPasskeys')}
          description={t('admin.noPasskeysDescription')}
        />
      ) : (
        passkeys.map((passkey) => (
          <SettingRow
            key={passkey.id}
            title={passkey.name}
            description={t('admin.passkeyCredentialDescription')}
            actions={(
              <>
                <Button type="button" variant="ghost" size="icon-sm" onClick={() => onRename(passkey)} title={t('common.edit')} aria-label={t('common.edit')}>
                  <Pencil />
                </Button>
                <Button type="button" variant="ghost" size="icon-sm" onClick={() => onDelete(passkey)} title={t('common.delete')} aria-label={t('common.delete')}>
                  <Trash2 />
                </Button>
              </>
            )}
          />
        ))
      )}
    </SettingsList>
  );
}

function SettingRow({
  title,
  description,
  value,
  actionLabel,
  actionVariant = 'outline',
  actionDisabled = false,
  onAction,
  actions,
}: {
  title: string;
  description: string;
  value?: React.ReactNode;
  actionLabel?: string;
  actionVariant?: React.ComponentProps<typeof Button>['variant'];
  actionDisabled?: boolean;
  onAction?: () => void;
  actions?: React.ReactNode;
}) {
  return (
    <div className="grid gap-4 px-4 py-4 sm:grid-cols-[minmax(0,1fr)_auto] sm:items-center sm:px-5">
      <div className="min-w-0">
        <p className="font-medium text-foreground">{title}</p>
        <p className="mt-1 max-w-2xl text-sm text-muted-foreground">{description}</p>
      </div>
      <div className="flex min-w-0 items-center gap-3 sm:justify-end">
        {value ? (
          <span className="min-w-0 truncate text-sm font-medium text-muted-foreground">
            {value}
          </span>
        ) : null}
        {actions}
        {actionLabel && onAction ? (
          <Button type="button" variant={actionVariant} disabled={actionDisabled} onClick={onAction}>
            {actionLabel}
          </Button>
        ) : null}
      </div>
    </div>
  );
}

function SettingsList({ children }: { children: React.ReactNode }) {
  return (
    <div className="overflow-hidden rounded-xl border border-border/50 bg-background/90 divide-y divide-border/50">
      {children}
    </div>
  );
}

function FactorStatusRow({ enabled, title, description }: {
  enabled: boolean;
  title: string;
  description: string;
}) {
  return (
    <div className={cn(
      'flex items-start gap-3 rounded-lg border p-4',
      enabled ? 'border-primary/25 bg-primary/5' : 'border-border/50 bg-muted/20',
    )}>
      <div className={cn(
        'mt-0.5 flex size-8 shrink-0 items-center justify-center rounded-lg',
        enabled ? 'bg-primary/10 text-primary' : 'bg-muted text-muted-foreground',
      )}>
        {enabled ? <CheckCircle2 /> : <ShieldOff />}
      </div>
      <div>
        <p className="text-sm font-medium">{title}</p>
        <p className="mt-1 text-sm text-muted-foreground">{description}</p>
      </div>
    </div>
  );
}

function CredentialBlock({ requiresMFA, form, compact = false, onChange }: {
  requiresMFA: boolean;
  form: CredentialForm;
  compact?: boolean;
  onChange: (patch: Partial<CredentialForm>) => void;
}) {
  const { t } = useTranslation();

  return (
    <div className={cn('rounded-lg border border-border/50 bg-muted/20 p-4', compact && 'p-3')}>
      <div className="mb-3 flex items-start gap-2">
        <KeyRound className="mt-0.5 shrink-0 text-muted-foreground" />
        <div>
          <p className="text-sm font-medium">{t('admin.credentialsRequiredTitle')}</p>
          <p className="mt-0.5 text-xs text-muted-foreground">{t('admin.credentialsRequiredDescription')}</p>
        </div>
      </div>
      <div className="grid gap-3">
        <Input
          type="password"
          value={form.currentPassword}
          onChange={(e) => onChange({ currentPassword: e.target.value })}
          placeholder={t('admin.currentPassword')}
          autoComplete="current-password"
        />
        {requiresMFA ? (
          <Input
            value={form.mfaCode}
            onChange={(e) => onChange({ mfaCode: e.target.value })}
            placeholder={t('admin.mfaCode')}
            inputMode="numeric"
            autoComplete="one-time-code"
          />
        ) : null}
      </div>
    </div>
  );
}

function LabeledInput({ id, label, value, onChange, type = 'text', autoComplete }: {
  id: string;
  label: string;
  value: string;
  onChange: (value: string) => void;
  type?: string;
  autoComplete?: string;
}) {
  return (
    <label htmlFor={id} className="flex flex-col gap-2">
      <span className="text-sm font-medium">{label}</span>
      <Input
        id={id}
        type={type}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        autoComplete={autoComplete}
      />
    </label>
  );
}

function UsernameDialog({
  open,
  form,
  requiresMFA,
  isPending,
  onChange,
  onClose,
  onSubmit,
}: {
  open: boolean;
  form: UsernameForm;
  requiresMFA: boolean;
  isPending: boolean;
  onChange: (patch: Partial<UsernameForm>) => void;
  onClose: () => void;
  onSubmit: (event: React.FormEvent) => void;
}) {
  const { t } = useTranslation();

  return (
    <Dialog open={open} onOpenChange={(nextOpen) => { if (!nextOpen) onClose(); }}>
      <DialogContent>
        <form onSubmit={onSubmit} className="flex flex-col gap-4">
          <DialogHeader>
            <DialogTitle>{t('admin.updateUsername')}</DialogTitle>
            <DialogDescription>{t('admin.accountProfileDescription')}</DialogDescription>
          </DialogHeader>
          <LabeledInput
            id="admin-new-username"
            label={t('admin.newUsername')}
            value={form.newUsername}
            onChange={(value) => onChange({ newUsername: value })}
            autoComplete="username"
          />
          <CredentialBlock
            compact
            requiresMFA={requiresMFA}
            form={form}
            onChange={onChange}
          />
          <DialogFooter>
            <Button type="button" variant="outline" onClick={onClose}>{t('common.cancel')}</Button>
            <Button type="submit" disabled={isPending}>
              <Check data-icon="inline-start" />
              {t('admin.updateUsername')}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

function PasswordDialog({
  open,
  form,
  requiresMFA,
  isPending,
  onChange,
  onClose,
  onSubmit,
}: {
  open: boolean;
  form: PasswordForm;
  requiresMFA: boolean;
  isPending: boolean;
  onChange: (patch: Partial<PasswordForm>) => void;
  onClose: () => void;
  onSubmit: (event: React.FormEvent) => void;
}) {
  const { t } = useTranslation();

  return (
    <Dialog open={open} onOpenChange={(nextOpen) => { if (!nextOpen) onClose(); }}>
      <DialogContent>
        <form onSubmit={onSubmit} className="flex flex-col gap-4">
          <DialogHeader>
            <DialogTitle>{t('admin.updatePassword')}</DialogTitle>
            <DialogDescription>{t('admin.passwordSecurityDescription')}</DialogDescription>
          </DialogHeader>
          <LabeledInput
            id="admin-new-password"
            label={t('admin.newPassword')}
            type="password"
            value={form.newPassword}
            onChange={(value) => onChange({ newPassword: value })}
            autoComplete="new-password"
          />
          <CredentialBlock
            compact
            requiresMFA={requiresMFA}
            form={form}
            onChange={onChange}
          />
          <DialogFooter>
            <Button type="button" variant="outline" onClick={onClose}>{t('common.cancel')}</Button>
            <Button type="submit" disabled={isPending}>
              <Check data-icon="inline-start" />
              {t('admin.updatePassword')}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

function PasskeyAddDialog({
  open,
  form,
  requiresMFA,
  passkeySupported,
  isAdding,
  onChange,
  onClose,
  onSubmit,
}: {
  open: boolean;
  form: PasskeyForm;
  requiresMFA: boolean;
  passkeySupported: boolean;
  isAdding: boolean;
  onChange: (patch: Partial<PasskeyForm>) => void;
  onClose: () => void;
  onSubmit: (event: React.FormEvent) => void;
}) {
  const { t } = useTranslation();

  return (
    <Dialog open={open} onOpenChange={(nextOpen) => { if (!nextOpen) onClose(); }}>
      <DialogContent>
        <form onSubmit={onSubmit} className="flex flex-col gap-4">
          <DialogHeader>
            <DialogTitle>{t('admin.addPasskey')}</DialogTitle>
            <DialogDescription>{t('admin.passkeyAddDescription')}</DialogDescription>
          </DialogHeader>
          {!passkeySupported ? (
            <FactorStatusRow enabled={false} title={t('admin.passkeyUnavailableTitle')} description={t('admin.passkeyUnsupported')} />
          ) : null}
          <LabeledInput
            id="admin-passkey-name"
            label={t('admin.passkeyName')}
            value={form.name}
            onChange={(value) => onChange({ name: value })}
          />
          <CredentialBlock
            compact
            requiresMFA={requiresMFA}
            form={form}
            onChange={onChange}
          />
          <DialogFooter>
            <Button type="button" variant="outline" onClick={onClose}>{t('common.cancel')}</Button>
            <Button type="submit" disabled={!passkeySupported || isAdding}>
              <Plus data-icon="inline-start" />
              {t('admin.addPasskey')}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

function TOTPActionDialog({
  action,
  form,
  requiresMFA,
  isPending,
  onChange,
  onClose,
  onBegin,
  onRegenerate,
  onDisable,
}: {
  action: 'enable' | 'regenerate' | 'disable' | null;
  form: CredentialForm;
  requiresMFA: boolean;
  isPending: boolean;
  onChange: (patch: Partial<CredentialForm>) => void;
  onClose: () => void;
  onBegin: (event: React.FormEvent) => void;
  onRegenerate: () => void;
  onDisable: () => void;
}) {
  const { t } = useTranslation();

  const title = action === 'enable'
    ? t('admin.enableTOTP')
    : action === 'regenerate'
      ? t('admin.regenerateRecoveryCodes')
      : t('admin.disableTOTP');
  const description = action === 'enable'
    ? t('admin.totpEnableActionDescription')
    : action === 'regenerate'
      ? t('admin.recoveryCodesRegenerateDescription')
      : t('admin.totpDisableActionDescription');

  const submit = (event: React.FormEvent) => {
    event.preventDefault();
    if (action === 'enable') {
      void onBegin(event);
    } else if (action === 'regenerate') {
      onRegenerate();
    } else if (action === 'disable') {
      onDisable();
    }
  };

  return (
    <Dialog open={!!action} onOpenChange={(nextOpen) => { if (!nextOpen) onClose(); }}>
      <DialogContent>
        <form onSubmit={submit} className="flex flex-col gap-4">
          <DialogHeader>
            <DialogTitle>{title}</DialogTitle>
            <DialogDescription>{description}</DialogDescription>
          </DialogHeader>
          <CredentialBlock
            compact
            requiresMFA={requiresMFA}
            form={form}
            onChange={onChange}
          />
          <DialogFooter>
            <Button type="button" variant="outline" onClick={onClose}>{t('common.cancel')}</Button>
            <Button type="submit" variant={action === 'disable' ? 'destructive' : 'default'} disabled={isPending}>
              {action === 'regenerate' ? <RotateCcw data-icon="inline-start" /> : action === 'disable' ? <X data-icon="inline-start" /> : <Plus data-icon="inline-start" />}
              {title}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

function TOTPSetupDialog({ setup, code, onCodeChange, onCancel, onConfirm }: {
  setup: TOTPBeginResponse | null;
  code: string;
  onCodeChange: (value: string) => void;
  onCancel: () => void;
  onConfirm: () => void;
}) {
  const { t } = useTranslation();
  return (
    <Dialog open={!!setup} onOpenChange={(open) => { if (!open) onCancel(); }}>
      <DialogContent className="sm:max-w-[620px]">
        <DialogHeader>
          <DialogTitle>{t('admin.enableTOTP')}</DialogTitle>
          <DialogDescription>{t('admin.totpSetupDescription')}</DialogDescription>
        </DialogHeader>
        {setup ? (
          <div className="grid gap-5 sm:grid-cols-[220px_1fr]">
            <div className="rounded-xl border border-border/50 bg-background p-3">
              <img src={setup.qr_data_url} alt={t('admin.totpQRCode')} className="aspect-square w-full rounded-lg" />
            </div>
            <div className="flex flex-col gap-4">
              <label className="flex flex-col gap-2">
                <span className="text-sm font-medium">{t('admin.totpSecret')}</span>
                <Input value={setup.secret} readOnly className="font-mono" />
              </label>
              <label className="flex flex-col gap-2">
                <span className="text-sm font-medium">{t('admin.mfaCode')}</span>
                <Input value={code} onChange={(e) => onCodeChange(e.target.value)} inputMode="numeric" autoComplete="one-time-code" />
              </label>
            </div>
          </div>
        ) : null}
        <DialogFooter>
          <Button type="button" variant="outline" onClick={onCancel}>{t('common.cancel')}</Button>
          <Button type="button" onClick={onConfirm}>{t('common.confirm')}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function RecoveryCodesDialog({ codes, onClose }: { codes: string[]; onClose: () => void }) {
  const { t } = useTranslation();
  return (
    <Dialog open={codes.length > 0} onOpenChange={(open) => { if (!open) onClose(); }}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t('admin.recoveryCodes')}</DialogTitle>
          <DialogDescription>{t('admin.recoveryCodesDescription')}</DialogDescription>
        </DialogHeader>
        <div className="grid grid-cols-2 gap-2">
          {codes.map((code) => (
            <code key={code} className="rounded-md border bg-muted px-3 py-2 text-center text-sm font-medium">{code}</code>
          ))}
        </div>
        <DialogFooter>
          <Button type="button" onClick={onClose}>{t('admin.savedRecoveryCodes')}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function PasskeyNameDialog({ passkey, form, requiresMFA, onChange, onClose, onSubmit }: {
  passkey: PasskeySummary | null;
  form: PasskeyForm;
  requiresMFA: boolean;
  onChange: (patch: Partial<PasskeyForm>) => void;
  onClose: () => void;
  onSubmit: () => void;
}) {
  const { t } = useTranslation();
  return (
    <Dialog open={!!passkey} onOpenChange={(open) => { if (!open) onClose(); }}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t('admin.renamePasskey')}</DialogTitle>
          <DialogDescription>{t('admin.passkeyRenameDescription')}</DialogDescription>
        </DialogHeader>
        <div className="flex flex-col gap-4">
          <LabeledInput id="admin-rename-passkey" label={t('admin.passkeyName')} value={form.name} onChange={(value) => onChange({ name: value })} />
          <CredentialBlock requiresMFA={requiresMFA} form={form} onChange={onChange} />
        </div>
        <DialogFooter>
          <Button type="button" variant="outline" onClick={onClose}>{t('common.cancel')}</Button>
          <Button type="button" onClick={onSubmit}>{t('common.save')}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function PasskeyDeleteDialog({ passkey, form, requiresMFA, onChange, onClose, onSubmit }: {
  passkey: PasskeySummary | null;
  form: CredentialForm;
  requiresMFA: boolean;
  onChange: (patch: Partial<CredentialForm>) => void;
  onClose: () => void;
  onSubmit: () => void;
}) {
  const { t } = useTranslation();
  return (
    <Dialog open={!!passkey} onOpenChange={(open) => { if (!open) onClose(); }}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t('admin.deletePasskey')}</DialogTitle>
          <DialogDescription>{passkey?.name}</DialogDescription>
        </DialogHeader>
        <div className="flex flex-col gap-4">
          <FactorStatusRow enabled={false} title={t('admin.deletePasskey')} description={t('admin.deletePasskeyDescription')} />
          <CredentialBlock requiresMFA={requiresMFA} form={form} onChange={onChange} />
        </div>
        <DialogFooter>
          <Button type="button" variant="outline" onClick={onClose}>{t('common.cancel')}</Button>
          <Button type="button" variant="destructive" onClick={onSubmit}>{t('common.delete')}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
