import { useMemo } from 'react';
import { Activity, ArrowDown, LoaderCircle, ScanLine, TriangleAlert } from 'lucide-react';
import { motion } from 'motion/react';
import { useTranslation } from 'react-i18next';

import { ActivityItem } from './ActivityItem';
import { Button } from '@/components/ui/button';
import { Skeleton } from '@/components/ui/skeleton';
import { useActivity } from '@/hooks/use-activity';
import { activityDayKey, formatActivityDay } from '@/lib/activity-format';
import type { ActivityItem as ActivityItemType, ActivityQuery } from '@/types';

function TimelineSkeleton({ rows }: { rows: number }) {
  return (
    <div className="space-y-7">
      {Array.from({ length: rows }, (_, index) => (
        <div key={index} className="grid grid-cols-[2.25rem_1fr] gap-x-4">
          <div className="flex justify-center"><Skeleton className="size-9 rounded-full" /></div>
          <div className="space-y-2 py-1.5">
            <Skeleton className="h-4 w-3/5" />
            <Skeleton className="h-3 w-2/5" />
          </div>
        </div>
      ))}
    </div>
  );
}

function TimelineState({ icon: Icon, title, description, action }: {
  icon: typeof Activity;
  title: string;
  description: string;
  action?: React.ReactNode;
}) {
  return (
    <motion.div
      initial={{ opacity: 0, y: 12 }}
      animate={{ opacity: 1, y: 0 }}
      transition={{ duration: 0.35, ease: 'easeOut' }}
      className="flex flex-col items-center gap-3 rounded-xl border border-border/40 bg-card/50 py-16 text-center shadow-sm"
    >
      <span className="flex size-14 items-center justify-center rounded-full border border-dashed border-border text-muted-foreground">
        <Icon className="size-6" strokeWidth={1.5} />
      </span>
      <p className="text-sm font-semibold tracking-wide text-foreground">{title}</p>
      <p className="max-w-sm text-xs leading-5 text-muted-foreground">{description}</p>
      {action}
    </motion.div>
  );
}

export function ActivityTimeline({ query, compact = false }: { query: ActivityQuery; compact?: boolean }) {
  const { t } = useTranslation();
  const activity = useActivity(query);

  const groups = useMemo(() => {
    const byDay = new Map<string, ActivityItemType[]>();
    for (const item of activity.items) {
      const key = activityDayKey(item.occurred_at);
      const bucket = byDay.get(key);
      if (bucket) bucket.push(item);
      else byDay.set(key, [item]);
    }
    return [...byDay.entries()];
  }, [activity.items]);

  if (activity.isLoading) {
    return <TimelineSkeleton rows={compact ? 3 : 6} />;
  }
  if (activity.isError) {
    return (
      <TimelineState
        icon={TriangleAlert}
        title={t('activity.loadFailed')}
        description={t('activity.loadFailedHelp')}
        action={<Button variant="outline" size="sm" onClick={() => activity.refetch()}>{t('common.retry')}</Button>}
      />
    );
  }
  if (activity.items.length === 0) {
    return (
      <TimelineState
        icon={ScanLine}
        title={t('activity.emptyTitle')}
        description={t('activity.emptyDescription')}
      />
    );
  }

  let rowIndex = 0;
  return (
    <div>
      {groups.map(([day, items]) => (
        <section key={day}>
          <div className="sticky top-0 z-10 -mx-2 flex items-center gap-3 bg-background/80 px-2 py-2 backdrop-blur-sm">
            <span className="text-xs font-medium text-muted-foreground">
              {formatActivityDay(items[0].occurred_at)}
            </span>
            <span className="h-px flex-1 bg-border/60" />
            <span className="tabular-nums text-[11px] text-muted-foreground/60">{items.length}</span>
          </div>
          <div className="pt-3">
            {items.map((item) => <ActivityItem key={item.id} item={item} index={rowIndex++} />)}
          </div>
        </section>
      ))}
      <div className="mt-6 flex justify-center">
        {activity.hasNextPage ? (
          <Button variant="outline" size="sm" className="gap-2" disabled={activity.isFetchingNextPage} onClick={() => activity.fetchNextPage()}>
            {activity.isFetchingNextPage ? <LoaderCircle className="size-3.5 animate-spin" /> : <ArrowDown className="size-3.5" />}
            {activity.isFetchingNextPage ? t('activity.loadingOlder') : t('activity.loadOlder')}
          </Button>
        ) : (
          <span className="inline-flex items-center gap-3 text-xs text-muted-foreground/60">
            <span className="h-px w-10 bg-border/60" />
            {t('activity.endOfRecord')}
            <span className="h-px w-10 bg-border/60" />
          </span>
        )}
      </div>
    </div>
  );
}
