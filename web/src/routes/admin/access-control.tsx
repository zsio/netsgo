import { useState } from 'react';
import { createRoute } from '@tanstack/react-router';
import { RotateCcw, Save, ShieldOff, Trash2 } from 'lucide-react';
import toast from 'react-hot-toast';
import { useTranslation } from 'react-i18next';
import { adminRoute } from '../admin';
import { useClientAuthRateLimitMutations, useClientAuthRateLimits } from '@/hooks/use-admin-rate-limits';
import { formatTimestamp, formatUptime } from '@/lib/format';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Switch } from '@/components/ui/switch';
import { Skeleton } from '@/components/ui/skeleton';
import type { ClientAuthRateLimitSettings, RateLimitEntry } from '@/types';

export const adminAccessControlRoute = createRoute({
  getParentRoute: () => adminRoute,
  path: '/access-control',
  component: AdminAccessControlPage,
});

function AdminAccessControlPage() {
  const { t } = useTranslation();
  const { data, isLoading } = useClientAuthRateLimits();
  const mutations = useClientAuthRateLimitMutations();
  const [resettingIP, setResettingIP] = useState<string | null>(null);
  const [settings, setSettings] = useState<ClientAuthRateLimitSettings | null>(null);
  const displayedSettings = settings ?? {
    enabled: data?.enabled ?? false,
    requests_per_minute: data?.requests_per_minute ?? 20,
  };
  const settingsDirty = data != null && (
    displayedSettings.enabled !== data.enabled
    || displayedSettings.requests_per_minute !== data.requests_per_minute
  );
  const settingsValid = Number.isInteger(displayedSettings.requests_per_minute)
    && displayedSettings.requests_per_minute >= 1
    && displayedSettings.requests_per_minute <= 1000;

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
    <div className="flex flex-col gap-6 pb-10">
      <ClientAuthRateLimitSettingsCard
        settings={displayedSettings}
        isLoading={isLoading}
        isDirty={settingsDirty}
        isValid={settingsValid}
        isSaving={mutations.updateSettings.isPending}
        onChange={setSettings}
        onSave={saveSettings}
      />
      <ClientAuthRateLimitsSection
        entries={data?.entries ?? []}
        isLoading={isLoading}
        resettingIP={resettingIP}
        onReset={resetClientAuthRateLimit}
      />
    </div>
  );
}

function ClientAuthRateLimitSettingsCard({
  settings,
  isLoading,
  isDirty,
  isValid,
  isSaving,
  onChange,
  onSave,
}: {
  settings: ClientAuthRateLimitSettings;
  isLoading: boolean;
  isDirty: boolean;
  isValid: boolean;
  isSaving: boolean;
  onChange: (settings: ClientAuthRateLimitSettings) => void;
  onSave: () => void;
}) {
  const { t } = useTranslation();

  if (isLoading) {
    return <Skeleton className="h-44 w-full rounded-xl" />;
  }

  return (
    <section className="overflow-hidden rounded-xl border border-border/50 bg-background/90">
      <div className="grid gap-5 px-5 py-5 md:grid-cols-[minmax(0,1fr)_minmax(280px,360px)] md:items-start">
        <div className="min-w-0">
          <div className="flex items-center gap-2">
            <h3 className="font-medium text-foreground">{t('admin.clientAuthRateLimitSettings')}</h3>
            <Badge variant={settings.enabled ? 'default' : 'outline'}>
              {settings.enabled ? t('common.enabled') : t('common.disabled')}
            </Badge>
          </div>
          <p className="mt-1 max-w-2xl text-sm text-muted-foreground">
            {t('admin.clientAuthRateLimitSettingsDescription')}
          </p>
        </div>

        <div className="flex flex-col gap-4 rounded-lg border border-border/50 bg-muted/20 p-4">
          <label className="flex cursor-pointer items-center justify-between gap-4">
            <span>
              <span className="block text-sm font-medium text-foreground">{t('admin.enableClientAuthRateLimit')}</span>
              <span className="mt-0.5 block text-xs text-muted-foreground">{t('admin.enableClientAuthRateLimitHelp')}</span>
            </span>
            <Switch
              checked={settings.enabled}
              onCheckedChange={(enabled) => onChange({ ...settings, enabled })}
              aria-label={t('admin.enableClientAuthRateLimit')}
            />
          </label>

          <div>
            <label htmlFor="client-auth-rate-limit-per-minute" className="text-sm font-medium text-foreground">
              {t('admin.clientAuthRequestsPerMinute')}
            </label>
            <Input
              id="client-auth-rate-limit-per-minute"
              type="number"
              min={1}
              max={1000}
              step={1}
              value={settings.requests_per_minute}
              onChange={(event) => onChange({
                ...settings,
                requests_per_minute: Number(event.target.value),
              })}
              aria-invalid={!isValid}
              className="mt-2"
            />
            <p className={`mt-1.5 text-xs ${isValid ? 'text-muted-foreground' : 'text-destructive'}`}>
              {isValid ? t('admin.clientAuthRequestsPerMinuteHelp') : t('admin.clientAuthRateLimitInvalid')}
            </p>
          </div>

          <Button
            type="button"
            size="sm"
            disabled={!isDirty || !isValid || isSaving}
            onClick={onSave}
            className="self-end gap-2"
          >
            {isSaving ? <RotateCcw className="animate-spin" /> : <Save />}
            {isSaving ? t('common.saving') : t('common.save')}
          </Button>
        </div>
      </div>
    </section>
  );
}

function ClientAuthRateLimitsSection({
  entries,
  isLoading,
  resettingIP,
  onReset,
}: {
  entries: RateLimitEntry[];
  isLoading: boolean;
  resettingIP: string | null;
  onReset: (ip: string) => void;
}) {
  const { t } = useTranslation();
  const limitedCount = entries.filter((entry) => entry.limited).length;

  if (isLoading) {
    return (
      <div className="flex flex-col gap-3">
        <Skeleton className="h-20 w-full rounded-xl" />
        <Skeleton className="h-48 w-full rounded-xl" />
      </div>
    );
  }

  return (
    <div className="overflow-hidden rounded-xl border border-border/50 bg-background/90">
      <div className="grid gap-4 border-b border-border/50 px-4 py-4 sm:grid-cols-[minmax(0,1fr)_auto] sm:items-center sm:px-5">
        <div className="min-w-0">
          <p className="font-medium text-foreground">{t('admin.clientAuthRateLimits')}</p>
          <p className="mt-1 max-w-2xl text-sm text-muted-foreground">{t('admin.clientAuthRateLimitsDescription')}</p>
        </div>
        <div className="flex items-center gap-2">
          <Badge variant={limitedCount > 0 ? 'destructive' : 'secondary'}>
            {t('admin.clientAuthRateLimitedCount', { count: limitedCount })}
          </Badge>
          <Badge variant="outline">
            {t('admin.clientAuthRateLimitEntryCount', { count: entries.length })}
          </Badge>
        </div>
      </div>

      {entries.length === 0 ? (
        <div className="flex min-h-40 flex-col items-center justify-center gap-2 px-5 py-10 text-center">
          <ShieldOff className="size-8 text-muted-foreground" />
          <p className="text-sm font-medium text-foreground">{t('admin.noClientAuthRateLimits')}</p>
          <p className="max-w-md text-sm text-muted-foreground">{t('admin.noClientAuthRateLimitsDescription')}</p>
        </div>
      ) : (
        <div className="overflow-x-auto">
          <table className="w-full min-w-[780px] text-sm">
            <thead className="border-b border-border/50 bg-muted/30 text-left text-muted-foreground">
              <tr>
                <th className="px-4 py-2.5 font-medium">{t('admin.ipAddress')}</th>
                <th className="px-4 py-2.5 font-medium">{t('admin.limitStatus')}</th>
                <th className="px-4 py-2.5 font-medium">{t('admin.windowRequests')}</th>
                <th className="px-4 py-2.5 font-medium">{t('admin.retryAfter')}</th>
                <th className="px-4 py-2.5 font-medium">{t('admin.lastActivity')}</th>
                <th className="px-4 py-2.5 text-right font-medium">{t('admin.actions')}</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-border/40">
              {entries.map((entry) => {
                const resetPending = resettingIP === entry.ip;
                return (
                  <tr key={entry.ip} className="transition-colors hover:bg-muted/20">
                    <td className="px-4 py-3 font-mono text-xs text-foreground">{entry.ip}</td>
                    <td className="px-4 py-3">
                      <Badge variant={entry.limited ? 'destructive' : 'secondary'}>
                        {rateLimitStatusLabel(entry, t)}
                      </Badge>
                    </td>
                    <td className="px-4 py-3 font-mono text-xs text-muted-foreground">
                      {entry.request_count} / {entry.max_requests}
                    </td>
                    <td className="px-4 py-3 text-muted-foreground">
                      {entry.limited ? formatUptime(entry.retry_after_seconds) : '-'}
                    </td>
                    <td className="px-4 py-3 text-muted-foreground">
                      {formatTimestamp(entry.last_activity)}
                    </td>
                    <td className="px-4 py-2 text-right">
                      <Button
                        type="button"
                        variant="ghost"
                        size="icon-sm"
                        disabled={resetPending || resettingIP !== null}
                        onClick={() => onReset(entry.ip)}
                        title={t('admin.clearClientAuthRateLimit')}
                        aria-label={t('admin.clearClientAuthRateLimit')}
                      >
                        {resetPending ? <RotateCcw className="animate-spin" /> : <Trash2 />}
                      </Button>
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}

function rateLimitStatusLabel(entry: RateLimitEntry, t: ReturnType<typeof useTranslation>['t']) {
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
