import { type FormEvent, useState } from 'react';
import { Plus, RotateCcw, ShieldCheck, X } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { useAdminSecurityMutations } from '@/hooks/use-admin-security';
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
import { emptyCredentialForm, type CredentialForm } from '@/routes/admin/security-state';
import type { RecoveryCodesResponse, TOTPBeginResponse } from '@/types';
import { CredentialBlock, SettingRow, SettingsSectionCard } from './security-primitives';

export function TOTPSection({
  enabled,
  recoveryCodesRemaining,
  requiresMFA,
  onForceRelogin,
  onError,
}: {
  enabled: boolean;
  recoveryCodesRemaining: number;
  requiresMFA: boolean;
  onForceRelogin: (message: string) => void;
  onError: (error: unknown) => void;
}) {
  const { t } = useTranslation();
  const mutations = useAdminSecurityMutations();
  const [totpForm, setTotpForm] = useState(() => emptyCredentialForm());
  const [totpSetup, setTotpSetup] = useState<TOTPBeginResponse | null>(null);
  const [totpCode, setTotpCode] = useState('');
  const [recoveryCodes, setRecoveryCodes] = useState<string[]>([]);
  const [totpAction, setTotpAction] = useState<'enable' | 'regenerate' | 'disable' | null>(null);

  const closeTOTPAction = () => {
    setTotpAction(null);
    setTotpForm(emptyCredentialForm());
  };

  const closeTOTPSetup = () => {
    setTotpSetup(null);
    setTotpCode('');
  };

  const beginTOTP = async (event: FormEvent) => {
    event.preventDefault();
    try {
      const setup = await mutations.beginTOTP.mutateAsync({
        current_password: totpForm.currentPassword,
        mfa_code: totpForm.mfaCode || undefined,
      });
      setTotpSetup(setup);
      closeTOTPAction();
    } catch (error) {
      onError(error);
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
      onError(error);
    }
  };

  const disableTOTP = async () => {
    try {
      const resp = await mutations.disableTOTP.mutateAsync({
        current_password: totpForm.currentPassword,
        mfa_code: totpForm.mfaCode || undefined,
      });
      closeTOTPAction();
      if (resp.requires_relogin) {
        closeTOTPSetup();
        onForceRelogin(t('admin.securityReloginRequired'));
      }
    } catch (error) {
      onError(error);
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
      onError(error);
    }
  };

  return (
    <>
      <SettingsSectionCard
        icon={ShieldCheck}
        title={t('admin.twoFactorAuth')}
        description={t('admin.totpPanelDescription')}
        badge={
          <Badge variant={enabled ? 'default' : 'outline'}>
            {enabled ? t('common.enabled') : t('common.disabled')}
          </Badge>
        }
      >
        <SettingRow
          title={enabled ? t('admin.totpEnabled') : t('admin.totpDisabled')}
          description={
            enabled
              ? t('admin.totpEnabledDescription', { count: recoveryCodesRemaining })
              : t('admin.totpDisabledDescription')
          }
          actionLabel={enabled ? t('admin.disableTOTP') : t('admin.enableTOTP')}
          actionVariant={enabled ? 'destructive' : 'default'}
          onAction={
            enabled
              ? () => {
                  setTotpForm(emptyCredentialForm());
                  setTotpAction('disable');
                }
              : () => {
                  setTotpForm(emptyCredentialForm());
                  setTotpAction('enable');
                }
          }
        />

        {enabled ? (
          <SettingRow
            title={t('admin.recoveryCodes')}
            description={t('admin.recoveryCodesManageDescription')}
            value={
              <Badge variant="outline" className="font-mono text-xs">
                {t('admin.recoveryCodesRemaining', { count: recoveryCodesRemaining })}
              </Badge>
            }
            actionLabel={t('admin.regenerateRecoveryCodes')}
            onAction={() => {
              setTotpForm(emptyCredentialForm());
              setTotpAction('regenerate');
            }}
          />
        ) : null}
      </SettingsSectionCard>

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
        onBegin={(event) => void beginTOTP(event)}
        onRegenerate={() => void regenerateRecoveryCodes()}
        onDisable={() => void disableTOTP()}
      />
      <TOTPSetupDialog
        setup={totpSetup}
        code={totpCode}
        onCodeChange={setTotpCode}
        onCancel={closeTOTPSetup}
        onConfirm={() => void confirmTOTP()}
      />
      <RecoveryCodesDialog
        codes={recoveryCodes}
        onClose={() => {
          setRecoveryCodes([]);
          closeTOTPAction();
          closeTOTPSetup();
          onForceRelogin(t('admin.securityReloginRequired'));
        }}
      />
    </>
  );
}

export function TOTPActionDialog({
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
  onBegin: (event: FormEvent) => void;
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

  const submit = (event: FormEvent) => {
    event.preventDefault();
    if (action === 'enable') {
      onBegin(event);
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

export function TOTPSetupDialog({
  setup,
  code,
  onCodeChange,
  onCancel,
  onConfirm,
}: {
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
            <div className="rounded-xl border border-border/50 bg-white p-3 shadow-2xs">
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

export function RecoveryCodesDialog({
  codes,
  onClose,
}: {
  codes: string[];
  onClose: () => void;
}) {
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
