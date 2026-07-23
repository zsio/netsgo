import { Activity, LoaderCircle, RefreshCw } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import { ActivityItem } from './ActivityItem';
import { Button } from '@/components/ui/button';
import { Empty, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from '@/components/ui/empty';
import { Skeleton } from '@/components/ui/skeleton';
import { useActivity } from '@/hooks/use-activity';
import type { ActivityQuery } from '@/types';

export function ActivityTimeline({ query, compact = false }: { query: ActivityQuery; compact?: boolean }) {
  const { t } = useTranslation();
  const activity = useActivity(query);

  if (activity.isLoading) {
    return <div className="space-y-4">{Array.from({ length: compact ? 3 : 5 }, (_, index) => <Skeleton key={index} className="h-24 w-full" />)}</div>;
  }
  if (activity.isError) {
    return (
      <Empty className="min-h-48 border">
        <EmptyHeader>
          <EmptyMedia variant="icon"><RefreshCw /></EmptyMedia>
          <EmptyTitle>{t('activity.loadFailed')}</EmptyTitle>
          <EmptyDescription>{t('activity.loadFailedHelp')}</EmptyDescription>
        </EmptyHeader>
        <Button variant="outline" onClick={() => activity.refetch()}>{t('common.retry')}</Button>
      </Empty>
    );
  }
  if (activity.items.length === 0) {
    return (
      <Empty className="min-h-48 border">
        <EmptyHeader>
          <EmptyMedia variant="icon"><Activity /></EmptyMedia>
          <EmptyTitle>{t('activity.emptyTitle')}</EmptyTitle>
          <EmptyDescription>{t('activity.emptyDescription')}</EmptyDescription>
        </EmptyHeader>
      </Empty>
    );
  }

  return (
    <div>
      <div>{activity.items.map((item) => <ActivityItem key={item.id} item={item} />)}</div>
      {activity.hasNextPage ? (
        <div className="mt-5 flex justify-center">
          <Button variant="outline" disabled={activity.isFetchingNextPage} onClick={() => activity.fetchNextPage()}>
            {activity.isFetchingNextPage ? <LoaderCircle className="animate-spin" /> : null}
            {activity.isFetchingNextPage ? t('activity.loadingOlder') : t('activity.loadOlder')}
          </Button>
        </div>
      ) : null}
    </div>
  );
}
