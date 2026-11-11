import { type FormEvent, useMemo, useState } from 'react';
import { Fingerprint, KeyRound, Pencil, Plus, ShieldAlert, Trash2 } from 'lucide-react';
import toast from 'react-hot-toast';
import { useTranslation } from 'react-i18next';
import { useAdminSecurityMutations } from '@/hooks/use-admin-security';
import { formatTimestamp } from '@/lib/format';
import {
  isPasskeySupported,
  normalizeCreationOptions,
  publicKeyCredentialToJSON,
} from '@/lib/webauthn';
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
import { TableActionIconButton } from '@/components/custom/common/TableActionIconButton';
import {
  emptyCredentialForm,
  emptyPasskeyForm,
  type CredentialForm,
  type PasskeyForm,
} from '@/routes/admin/security-state';
import type { PasskeyChallengeResponse, PasskeySummary } from '@/types';
import {
  CredentialBlock,
  FactorStatusRow,
  LabeledInput,
  SettingsSectionCard,
} from './security-primitives';

export function PasskeySection({
  passkeys,
  requiresMFA,
  onForceRelogin,
  onError,
}: {
  passkeys: PasskeySummary[];
  requiresMFA: boolean;
  onForceRelogin: (message: string) => void;
  onError: (error: unknown) => void;
}) {
  const { t } = useTranslation();
  const mutations = useAdminSecurityMutations();
  const [passkeyForm, setPasskeyForm] = useState(() => emptyPasskeyForm());
  const [renamePasskey, setRenamePasskey] = useState<PasskeySummary | null>(null);
  const [renameForm, setRenameForm] = useState(() => emptyPasskeyForm());
  const [deletePasskey, setDeletePasskey] = useState<PasskeySummary | null>(null);
  const [deleteForm, setDeleteForm] = useState(() => emptyCredentialForm());
  const [passkeyAddOpen, setPasskeyAddOpen] = useState(false);

  const passkeySupported = useMemo(() => isPasskeySupported(), []);
  const isInsecureOrigin =
    typeof window !== 'undefined' && window.isSecureContext === false;
  const unsupportedReason = isInsecureOrigin
    ? t('admin.passkeySecureContextHint')
    : t('admin.passkeyUnsupported');

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

  const addPasskey = async (event: FormEvent) => {
    event.preventDefault();
    if (!passkeySupported) {
      toast.error(unsupportedReason);
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
      closePasskeyAdd();
      if (resp.requires_relogin) {
        closeRenamePasskey();
        closeDeletePasskey();
        onForceRelogin(t('admin.securityReloginRequired'));
      }
    } catch (error) {
      onError(error);
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
      onError(error);
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
      closeDeletePasskey();
      if (resp.requires_relogin) {
        closePasskeyAdd();
        closeRenamePasskey();
        onForceRelogin(t('admin.securityReloginRequired'));
      }
    } catch (error) {
      onError(error);
    }
  };

  return (
    <>
      <SettingsSectionCard
        icon={Fingerprint}
        title={t('admin.passkeys')}
        description={t('admin.passkeyAddDescription')}
        badge={
          passkeys.length > 0 ? (
            <Badge variant="secondary" className="font-mono text-xs">
              {passkeys.length}
            </Badge>
          ) : !passkeySupported ? (
            <Badge variant="outline" className="text-muted-foreground">
              {t('common.disabled')}
            </Badge>
          ) : undefined
        }
        actions={
          <Button
            type="button"
            size="sm"
            variant={passkeys.length === 0 ? 'default' : 'outline'}
            disabled={!passkeySupported}
            onClick={() => {
              setPasskeyForm(emptyPasskeyForm());
              setPasskeyAddOpen(true);
            }}
          >
            <Plus data-icon="inline-start" />
            {t('admin.addPasskey')}
          </Button>
        }
      >
        {!passkeySupported && passkeys.length > 0 ? (
          <div className="flex items-center gap-2.5 bg-amber-500/5 px-4 py-2.5 text-xs text-amber-600 dark:text-amber-400 sm:px-5">
            <ShieldAlert className="size-4 shrink-0" />
            <span>{unsupportedReason}</span>
          </div>
        ) : null}

        {passkeys.length === 0 ? (
          <div className="flex items-start gap-3 px-4 py-4 sm:px-5">
            <div className="mt-0.5 flex size-8 shrink-0 items-center justify-center rounded-lg bg-muted/60 text-muted-foreground">
              <KeyRound className="size-4" />
            </div>
            <div className="min-w-0">
              <p className="text-sm font-medium text-foreground">
                {t('admin.noPasskeys')}
              </p>
              <p className="mt-0.5 max-w-2xl text-xs leading-relaxed text-muted-foreground">
                {passkeySupported ? t('admin.noPasskeysDescription') : unsupportedReason}
              </p>
            </div>
          </div>
        ) : (
          passkeys.map((passkey) => (
            <div
              key={passkey.id}
              className="flex flex-wrap items-center justify-between gap-3 px-4 py-3.5 sm:px-5"
            >
              <div className="flex min-w-0 items-start gap-3">
                <div className="mt-0.5 flex size-8 shrink-0 items-center justify-center rounded-lg bg-primary/10 text-primary">
                  <Fingerprint className="size-4" />
                </div>
                <div className="min-w-0">
                  <p className="truncate text-sm font-medium text-foreground">
                    {passkey.name}
                  </p>
                  <p className="mt-0.5 flex flex-wrap items-center gap-x-2 gap-y-0.5 text-xs text-muted-foreground">
                    <span>
                      {t('admin.passkeyCreatedAt', {
                        time: formatTimestamp(passkey.created_at),
                      })}
                    </span>
                    <span>·</span>
                    <span>
                      {passkey.last_used_at
                        ? t('admin.passkeyLastUsedAt', {
                            time: formatTimestamp(passkey.last_used_at),
                          })
                        : t('admin.passkeyNeverUsed')}
                    </span>
                  </p>
                </div>
              </div>
              <div className="flex items-center gap-1">
                <TableActionIconButton
                  type="button"
                  label={t('common.edit')}
                  tone="primary"
                  onClick={() => {
                    setRenamePasskey(passkey);
                    setRenameForm(emptyPasskeyForm(passkey.name));
                  }}
                >
                  <Pencil className="size-3.5" />
                </TableActionIconButton>
                <TableActionIconButton
                  type="button"
                  label={t('common.delete')}
                  tone="destructive"
                  onClick={() => {
                    setDeletePasskey(passkey);
                    setDeleteForm(emptyCredentialForm());
                  }}
                >
                  <Trash2 className="size-3.5" />
                </TableActionIconButton>
              </div>
            </div>
          ))
        )}
      </SettingsSectionCard>

      <PasskeyAddDialog
        open={passkeyAddOpen}
        form={passkeyForm}
        requiresMFA={requiresMFA}
        passkeySupported={passkeySupported}
        isAdding={mutations.beginPasskey.isPending || mutations.finishPasskey.isPending}
        onChange={(patch) => setPasskeyForm({ ...passkeyForm, ...patch })}
        onClose={closePasskeyAdd}
        onSubmit={(event) => void addPasskey(event)}
      />
      <PasskeyNameDialog
        passkey={renamePasskey}
        form={renameForm}
        requiresMFA={requiresMFA}
        onChange={(patch) => setRenameForm({ ...renameForm, ...patch })}
        onClose={closeRenamePasskey}
        onSubmit={() => void submitRenamePasskey()}
      />
      <PasskeyDeleteDialog
        passkey={deletePasskey}
        form={deleteForm}
        requiresMFA={requiresMFA}
        onChange={(patch) => setDeleteForm({ ...deleteForm, ...patch })}
        onClose={closeDeletePasskey}
        onSubmit={() => void submitDeletePasskey()}
      />
    </>
  );
}

export function PasskeyAddDialog({
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
  onSubmit: (event: FormEvent) => void;
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

export function PasskeyNameDialog({
  passkey,
  form,
  requiresMFA,
  onChange,
  onClose,
  onSubmit,
}: {
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
          <LabeledInput
            id="admin-rename-passkey"
            label={t('admin.passkeyName')}
            value={form.name}
            onChange={(value) => onChange({ name: value })}
          />
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

export function PasskeyDeleteDialog({
  passkey,
  form,
  requiresMFA,
  onChange,
  onClose,
  onSubmit,
}: {
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
