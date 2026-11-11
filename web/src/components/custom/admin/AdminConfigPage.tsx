import { useState } from 'react';
import { useTranslation, Trans } from 'react-i18next';
import {
  AlertTriangle,
  Check,
  Edit2,
  Globe,
  History,
  Lock,
  Network,
  Plus,
  RotateCcw,
  Save,
  ShieldAlert,
  Trash2,
  X,
} from 'lucide-react';
import { motion, AnimatePresence } from 'motion/react';
import toast from 'react-hot-toast';
import { ApiError, api } from '@/lib/api';
import { useAdminConfig, useUpdateAdminConfig } from '@/hooks/use-admin-config';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import {
  InputGroup,
  InputGroupAddon,
  InputGroupInput,
} from '@/components/ui/input-group';
import { Skeleton } from '@/components/ui/skeleton';
import { Spinner } from '@/components/ui/spinner';
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip';
import { TableActionIconButton } from '@/components/custom/common/TableActionIconButton';
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogMedia,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog';
import {
  getServerAddrValidationIssue,
  normalizeServerAddr,
  SERVER_ADDR_HELP_TEXT,
  SERVER_ADDR_PLACEHOLDER,
} from '@/lib/server-address';
import { cn, createLocalId } from '@/lib/utils';
import { resolveTunnelStatus } from '@/lib/tunnel-model';
import type {
  AdminConfig,
  AdminConfigUpdateResponse,
  AffectedTunnel,
  PortRange,
  ActivityRetentionPolicy,
  ActivitySeverity,
} from '@/types';
import { SettingsSectionCard } from './security/security-primitives';

type LocalPortRange = PortRange & { _id: string };

const SEVERITY_DOT_CLASS: Record<ActivitySeverity, string> = {
  debug: 'bg-zinc-400',
  info: 'bg-sky-500',
  warning: 'bg-amber-500',
  error: 'bg-rose-500',
};

function toLocalPortRanges(ports?: PortRange[]): LocalPortRange[] {
  return (ports ?? []).map((p) => ({ ...p, _id: createLocalId('port-range') }));
}

export function AdminConfigPage() {
  const { data: config, isLoading } = useAdminConfig();

  if (isLoading || !config) {
    return (
      <div className="flex flex-col gap-5 pb-10">
        <Skeleton className="h-40 w-full rounded-xl" />
        <Skeleton className="h-52 w-full rounded-xl" />
        <Skeleton className="h-56 w-full rounded-xl" />
      </div>
    );
  }

  return <AdminConfigForm key={JSON.stringify(config)} initialConfig={config} />;
}

function AdminConfigForm({ initialConfig }: { initialConfig: AdminConfig }) {
  const { t } = useTranslation();
  const updateConfig = useUpdateAdminConfig();
  const [serverAddr, setServerAddr] = useState(initialConfig.server_addr || '');
  const initialServerAddr = (initialConfig.server_addr || '').trim();
  const initialServerAddrIsLegacy =
    initialServerAddr !== '' && getServerAddrValidationIssue(initialServerAddr) !== null;
  const serverAddrLocked = initialConfig.server_addr_locked;

  const [portRanges, setPortRanges] = useState<LocalPortRange[]>(() =>
    toLocalPortRanges(initialConfig.allowed_ports),
  );
  const [newRange, setNewRange] = useState<{ start: string; end: string }>({
    start: '',
    end: '',
  });
  const [editingIndex, setEditingIndex] = useState<number | null>(null);
  const [editForm, setEditForm] = useState<{ start: number | ''; end: number | '' }>({
    start: '',
    end: '',
  });

  const [activityRetention, setActivityRetention] = useState<ActivityRetentionPolicy>(
    initialConfig.activity_retention,
  );

  const [saved, setSaved] = useState(false);
  const [checking, setChecking] = useState(false);
  const [affectedTunnels, setAffectedTunnels] = useState<AffectedTunnel[]>([]);
  const [conflictingHTTPTunnels, setConflictingHTTPTunnels] = useState<string[]>([]);
  const [pendingServerAddr, setPendingServerAddr] = useState<string | null>(null);
  const [showConfirm, setShowConfirm] = useState(false);

  const severities = Object.keys(activityRetention) as ActivitySeverity[];
  const initialPorts = initialConfig.allowed_ports ?? [];
  const hasPendingNewRange = newRange.start.trim() !== '' || newRange.end.trim() !== '';

  const portsChanged =
    portRanges.length !== initialPorts.length ||
    portRanges.some(
      (range, index) =>
        range.start !== initialPorts[index]?.start || range.end !== initialPorts[index]?.end,
    );

  const retentionChanged = severities.some((severity) => {
    const initialRule = initialConfig.activity_retention[severity];
    const currentRule = activityRetention[severity];
    return (
      currentRule.days !== initialRule?.days ||
      currentRule.min_count !== initialRule?.min_count
    );
  });

  const isDirty =
    serverAddr.trim() !== initialServerAddr ||
    portsChanged ||
    retentionChanged ||
    hasPendingNewRange;

  const totalPortCount = portRanges.reduce(
    (sum, range) => sum + Math.max(0, range.end - range.start + 1),
    0,
  );

  const resetDraft = () => {
    setServerAddr(initialConfig.server_addr || '');
    setPortRanges(toLocalPortRanges(initialConfig.allowed_ports));
    setNewRange({ start: '', end: '' });
    setEditingIndex(null);
    setEditForm({ start: '', end: '' });
    setActivityRetention(initialConfig.activity_retention);
    setConflictingHTTPTunnels([]);
    setSaved(false);
  };

  const updateRetentionRule = (
    severity: ActivitySeverity,
    field: 'days' | 'min_count',
    value: number,
  ) => {
    setActivityRetention((current) => ({
      ...current,
      [severity]: { ...current[severity], [field]: value },
    }));
  };

  const toPayloadPorts = (ranges: LocalPortRange[]): PortRange[] =>
    ranges.map((range) => ({ start: range.start, end: range.end }));

  const resolveServerAddrForSubmit = () => {
    const trimmedServerAddr = serverAddr.trim();
    const isUnchangedLegacy =
      initialServerAddrIsLegacy && trimmedServerAddr === initialServerAddr;

    if (isUnchangedLegacy) {
      return {
        value: initialServerAddr,
        shouldUpdateInput: false,
      };
    }

    const addrIssue = getServerAddrValidationIssue(serverAddr);
    if (addrIssue) {
      toast.error(t(`serverAddress.${addrIssue.code}`, { defaultValue: addrIssue.message }));
      return null;
    }
    const normalizedServerAddr = normalizeServerAddr(serverAddr);
    if (!normalizedServerAddr) {
      toast.error(t('admin.invalidClientAddress'));
      return null;
    }

    return {
      value: normalizedServerAddr,
      shouldUpdateInput: normalizedServerAddr !== serverAddr,
    };
  };

  const validatePortPair = (
    startVal: number,
    endVal: number,
    ignoreIndex: number | null = null,
    existingRanges: LocalPortRange[] = portRanges,
  ): boolean => {
    if (
      !Number.isInteger(startVal) ||
      !Number.isInteger(endVal) ||
      startVal < 1 ||
      endVal > 65535
    ) {
      toast.error(t('admin.invalidPortRangeFields'));
      return false;
    }
    if (startVal > endVal) {
      toast.error(t('admin.startPortAfterEnd'));
      return false;
    }
    const isOverlap = existingRanges.some((range, index) => {
      if (index === ignoreIndex) return false;
      return Math.max(startVal, range.start) <= Math.min(endVal, range.end);
    });
    if (isOverlap) {
      toast.error(t('admin.overlappingPortRange'));
      return false;
    }
    return true;
  };

  const commitNewRange = (
    customStart?: number,
    customEnd?: number,
  ): LocalPortRange[] | null => {
    const rawStart = customStart !== undefined ? String(customStart) : newRange.start.trim();
    const rawEnd = customEnd !== undefined ? String(customEnd) : newRange.end.trim();
    const startVal = Number.parseInt(rawStart, 10);
    const endVal = rawEnd === '' ? startVal : Number.parseInt(rawEnd, 10);

    if (!validatePortPair(startVal, endVal, null, portRanges)) {
      return null;
    }

    const nextRanges = [
      ...portRanges,
      { start: startVal, end: endVal, _id: createLocalId('port-range') },
    ].sort((a, b) => a.start - b.start);

    setPortRanges(nextRanges);
    setNewRange({ start: '', end: '' });
    return nextRanges;
  };

  const startEdit = (index: number) => {
    setEditingIndex(index);
    setEditForm({ start: portRanges[index].start, end: portRanges[index].end });
  };

  const cancelEdit = () => {
    setEditingIndex(null);
    setEditForm({ start: '', end: '' });
  };

  const saveEdit = () => {
    if (editingIndex === null) return;
    const startVal = Number(editForm.start);
    const endVal = Number(editForm.end);

    if (!validatePortPair(startVal, endVal, editingIndex, portRanges)) {
      return;
    }

    const updated = [...portRanges];
    const existingId = updated[editingIndex]?._id ?? createLocalId('port-range');
    updated[editingIndex] = { start: startVal, end: endVal, _id: existingId };
    updated.sort((a, b) => a.start - b.start);
    setPortRanges(updated);
    setEditingIndex(null);
  };

  const handleEditKeyDown = (event: React.KeyboardEvent) => {
    if (event.key === 'Enter') {
      event.preventDefault();
      saveEdit();
    } else if (event.key === 'Escape') {
      cancelEdit();
    }
  };

  const handleNewRangeKeyDown = (event: React.KeyboardEvent) => {
    if (event.key === 'Enter') {
      event.preventDefault();
      commitNewRange();
    }
  };

  const removePortRange = (index: number) => {
    setPortRanges(portRanges.filter((_, i) => i !== index));
    if (editingIndex === index) {
      cancelEdit();
    } else if (editingIndex !== null && editingIndex > index) {
      setEditingIndex(editingIndex - 1);
    }
  };

  const activityRetentionIsValid = severities.every((severity) => {
    const rule = activityRetention[severity];
    return (
      Number.isInteger(rule.days) &&
      rule.days >= 1 &&
      rule.days <= 3650 &&
      Number.isInteger(rule.min_count) &&
      rule.min_count >= 0 &&
      rule.min_count <= 100000
    );
  });

  const checkAndSave = async (event: React.FormEvent) => {
    event.preventDefault();

    if (editingIndex !== null) {
      toast.error(t('admin.editingRulesActive'));
      return;
    }

    let effectiveRanges = portRanges;
    if (hasPendingNewRange) {
      const committed = commitNewRange();
      if (!committed) return;
      effectiveRanges = committed;
    }

    const resolvedServerAddr = resolveServerAddrForSubmit();
    if (!resolvedServerAddr) {
      return;
    }
    if (!activityRetentionIsValid) {
      toast.error(t('admin.activityRetentionInvalid'));
      return;
    }
    if (resolvedServerAddr.shouldUpdateInput) {
      setServerAddr(resolvedServerAddr.value);
    }

    setSaved(false);
    setChecking(true);

    try {
      const cleanPorts = toPayloadPorts(effectiveRanges);
      setPendingServerAddr(resolvedServerAddr.value);
      const result = await api.put<AdminConfigUpdateResponse>(
        '/api/admin/config?dry_run=true',
        {
          server_addr: resolvedServerAddr.value,
          allowed_ports: cleanPorts,
          activity_retention: activityRetention,
        },
      );

      const conflicts = result.conflicting_http_tunnels ?? [];
      setConflictingHTTPTunnels(conflicts);
      if (conflicts.length > 0) {
        setAffectedTunnels([]);
        setPendingServerAddr(null);
        toast.error(t('admin.serverAddrConflictToast'));
        return;
      }

      if (result.affected_tunnels && result.affected_tunnels.length > 0) {
        setAffectedTunnels(result.affected_tunnels);
        setShowConfirm(true);
      } else {
        await doSave(resolvedServerAddr.value, effectiveRanges);
      }
    } catch (error: unknown) {
      setPendingServerAddr(null);
      if (error instanceof ApiError) {
        const body = error.body as AdminConfigUpdateResponse | undefined;
        setConflictingHTTPTunnels(body?.conflicting_http_tunnels ?? []);
      }
      const message = error instanceof Error ? error.message : t('admin.configCheckFailed');
      toast.error(message);
      console.error('config check failed', error);
    } finally {
      setChecking(false);
    }
  };

  const doSave = async (
    serverAddrToSave?: string,
    rangesToSave: LocalPortRange[] = portRanges,
  ) => {
    try {
      const resolvedServerAddr = serverAddrToSave
        ? { value: serverAddrToSave, shouldUpdateInput: false }
        : resolveServerAddrForSubmit();
      if (!resolvedServerAddr) {
        return;
      }
      const cleanPorts = toPayloadPorts(rangesToSave);
      await updateConfig.mutateAsync({
        server_addr: resolvedServerAddr.value,
        allowed_ports: cleanPorts,
        activity_retention: activityRetention,
      });
      setServerAddr(resolvedServerAddr.value);
      setConflictingHTTPTunnels([]);
      setSaved(true);
      setShowConfirm(false);
      setAffectedTunnels([]);
      setPendingServerAddr(null);
      setTimeout(() => setSaved(false), 2500);
    } catch (error: unknown) {
      if (error instanceof ApiError) {
        const body = error.body as AdminConfigUpdateResponse | undefined;
        setConflictingHTTPTunnels(body?.conflicting_http_tunnels ?? []);
      }
      const message = error instanceof Error ? error.message : t('admin.configSaveFailed');
      toast.error(message);
      console.error('config save failed', error);
    }
  };

  const affectedTunnelStatus = (tunnel: AffectedTunnel) =>
    resolveTunnelStatus(
      {
        desired_state: tunnel.desired_state,
        runtime_state: tunnel.runtime_state,
        error: tunnel.error,
      },
      tunnel.runtime_state !== 'offline',
    );

  return (
    <>
      <form onSubmit={checkAndSave} className="flex flex-col gap-5 pb-10">
        {/* Card 1: Recommended Server URL */}
        <SettingsSectionCard
          icon={Globe}
          title={t('admin.defaultServerAddress')}
          description={t('admin.serverAddrSectionDesc')}
          badge={
            serverAddrLocked ? (
              <Tooltip>
                <TooltipTrigger asChild>
                  <Badge variant="secondary" className="cursor-help gap-1">
                    <Lock className="size-3" />
                    {t('admin.serverAddrLockedLabel')}
                  </Badge>
                </TooltipTrigger>
                <TooltipContent side="top" className="max-w-[320px]">
                  <p>{t('admin.serverAddrLockedTooltip')}</p>
                </TooltipContent>
              </Tooltip>
            ) : (
              <Badge variant="outline" className="text-muted-foreground">
                {t('admin.serverAddrEditableBadge')}
              </Badge>
            )
          }
          bodyClassName="divide-y-0 p-4 sm:p-5"
        >
          <div className="flex flex-col gap-2">
            <label htmlFor="admin-server-addr" className="text-sm font-medium text-foreground">
              {t('admin.serverAddrFieldLabel')}
            </label>
            <Input
              id="admin-server-addr"
              value={serverAddr}
              onChange={(event) => {
                setServerAddr(event.target.value);
                setConflictingHTTPTunnels([]);
              }}
              placeholder={SERVER_ADDR_PLACEHOLDER}
              className="max-w-xl font-mono text-sm"
              disabled={serverAddrLocked}
            />
            <p className="text-xs text-muted-foreground">{SERVER_ADDR_HELP_TEXT}</p>

            {initialServerAddrIsLegacy && serverAddr.trim() === initialServerAddr ? (
              <p className="text-xs text-amber-600 dark:text-amber-400">
                {t('admin.legacyServerAddrWarning')}
              </p>
            ) : null}

            {conflictingHTTPTunnels.length > 0 ? (
              <div className="mt-1 max-w-xl rounded-lg border border-destructive/30 bg-destructive/8 p-3 text-sm">
                <div className="flex items-start gap-2 text-destructive">
                  <AlertTriangle className="mt-0.5 size-4 shrink-0" />
                  <div className="flex flex-col gap-1">
                    <p className="font-medium">{t('admin.serverAddrConflictTitle')}</p>
                    <p className="text-xs text-destructive/80">
                      {t('admin.serverAddrConflictHelp')}
                    </p>
                    <div className="mt-1 flex flex-wrap gap-1.5">
                      {conflictingHTTPTunnels.map((tunnel) => (
                        <code
                          key={tunnel}
                          className="rounded border border-border/50 bg-background/80 px-2 py-0.5 font-mono text-[11px] text-foreground"
                        >
                          {tunnel}
                        </code>
                      ))}
                    </div>
                  </div>
                </div>
              </div>
            ) : null}
          </div>
        </SettingsSectionCard>

        {/* Card 2: Allowed Port Pool */}
        <SettingsSectionCard
          icon={Network}
          title={t('admin.portRanges')}
          description={t('admin.portRangesHelp')}
          badge={
            portRanges.length === 0 ? (
              <Badge
                variant="outline"
                className="border-amber-500/40 bg-amber-500/10 text-amber-600 dark:text-amber-400"
              >
                {t('admin.portPoolUnrestrictedBadge')}
              </Badge>
            ) : (
              <Badge variant="secondary" className="font-mono text-xs">
                {t('admin.portPoolSummaryBadge', {
                  ranges: portRanges.length,
                  ports: totalPortCount,
                })}
              </Badge>
            )
          }
        >
          {/* Quick Add Bar */}
          <div className="flex flex-wrap items-center justify-between gap-3 bg-muted/10 px-4 py-3 sm:px-5">
            <div className="flex flex-wrap items-center gap-2">
              <Input
                type="number"
                min={1}
                max={65535}
                placeholder={t('admin.startPort')}
                value={newRange.start}
                onChange={(event) =>
                  setNewRange((prev) => ({ ...prev, start: event.target.value }))
                }
                onKeyDown={handleNewRangeKeyDown}
                className="h-8 w-32 font-mono text-sm"
              />
              <span className="text-muted-foreground">–</span>
              <Input
                type="number"
                min={1}
                max={65535}
                placeholder={t('admin.endPort')}
                value={newRange.end}
                onChange={(event) =>
                  setNewRange((prev) => ({ ...prev, end: event.target.value }))
                }
                onKeyDown={handleNewRangeKeyDown}
                className="h-8 w-32 font-mono text-sm"
              />
              <Button
                type="button"
                variant="outline"
                size="sm"
                disabled={newRange.start.trim() === '' || editingIndex !== null}
                onClick={() => commitNewRange()}
              >
                <Plus data-icon="inline-start" />
                {t('admin.addRangeAction')}
              </Button>
            </div>
            {portRanges.length === 0 ? (
              <Button
                type="button"
                variant="ghost"
                size="sm"
                className="text-xs text-muted-foreground hover:text-foreground"
                onClick={() => commitNewRange(10000, 20000)}
              >
                {t('admin.quickAddPresetPorts')}
              </Button>
            ) : null}
          </div>

          {/* Empty Warning or Port Range Rows */}
          {portRanges.length === 0 ? (
            <div className="flex items-center gap-2.5 px-4 py-3.5 text-xs text-amber-600 dark:text-amber-400 sm:px-5">
              <ShieldAlert className="size-4 shrink-0" />
              <span>{t('admin.openPortsWarning')}</span>
            </div>
          ) : (
            <div className="divide-y divide-border/30">
              {portRanges.map((range, index) => {
                const isEditing = editingIndex === index;
                const count = range.end - range.start + 1;

                return (
                  <div
                    key={range._id}
                    className="flex flex-wrap items-center justify-between gap-3 px-4 py-2.5 transition-colors hover:bg-muted/20 sm:px-5"
                  >
                    {isEditing ? (
                      <>
                        <div className="flex items-center gap-2">
                          <Input
                            type="number"
                            min={1}
                            max={65535}
                            value={editForm.start}
                            onChange={(event) =>
                              setEditForm({
                                ...editForm,
                                start:
                                  event.target.value === ''
                                    ? ''
                                    : Number.parseInt(event.target.value, 10) || '',
                              })
                            }
                            onKeyDown={handleEditKeyDown}
                            className="h-8 w-28 font-mono text-sm"
                          />
                          <span className="text-muted-foreground">–</span>
                          <Input
                            type="number"
                            min={1}
                            max={65535}
                            value={editForm.end}
                            onChange={(event) =>
                              setEditForm({
                                ...editForm,
                                end:
                                  event.target.value === ''
                                    ? ''
                                    : Number.parseInt(event.target.value, 10) || '',
                              })
                            }
                            onKeyDown={handleEditKeyDown}
                            className="h-8 w-28 font-mono text-sm"
                          />
                        </div>
                        <div className="flex items-center gap-1">
                          <TableActionIconButton
                            type="button"
                            label={t('common.save')}
                            tone="success"
                            onClick={saveEdit}
                          >
                            <Check className="size-3.5" />
                          </TableActionIconButton>
                          <TableActionIconButton
                            type="button"
                            label={t('common.cancel')}
                            tone="neutral"
                            onClick={cancelEdit}
                          >
                            <X className="size-3.5" />
                          </TableActionIconButton>
                        </div>
                      </>
                    ) : (
                      <>
                        <div className="flex items-center gap-3">
                          <span className="font-mono text-sm font-medium text-foreground">
                            {range.start === range.end
                              ? `${range.start}`
                              : `${range.start} – ${range.end}`}
                          </span>
                          <Badge
                            variant="outline"
                            className="font-normal text-muted-foreground"
                          >
                            {t('admin.portRangeCountLabel', { count })}
                          </Badge>
                        </div>
                        <div className="flex items-center gap-1">
                          <TableActionIconButton
                            type="button"
                            label={t('common.edit')}
                            tone="primary"
                            disabled={editingIndex !== null}
                            onClick={() => startEdit(index)}
                          >
                            <Edit2 className="size-3.5" />
                          </TableActionIconButton>
                          <TableActionIconButton
                            type="button"
                            label={t('common.delete')}
                            tone="destructive"
                            disabled={editingIndex !== null}
                            onClick={() => removePortRange(index)}
                          >
                            <Trash2 className="size-3.5" />
                          </TableActionIconButton>
                        </div>
                      </>
                    )}
                  </div>
                );
              })}
            </div>
          )}
        </SettingsSectionCard>

        {/* Card 3: Activity Retention Policy */}
        <SettingsSectionCard
          icon={History}
          title={t('admin.activityRetentionTitle')}
          description={t('admin.activityRetentionHelp')}
          footer={
            <p className="text-xs leading-relaxed text-muted-foreground">
              {t('admin.activityRetentionExactRule')}
            </p>
          }
        >
          {severities.map((severity) => {
            const labelKey = `admin.activitySeverity${severity[0].toUpperCase()}${severity.slice(1)}`;
            return (
              <div
                key={severity}
                className="grid gap-3 px-4 py-3.5 sm:grid-cols-[minmax(0,1fr)_auto] sm:items-center sm:px-5"
              >
                <div className="flex items-center gap-2.5">
                  <span
                    className={cn(
                      'size-2 shrink-0 rounded-full',
                      SEVERITY_DOT_CLASS[severity] ?? 'bg-zinc-400',
                    )}
                  />
                  <span className="text-sm font-medium text-foreground">
                    {t(labelKey)}
                  </span>
                </div>
                <div className="flex flex-wrap items-center gap-2.5 sm:justify-end">
                  <InputGroup className="w-32">
                    <InputGroupInput
                      type="number"
                      min={1}
                      max={3650}
                      value={activityRetention[severity].days}
                      aria-label={`${severity} ${t('admin.activityRetentionDays')}`}
                      onChange={(event) =>
                        updateRetentionRule(
                          severity,
                          'days',
                          Number.parseInt(event.target.value, 10) || 0,
                        )
                      }
                      className="font-mono text-sm"
                    />
                    <InputGroupAddon align="inline-end" className="text-xs">
                      {t('admin.retentionDaysUnit')}
                    </InputGroupAddon>
                  </InputGroup>

                  <InputGroup className="w-40">
                    <InputGroupInput
                      type="number"
                      min={0}
                      max={100000}
                      value={activityRetention[severity].min_count}
                      aria-label={`${severity} ${t('admin.activityRetentionMinCount')}`}
                      onChange={(event) =>
                        updateRetentionRule(
                          severity,
                          'min_count',
                          Number.parseInt(event.target.value, 10) || 0,
                        )
                      }
                      className="font-mono text-sm"
                    />
                    <InputGroupAddon align="inline-end" className="text-xs">
                      {t('admin.retentionMinUnit')}
                    </InputGroupAddon>
                  </InputGroup>
                </div>
              </div>
            );
          })}
        </SettingsSectionCard>

        {/* Form Save Bar */}
        <div
          className={cn(
            'sticky bottom-4 z-20 flex flex-wrap items-center justify-between gap-3 rounded-xl border px-4 py-3 transition-colors sm:px-5',
            isDirty
              ? 'border-primary/30 bg-card/95 shadow-md backdrop-blur-md'
              : 'border-border/40 bg-card/50 shadow-2xs backdrop-blur-sm',
          )}
        >
          <div className="flex items-center gap-2 text-xs">
            <AnimatePresence mode="wait">
              {saved ? (
                <motion.span
                  key="saved"
                  initial={{ opacity: 0, y: 4 }}
                  animate={{ opacity: 1, y: 0 }}
                  exit={{ opacity: 0, y: -4 }}
                  className="inline-flex items-center gap-1.5 font-medium text-emerald-600 dark:text-emerald-400"
                >
                  <span className="size-2 rounded-full bg-emerald-500" />
                  {t('admin.configSaved')}
                </motion.span>
              ) : isDirty ? (
                <motion.span
                  key="dirty"
                  initial={{ opacity: 0, y: 4 }}
                  animate={{ opacity: 1, y: 0 }}
                  exit={{ opacity: 0, y: -4 }}
                  className="inline-flex items-center gap-1.5 font-medium text-amber-600 dark:text-amber-400"
                >
                  <span className="size-2 rounded-full bg-amber-500" />
                  {t('admin.unsavedChangesHint')}
                </motion.span>
              ) : (
                <motion.span
                  key="clean"
                  initial={{ opacity: 0 }}
                  animate={{ opacity: 1 }}
                  exit={{ opacity: 0 }}
                  className="text-muted-foreground"
                >
                  {t('admin.configDescription')}
                </motion.span>
              )}
            </AnimatePresence>
          </div>

          <div className="flex items-center gap-2">
            {isDirty ? (
              <Button
                type="button"
                variant="ghost"
                size="sm"
                onClick={resetDraft}
                disabled={checking || updateConfig.isPending}
              >
                <RotateCcw data-icon="inline-start" />
                {t('admin.discardChanges')}
              </Button>
            ) : null}
            <Button
              type="submit"
              size="sm"
              disabled={!isDirty || updateConfig.isPending || checking || editingIndex !== null}
            >
              {checking || updateConfig.isPending ? (
                <Spinner data-icon="inline-start" />
              ) : (
                <Save data-icon="inline-start" />
              )}
              {checking
                ? t('common.checking')
                : updateConfig.isPending
                  ? t('common.saving')
                  : t('common.save')}
            </Button>
          </div>
        </div>
      </form>

      <AlertDialog
        open={showConfirm}
        onOpenChange={(open) => {
          setShowConfirm(open);
          if (!open) {
            setAffectedTunnels([]);
            setPendingServerAddr(null);
          }
        }}
      >
        <AlertDialogContent className="!max-w-lg w-[calc(100vw-2rem)]">
          <AlertDialogHeader>
            <AlertDialogMedia className="bg-amber-500/10">
              <AlertTriangle className="text-amber-500" />
            </AlertDialogMedia>
            <AlertDialogTitle>{t('admin.affectedPortsTitle')}</AlertDialogTitle>
            <AlertDialogDescription>
              <Trans
                i18nKey="admin.affectedPortsDescription"
                values={{ count: affectedTunnels.length }}
                components={{ strong: <span className="font-semibold text-foreground" /> }}
              />
            </AlertDialogDescription>
          </AlertDialogHeader>

          <div className="max-h-60 overflow-auto rounded-lg border border-border/60 bg-muted/20">
            <table className="w-full min-w-[400px] text-sm">
              <thead>
                <tr className="border-b border-border/40 text-muted-foreground">
                  <th className="px-3 py-2 text-left font-medium">{t('admin.node')}</th>
                  <th className="px-3 py-2 text-left font-medium">{t('tunnels.tunnel')}</th>
                  <th className="px-3 py-2 text-right font-medium">{t('tunnels.publicPort')}</th>
                  <th className="px-3 py-2 text-right font-medium">{t('tunnels.status')}</th>
                </tr>
              </thead>
              <tbody>
                {affectedTunnels.map((tunnel, index) => (
                  <tr key={index} className="border-b border-border/20 last:border-0">
                    <td className="px-3 py-2 font-mono text-xs">
                      {tunnel.display_name || tunnel.hostname || tunnel.client_id.slice(0, 8)}
                    </td>
                    <td className="px-3 py-2">{tunnel.tunnel_name}</td>
                    <td className="px-3 py-2 text-right font-mono">{tunnel.remote_port}</td>
                    <td className="px-3 py-2 text-right">
                      <span
                        className={`inline-flex items-center rounded px-1.5 py-0.5 text-xs font-medium ${
                          affectedTunnelStatus(tunnel).key === 'exposed'
                            ? 'bg-emerald-500/10 text-emerald-600'
                            : affectedTunnelStatus(tunnel).key === 'stopped'
                              ? 'bg-amber-500/10 text-amber-600'
                              : affectedTunnelStatus(tunnel).key === 'error'
                                ? 'bg-destructive/10 text-destructive'
                                : 'bg-zinc-500/10 text-zinc-500'
                        }`}
                      >
                        {affectedTunnelStatus(tunnel).label}
                      </span>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>

          <AlertDialogFooter>
            <AlertDialogCancel>{t('admin.returnToEdit')}</AlertDialogCancel>
            <AlertDialogAction
              variant="destructive"
              onClick={() => doSave(pendingServerAddr ?? undefined)}
              disabled={updateConfig.isPending}
            >
              <AlertTriangle className="mr-1.5 size-3.5" />
              {updateConfig.isPending ? t('admin.forceSaving') : t('admin.forceSave')}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </>
  );
}
