import { LayoutDashboard } from 'lucide-react';

export function OverviewHeader() {
  return (
    <div className="flex flex-col gap-2">
      <h1 className="text-2xl font-bold tracking-tight text-foreground flex items-center gap-2">
        <div className="p-2.5 bg-primary/10 rounded-xl border border-primary/20">
          <LayoutDashboard className="h-6 w-6 text-primary" />
        </div>
        全局系统监控
      </h1>
      <p className="text-muted-foreground text-sm flex items-center gap-2">
        实时查看服务端运行状态、Agent 连接分布以及所有网络隧道的健康状况。
      </p>
    </div>
  );
}
