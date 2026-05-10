import { CircleAlert, Copy, RefreshCw } from 'lucide-react';
import toast from 'react-hot-toast';
import {
  useForceVersionCheck,
  useVersionCheck,
  type VersionCheckTarget,
} from '@/hooks/use-version-check';
import type { VersionCheckResult } from '@/types';
import { Button } from '@/components/ui/button';
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

interface VersionUpdateIndicatorProps {
  target: VersionCheckTarget;
  label?: string;
}

function displayVersion(version?: string) {
  return version || '-';
}

function targetInstruction(kind: 'server' | 'client') {
  if (kind === 'client') {
    return '请在该 client 所在机器上执行以下命令。不要在 server 机器上执行，除非 server 与该 client 本来就在同一台机器。该命令会下载并验证可信的 NetsGo release，然后升级并重启本机所有 NetsGo 托管服务。';
  }
  return '请在运行 NetsGo server 的机器上执行以下命令。该命令会下载并验证可信的 NetsGo release，然后升级并重启本机所有 NetsGo 托管服务。';
}

function copy(text: string) {
  void navigator.clipboard?.writeText(text);
}

export function VersionUpdateContent({
  data,
  target,
}: {
  data: VersionCheckResult;
  target: VersionCheckTarget;
}) {
  const releaseHref = data.release_url || 'https://github.com/zsio/netsgo/releases';
  const isDocker = data.install_method === 'docker';
  const isService = data.install_method === 'service';

  return (
    <>
      <div className="grid gap-2 rounded-md border bg-muted/30 p-3 text-sm">
        <div className="flex items-center justify-between gap-3">
          <span className="text-muted-foreground">当前版本</span>
          <span className="font-mono text-foreground">{displayVersion(data.current_version || target.version)}</span>
        </div>
        <div className="flex items-center justify-between gap-3">
          <span className="text-muted-foreground">最新版本</span>
          <span className="font-mono text-foreground">{data.latest_version}</span>
        </div>
        <div className="flex items-center justify-between gap-3">
          <span className="text-muted-foreground">推荐通道</span>
          <span className="text-foreground">{data.recommended_channel || '-'}</span>
        </div>
      </div>
      {isService && data.commands ? (
        <div className="grid gap-3 text-sm">
          <p className="text-muted-foreground">{targetInstruction(target.kind)}</p>
          {[
            ['国内源', data.commands.domestic],
            ['国外源', data.commands.global],
          ].map(([name, command]) => (
            <div key={name} className="grid gap-1.5">
              <div className="text-xs text-muted-foreground">{name}</div>
              <div className="flex items-start gap-2 rounded-md bg-muted p-2">
                <code className="min-w-0 flex-1 break-all text-xs text-foreground">{command}</code>
                <Button type="button" variant="ghost" size="icon-xs" onClick={() => copy(command)}>
                  <Copy className="size-3.5" />
                </Button>
              </div>
            </div>
          ))}
        </div>
      ) : isDocker ? (
        <p className="text-sm text-muted-foreground">当前目标以容器方式运行，请使用镜像发布页或部署文档手动更新。</p>
      ) : (
        <p className="text-sm text-muted-foreground">当前目标以二进制方式运行，请前往 GitHub Releases 手动下载更新。</p>
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

export function VersionUpdateIndicator({ target, label = '运行版本' }: VersionUpdateIndicatorProps) {
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
          toast.error('检查更新失败，请前往 GitHub Releases 手动确认');
          return;
        }
        if (toastKind === 'success') toast.success('已是最新版本');
      },
      onError: () => {
        if (manualVersionCheckToast(null, true) === 'error') toast.error('检查更新失败，请前往 GitHub Releases 手动确认');
      },
    });
  };

  if (!hasUpdate) {
    return (
      <Button
        type="button"
        variant="ghost"
        size="icon-xs"
        title={manualFailed ? '检查失败' : '检查更新'}
        disabled={forceCheck.isPending}
        onClick={handleManualCheck}
      >
        <RefreshCw className={`size-3.5 ${forceCheck.isPending ? 'animate-spin' : ''}`} />
      </Button>
    );
  }

  return (
    <Dialog>
      <DialogTrigger asChild>
        <button
          type="button"
          className="inline-flex shrink-0 items-center justify-center text-amber-500 transition-colors hover:text-amber-600 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/50"
          aria-label={`${label}可更新到 ${data?.latest_version}`}
        >
          <CircleAlert className="size-4" />
        </button>
      </DialogTrigger>
      <DialogContent>
        {data && (
          <>
            <DialogHeader>
              <DialogTitle>发现可用更新</DialogTitle>
              <DialogDescription>
                {label}可以从 {displayVersion(data.current_version || target.version)} 更新到 {data.latest_version}。
              </DialogDescription>
            </DialogHeader>
            <VersionUpdateContent data={data} target={target} />
          </>
        )}
      </DialogContent>
    </Dialog>
  );
}
