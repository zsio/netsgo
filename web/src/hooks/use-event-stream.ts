import { useEffect } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { useConnectionStore } from '@/stores/connection-store';
import type { Agent } from '@/types';

/**
 * SSE 事件流 Hook — 连接 /api/events 并将推送写入 TanStack Query 缓存
 * 应在应用根组件中调用一次
 */
export function useEventStream() {
  const queryClient = useQueryClient();
  const setStatus = useConnectionStore((s) => s.setStatus);

  useEffect(() => {
    const es = new EventSource('/api/events');

    es.onopen = () => {
      setStatus('connected');
    };

    es.onerror = () => {
      // EventSource 会自动重连，我们只需更新状态
      setStatus('reconnecting');
    };

    es.addEventListener('stats_update', (e) => {
      try {
        const { agent_id, stats } = JSON.parse(e.data);
        queryClient.setQueryData<Agent[]>(['agents'], (old) =>
          old?.map((a) => (a.id === agent_id ? { ...a, stats } : a)),
        );
      } catch { /* ignore malformed events */ }
    });

    es.addEventListener('agent_online', () => {
      queryClient.invalidateQueries({ queryKey: ['agents'] });
    });

    es.addEventListener('agent_offline', () => {
      queryClient.invalidateQueries({ queryKey: ['agents'] });
    });

    es.addEventListener('tunnel_changed', () => {
      queryClient.invalidateQueries({ queryKey: ['agents'] });
    });

    return () => {
      es.close();
      setStatus('disconnected');
    };
  }, [queryClient, setStatus]);
}
