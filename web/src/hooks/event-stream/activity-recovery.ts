import type { QueryClient } from '@tanstack/react-query';
import { activityApi, type ActivityReadScope } from '@/lib/api';
import { activityReadScopeKey, prependActivityToMatchingQueries } from '@/hooks/use-activity';
import type { ActivityItem, ActivityPage } from '@/types';
import type { ActivityReadyPayload } from './guards';

export interface ActivityRecoveryState {
  lastScannedId?: number;
  targetId: number;
  hints: Map<number, ActivityItem>;
  running: boolean;
  retryTimer?: ReturnType<typeof setTimeout>;
  retryAttempt: number;
  cancelled: boolean;
}

const activityHintBufferLimit = 256;
const activityRecoveryRetryDelays = [1000, 2000, 5000, 10000] as const;

export function createActivityRecoveryState(): ActivityRecoveryState {
  return { targetId: 0, hints: new Map(), running: false, retryAttempt: 0, cancelled: false };
}

export function invalidateActivityQueries(queryClient: QueryClient, readScope: ActivityReadScope) {
  queryClient.invalidateQueries({ queryKey: ['users', activityReadScopeKey(readScope), 'activity'] });
}

function scheduleActivityRecoveryRetry(
  queryClient: QueryClient,
  readScope: ActivityReadScope,
  state: ActivityRecoveryState,
) {
  if (state.cancelled || state.retryTimer) return;
  const delay = activityRecoveryRetryDelays[Math.min(state.retryAttempt, activityRecoveryRetryDelays.length - 1)];
  state.retryAttempt += 1;
  state.retryTimer = setTimeout(() => {
    state.retryTimer = undefined;
    void recoverActivityGap(queryClient, readScope, state);
  }, delay);
}

export async function recoverActivityGap(
  queryClient: QueryClient,
  readScope: ActivityReadScope,
  state: ActivityRecoveryState,
) {
  if (state.cancelled || state.running || state.lastScannedId === undefined || state.targetId <= state.lastScannedId) return;
  state.running = true;
  try {
    while (!state.cancelled && state.lastScannedId !== undefined && state.targetId > state.lastScannedId) {
      const targetAtStart = state.targetId;
      const page: ActivityPage = await activityApi.recovery(readScope, state.lastScannedId);
      for (const item of [...page.items].reverse()) {
        prependActivityToMatchingQueries(queryClient, readScope, item);
        state.hints.delete(item.id);
      }
      if (page.next_cursor && page.next_cursor > state.lastScannedId) {
        state.lastScannedId = page.next_cursor;
      } else if (!page.has_more) {
        state.lastScannedId = targetAtStart;
      } else {
        throw new Error('activity recovery cursor did not advance');
      }
      if (!page.has_more && state.lastScannedId < targetAtStart) state.lastScannedId = targetAtStart;
    }
    state.retryAttempt = 0;
    for (const [id, hint] of state.hints) {
      if (state.lastScannedId !== undefined && id <= state.lastScannedId) {
        prependActivityToMatchingQueries(queryClient, readScope, hint);
        state.hints.delete(id);
      }
    }
  } catch (error) {
    console.warn('Failed to recover activity gap:', error);
    scheduleActivityRecoveryRetry(queryClient, readScope, state);
  } finally {
    state.running = false;
    if (!state.cancelled && !state.retryTimer && state.lastScannedId !== undefined && state.targetId > state.lastScannedId) {
      void recoverActivityGap(queryClient, readScope, state);
    }
  }
}

export function applyActivityReady(
  queryClient: QueryClient,
  readScope: ActivityReadScope,
  state: ActivityRecoveryState,
  ready: ActivityReadyPayload,
) {
  if (state.lastScannedId === undefined) {
    state.lastScannedId = ready.activity_cursor;
    state.targetId = Math.max(state.targetId, ready.activity_cursor);
    for (const id of state.hints.keys()) if (id <= ready.activity_cursor) state.hints.delete(id);
    invalidateActivityQueries(queryClient, readScope);
    return;
  }
  state.targetId = Math.max(state.targetId, ready.activity_cursor);
  void recoverActivityGap(queryClient, readScope, state);
}

export function applyActivityHint(
  queryClient: QueryClient,
  readScope: ActivityReadScope,
  state: ActivityRecoveryState,
  item: ActivityItem,
) {
  prependActivityToMatchingQueries(queryClient, readScope, item);
  state.targetId = Math.max(state.targetId, item.id);
  if (state.hints.size >= activityHintBufferLimit && !state.hints.has(item.id)) {
    state.hints.clear();
    invalidateActivityQueries(queryClient, readScope);
  } else {
    state.hints.set(item.id, item);
  }
  if (state.retryTimer) {
    clearTimeout(state.retryTimer);
    state.retryTimer = undefined;
  }
  void recoverActivityGap(queryClient, readScope, state);
}
