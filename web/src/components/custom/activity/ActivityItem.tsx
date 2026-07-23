import { AlertTriangle, Bug, CircleAlert, CircleHelp, Cpu, Info, KeyRound, Monitor, Network, ShieldAlert, UserCog, Waypoints } from 'lucide-react';
import type { LucideIcon } from 'lucide-react';
import { motion } from 'motion/react';
import { useTranslation } from 'react-i18next';
import type { TFunction } from 'i18next';

import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip';
import { formatActivityAbsoluteTime, formatActivityRelativeTime } from '@/lib/activity-format';
import { cn } from '@/lib/utils';
import type { ActivityActor, ActivityCategory, ActivityItem as ActivityItemType, ActivitySeverity } from '@/types';

const severityIcon: Record<ActivitySeverity, LucideIcon> = {
  debug: Bug,
  info: Info,
  warning: AlertTriangle,
  error: CircleAlert,
};

const severityNodeClass: Record<ActivitySeverity, string> = {
  debug: 'border-slate-400/30 bg-slate-500/10 text-slate-600 dark:text-slate-300',
  info: 'border-sky-400/30 bg-sky-500/10 text-sky-600 dark:text-sky-300',
  warning: 'border-amber-400/30 bg-amber-500/10 text-amber-600 dark:text-amber-300',
  error: 'border-rose-400/30 bg-rose-500/10 text-rose-600 dark:text-rose-300',
};

const severityTextClass: Record<ActivitySeverity, string> = {
  debug: 'text-slate-500 dark:text-slate-400',
  info: 'text-sky-600 dark:text-sky-400',
  warning: 'text-amber-600 dark:text-amber-400',
  error: 'text-rose-600 dark:text-rose-400',
};

const categoryIcon: Record<ActivityCategory, LucideIcon> = {
  client: Monitor,
  tunnel: Waypoints,
  p2p: Network,
  admin: UserCog,
  security: ShieldAlert,
};

const actorIcon: Record<string, LucideIcon> = {
  admin: KeyRound,
  client: Monitor,
  system: Cpu,
  security: ShieldAlert,
};

function activitySummary(item: ActivityItemType, t: TFunction) {
  if (item.payload_version !== 1 || !item.payload.summary_key) return t('activity.unknownSummary');
  return t(item.payload.summary_key, {
    ...item.payload.summary_args,
    defaultValue: t('activity.unknownSummary'),
  });
}

function actorLabel(actor: ActivityActor, t: TFunction) {
  return actor.name || actor.id || t(`activity.actor.${actor.type}`, { defaultValue: t('activity.actor.unknown') });
}

export function ActivityItem({ item, index = 0 }: { item: ActivityItemType; index?: number }) {
  const { t } = useTranslation();
  const SeverityIcon = severityIcon[item.severity];
  const CategoryIcon = categoryIcon[item.category];
  const ActorIcon = actorIcon[item.actor.type] ?? CircleHelp;
  const reason = item.payload.reason_code
    ? t(`activity.reason.${item.payload.reason_code}`, { defaultValue: '' })
    : '';

  return (
    <motion.article
      initial={{ opacity: 0, y: 12 }}
      animate={{ opacity: 1, y: 0 }}
      transition={{ duration: 0.35, delay: Math.min(index * 0.03, 0.3), ease: 'easeOut' }}
      className="group relative grid grid-cols-[2.25rem_1fr] gap-x-4 pb-6 last:pb-0"
    >
      <div className="relative flex justify-center">
        <span className={cn('relative z-10 flex size-9 items-center justify-center rounded-full border bg-background shadow-sm', severityNodeClass[item.severity])}>
          <SeverityIcon className="size-4" strokeWidth={2.25} />
        </span>
        <span className="absolute bottom-[-1.5rem] left-1/2 top-9 w-px -translate-x-1/2 bg-border/60 group-last:hidden" />
      </div>
      <div className="relative min-w-0 rounded-lg border border-border/50 bg-card/50 px-4 py-3 shadow-sm transition-colors group-hover:bg-muted/20">
        <div className="flex flex-wrap items-baseline justify-between gap-x-4 gap-y-1">
          <p className="min-w-0 text-sm font-medium leading-6 text-foreground">{activitySummary(item, t)}</p>
          <Tooltip>
            <TooltipTrigger asChild>
              <time className="shrink-0 cursor-default text-xs tabular-nums text-muted-foreground" dateTime={item.occurred_at}>
                {formatActivityRelativeTime(item.occurred_at)}
              </time>
            </TooltipTrigger>
            <TooltipContent>{formatActivityAbsoluteTime(item.occurred_at)}</TooltipContent>
          </Tooltip>
        </div>
        {reason ? (
          <p className="mt-1 text-xs leading-5 text-muted-foreground">{reason}</p>
        ) : null}
        <div className="mt-2 flex flex-wrap items-center gap-x-3 gap-y-1.5 text-[11px] text-muted-foreground">
          <span className={cn('inline-flex items-center gap-1 font-medium', severityTextClass[item.severity])}>
            {t(`activity.severity.${item.severity}`)}
          </span>
          <span className="inline-flex items-center gap-1">
            <CategoryIcon className="size-3" />
            {t(`activity.category.${item.category}`)}
          </span>
          <span className="inline-flex items-center gap-1">
            <ActorIcon className="size-3" />
            {actorLabel(item.actor, t)}
          </span>
          {item.clients.map((subject) => (
            <span key={`${subject.client_id}:${subject.relation}`} className="inline-flex max-w-56 items-center gap-1 rounded-full border border-border/60 px-2 py-px tabular-nums">
              <Monitor className="size-3 shrink-0 text-muted-foreground/60" />
              <span className="truncate">{subject.display_name || subject.hostname || subject.client_id}</span>
            </span>
          ))}
          {item.tunnels.map((subject) => (
            <span key={`${subject.tunnel_id}:${subject.relation}`} className="inline-flex max-w-56 items-center gap-1 rounded-full border border-border/60 px-2 py-px">
              <Waypoints className="size-3 shrink-0 text-muted-foreground/60" />
              <span className="truncate">{subject.name || subject.tunnel_id}</span>
            </span>
          ))}
          <span className="ml-auto tabular-nums text-muted-foreground/40">#{String(item.id).padStart(4, '0')}</span>
        </div>
      </div>
    </motion.article>
  );
}
