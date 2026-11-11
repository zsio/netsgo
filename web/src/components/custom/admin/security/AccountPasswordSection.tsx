import { type FormEvent, useState } from 'react';
import { Check } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { useAdminSecurityMutations } from '@/hooks/use-admin-security';
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
  emptyPasswordForm,
  emptyUsernameForm,
  type PasswordForm,
  type UsernameForm,
} from '@/routes/admin/security-state';
import {
  CredentialBlock,
  LabeledInput,
  SettingRow,
  SettingsList,
} from './security-primitives';

export function AccountPasswordSection({
  currentUser,
  requiresMFA,
  onForceRelogin,
  onError,
}: {
  currentUser: { username: string };
  requiresMFA: boolean;
  onForceRelogin: (message: string) => void;
  onError: (error: unknown) => void;
}) {
  const { t } = useTranslation();
  const mutations = useAdminSecurityMutations();
  const [usernameForm, setUsernameForm] = useState(() => emptyUsernameForm());
  const [passwordForm, setPasswordForm] = useState(() => emptyPasswordForm());
  const [accountDialog, setAccountDialog] = useState<'username' | 'password' | null>(null);

  const closeAccountDialog = () => {
    setAccountDialog(null);
    setUsernameForm(emptyUsernameForm());
    setPasswordForm(emptyPasswordForm());
  };

  const handleUsernameSubmit = async (event: FormEvent) => {
    event.preventDefault();
    try {
      const resp = await mutations.updateUsername.mutateAsync({
        current_password: usernameForm.currentPassword,
        mfa_code: usernameForm.mfaCode || undefined,
        new_username: usernameForm.newUsername,
      });
      closeAccountDialog();
      if (resp.requires_relogin) {
        onForceRelogin(t('admin.securityReloginRequired'));
      }
    } catch (error) {
      onError(error);
    }
  };

  const handlePasswordSubmit = async (event: FormEvent) => {
    event.preventDefault();
    try {
      const resp = await mutations.updatePassword.mutateAsync({
        current_password: passwordForm.currentPassword,
        mfa_code: passwordForm.mfaCode || undefined,
        new_password: passwordForm.newPassword,
      });
      closeAccountDialog();
      if (resp.requires_relogin) {
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
          title={t('admin.currentAdmin')}
          description={t('admin.accountProfileDescription')}
          value={currentUser.username}
          actionLabel={t('admin.updateUsername')}
          onAction={() => {
            setUsernameForm(emptyUsernameForm(currentUser.username));
            setAccountDialog('username');
          }}
        />
        <SettingRow
          title={t('admin.passwordSecurity')}
          description={t('admin.passwordSecurityDescription')}
          value={t('admin.passwordConfigured')}
          actionLabel={t('admin.updatePassword')}
          onAction={() => {
            setPasswordForm(emptyPasswordForm());
            setAccountDialog('password');
          }}
        />
      </SettingsList>

      <UsernameDialog
        open={accountDialog === 'username'}
        form={usernameForm}
        requiresMFA={requiresMFA}
        isPending={mutations.updateUsername.isPending}
        onChange={(patch) => setUsernameForm({ ...usernameForm, ...patch })}
        onClose={closeAccountDialog}
        onSubmit={(event) => void handleUsernameSubmit(event)}
      />
      <PasswordDialog
        open={accountDialog === 'password'}
        form={passwordForm}
        requiresMFA={requiresMFA}
        isPending={mutations.updatePassword.isPending}
        onChange={(patch) => setPasswordForm({ ...passwordForm, ...patch })}
        onClose={closeAccountDialog}
        onSubmit={(event) => void handlePasswordSubmit(event)}
      />
    </>
  );
}

export function UsernameDialog({
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
  onSubmit: (event: FormEvent) => void;
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

export function PasswordDialog({
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
  onSubmit: (event: FormEvent) => void;
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
