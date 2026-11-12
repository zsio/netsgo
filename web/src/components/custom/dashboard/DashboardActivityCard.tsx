import { Link } from '@tanstack/react-router';
import { AlertTriangle, ArrowUpRight, CircleAlert, History } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import {
  formatActivityCompactMeta,
  formatActivityItemSummary,
} from '@/components/custom/activity/activity-text';
import { Skeleton } from '@/components/ui/skeleton';
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip';
import { useActivity } from '@/hooks/use-activity';
import {
  formatActivityAbsoluteTime,
  formatActivityRelativeTime,
} from '@/lib/activity-format';
import type { ResourceScope } from '@/lib/resource-scope';
import { cn } from '@/lib/utils';
import type { ActivityItem as ActivityItemType, ActivitySeverity } from '@/types';

const DASHBOARD_ACTIVITY_SEVERITIES: ActivitySeverity[] = ['info', 'warning', 'error'];
const MAX_VISIBLE_ITEMS = 5;

function SeverityIndicator({ severity }: { severity: ActivitySeverity }) {
  if (severity === 'error') {
    return <CircleAlert className="mt-0.5 size-3.5 shrink-0 text-rose-500" strokeWidth={2.2} />;
  }
  if (severity === 'warning') {
    return <AlertTriangle className="mt-0.5 size-3.5 shrink-0 text-amber-500" strokeWidth={2.2} />;
  }
  return (
    <span
      className={cn(
        'mt-1.5 size-2 shrink-0 rounded-full',
        severity === 'info' ? 'bg-emerald-500/85' : 'bg-muted-foreground/45',
      )}
    />
  );
}

function CompactActivityRow({ item }: { item: ActivityItemType }) {
  const { t } = useTranslation();
  const summary = formatActivityItemSummary(item, t);
  const metaText = formatActivityCompactMeta(item, summary, t).join(' · ');

  return (
    <div className="flex items-start gap-2.5 px-4 py-2.5 transition-colors hover:bg-muted/25 sm:px-5">
      <SeverityIndicator severity={item.severity} />

      <div className="min-w-0 flex-1">
        <div className="flex items-center justify-between gap-2">
          <p className="min-w-0 truncate text-xs font-medium text-foreground" title={summary}>
            {summary}
          </p>
          <Tooltip>
            <TooltipTrigger asChild>
              <time
                dateTime={item.occurred_at}
                className="shrink-0 cursor-default font-mono text-[11px] tabular-nums text-muted-foreground"
              >
                {formatActivityRelativeTime(item.occurred_at)}
              </time>
            </TooltipTrigger>
            <TooltipContent>
              {formatActivityAbsoluteTime(item.occurred_at)}
            </TooltipContent>
          </Tooltip>
        </div>

        <div className="mt-0.5 flex min-w-0 items-center gap-1.5 text-[11px] text-muted-foreground">
          <span className="shrink-0 rounded border border-border/60 bg-muted/50 px-1.5 py-px text-[10px] leading-tight text-muted-foreground">
            {t(`activity.category.${item.category}`)}
          </span>
          {metaText ? (
            <span className="min-w-0 truncate text-muted-foreground/85" title={metaText}>
              {metaText}
            </span>
          ) : null}
        </div>
      </div>
    </div>
  );
}

export function DashboardActivityCard({ scope }: { scope: ResourceScope }) {
  const { t } = useTranslation();
  const activity = useActivity(scope, {
    scope: 'global',
    limit: 8,
    severities: DASHBOARD_ACTIVITY_SEVERITIES,
  });

  if (activity.isLoading) {
    return <Skeleton className="h-[316px] w-full rounded-xl" />;
  }

  const visibleItems = activity.items.slice(0, MAX_VISIBLE_ITEMS);
  const activitySearch = {
    scope: 'global' as const,
    user_id: scope.kind === 'admin-user' ? scope.userId : undefined,
    severity: DASHBOARD_ACTIVITY_SEVERITIES,
    category: [],
  };

  return (
    <section className="flex h-full min-h-[316px] flex-col overflow-hidden rounded-xl border border-border/60 bg-card shadow-2xs">
      {/* 卡片头部 */}
      <div className="flex items-center justify-between gap-3 border-b border-border/50 px-4 py-2.5 sm:px-5">
        <div className="flex items-center gap-2.5">
          <span className="flex size-7 shrink-0 items-center justify-center rounded-lg border border-border/70 bg-muted/60 text-foreground">
            <History className="size-4" />
          </span>
          <h3 className="text-sm font-semibold text-foreground">
            {t('dashboard.recentActivityTitle')}
          </h3>
        </div>

        <Link
          to="/dashboard/activity"
          search={activitySearch}
          className="inline-flex items-center gap-1 rounded-md px-2 py-1 text-xs font-medium text-muted-foreground transition-colors hover:bg-muted/70 hover:text-foreground"
        >
          <span>{t('dashboard.recentActivityViewAll')}</span>
          <ArrowUpRight className="size-3.5" />
        </Link>
      </div>

      {/* 最近活动列表 */}
      {visibleItems.length === 0 ? (
        <div className="flex flex-1 flex-col items-center justify-center gap-2 px-6 py-10 text-center text-muted-foreground">
          <History className="size-7 opacity-25" />
          <p className="text-xs">{t('dashboard.recentActivityEmpty')}</p>
        </div>
      ) : (
        <div className="flex-1 divide-y divide-border/45">
          {visibleItems.map((item) => (
            <CompactActivityRow key={item.id} item={item} />
          ))}
        </div>
      )}
    </section>
  );
}
