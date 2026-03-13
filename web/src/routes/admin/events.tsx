import { useState, useMemo } from 'react';
import { createRoute } from '@tanstack/react-router';
import { adminRoute } from '../admin';
import { useAdminEvents } from '@/hooks/use-admin-events';
import { Skeleton } from '@/components/ui/skeleton';
import { Input } from '@/components/ui/input';
import { Search } from 'lucide-react';

export const adminEventsRoute = createRoute({
  getParentRoute: () => adminRoute,
  path: '/events',
  component: AdminEventsPage,
});

function AdminEventsPage() {
  const { data: events = [], isLoading } = useAdminEvents();
  const [typeFilter, setTypeFilter] = useState('');
  const [search, setSearch] = useState('');

  const eventTypes = useMemo(() => {
    const types = new Set(events.map((e) => e.type));
    return Array.from(types).sort();
  }, [events]);

  const filteredEvents = useMemo(() => {
    return events.filter((evt) => {
      if (typeFilter && evt.type !== typeFilter) return false;
      if (search) {
        const q = search.toLowerCase();
        return (
          evt.type.toLowerCase().includes(q) ||
          evt.data.toLowerCase().includes(q)
        );
      }
      return true;
    });
  }, [events, typeFilter, search]);

  return (
    <div className="flex flex-col gap-6 w-full">
      <div className="flex items-center justify-between shrink-0">
        <h2 className="text-2xl font-bold tracking-tight">审计与系统事件时间线</h2>
      </div>

      {/* 过滤 */}
      <div className="flex items-center gap-3 shrink-0">
        <select
          value={typeFilter}
          onChange={(e) => setTypeFilter(e.target.value)}
          className="flex h-9 rounded-md border border-input bg-transparent px-3 py-1 text-sm shadow-sm transition-colors focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring w-48"
        >
          <option value="">全部类型</option>
          {eventTypes.map((type) => (
            <option key={type} value={type}>{type}</option>
          ))}
        </select>
        <div className="relative flex-1">
          <Search className="absolute left-3 top-1/2 -translate-y-1/2 h-4 w-4 text-muted-foreground" />
          <Input
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            placeholder="搜索事件内容..."
            className="pl-9"
          />
        </div>
      </div>

      <div className="rounded-xl border border-border/40 bg-card/50 backdrop-blur-sm p-6 overflow-hidden flex flex-col">
        <div className="w-full">
          {isLoading ? (
            <div className="space-y-4">
              <Skeleton className="h-16 w-full opacity-50" />
              <Skeleton className="h-16 w-full opacity-30" />
              <Skeleton className="h-16 w-full opacity-10" />
            </div>
          ) : filteredEvents.length === 0 ? (
            <div className="h-full flex items-center justify-center text-muted-foreground">
              {events.length > 0 ? '没有匹配的事件' : '暂无事件记录'}
            </div>
          ) : (
            <div className="relative border-l border-border/50 ml-3 space-y-6 pb-4">
              {filteredEvents.map((evt) => (
                <div key={evt.id} className="relative pl-6">
                  {/* Timeline dot */}
                  <div className="absolute top-1.5 -left-1.5 w-3 h-3 bg-card border-2 border-primary rounded-full z-10" />
                  
                  <div className="flex flex-col gap-1">
                    <div className="flex items-center gap-2">
                      <span className="text-sm font-medium text-foreground">{evt.type}</span>
                      <span className="text-xs text-muted-foreground font-mono bg-muted/50 px-1.5 py-0.5 rounded">
                        {new Date(evt.timestamp).toLocaleString()}
                      </span>
                    </div>
                    <div className="text-sm text-foreground bg-muted/20 p-3 rounded-md border border-border/30 break-words font-mono">
                      {evt.data}
                    </div>
                  </div>
                </div>
              ))}
            </div>
          )}
        </div>
      </div>
    </div>
  );
}
