import { useConnectionStore } from '@/stores/connection-store';

/** SSE 连接状态指示灯 — 从 connection-store 读取真实状态 */
export function ConnectionIndicator() {
  const status = useConnectionStore((s) => s.status);

  const config = {
    connected: {
      color: 'bg-emerald-500',
      ping: 'bg-emerald-400',
      label: '已连接',
    },
    reconnecting: {
      color: 'bg-amber-500',
      ping: 'bg-amber-400',
      label: '重连中…',
    },
    disconnected: {
      color: 'bg-muted-foreground/50',
      ping: '',
      label: '未连接',
    },
  }[status];

  return (
    <div className="flex items-center gap-1.5 ml-2" title={config.label}>
      <span className="relative flex h-2 w-2">
        {config.ping && (
          <span className={`animate-ping absolute inline-flex h-full w-full rounded-full ${config.ping} opacity-75`} />
        )}
        <span className={`relative inline-flex rounded-full h-2 w-2 ${config.color}`} />
      </span>
      <span className="text-[10px] text-muted-foreground hidden sm:inline">{config.label}</span>
    </div>
  );
}
