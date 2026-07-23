import { Activity, AlertTriangle, Bug, CircleAlert, Info, ShieldCheck } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import type { TFunction } from 'i18next';

import { Badge } from '@/components/ui/badge';
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip';
import { formatActivityAbsoluteTime, formatActivityRelativeTime } from '@/lib/activity-format';
import { cn } from '@/lib/utils';
import type { ActivityItem as ActivityItemType, ActivitySeverity } from '@/types';

const severityStyle: Record<ActivitySeverity, string> = {
  debug: 'border-slate-400/30 bg-slate-500/8 text-slate-600 dark:text-slate-300',
  info: 'border-sky-400/30 bg-sky-500/8 text-sky-700 dark:text-sky-300',
  warning: 'border-amber-400/30 bg-amber-500/10 text-amber-700 dark:text-amber-300',
  error: 'border-rose-400/30 bg-rose-500/10 text-rose-700 dark:text-rose-300',
};

const severityIcon = {
  debug: Bug,
  info: Info,
  warning: AlertTriangle,
  error: CircleAlert,
} satisfies Record<ActivitySeverity, typeof Activity>;

function activitySummary(item: ActivityItemType, t: TFunction) {
  if (item.payload_version !== 1 || !item.payload.summary_key) return t('activity.unknownSummary');
  return t(item.payload.summary_key, {
    ...item.payload.summary_args,
    defaultValue: t('activity.unknownSummary'),
  });
}

export function ActivityItem({ item }: { item: ActivityItemType }) {
  const { t } = useTranslation();
  const SeverityIcon = severityIcon[item.severity];
  const actor = item.actor.name || item.actor.id || t(`activity.actor.${item.actor.type}`, { defaultValue: item.actor.type });
  const reason = item.payload.reason_code
    ? t(`activity.reason.${item.payload.reason_code}`, { defaultValue: '' })
    : '';

  return (
    <article className="group relative grid grid-cols-[2rem_1fr] gap-3 pb-6 last:pb-0">
      <div className="relative flex justify-center">
        <span className={cn('relative z-10 flex size-8 items-center justify-center rounded-full border bg-background shadow-sm', severityStyle[item.severity])}>
          <SeverityIcon className="size-4" />
        </span>
        <span className="absolute bottom-[-1.5rem] top-8 w-px bg-border/60 group-last:hidden" />
      </div>
      <div className="min-w-0 rounded-lg border border-border/50 bg-card/50 px-4 py-3 shadow-sm transition-colors group-hover:bg-muted/20">
        <div className="flex flex-wrap items-start justify-between gap-2">
          <div className="min-w-0">
            <p className="text-sm font-medium leading-5 text-foreground">{activitySummary(item, t)}</p>
            {reason ? <p className="mt-1 text-xs text-muted-foreground">{reason}</p> : null}
          </div>
          <Tooltip>
            <TooltipTrigger asChild>
              <time className="shrink-0 cursor-default text-xs tabular-nums text-muted-foreground" dateTime={item.occurred_at}>
                {formatActivityRelativeTime(item.occurred_at)}
              </time>
            </TooltipTrigger>
            <TooltipContent>{formatActivityAbsoluteTime(item.occurred_at)}</TooltipContent>
          </Tooltip>
        </div>
        <div className="mt-3 flex flex-wrap items-center gap-1.5">
          <Badge variant="outline" className={cn('capitalize', severityStyle[item.severity])}>{t(`activity.severity.${item.severity}`)}</Badge>
          <Badge variant="secondary" className="gap-1"><ShieldCheck className="size-3" />{actor}</Badge>
          {item.clients.map((subject) => (
            <Badge variant="outline" key={`${subject.client_id}:${subject.relation}`} className="max-w-48 truncate font-mono text-[11px]">
              {subject.display_name || subject.hostname || subject.client_id}
            </Badge>
          ))}
          {item.tunnels.map((subject) => (
            <Badge variant="outline" key={`${subject.tunnel_id}:${subject.relation}`} className="max-w-48 truncate text-[11px]">
              {subject.name || subject.tunnel_id}
            </Badge>
          ))}
        </div>
      </div>
    </article>
  );
}
