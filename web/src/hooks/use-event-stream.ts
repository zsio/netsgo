import { useEffect } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { useRouterState } from '@tanstack/react-router';
import { useConnectionStore } from '@/stores/connection-store';
import { useAuthStore } from '@/stores/auth-store';
import type { Agent } from '@/types';

function applyEvent(queryClient: ReturnType<typeof useQueryClient>, eventType: string, data: string) {
  switch (eventType) {
    case 'stats_update': {
      try {
        const parsed = JSON.parse(data) as { agent_id: string; stats: Agent['stats'] };
        queryClient.setQueryData<Agent[]>(['agents'], (old) =>
          old?.map((agent) => (agent.id === parsed.agent_id ? { ...agent, stats: parsed.stats } : agent)),
        );
      } catch {
        // ignore malformed events
      }
      return;
    }
    case 'agent_online':
    case 'agent_offline':
    case 'tunnel_changed':
      queryClient.invalidateQueries({ queryKey: ['agents'] });
      return;
    default:
      return;
  }
}

function parseSSE(buffer: string, onEvent: (eventType: string, data: string) => void): string {
  let remaining = buffer;

  while (true) {
    const delimiterIndex = remaining.indexOf('\n\n');
    if (delimiterIndex === -1) {
      break;
    }

    const rawEvent = remaining.slice(0, delimiterIndex);
    remaining = remaining.slice(delimiterIndex + 2);

    let eventType = 'message';
    const dataLines: string[] = [];

    for (const line of rawEvent.split('\n')) {
      if (line.startsWith('event:')) {
        eventType = line.slice(6).trim();
      }
      if (line.startsWith('data:')) {
        dataLines.push(line.slice(5).trim());
      }
    }

    if (dataLines.length > 0) {
      onEvent(eventType, dataLines.join('\n'));
    }
  }

  return remaining;
}

export function useEventStream() {
  const queryClient = useQueryClient();
  const setStatus = useConnectionStore((state) => state.setStatus);
  const token = useAuthStore((state) => state.token);
  const pathname = useRouterState({ select: (state) => state.location.pathname });
  const shouldConnect = Boolean(token) && pathname !== '/setup' && pathname !== '/login';

  useEffect(() => {
    if (!shouldConnect || !token) {
      setStatus('disconnected');
      return;
    }

    let cancelled = false;
    let activeController: AbortController | null = null;
    let hasConnected = false;

    const connect = async () => {
      while (!cancelled) {
        activeController = new AbortController();

        try {
          setStatus(hasConnected ? 'reconnecting' : 'connecting');

          const response = await fetch('/api/events', {
            method: 'GET',
            headers: {
              Accept: 'text/event-stream',
              Authorization: `Bearer ${token}`,
            },
            signal: activeController.signal,
          });

          if (response.status === 401) {
            useAuthStore.getState().logout();
            window.location.hash = '#/login';
            setStatus('disconnected');
            return;
          }

          if (!response.ok || !response.body) {
            throw new Error(`event stream failed: ${response.status}`);
          }

          hasConnected = true;
          setStatus('connected');

          const reader = response.body.getReader();
          const decoder = new TextDecoder();
          let buffer = '';

          while (!cancelled) {
            const { done, value } = await reader.read();
            if (done) {
              throw new Error('event stream closed');
            }

            buffer += decoder.decode(value, { stream: true });
            buffer = parseSSE(buffer, (eventType, data) => applyEvent(queryClient, eventType, data));
          }
        } catch (error) {
          if (cancelled) {
            return;
          }
          if (error instanceof DOMException && error.name === 'AbortError') {
            return;
          }

          setStatus(hasConnected ? 'reconnecting' : 'connecting');
          await new Promise((resolve) => window.setTimeout(resolve, 1000));
        }
      }
    };

    void connect();

    return () => {
      cancelled = true;
      activeController?.abort();
      setStatus('disconnected');
    };
  }, [queryClient, setStatus, shouldConnect, token]);
}
