import { useState } from 'react';
import { RotateCcw, Save, Webhook } from 'lucide-react';
import toast from 'react-hot-toast';
import { useTranslation } from 'react-i18next';
import {
  useWebhookSettings,
  useWebhookSettingsMutations,
  type WebhookDeliverySettings,
} from '@/hooks/use-webhook-settings';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import {
  InputGroup,
  InputGroupAddon,
  InputGroupInput,
} from '@/components/ui/input-group';
import { Skeleton } from '@/components/ui/skeleton';
import { Spinner } from '@/components/ui/spinner';
import { Switch } from '@/components/ui/switch';
import { SettingRow, SettingsSectionCard } from './security-primitives';

export function WebhookDeliverySection() {
  const { t } = useTranslation();
  const { data, isLoading } = useWebhookSettings();
  const mutations = useWebhookSettingsMutations();
  const [draft, setDraft] = useState<WebhookDeliverySettings | null>(null);
  const settings = draft ?? {
    allow_private_targets: data?.allow_private_targets ?? false,
    daily_delivery_cap: data?.daily_delivery_cap ?? 50,
  };
  const isDirty =
    data != null &&
    (settings.allow_private_targets !== data.allow_private_targets ||
      settings.daily_delivery_cap !== data.daily_delivery_cap);
  const isValid =
    Number.isInteger(settings.daily_delivery_cap) &&
    settings.daily_delivery_cap >= 1 &&
    settings.daily_delivery_cap <= 10000;
  const saving = mutations.updateSettings.isPending;

  if (isLoading) {
    return <Skeleton className="h-48 w-full rounded-xl" />;
  }

  const save = async () => {
    if (!isValid) {
      toast.error(t('admin.webhookDailyCapInvalid'));
      return;
    }
    try {
      const result = await mutations.updateSettings.mutateAsync(settings);
      setDraft(null);
      if (result?.disabled_webhooks > 0) {
        toast.success(
          t('admin.webhookPolicyDisabledCount', { count: result.disabled_webhooks }),
        );
      } else {
        toast.success(t('admin.webhookSettingsSaved'));
      }
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t('errors.generic'));
    }
  };

  return (
    <SettingsSectionCard
      icon={Webhook}
      title={t('admin.webhookDeliverySettings')}
      description={t('admin.webhookDeliverySettingsDescription')}
      badge={
        <Badge variant={settings.allow_private_targets ? 'destructive' : 'outline'}>
          {settings.allow_private_targets
            ? t('admin.webhookPrivateTargetsAllowed')
            : t('common.disabled')}
        </Badge>
      }
      footer={
        <>
          <span className="text-xs text-muted-foreground">
            {isDirty ? t('admin.unsavedChangesHint') : ''}
          </span>
          <div className="flex items-center gap-2">
            {isDirty ? (
              <Button
                type="button"
                variant="ghost"
                size="sm"
                disabled={saving}
                onClick={() => setDraft(null)}
              >
                <RotateCcw data-icon="inline-start" />
                {t('admin.discardChanges')}
              </Button>
            ) : null}
            <Button
              type="button"
              size="sm"
              disabled={!isDirty || !isValid || saving}
              onClick={() => void save()}
            >
              {saving ? (
                <Spinner data-icon="inline-start" />
              ) : (
                <Save data-icon="inline-start" />
              )}
              {saving ? t('common.saving') : t('common.save')}
            </Button>
          </div>
        </>
      }
    >
      <SettingRow
        title={t('admin.webhookAllowPrivateTargets')}
        description={t('admin.webhookAllowPrivateTargetsHelp')}
        actions={
          <Switch
            id="webhook-allow-private-targets"
            checked={settings.allow_private_targets}
            onCheckedChange={(allow) =>
              setDraft({ ...settings, allow_private_targets: allow })
            }
          />
        }
      />
      <SettingRow
        title={t('admin.webhookDailyCap')}
        description={
          isValid ? t('admin.webhookDailyCapHelp') : t('admin.webhookDailyCapInvalid')
        }
        actions={
          <InputGroup className="w-40">
            <InputGroupInput
              id="webhook-daily-cap"
              type="number"
              min={1}
              max={10000}
              step={1}
              value={settings.daily_delivery_cap}
              onChange={(event) =>
                setDraft({
                  ...settings,
                  daily_delivery_cap: Number(event.target.value),
                })
              }
              aria-invalid={!isValid}
              className="font-mono text-sm"
            />
            <InputGroupAddon align="inline-end" className="text-xs">
              {t('admin.deliveriesPerDayUnit')}
            </InputGroupAddon>
          </InputGroup>
        }
      />
    </SettingsSectionCard>
  );
}
