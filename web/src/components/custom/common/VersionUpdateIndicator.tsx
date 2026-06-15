import { CircleAlert, RefreshCw } from 'lucide-react';
import toast from 'react-hot-toast';
import { useTranslation } from 'react-i18next';
import {
  useForceVersionCheck,
  useVersionCheck,
  type VersionCheckTarget,
} from '@/hooks/use-version-check';
import type { VersionCheckResult } from '@/types';
import { Button } from '@/components/ui/button';
import { cn } from '@/lib/utils';
import { CopyButton } from './CopyButton';
import { manualVersionCheckToast } from './version-update-toast';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from '@/components/ui/dialog';
import { safeReleaseUrl, safeUpgradeCommand } from './version-update-security';

interface VersionUpdateIndicatorProps {
  target: VersionCheckTarget;
  label?: string;
}

function displayVersion(version?: string) {
  return version || '-';
}

function targetInstruction(kind: 'server' | 'client', t: ReturnType<typeof useTranslation>['t']) {
  if (kind === 'client') {
    return t('updates.clientInstruction');
  }
  return t('updates.serverInstruction');
}

export function VersionUpdateContent({
  data,
  target,
}: {
  data: VersionCheckResult;
  target: VersionCheckTarget;
}) {
  const { t } = useTranslation();
  const releaseHref = safeReleaseUrl(data.release_url);
  const upgradeCommand = safeUpgradeCommand(data.commands?.command);
  const isDocker = data.install_method === 'docker';
  const isService = data.install_method === 'service';

  return (
    <>
      <div className="grid gap-2 rounded-md border bg-muted/30 p-3 text-sm">
        <div className="flex items-center justify-between gap-3">
          <span className="text-muted-foreground">{t('updates.currentVersion')}</span>
          <span className="font-mono text-foreground">{displayVersion(data.current_version || target.version)}</span>
        </div>
        <div className="flex items-center justify-between gap-3">
          <span className="text-muted-foreground">{t('updates.latestVersion')}</span>
          <span className="font-mono text-foreground">{data.latest_version}</span>
        </div>
        <div className="flex items-center justify-between gap-3">
          <span className="text-muted-foreground">{t('updates.recommendedChannel')}</span>
          <span className="text-foreground">{data.recommended_channel || '-'}</span>
        </div>
      </div>
      {isService && upgradeCommand ? (
        <div className="grid gap-3 text-sm">
          <p className="text-muted-foreground">{targetInstruction(target.kind, t)}</p>
          <div className="flex items-start gap-2 rounded-md bg-muted p-2">
            <code className="min-w-0 flex-1 break-all text-xs text-foreground">{upgradeCommand}</code>
            <CopyButton
              value={upgradeCommand}
              title={t('updates.copyUpgradeCommand')}
              className="inline-flex size-6 items-center justify-center rounded-[min(var(--radius-md),10px)] transition-colors hover:bg-background/70"
            />
          </div>
        </div>
      ) : isDocker ? (
        <p className="text-sm text-muted-foreground">{t('updates.dockerManual')}</p>
      ) : (
        <p className="text-sm text-muted-foreground">{t('updates.binaryManual')}</p>
      )}
      <DialogFooter>
        <Button type="button" variant="outline" asChild>
          <a href={releaseHref} target="_blank" rel="noreferrer">
            GitHub Releases
          </a>
        </Button>
      </DialogFooter>
    </>
  );
}

export function VersionUpdateIndicator({ target, label }: VersionUpdateIndicatorProps) {
  const { t } = useTranslation();
  const displayLabel = label ?? t('updates.runtimeVersion');
  const check = useVersionCheck(target);
  const forceCheck = useForceVersionCheck(target);
  const data = forceCheck.data || check.data;
  const hasUpdate = Boolean(data?.update_available);
  const manualFailed = Boolean(forceCheck.data?.check_failed || forceCheck.error);

  if (!target.version || target.enabled === false) return null;

  const handleManualCheck = () => {
    forceCheck.mutate(undefined, {
      onSuccess: (result) => {
        const toastKind = manualVersionCheckToast(result);
        if (toastKind === 'error') {
          toast.error(t('updates.checkFailed'));
          return;
        }
        if (toastKind === 'success') toast.success(t('updates.upToDate'));
      },
      onError: () => {
        if (manualVersionCheckToast(null, true) === 'error') toast.error(t('updates.checkFailed'));
      },
    });
  };

  if (!hasUpdate) {
    return (
      <Button
        type="button"
        variant="ghost"
        size="icon-xs"
        title={manualFailed ? t('updates.checkFailedTitle') : t('updates.checkUpdate')}
        disabled={forceCheck.isPending}
        onClick={handleManualCheck}
        className={cn(
          'size-4 opacity-0 transition-opacity hover:opacity-100 focus-visible:opacity-100 group-hover/version-update:opacity-100',
          forceCheck.isPending && 'opacity-100',
        )}
      >
        <RefreshCw className={cn('size-3', forceCheck.isPending && 'animate-spin')} />
      </Button>
    );
  }

  return (
    <Dialog>
      <DialogTrigger asChild>
        <button
          type="button"
          className="inline-flex size-4 shrink-0 items-center justify-center text-amber-500 transition-colors hover:text-amber-600 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/50"
          aria-label={t('updates.availableLabel', { label: displayLabel, version: data?.latest_version })}
        >
          <CircleAlert className="size-3.5" />
        </button>
      </DialogTrigger>
      <DialogContent>
        {data && (
          <>
            <DialogHeader>
              <DialogTitle>{t('updates.availableTitle')}</DialogTitle>
              <DialogDescription>
                {t('updates.availableDescription', {
                  current: displayVersion(data.current_version || target.version),
                  latest: data.latest_version,
                })}
              </DialogDescription>
            </DialogHeader>
            <VersionUpdateContent data={data} target={target} />
          </>
        )}
      </DialogContent>
    </Dialog>
  );
}
