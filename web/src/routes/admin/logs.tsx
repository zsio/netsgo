import { useState, useMemo } from 'react';
import { createRoute } from '@tanstack/react-router';
import { adminRoute } from '../admin';
import { useAdminLogs } from '@/hooks/use-admin-logs';
import { Skeleton } from '@/components/ui/skeleton';
import { Input } from '@/components/ui/input';
import { Search, RefreshCw } from 'lucide-react';

export const adminLogsRoute = createRoute({
  getParentRoute: () => adminRoute,
  path: '/logs',
  component: AdminLogsPage,
});

const LEVEL_OPTIONS = ['ALL', 'INFO', 'WARN', 'ERROR'] as const;

function AdminLogsPage() {
  const { data: logs = [], isLoading, isFetching } = useAdminLogs();
  const [levelFilter, setLevelFilter] = useState<string>('ALL');
  const [search, setSearch] = useState('');

  const filteredLogs = useMemo(() => {
    return logs.filter((log) => {
      if (levelFilter !== 'ALL' && log.level !== levelFilter) return false;
      if (search) {
        const q = search.toLowerCase();
        return (
          log.message.toLowerCase().includes(q) ||
          log.source.toLowerCase().includes(q)
        );
      }
      return true;
    });
  }, [logs, levelFilter, search]);

  return (
    <div className="flex flex-col gap-6 w-full">
      <div className="flex items-center justify-between shrink-0">
        <h2 className="text-2xl font-bold tracking-tight">系统日志</h2>
        <div className="flex items-center gap-2 text-xs text-muted-foreground">
          <RefreshCw className={`h-3.5 w-3.5 ${isFetching ? 'animate-spin text-primary' : ''}`} />
          <span>{isFetching ? '刷新中...' : '每 5s 自动刷新'}</span>
        </div>
      </div>

      {/* 过滤 */}
      <div className="flex items-center gap-3 shrink-0">
        <div className="flex rounded-md border border-border/50 overflow-hidden">
          {LEVEL_OPTIONS.map((level) => (
            <button
              key={level}
              type="button"
              onClick={() => setLevelFilter(level)}
              className={`px-3 py-1.5 text-xs font-medium transition-colors ${
                levelFilter === level
                  ? level === 'ERROR'
                    ? 'bg-destructive/10 text-destructive'
                    : level === 'WARN'
                      ? 'bg-amber-500/10 text-amber-500'
                      : level === 'INFO'
                        ? 'bg-primary/10 text-primary'
                        : 'bg-muted text-foreground'
                  : 'text-muted-foreground hover:bg-muted/50'
              }`}
            >
              {level}
            </button>
          ))}
        </div>
        <div className="relative flex-1">
          <Search className="absolute left-3 top-1/2 -translate-y-1/2 h-4 w-4 text-muted-foreground" />
          <Input
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            placeholder="搜索日志内容或模块..."
            className="pl-9"
          />
        </div>
      </div>

      <div className="rounded-xl border border-border/40 bg-card/50 backdrop-blur-sm overflow-hidden flex flex-col">
        <div className="w-full overflow-x-auto">
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
              ) : filteredLogs.length === 0 ? (
                <tr><td colSpan={4} className="px-6 py-8 text-center text-muted-foreground">
                  {logs.length > 0 ? '没有匹配的日志' : '暂无日志'}
                </td></tr>
              ) : (
                filteredLogs.map(log => (
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
