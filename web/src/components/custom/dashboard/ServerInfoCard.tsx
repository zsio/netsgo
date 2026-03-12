import { Server as ServerIcon, HardDrive, Clock, Activity } from 'lucide-react';
import { useServerStatus } from '@/hooks/use-server-status';
import { Skeleton } from '@/components/ui/skeleton';
import { formatUptime } from '@/lib/format';

export function ServerInfoCard() {
  const { data: status, isLoading } = useServerStatus();

  if (isLoading) {
    return <Skeleton className="h-32 rounded-xl" />;
  }

  return (
    <div className="rounded-xl border border-border/40 bg-card/50 backdrop-blur-sm shadow-sm overflow-hidden">
      <div className="px-6 py-4 border-b border-border/40 bg-muted/20 flex items-center gap-2">
        <ServerIcon className="h-5 w-5 text-primary" />
        <h3 className="font-semibold text-foreground">服务端信息</h3>
      </div>
      <div className="grid grid-cols-1 md:grid-cols-4 divide-y md:divide-y-0 md:divide-x divide-border/40">
        <div className="p-6 flex flex-col gap-1">
          <span className="text-sm text-muted-foreground flex items-center gap-1.5"><Activity className="w-4 h-4" />运行状态</span>
          <span className="font-medium flex items-center gap-2">
            <div className="w-2 h-2 rounded-full bg-emerald-500" />
            {status?.status === 'running' ? '正常运行' : '未知'}
          </span>
        </div>
        <div className="p-6 flex flex-col gap-1">
          <span className="text-sm text-muted-foreground flex items-center gap-1.5"><Clock className="w-4 h-4" />已运行时间</span>
          <span className="font-medium">{formatUptime(status?.uptime ?? 0)}</span>
        </div>
        <div className="p-6 flex flex-col gap-1">
          <span className="text-sm text-muted-foreground flex items-center gap-1.5"><ServerIcon className="w-4 h-4" />监听端口</span>
          <span className="font-medium">{status?.listen_port}</span>
        </div>
        <div className="p-6 flex flex-col gap-1">
          <span className="text-sm text-muted-foreground flex items-center gap-1.5"><HardDrive className="w-4 h-4" />数据存储路径</span>
          <span className="font-medium text-sm break-all">{status?.store_path}</span>
        </div>
      </div>
    </div>
  );
}
