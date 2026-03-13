import type { QueryClient } from '@tanstack/react-query';
import type { Agent } from '@/types';

const AGENTS_CACHE_KEY = 'netsgo:agents-cache:v1';

interface AgentsCachePayload {
  agents: Agent[];
  updatedAt: number;
}

function hasStorage() {
  return typeof window !== 'undefined' && typeof window.localStorage !== 'undefined';
}

export function readAgentsCache(): AgentsCachePayload | undefined {
  if (!hasStorage()) {
    return undefined;
  }

  try {
    const raw = window.localStorage.getItem(AGENTS_CACHE_KEY);
    if (!raw) {
      return undefined;
    }

    const parsed = JSON.parse(raw) as Partial<AgentsCachePayload>;
    if (!Array.isArray(parsed.agents) || typeof parsed.updatedAt !== 'number') {
      return undefined;
    }

    return {
      agents: parsed.agents as Agent[],
      updatedAt: parsed.updatedAt,
    };
  } catch {
    return undefined;
  }
}

export function writeAgentsCache(agents: Agent[]) {
  if (!hasStorage()) {
    return;
  }

  try {
    const payload: AgentsCachePayload = {
      agents,
      updatedAt: Date.now(),
    };

    window.localStorage.setItem(AGENTS_CACHE_KEY, JSON.stringify(payload));
  } catch {
    // ignore storage errors
  }
}

export function persistAgentsQueryCache(queryClient: QueryClient) {
  const agents = queryClient.getQueryData<Agent[]>(['agents']);
  if (agents) {
    writeAgentsCache(agents);
  }
}
