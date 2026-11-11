import { type FormEvent, useMemo, useState } from 'react';
import { Pencil, Plus, Trash2 } from 'lucide-react';
import toast from 'react-hot-toast';
import { useTranslation } from 'react-i18next';
import { useAdminSecurityMutations } from '@/hooks/use-admin-security';
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
  SettingRow,
  SettingsList,
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
      <SettingsList>
        <SettingRow
          title={t('admin.addPasskey')}
          description={passkeySupported ? t('admin.passkeyAddDescription') : t('admin.passkeyUnsupported')}
          value={!passkeySupported ? <Badge variant="secondary">{t('common.disabled')}</Badge> : undefined}
          actionLabel={t('admin.addPasskey')}
          actionDisabled={!passkeySupported}
          onAction={() => {
            setPasskeyForm(emptyPasskeyForm());
            setPasskeyAddOpen(true);
          }}
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
                  <Button
                    type="button"
                    variant="ghost"
                    size="icon-sm"
                    onClick={() => {
                      setRenamePasskey(passkey);
                      setRenameForm(emptyPasskeyForm(passkey.name));
                    }}
                    title={t('common.edit')}
                    aria-label={t('common.edit')}
                  >
                    <Pencil />
                  </Button>
                  <Button
                    type="button"
                    variant="ghost"
                    size="icon-sm"
                    onClick={() => {
                      setDeletePasskey(passkey);
                      setDeleteForm(emptyCredentialForm());
                    }}
                    title={t('common.delete')}
                    aria-label={t('common.delete')}
                  >
                    <Trash2 />
                  </Button>
                </>
              )}
            />
          ))
        )}
      </SettingsList>

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
