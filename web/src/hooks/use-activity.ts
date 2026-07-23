import { useInfiniteQuery } from '@tanstack/react-query';
import type { InfiniteData, QueryClient, QueryKey } from '@tanstack/react-query';

import { activityApi } from '@/lib/api';
import type {
  ActivityCategory,
  ActivityItem,
  ActivityPage,
  ActivityQuery,
  ActivitySeverity,
} from '@/types';

export interface NormalizedActivityQuery extends ActivityQuery {
  scope: 'global' | 'client' | 'tunnel';
  limit: number;
  severities: ActivitySeverity[];
  categories: ActivityCategory[];
}

function sortedUnique<T extends string>(values: T[] | undefined): T[] {
  return Array.from(new Set(values ?? [])).sort();
}

export function normalizeActivityQuery(query: ActivityQuery = {}): NormalizedActivityQuery {
  return {
    scope: query.scope ?? 'global',
    scopeId: query.scopeId,
    limit: Math.min(200, Math.max(1, query.limit ?? 50)),
    severities: sortedUnique(query.severities ?? ['info', 'warning', 'error']),
    categories: sortedUnique(query.categories),
    from: query.from,
    to: query.to,
  };
}

export function buildActivityQueryKey(query: ActivityQuery = {}) {
  const normalized = normalizeActivityQuery(query);
  return [
    'activity',
    normalized.scope,
    normalized.scopeId ?? null,
    normalized.limit,
    normalized.severities,
    normalized.categories,
    normalized.from ?? null,
    normalized.to ?? null,
  ] as const;
}

export function useActivity(query: ActivityQuery = {}) {
  const normalized = normalizeActivityQuery(query);
  const result = useInfiniteQuery({
    queryKey: buildActivityQueryKey(normalized),
    initialPageParam: undefined as number | undefined,
    queryFn: ({ pageParam }) => activityApi.list({ ...normalized, before: pageParam }),
    getNextPageParam: (page) => page.has_more ? page.next_cursor : undefined,
  });
  return {
    ...result,
    items: flattenActivityPages(result.data),
  };
}

export function flattenActivityPages(data: InfiniteData<ActivityPage> | undefined): ActivityItem[] {
  const byId = new Map<number, ActivityItem>();
  for (const page of data?.pages ?? []) {
    for (const item of page.items) byId.set(item.id, item);
  }
  return Array.from(byId.values()).sort((a, b) => b.id - a.id);
}

function activityQueryFromKey(queryKey: QueryKey): NormalizedActivityQuery | null {
  if (queryKey[0] !== 'activity' || typeof queryKey[1] !== 'string' || typeof queryKey[3] !== 'number') return null;
  return {
    scope: queryKey[1] as NormalizedActivityQuery['scope'],
    scopeId: typeof queryKey[2] === 'string' ? queryKey[2] : undefined,
    limit: queryKey[3],
    severities: Array.isArray(queryKey[4]) ? queryKey[4] as ActivitySeverity[] : [],
    categories: Array.isArray(queryKey[5]) ? queryKey[5] as ActivityCategory[] : [],
    from: typeof queryKey[6] === 'string' ? queryKey[6] : undefined,
    to: typeof queryKey[7] === 'string' ? queryKey[7] : undefined,
  };
}

export function activityMatchesQuery(item: ActivityItem, query: NormalizedActivityQuery) {
  if (query.scope === 'client' && !item.clients.some((subject) => subject.client_id === query.scopeId)) return false;
  if (query.scope === 'tunnel' && !item.tunnels.some((subject) => subject.tunnel_id === query.scopeId)) return false;
  if (query.severities.length > 0 && !query.severities.includes(item.severity)) return false;
  if (query.categories.length > 0 && !query.categories.includes(item.category)) return false;
  const occurredAt = Date.parse(item.occurred_at);
  if (query.from && occurredAt < Date.parse(query.from)) return false;
  if (query.to && occurredAt >= Date.parse(query.to)) return false;
  return true;
}

export function prependActivityToMatchingQueries(queryClient: QueryClient, item: ActivityItem) {
  for (const query of queryClient.getQueryCache().findAll({ queryKey: ['activity'] })) {
    const metadata = activityQueryFromKey(query.queryKey);
    if (!metadata || !activityMatchesQuery(item, metadata)) continue;
    queryClient.setQueryData<InfiniteData<ActivityPage>>(query.queryKey, (old) => {
      if (!old || old.pages.some((page) => page.items.some((entry) => entry.id === item.id))) return old;
      const pages = [...old.pages];
      const first = pages[0] ?? { items: [], has_more: false, direction: 'before' as const };
      pages[0] = { ...first, items: [item, ...first.items].sort((a, b) => b.id - a.id) };
      return { ...old, pages };
    });
  }
}
