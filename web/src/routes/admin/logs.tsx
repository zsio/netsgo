import { createRoute } from '@tanstack/react-router';
import { adminRoute } from '../admin';
import { useAdminLogs } from '@/hooks/use-admin-logs';
import { Skeleton } from '@/components/ui/skeleton';

export const adminLogsRoute = createRoute({
  getParentRoute: () => adminRoute,
  path: '/logs',
  component: AdminLogsPage,
});

function AdminLogsPage() {
  const { data: logs = [], isLoading } = useAdminLogs();

  return (
    <div className="flex flex-col gap-6 max-w-5xl mx-auto h-[calc(100vh-140px)]">
      <div className="flex items-center justify-between shrink-0">
        <h2 className="text-2xl font-bold tracking-tight">系统日志</h2>
      </div>

      <div className="rounded-xl border border-border/40 bg-card/50 backdrop-blur-sm overflow-hidden flex-1 flex flex-col min-h-0">
        <div className="flex-1 overflow-y-auto">
          <table className="w-full text-sm text-left">
            <thead className="text-xs text-muted-foreground bg-muted/50 uppercase sticky top-0 z-10 shadow-sm backdrop-blur-md">
              <tr>
                <th className="px-6 py-3 font-medium w-48">时间</th>
                <th className="px-6 py-3 font-medium w-24">级别</th>
                <th className="px-6 py-3 font-medium w-32">模块</th>
                <th className="px-6 py-3 font-medium">详情</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-border/40">
              {isLoading ? (
                <tr><td colSpan={4} className="p-4"><Skeleton className="h-64 w-full" /></td></tr>
              ) : logs.length === 0 ? (
                <tr><td colSpan={4} className="px-6 py-8 text-center text-muted-foreground">暂无日志</td></tr>
              ) : (
                logs.map(log => (
                  <tr key={log.id} className="hover:bg-muted/30">
                    <td className="px-6 py-3 text-muted-foreground tabular-nums">{new Date(log.timestamp).toLocaleString()}</td>
                    <td className="px-6 py-3">
                      <span className={`px-2 py-1 rounded-md text-xs font-medium ${
                        log.level === 'ERROR' ? 'bg-destructive/10 text-destructive' :
                        log.level === 'WARN' ? 'bg-amber-500/10 text-amber-500' :
                        'bg-primary/10 text-primary'
                      }`}>
                        {log.level}
                      </span>
                    </td>
                    <td className="px-6 py-3 text-muted-foreground font-mono text-xs">{log.source}</td>
                    <td className="px-6 py-3 font-medium">{log.message}</td>
                  </tr>
                ))
              )}
            </tbody>
          </table>
        </div>
      </div>
    </div>
  );
}
