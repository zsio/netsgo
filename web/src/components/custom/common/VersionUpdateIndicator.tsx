import { CircleAlert } from 'lucide-react';
import { useVersionCheck } from '@/hooks/use-version-check';
import { Button } from '@/components/ui/button';
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
  version?: string;
  label?: string;
}

export function VersionUpdateIndicator({ version, label = '运行版本' }: VersionUpdateIndicatorProps) {
  const { data } = useVersionCheck(version);
  const latestVersion = data?.latest_version;
  const installMethod = data?.install_method || 'cli';

  if (!version || !data?.update_available) return null;

  const isDocker = installMethod === 'docker';
  const isService = installMethod === 'service';
  const releaseHref = 'https://github.com/zsio/netsgo/releases';

  return (
    <Dialog>
      <DialogTrigger asChild>
        <button
          type="button"
          className="inline-flex shrink-0 items-center justify-center text-amber-500 transition-colors hover:text-amber-600 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/50"
          aria-label={`${label}可更新到 ${latestVersion}`}
        >
          <CircleAlert className="size-4" />
        </button>
      </DialogTrigger>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>发现可用更新</DialogTitle>
          <DialogDescription>
            {label}可以从 v{version} 更新到 {latestVersion}。
          </DialogDescription>
        </DialogHeader>
        <div className="grid gap-2 rounded-lg border bg-muted/30 p-3 text-sm">
          <div className="flex items-center justify-between gap-3">
            <span className="text-muted-foreground">当前版本</span>
            <span className="font-mono text-foreground">v{version}</span>
          </div>
          <div className="flex items-center justify-between gap-3">
            <span className="text-muted-foreground">最新版本</span>
            <span className="font-mono text-foreground">{latestVersion}</span>
          </div>
          <div className="flex items-center justify-between gap-3">
            <span className="text-muted-foreground">运行方式</span>
            <span className="text-foreground">{isDocker ? 'Docker 镜像' : isService ? '系统服务' : 'CLI / 二进制'}</span>
          </div>
        </div>
        {isDocker ? (
          <div className="grid gap-2 text-sm text-muted-foreground">
            <p>您当前使用 Docker 镜像运行，请前往以下制品页面查看最新镜像。</p>
            <div className="grid gap-2">
              <a
                className="rounded-md bg-muted px-3 py-2 text-sm text-foreground underline-offset-4 hover:underline"
                href="https://cnb.cool/zsio/netsgo/-/packages/docker/netsgo"
                target="_blank"
                rel="noreferrer"
              >
                CNB 制品库
              </a>
              <a
                className="rounded-md bg-muted px-3 py-2 text-sm text-foreground underline-offset-4 hover:underline"
                href="https://hub.docker.com/r/zsio/netsgo"
                target="_blank"
                rel="noreferrer"
              >
                Docker Hub
              </a>
            </div>
          </div>
        ) : isService ? (
          null
        ) : (
          <div className="grid gap-2 text-sm text-muted-foreground">
            <p>您当前使用二进制直接运行，请您前往 GitHub 下载最新二进制文件进行更新。</p>
          </div>
        )}
        <DialogFooter>
          {isService && (
            <Button type="button">
              开始更新
            </Button>
          )}
          <Button type="button" variant="outline" asChild>
            <a href={releaseHref} target="_blank" rel="noreferrer">
              查看 Release
            </a>
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
