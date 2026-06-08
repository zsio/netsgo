import { useMemo, useState } from 'react';
import { createRoute, useNavigate } from '@tanstack/react-router';
import { Check, KeyRound, Lock, Plus, RotateCcw, Shield, Trash2, User, X } from 'lucide-react';
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
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Skeleton } from '@/components/ui/skeleton';
import type { PasskeyChallengeResponse, PasskeySummary, RecoveryCodesResponse, TOTPBeginResponse } from '@/types';

export const adminSecurityRoute = createRoute({
  getParentRoute: () => adminRoute,
  path: '/security',
  component: AdminSecurityPage,
});

type CredentialForm = {
  currentPassword: string;
  mfaCode: string;
};

const emptyCredentialForm: CredentialForm = { currentPassword: '', mfaCode: '' };

function AdminSecurityPage() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const logout = useAuthStore((s) => s.logout);
  const { data, isLoading } = useAdminSecurity();
  const mutations = useAdminSecurityMutations();
  const [usernameForm, setUsernameForm] = useState({ ...emptyCredentialForm, newUsername: '' });
  const [passwordForm, setPasswordForm] = useState({ ...emptyCredentialForm, newPassword: '' });
  const [totpForm, setTotpForm] = useState(emptyCredentialForm);
  const [totpSetup, setTotpSetup] = useState<TOTPBeginResponse | null>(null);
  const [totpCode, setTotpCode] = useState('');
  const [recoveryCodes, setRecoveryCodes] = useState<string[]>([]);
  const [passkeyForm, setPasskeyForm] = useState({ ...emptyCredentialForm, name: '' });
  const [renamePasskey, setRenamePasskey] = useState<PasskeySummary | null>(null);
  const [renameForm, setRenameForm] = useState({ ...emptyCredentialForm, name: '' });
  const [deletePasskey, setDeletePasskey] = useState<PasskeySummary | null>(null);
  const [deleteForm, setDeleteForm] = useState(emptyCredentialForm);

  const passkeySupported = useMemo(() => isPasskeySupported(), []);

  const showSecurityError = (error: unknown) => {
    toast.error(error instanceof Error ? error.message : t('errors.generic'));
  };

  const forceRelogin = (message: string) => {
    toast.success(message);
    logout();
    void navigate({ to: '/login' });
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
      if (resp.requires_relogin) forceRelogin(t('admin.securityReloginRequired'));
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
      if (resp.requires_relogin) forceRelogin(t('admin.securityReloginRequired'));
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
      setTotpSetup(null);
      setTotpCode('');
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
      if (resp.requires_relogin) forceRelogin(t('admin.securityReloginRequired'));
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
      if (resp.requires_relogin) forceRelogin(t('admin.securityReloginRequired'));
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
      setRenamePasskey(null);
      setRenameForm({ ...emptyCredentialForm, name: '' });
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
      if (resp.requires_relogin) forceRelogin(t('admin.securityReloginRequired'));
    } catch (error) {
      showSecurityError(error);
    }
  };

  if (isLoading || !data || !currentUser) {
    return (
      <div className="flex flex-col gap-4">
        <Skeleton className="h-12 w-full" />
        <Skeleton className="h-52 w-full" />
        <Skeleton className="h-52 w-full" />
      </div>
    );
  }

  return (
    <div className="flex flex-col gap-5 pb-10">
      <div>
        <h3 className="text-xl font-semibold tracking-tight">{t('admin.securityTitle')}</h3>
        <p className="mt-1 text-sm text-muted-foreground">{t('admin.securityDescription')}</p>
      </div>

      <section className="rounded-lg border bg-background p-5">
        <div className="flex items-start justify-between gap-4">
          <div>
            <div className="flex items-center gap-2 text-sm font-semibold">
              <User data-icon="inline-start" />
              {t('admin.accountProfile')}
            </div>
            <p className="mt-1 text-sm text-muted-foreground">{currentUser.username}</p>
          </div>
          <Badge variant="secondary">{currentUser.role}</Badge>
        </div>
        <form onSubmit={handleUsernameSubmit} className="mt-5 grid gap-3 md:grid-cols-[1fr_1fr_auto]">
          <Input value={usernameForm.newUsername} onChange={(e) => setUsernameForm({ ...usernameForm, newUsername: e.target.value })} placeholder={t('admin.newUsername')} />
          <SecurityCredentialInputs requiresMFA={requiresMFA} form={usernameForm} onChange={(patch) => setUsernameForm({ ...usernameForm, ...patch })} />
          <Button type="submit" disabled={mutations.updateUsername.isPending}>
            <Check data-icon="inline-start" />
            {t('admin.updateUsername')}
          </Button>
        </form>
      </section>

      <section className="rounded-lg border bg-background p-5">
        <div className="flex items-center gap-2 text-sm font-semibold">
          <Lock data-icon="inline-start" />
          {t('admin.passwordSecurity')}
        </div>
        <form onSubmit={handlePasswordSubmit} className="mt-5 grid gap-3 md:grid-cols-[1fr_1fr_auto]">
          <Input type="password" value={passwordForm.newPassword} onChange={(e) => setPasswordForm({ ...passwordForm, newPassword: e.target.value })} placeholder={t('admin.newPassword')} autoComplete="new-password" />
          <SecurityCredentialInputs requiresMFA={requiresMFA} form={passwordForm} onChange={(patch) => setPasswordForm({ ...passwordForm, ...patch })} />
          <Button type="submit" disabled={mutations.updatePassword.isPending}>
            <Check data-icon="inline-start" />
            {t('admin.updatePassword')}
          </Button>
        </form>
      </section>

      <section className="rounded-lg border bg-background p-5">
        <div className="flex flex-wrap items-start justify-between gap-4">
          <div>
            <div className="flex items-center gap-2 text-sm font-semibold">
              <Shield data-icon="inline-start" />
              {t('admin.twoFactorAuth')}
            </div>
            <p className="mt-1 text-sm text-muted-foreground">
              {data.totp_enabled ? t('admin.totpEnabledDescription', { count: data.recovery_codes_remaining }) : t('admin.totpDisabledDescription')}
            </p>
          </div>
          <Badge variant={data.totp_enabled ? 'default' : 'secondary'}>
            {data.totp_enabled ? t('common.enabled') : t('common.disabled')}
          </Badge>
        </div>
        <form onSubmit={beginTOTP} className="mt-5 grid gap-3 md:grid-cols-[1fr_1fr_auto]">
          <SecurityCredentialInputs requiresMFA={requiresMFA} form={totpForm} onChange={(patch) => setTotpForm({ ...totpForm, ...patch })} />
          <div className="flex gap-2 md:col-span-1">
            {!data.totp_enabled ? (
              <Button type="submit" disabled={mutations.beginTOTP.isPending}>
                <Plus data-icon="inline-start" />
                {t('admin.enableTOTP')}
              </Button>
            ) : (
              <>
                <Button type="button" variant="outline" onClick={regenerateRecoveryCodes} disabled={mutations.regenerateRecoveryCodes.isPending}>
                  <RotateCcw data-icon="inline-start" />
                  {t('admin.regenerateRecoveryCodes')}
                </Button>
                <Button type="button" variant="destructive" onClick={disableTOTP} disabled={mutations.disableTOTP.isPending}>
                  <X data-icon="inline-start" />
                  {t('admin.disableTOTP')}
                </Button>
              </>
            )}
          </div>
        </form>
      </section>

      <section className="rounded-lg border bg-background p-5">
        <div className="flex flex-wrap items-start justify-between gap-4">
          <div>
            <div className="flex items-center gap-2 text-sm font-semibold">
              <KeyRound data-icon="inline-start" />
              {t('admin.passkeys')}
            </div>
            <p className="mt-1 text-sm text-muted-foreground">
              {passkeySupported ? t('admin.passkeyDescription', { origin: data.webauthn.origin || t('common.unknown') }) : t('admin.passkeyUnsupported')}
            </p>
          </div>
          <Badge variant="secondary">{data.passkeys.length}</Badge>
        </div>
        <form onSubmit={addPasskey} className="mt-5 grid gap-3 md:grid-cols-[1fr_1fr_1fr_auto]">
          <Input value={passkeyForm.name} onChange={(e) => setPasskeyForm({ ...passkeyForm, name: e.target.value })} placeholder={t('admin.passkeyName')} />
          <SecurityCredentialInputs requiresMFA={requiresMFA} form={passkeyForm} onChange={(patch) => setPasskeyForm({ ...passkeyForm, ...patch })} />
          <Button type="submit" disabled={!passkeySupported || mutations.beginPasskey.isPending || mutations.finishPasskey.isPending}>
            <Plus data-icon="inline-start" />
            {t('admin.addPasskey')}
          </Button>
        </form>
        <div className="mt-5 flex flex-col gap-2">
          {data.passkeys.length === 0 ? (
            <p className="text-sm text-muted-foreground">{t('admin.noPasskeys')}</p>
          ) : data.passkeys.map((passkey) => (
            <div key={passkey.id} className="flex flex-wrap items-center justify-between gap-3 rounded-md border p-3">
              <div>
                <div className="font-medium">{passkey.name}</div>
                <div className="text-xs text-muted-foreground">{passkey.origin}</div>
              </div>
              <div className="flex gap-2">
                <Button type="button" variant="outline" size="sm" onClick={() => {
                  setRenamePasskey(passkey);
                  setRenameForm({ ...emptyCredentialForm, name: passkey.name });
                }}>{t('common.edit')}</Button>
                <Button type="button" variant="destructive" size="sm" onClick={() => {
                  setDeletePasskey(passkey);
                  setDeleteForm(emptyCredentialForm);
                }}>
                  <Trash2 data-icon="inline-start" />
                  {t('common.delete')}
                </Button>
              </div>
            </div>
          ))}
        </div>
      </section>

      <TOTPSetupDialog setup={totpSetup} code={totpCode} onCodeChange={setTotpCode} onCancel={() => setTotpSetup(null)} onConfirm={confirmTOTP} />
      <RecoveryCodesDialog codes={recoveryCodes} onClose={() => {
        setRecoveryCodes([]);
        forceRelogin(t('admin.securityReloginRequired'));
      }} />
      <PasskeyNameDialog passkey={renamePasskey} form={renameForm} requiresMFA={requiresMFA} onChange={(patch) => setRenameForm({ ...renameForm, ...patch })} onClose={() => setRenamePasskey(null)} onSubmit={submitRenamePasskey} />
      <PasskeyDeleteDialog passkey={deletePasskey} form={deleteForm} requiresMFA={requiresMFA} onChange={(patch) => setDeleteForm({ ...deleteForm, ...patch })} onClose={() => setDeletePasskey(null)} onSubmit={submitDeletePasskey} />
    </div>
  );
}

function SecurityCredentialInputs({ requiresMFA, form, onChange }: {
  requiresMFA: boolean;
  form: CredentialForm;
  onChange: (patch: Partial<CredentialForm>) => void;
}) {
  const { t } = useTranslation();
  return (
    <>
      <Input type="password" value={form.currentPassword} onChange={(e) => onChange({ currentPassword: e.target.value })} placeholder={t('admin.currentPassword')} autoComplete="current-password" />
      {requiresMFA ? (
        <Input value={form.mfaCode} onChange={(e) => onChange({ mfaCode: e.target.value })} placeholder={t('admin.mfaCode')} inputMode="numeric" />
      ) : null}
    </>
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
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t('admin.enableTOTP')}</DialogTitle>
          <DialogDescription>{t('admin.totpSetupDescription')}</DialogDescription>
        </DialogHeader>
        {setup ? (
          <div className="flex flex-col gap-4">
            <img src={setup.qr_data_url} alt={t('admin.totpQRCode')} className="mx-auto size-48 rounded-md border" />
            <Input value={setup.secret} readOnly />
            <Input value={code} onChange={(e) => onCodeChange(e.target.value)} placeholder={t('admin.mfaCode')} inputMode="numeric" />
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
            <code key={code} className="rounded-md bg-muted px-2 py-1 text-sm">{code}</code>
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
  form: CredentialForm & { name: string };
  requiresMFA: boolean;
  onChange: (patch: Partial<CredentialForm & { name: string }>) => void;
  onClose: () => void;
  onSubmit: () => void;
}) {
  const { t } = useTranslation();
  return (
    <Dialog open={!!passkey} onOpenChange={(open) => { if (!open) onClose(); }}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t('admin.renamePasskey')}</DialogTitle>
          <DialogDescription>{passkey?.origin}</DialogDescription>
        </DialogHeader>
        <div className="flex flex-col gap-3">
          <Input value={form.name} onChange={(e) => onChange({ name: e.target.value })} placeholder={t('admin.passkeyName')} />
          <SecurityCredentialInputs requiresMFA={requiresMFA} form={form} onChange={onChange} />
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
        <div className="flex flex-col gap-3">
          <SecurityCredentialInputs requiresMFA={requiresMFA} form={form} onChange={onChange} />
        </div>
        <DialogFooter>
          <Button type="button" variant="outline" onClick={onClose}>{t('common.cancel')}</Button>
          <Button type="button" variant="destructive" onClick={onSubmit}>{t('common.delete')}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
