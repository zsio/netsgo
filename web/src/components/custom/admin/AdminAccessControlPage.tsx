import { useState } from "react";
import { Save, ShieldOff, Trash2 } from "lucide-react";
import toast from "react-hot-toast";
import { useTranslation } from "react-i18next";
import {
  useClientAuthRateLimitMutations,
  useClientAuthRateLimits,
} from "@/hooks/use-admin-rate-limits";
import { formatTimestamp, formatUptime } from "@/lib/format";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Card,
  CardAction,
  CardContent,
  CardDescription,
  CardFooter,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from "@/components/ui/empty";
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import { Spinner } from "@/components/ui/spinner";
import { Switch } from "@/components/ui/switch";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import type { ClientAuthRateLimitSettings, RateLimitEntry } from "@/types";

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
      toast.success(t("admin.clientAuthRateLimitCleared", { ip }));
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t("errors.generic"));
    } finally {
      setResettingIP(null);
    }
  };

  const saveSettings = async () => {
    if (!settingsValid) {
      toast.error(t("admin.clientAuthRateLimitInvalid"));
      return;
    }
    try {
      await mutations.updateSettings.mutateAsync(displayedSettings);
      setSettings(null);
      toast.success(t("admin.clientAuthRateLimitSettingsSaved"));
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t("errors.generic"));
    }
  };

  return (
    <div className="flex flex-col gap-6 pb-10">
      <div>
        <h3 className="text-xl font-semibold tracking-tight">
          {t("admin.accessControlTitle")}
        </h3>
        <p className="mt-1 text-sm text-muted-foreground">
          {t("admin.accessControlDescription")}
        </p>
      </div>
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
    <Card>
      <CardHeader>
        <CardTitle>{t("admin.clientAuthRateLimitSettings")}</CardTitle>
        <CardDescription>
          {t("admin.clientAuthRateLimitSettingsDescription")}
        </CardDescription>
        <CardAction>
          <Badge variant={settings.enabled ? "default" : "outline"}>
            {settings.enabled ? t("common.enabled") : t("common.disabled")}
          </Badge>
        </CardAction>
      </CardHeader>
      <CardContent>
        <div className="flex flex-col divide-y divide-border/50">
          <div className="grid gap-4 pb-5 sm:grid-cols-[minmax(0,1fr)_auto] sm:items-center sm:gap-6">
            <div className="min-w-0">
              <p className="font-medium text-foreground">
                {t("admin.enableClientAuthRateLimit")}
              </p>
              <p className="mt-1 max-w-2xl text-sm text-muted-foreground">
                {t("admin.enableClientAuthRateLimitHelp")}
              </p>
            </div>
            <Switch
              id="client-auth-rate-limit-enabled"
              checked={settings.enabled}
              onCheckedChange={(enabled) => onChange({ ...settings, enabled })}
            />
          </div>

          <div className="grid gap-4 pt-5 sm:grid-cols-[minmax(0,1fr)_auto] sm:items-center sm:gap-6">
            <div className="min-w-0">
              <p className="font-medium text-foreground">
                {t("admin.clientAuthRequestsPerMinute")}
              </p>
              <p className="mt-1 max-w-2xl text-sm text-muted-foreground">
                {isValid
                  ? t("admin.clientAuthRequestsPerMinuteHelp")
                  : t("admin.clientAuthRateLimitInvalid")}
              </p>
            </div>
            <Input
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
              className="w-28"
            />
          </div>
        </div>
      </CardContent>
      <CardFooter className="justify-end">
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
          {isSaving ? t("common.saving") : t("common.save")}
        </Button>
      </CardFooter>
    </Card>
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
    <Card>
      <CardHeader>
        <CardTitle>{t("admin.clientAuthRateLimits")}</CardTitle>
        <CardDescription>
          {t("admin.clientAuthRateLimitsDescription")}
        </CardDescription>
        <CardAction className="flex items-center gap-2">
          <Badge variant={limitedCount > 0 ? "destructive" : "secondary"}>
            {t("admin.clientAuthRateLimitedCount", { count: limitedCount })}
          </Badge>
          <Badge variant="outline">
            {t("admin.clientAuthRateLimitEntryCount", {
              count: entries.length,
            })}
          </Badge>
        </CardAction>
      </CardHeader>
      <CardContent className="px-0">
        {entries.length === 0 ? (
          <Empty>
            <EmptyHeader>
              <EmptyMedia variant="icon">
                <ShieldOff />
              </EmptyMedia>
              <EmptyTitle>{t("admin.noClientAuthRateLimits")}</EmptyTitle>
              <EmptyDescription>
                {t("admin.noClientAuthRateLimitsDescription")}
              </EmptyDescription>
            </EmptyHeader>
          </Empty>
        ) : (
          <div className="overflow-x-auto">
            <Table className="min-w-[780px]">
              <TableHeader>
                <TableRow>
                  <TableHead className="px-4">{t("admin.ipAddress")}</TableHead>
                  <TableHead className="px-4">{t("admin.limitStatus")}</TableHead>
                  <TableHead className="px-4">
                    {t("admin.windowRequests")}
                  </TableHead>
                  <TableHead className="px-4">{t("admin.retryAfter")}</TableHead>
                  <TableHead className="px-4">
                    {t("admin.lastActivity")}
                  </TableHead>
                  <TableHead className="px-4 text-right">
                    {t("admin.actions")}
                  </TableHead>
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
                        <Badge
                          variant={entry.limited ? "destructive" : "secondary"}
                        >
                          {rateLimitStatusLabel(entry, t)}
                        </Badge>
                      </TableCell>
                      <TableCell className="px-4 font-mono text-xs text-muted-foreground">
                        {entry.request_count} / {entry.max_requests}
                      </TableCell>
                      <TableCell className="px-4 text-muted-foreground">
                        {entry.limited
                          ? formatUptime(entry.retry_after_seconds)
                          : "-"}
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
                          onClick={() => onReset(entry.ip)}
                          title={t("admin.clearClientAuthRateLimit")}
                          aria-label={t("admin.clearClientAuthRateLimit")}
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
      </CardContent>
    </Card>
  );
}

function rateLimitStatusLabel(
  entry: RateLimitEntry,
  t: ReturnType<typeof useTranslation>["t"],
) {
  if (!entry.limited) {
    return t("admin.clientAuthRateLimitCounting");
  }
  if (entry.reason === "lockout") {
    return t("admin.clientAuthRateLimitLockout");
  }
  if (entry.reason === "window") {
    return t("admin.clientAuthRateLimitWindow");
  }
  return t("admin.clientAuthRateLimitLimited");
}
