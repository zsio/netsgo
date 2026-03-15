import { Activity } from 'lucide-react';

export function ClientEmptyState() {
  return (
    <div className="flex-1 flex flex-col items-center justify-center text-muted-foreground">
      <Activity className="h-16 w-16 mb-4 opacity-20" />
      <p className="text-lg font-medium">请选择一个节点进行管控</p>
      <p className="text-sm opacity-60 mt-2">支持查看统计指标、配置内网穿透隧道及下发终端指令</p>
    </div>
  );
}
