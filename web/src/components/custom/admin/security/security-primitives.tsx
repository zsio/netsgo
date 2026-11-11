import type { ComponentProps, ReactNode } from 'react';
import { CheckCircle2, KeyRound, ShieldOff } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { cn } from '@/lib/utils';
import type { CredentialForm } from '@/routes/admin/security-state';

export function SettingRow({
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
  value?: ReactNode;
  actionLabel?: string;
  actionVariant?: ComponentProps<typeof Button>['variant'];
  actionDisabled?: boolean;
  onAction?: () => void;
  actions?: ReactNode;
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

export function SettingsList({ children }: { children: ReactNode }) {
  return (
    <div className="overflow-hidden rounded-xl border border-border/50 bg-background/90 divide-y divide-border/50">
      {children}
    </div>
  );
}

export function FactorStatusRow({
  enabled,
  title,
  description,
}: {
  enabled: boolean;
  title: string;
  description: string;
}) {
  return (
    <div
      className={cn(
        'flex items-start gap-3 rounded-lg border p-4',
        enabled ? 'border-primary/25 bg-primary/5' : 'border-border/50 bg-muted/20',
      )}
    >
      <div
        className={cn(
          'mt-0.5 flex size-8 shrink-0 items-center justify-center rounded-lg',
          enabled ? 'bg-primary/10 text-primary' : 'bg-muted text-muted-foreground',
        )}
      >
        {enabled ? <CheckCircle2 /> : <ShieldOff />}
      </div>
      <div>
        <p className="text-sm font-medium">{title}</p>
        <p className="mt-1 text-sm text-muted-foreground">{description}</p>
      </div>
    </div>
  );
}

export function CredentialBlock({
  requiresMFA,
  form,
  compact = false,
  onChange,
}: {
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

export function LabeledInput({
  id,
  label,
  value,
  onChange,
  type = 'text',
  autoComplete,
}: {
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
