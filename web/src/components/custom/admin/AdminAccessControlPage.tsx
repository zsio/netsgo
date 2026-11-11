import { useState } from 'react';
import { RotateCcw, Save, ShieldAlert, Trash2 } from 'lucide-react';
import toast from 'react-hot-toast';
import { useTranslation } from 'react-i18next';
import {
  useClientAuthRateLimitMutations,
  useClientAuthRateLimits,
} from '@/hooks/use-admin-rate-limits';
import { formatTimestamp, formatUptime } from '@/lib/format';
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
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import type { ClientAuthRateLimitSettings, RateLimitEntry } from '@/types';
import { SettingRow, SettingsSectionCard } from './security/security-primitives';
import { WebhookDeliverySection } from './security/WebhookDeliverySection';

export function AdminAccessControlPage() {
  const { t } = useTranslation();
  const { data, isLoading } = useClientAuthRateLimits();
  const mutations = useClientAuthRateLimitMutations();
  const [resettingIP, setResettingIP] = useState<string | null>(null);
  const [settings, setSettings] = useState<ClientAuthRateLimitSettings | null>(
    null,
  );
  const displayedSettings = settings ?? {
    enabled: data?.enabled ?? false,
    requests_per_minute: data?.requests_per_minute ?? 20,
  };
  const settingsDirty =
    data != null &&
    (displayedSettings.enabled !== data.enabled ||
      displayedSettings.requests_per_minute !== data.requests_per_minute);
  const settingsValid =
    Number.isInteger(displayedSettings.requests_per_minute) &&
    displayedSettings.requests_per_minute >= 1 &&
    displayedSettings.requests_per_minute <= 1000;

  const resetClientAuthRateLimit = async (ip: string) => {
    setResettingIP(ip);
    try {
      await mutations.resetIP.mutateAsync(ip);
      toast.success(t('admin.clientAuthRateLimitCleared', { ip }));
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t('errors.generic'));
    } finally {
      setResettingIP(null);
    }
  };

  const saveSettings = async () => {
    if (!settingsValid) {
      toast.error(t('admin.clientAuthRateLimitInvalid'));
      return;
    }
    try {
      await mutations.updateSettings.mutateAsync(displayedSettings);
      setSettings(null);
      toast.success(t('admin.clientAuthRateLimitSettingsSaved'));
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t('errors.generic'));
    }
  };

  return (
    <div className="flex flex-col gap-5 pb-10">
      <ClientAuthRateLimitCard
        settings={displayedSettings}
        entries={data?.entries ?? []}
        isLoading={isLoading}
        isDirty={settingsDirty}
        isValid={settingsValid}
        isSaving={mutations.updateSettings.isPending}
        resettingIP={resettingIP}
        onChange={setSettings}
        onDiscard={() => setSettings(null)}
        onSave={saveSettings}
        onResetIP={resetClientAuthRateLimit}
      />
      <WebhookDeliverySection />
    </div>
  );
}

function ClientAuthRateLimitCard({
  settings,
  entries,
  isLoading,
  isDirty,
  isValid,
  isSaving,
  resettingIP,
  onChange,
  onDiscard,
  onSave,
  onResetIP,
}: {
  settings: ClientAuthRateLimitSettings;
  entries: RateLimitEntry[];
  isLoading: boolean;
  isDirty: boolean;
  isValid: boolean;
  isSaving: boolean;
  resettingIP: string | null;
  onChange: (settings: ClientAuthRateLimitSettings) => void;
  onDiscard: () => void;
  onSave: () => void;
  onResetIP: (ip: string) => void;
}) {
  const { t } = useTranslation();
  const limitedCount = entries.filter((entry) => entry.limited).length;

  if (isLoading) {
    return <Skeleton className="h-60 w-full rounded-xl" />;
  }

  return (
    <SettingsSectionCard
      icon={ShieldAlert}
      title={t('admin.clientAuthRateLimitSettings')}
      description={t('admin.clientAuthRateLimitSettingsDescription')}
      badge={
        <Badge variant={settings.enabled ? 'default' : 'outline'}>
          {settings.enabled ? t('common.enabled') : t('common.disabled')}
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
                disabled={isSaving}
                onClick={onDiscard}
              >
                <RotateCcw data-icon="inline-start" />
                {t('admin.discardChanges')}
              </Button>
            ) : null}
            <Button
              type="button"
              size="sm"
              disabled={!isDirty || !isValid || isSaving}
              onClick={onSave}
            >
              {isSaving ? (
                <Spinner data-icon="inline-start" />
              ) : (
                <Save data-icon="inline-start" />
              )}
              {isSaving ? t('common.saving') : t('common.save')}
            </Button>
          </div>
        </>
      }
    >
      <SettingRow
        title={t('admin.enableClientAuthRateLimit')}
        description={t('admin.enableClientAuthRateLimitHelp')}
        actions={
          <Switch
            id="client-auth-rate-limit-enabled"
            checked={settings.enabled}
            onCheckedChange={(enabled) => onChange({ ...settings, enabled })}
          />
        }
      />

      <SettingRow
        title={t('admin.clientAuthRequestsPerMinute')}
        description={
          isValid
            ? t('admin.clientAuthRequestsPerMinuteHelp')
            : t('admin.clientAuthRateLimitInvalid')
        }
        actions={
          <InputGroup className="w-40">
            <InputGroupInput
              id="client-auth-rate-limit-per-minute"
              type="number"
              min={1}
              max={1000}
              step={1}
              value={settings.requests_per_minute}
              onChange={(event) =>
                onChange({
                  ...settings,
                  requests_per_minute: Number(event.target.value),
                })
              }
              aria-invalid={!isValid}
              className="font-mono text-sm"
            />
            <InputGroupAddon align="inline-end" className="text-xs">
              {t('admin.requestsPerMinuteUnit')}
            </InputGroupAddon>
          </InputGroup>
        }
      />

      {/* Integrated Live Rate-Limit Records */}
      <div>
        <div className="flex flex-wrap items-center justify-between gap-2 bg-muted/15 px-4 py-2.5 sm:px-5">
          <span className="text-xs font-medium text-muted-foreground">
            {t('admin.clientAuthRateLimits')}
          </span>
          <div className="flex items-center gap-1.5">
            <Badge
              variant={limitedCount > 0 ? 'destructive' : 'secondary'}
              className="text-[11px]"
            >
              {t('admin.clientAuthRateLimitedCount', { count: limitedCount })}
            </Badge>
            <Badge variant="outline" className="text-[11px]">
              {t('admin.clientAuthRateLimitEntryCount', { count: entries.length })}
            </Badge>
          </div>
        </div>

        {entries.length === 0 ? (
          <div className="flex items-center gap-2.5 px-4 py-3.5 text-xs text-muted-foreground sm:px-5">
            <span className="size-2 shrink-0 rounded-full bg-emerald-500/80" />
            <span className="font-medium text-foreground/80">
              {t('admin.noClientAuthRateLimits')}
            </span>
            <span className="hidden truncate sm:inline">
              · {t('admin.noClientAuthRateLimitsDescription')}
            </span>
          </div>
        ) : (
          <div className="overflow-x-auto border-t border-border/30">
            <Table className="min-w-[680px]">
              <TableHeader>
                <TableRow>
                  <TableHead className="px-4">{t('admin.ipAddress')}</TableHead>
                  <TableHead className="px-4">{t('admin.limitStatus')}</TableHead>
                  <TableHead className="px-4">{t('admin.windowRequests')}</TableHead>
                  <TableHead className="px-4">{t('admin.retryAfter')}</TableHead>
                  <TableHead className="px-4">{t('admin.lastActivity')}</TableHead>
                  <TableHead className="px-4 text-right">{t('admin.actions')}</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {entries.map((entry) => {
                  const resetPending = resettingIP === entry.ip;
                  return (
                    <TableRow key={entry.ip}>
                      <TableCell className="px-4 font-mono text-xs">
                        {entry.ip}
                      </TableCell>
                      <TableCell className="px-4">
                        <Badge variant={entry.limited ? 'destructive' : 'secondary'}>
                          {rateLimitStatusLabel(entry, t)}
                        </Badge>
                      </TableCell>
                      <TableCell className="px-4 font-mono text-xs text-muted-foreground">
                        {entry.request_count} / {entry.max_requests}
                      </TableCell>
                      <TableCell className="px-4 text-muted-foreground">
                        {entry.limited ? formatUptime(entry.retry_after_seconds) : '-'}
                      </TableCell>
                      <TableCell className="px-4 text-muted-foreground">
                        {formatTimestamp(entry.last_activity)}
                      </TableCell>
                      <TableCell className="px-4 text-right">
                        <Button
                          type="button"
                          variant="ghost"
                          size="icon-sm"
                          disabled={resetPending || resettingIP !== null}
                          onClick={() => onResetIP(entry.ip)}
                          title={t('admin.clearClientAuthRateLimit')}
                          aria-label={t('admin.clearClientAuthRateLimit')}
                        >
                          {resetPending ? <Spinner /> : <Trash2 />}
                        </Button>
                      </TableCell>
                    </TableRow>
                  );
                })}
              </TableBody>
            </Table>
          </div>
        )}
      </div>
    </SettingsSectionCard>
  );
}

function rateLimitStatusLabel(
  entry: RateLimitEntry,
  t: ReturnType<typeof useTranslation>['t'],
) {
  if (!entry.limited) {
    return t('admin.clientAuthRateLimitCounting');
  }
  if (entry.reason === 'lockout') {
    return t('admin.clientAuthRateLimitLockout');
  }
  if (entry.reason === 'window') {
    return t('admin.clientAuthRateLimitWindow');
  }
  return t('admin.clientAuthRateLimitLimited');
}
